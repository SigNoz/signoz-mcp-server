package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
)

func TestToolInvocationDecoratorRecoversToCodedInternalError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h := &Handler{logger: logger}
	handler := h.toolInvocationDecorator("panic_probe", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("secret-panic-canary")
	})

	result, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("tool panic returned protocol error: %v", err)
	}
	if result == nil || !result.IsError || toolerrors.Code(result) != CodeInternalError {
		t.Fatalf("tool panic result = %#v, want coded INTERNAL_ERROR", result)
	}
	if strings.Contains(logs.String(), "secret-panic-canary") {
		t.Fatal("panic value leaked into operator log")
	}
}

type uncloneableSchema struct{}

func (uncloneableSchema) MarshalJSON() ([]byte, error) { return nil, fmt.Errorf("clone boom") }

func TestCloneSchemaValueFailureIsFatal(t *testing.T) {
	defer func() {
		got := fmt.Sprint(recover())
		if !strings.Contains(got, "clone tool schema (type tools.uncloneableSchema): marshal:") {
			t.Fatalf("panic = %q", got)
		}
	}()
	cloneSchemaValue(uncloneableSchema{})
}

func TestValidationDiagnosisUsesOnlyAdvertisedTopLevelParameters(t *testing.T) {
	compiled, err := compileToolSchema("diagnostic_probe", "input", json.RawMessage(`{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{
			"schemaVersion":{"type":"string"},
			"spec":{"$ref":"#/$defs/spec"},
			"tags":{"type":"array"}
		},
		"required":["schemaVersion","spec","tags"],
		"additionalProperties":false,
		"$defs":{"spec":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if compiled.diagnostic == nil {
		t.Fatal("top-level diagnostic schema did not compile with local $defs")
	}

	tests := []struct {
		name           string
		value          any
		wantParameters []string
		wantPath       string
		wantConstraint string
	}{
		{
			name:           "local ref property mismatch",
			value:          map[string]any{"schemaVersion": "v1", "spec": "wrong", "tags": []any{}},
			wantParameters: []string{"spec"},
			wantPath:       "spec",
			wantConstraint: "schema",
		},
		{
			name:           "missing required fields",
			value:          map[string]any{},
			wantParameters: []string{"schemaVersion", "spec", "tags"},
			wantPath:       "<multiple>",
			wantConstraint: "required",
		},
		{
			name:           "unknown client key is not reflected",
			value:          map[string]any{"schemaVersion": "v1", "spec": map[string]any{"name": "ok"}, "tags": []any{}, "attacker-controlled-secret": true},
			wantPath:       "<root>",
			wantConstraint: "schema",
		},
		{
			name:           "non-object remains root attributed",
			value:          "wrong",
			wantPath:       "<root>",
			wantConstraint: "type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diagnoseValidationMismatch(compiled, tt.value, true)
			if !reflect.DeepEqual(got.parameters, tt.wantParameters) || got.path != tt.wantPath || got.constraint != tt.wantConstraint {
				t.Fatalf("diagnosis = %#v, want parameters=%v path=%q constraint=%q", got, tt.wantParameters, tt.wantPath, tt.wantConstraint)
			}
			if strings.Contains(inputValidationNotice(got), "attacker-controlled-secret") {
				t.Fatal("validation notice reflected an undeclared client key")
			}
		})
	}
}

func TestNormalizeRawSchemaReplacesSchemaTrueWithEmptyObject(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"anything": true,
			"flag": {
				"type": "boolean",
				"default": true
			},
			"list": {
				"type": "array",
				"items": true
			},
			"object": {
				"type": "object",
				"additionalProperties": true
			}
		}
	}`)

	var got map[string]any
	if err := json.Unmarshal(normalizeRawSchema(raw), &got); err != nil {
		t.Fatalf("normalized schema should remain JSON: %v", err)
	}

	properties := got["properties"].(map[string]any)
	if len(properties["anything"].(map[string]any)) != 0 {
		t.Fatalf("properties.anything = %#v, want empty schema object", properties["anything"])
	}

	flag := properties["flag"].(map[string]any)
	if flag["type"] != "boolean" || flag["default"] != true {
		t.Fatalf("flag schema = %#v, want boolean schema with default true preserved", flag)
	}

	list := properties["list"].(map[string]any)
	if len(list["items"].(map[string]any)) != 0 {
		t.Fatalf("list.items = %#v, want empty schema object", list["items"])
	}

	object := properties["object"].(map[string]any)
	if len(object["additionalProperties"].(map[string]any)) != 0 {
		t.Fatalf("object.additionalProperties = %#v, want empty schema object", object["additionalProperties"])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestNumericParamsAdvertiseIntegerStringUnion pins the schema-honesty fix:
// limit/offset/stepInterval are registered with mcp.WithString but their parsers
// (paginate.ParseParamsClamped, intArg/parseLimit, parseStepInterval) ALSO
// accept a JSON number, so a schema-validating client sending {"limit": 50}
// (a JSON number — the natural form) must not be rejected before the handler
// runs. intOrStringType() overrides the advertised type to the union
// ["integer","string"]; this test asserts a representative sample of tools
// advertise BOTH on each numeric param. It reuses registeredToolProps (defined
// in param_schema_test.go), which builds the schema exactly as advertised.
func TestNumericParamsAdvertiseIntegerStringUnion(t *testing.T) {
	cases := []struct {
		tool  string
		param string
	}{
		{"signoz_search_logs", "limit"},
		{"signoz_search_logs", "offset"},
		{"signoz_query_metrics", "stepInterval"},
		{"signoz_list_services", "limit"},
		{"signoz_list_views", "limit"},
		{"signoz_list_views", "offset"},
		{"signoz_search_docs", "limit"},
	}

	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.param, func(t *testing.T) {
			props := registeredToolProps(t, tc.tool)
			gotType := propTypeUnion(t, props, tc.param)
			if !containsString(gotType, "integer") || !containsString(gotType, "string") {
				t.Fatalf("%s.%s type = %v, want a union containing both \"integer\" and \"string\"", tc.tool, tc.param, gotType)
			}
		})
	}
}

// propTypeUnion returns the JSON-Schema "type" of a property as a string slice,
// accepting either a single string ("string") or a type array
// (["integer","string"]). It fails if the property is absent or carries no type.
func propTypeUnion(t *testing.T, props map[string]any, name string) []string {
	t.Helper()
	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("property %q = %#v, want object", name, props[name])
	}
	switch v := prop["type"].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				t.Fatalf("type entry %#v on %q is not a string", e, name)
			}
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("property %q has no usable type: %#v", name, prop["type"])
		return nil
	}
}

