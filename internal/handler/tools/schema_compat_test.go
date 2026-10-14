package tools

import (
	"bytes"
	"context"
	"encoding/json"
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