// registeredToolProps registers every tool family on a fresh server (a nil
// client is fine: nothing dials upstream during registration) and returns the
// named tool's input-schema properties for schema-shape assertions.
func registeredToolProps(t *testing.T, toolName string) map[string]any {
	t.Helper()

	h := newTestHandler(nil)
	s := newMCPTestServer()

	h.RegisterLogsHandlers(s)
	h.RegisterTracesHandlers(s)
	h.RegisterMetricsHandlers(s)
	h.RegisterQueryBuilderV5Handlers(s)
	h.RegisterTopMetricsHandlers(s)
	h.RegisterAlertsHandlers(s)
	h.RegisterFieldsHandlers(s)
	h.RegisterServiceHandlers(s)
	h.RegisterViewHandlers(s)
	h.RegisterDocsHandlers(s)

	tools := listTestTools(t, s)
	st, ok := tools[toolName]
	if !ok {
		t.Fatalf("tool %q not registered", toolName)
	}

	b, err := json.Marshal(st.Tool)
	if err != nil {
		t.Fatalf("marshal tool %q: %v", toolName, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal tool %q JSON: %v", toolName, err)
	}
	schema, ok := doc["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q inputSchema = %#v, want object", toolName, doc["inputSchema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q properties = %#v, want object", toolName, schema["properties"])
	}
	return props
}
