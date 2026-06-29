package tools

import (
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
)

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

func TestWriteToolInputSchemasExposeSearchContext(t *testing.T) {
	tests := []struct {
		name       string
		tool       mcp.Tool
		wantFields []string
	}{
		{
			name:       "create dashboard",
			tool:       mcp.NewTool("signoz_create_dashboard", rawInputSchema(createDashboardSchema)),
			wantFields: []string{"schemaVersion", "spec", "tags", "searchContext"},
		},
		{
			name:       "update dashboard",
			tool:       mcp.NewTool("signoz_update_dashboard", rawInputSchema(updateDashboardSchema)),
			wantFields: []string{"id", "schemaVersion", "spec", "searchContext"},
		},
		{
			name:       "patch dashboard",
			tool:       mcp.NewTool("signoz_patch_dashboard", rawInputSchema(patchDashboardSchema)),
			wantFields: []string{"id", "patch", "searchContext"},
		},
		{
			name:       "create alert",
			tool:       mcp.NewTool("signoz_create_alert", mcp.WithInputSchema[types.CreateAlertInput]()),
			wantFields: []string{"alert", "alertType", "ruleType", "condition", "searchContext"},
		},
		{
			name:       "update alert",
			tool:       mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]()),
			wantFields: []string{"id", "alert", "alertType", "ruleType", "condition", "searchContext"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := inputSchemaProperties(t, tt.tool)
			for _, field := range tt.wantFields {
				if _, ok := props[field]; !ok {
					t.Fatalf("input schema properties missing %q: %#v", field, props)
				}
			}
			required := inputSchemaRequiredFields(t, tt.tool)
			if containsString(required, "searchContext") {
				t.Fatalf("input schema required fields should not include searchContext: %#v", required)
			}
		})
	}
}

func inputSchemaProperties(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()

	normalizeToolSchemas(&tool)
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal tool JSON: %v", err)
	}

	inputSchema, ok := doc["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v, want object", doc["inputSchema"])
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema.properties = %#v, want object", inputSchema["properties"])
	}
	return props
}

func inputSchemaRequiredFields(t *testing.T, tool mcp.Tool) []string {
	t.Helper()

	normalizeToolSchemas(&tool)
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal tool JSON: %v", err)
	}

	inputSchema, ok := doc["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v, want object", doc["inputSchema"])
	}

	rawRequired, ok := inputSchema["required"].([]any)
	if !ok {
		return nil
	}

	required := make([]string, 0, len(rawRequired))
	for _, field := range rawRequired {
		if fieldName, ok := field.(string); ok {
			required = append(required, fieldName)
		}
	}
	return required
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestTypedToolSchemasNeverEmitLiteralRequiredDescription guards
// SigNoz/signoz-ai-assistant#359: google/jsonschema-go uses the `jsonschema`
// tag value AS the field description, so a stray `jsonschema:"required"` would
// surface the literal word "required" instead of authored prose. With
// descriptions authored natively in the `jsonschema` tag, "required" must never
// appear as a description at any depth of any typed-struct tool's input schema.
func TestTypedToolSchemasNeverEmitLiteralRequiredDescription(t *testing.T) {
	for _, tc := range typedToolSchemaCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var descriptions []string
			collectDescriptions(tc.schema, &descriptions)
			if len(descriptions) == 0 {
				t.Fatalf("no descriptions found in %s schema", tc.name)
			}
			for _, d := range descriptions {
				if d == "required" {
					t.Fatalf("%s schema emits a description of %q (a stray jsonschema:\"required\" tag); descriptions=%d", tc.name, "required", len(descriptions))
				}
			}
		})
	}
}

// TestTypedToolSchemasExposeAuthoredDescriptions verifies the descriptions
// authored in the native `jsonschema` tags reach the schema the model sees — at
// the top level and through nested objects, slice element schemas (items), and
// map value schemas (additionalProperties).
func TestTypedToolSchemasExposeAuthoredDescriptions(t *testing.T) {
	cases := typedToolSchemaCases(t)
	schemaByName := map[string]map[string]any{}
	for _, tc := range cases {
		schemaByName[tc.name] = tc.schema
	}

	type check struct {
		tool string
		path []string
		want string
	}
	checks := []check{
		// top-level field (promoted from the embedded AlertRule)
		{"create alert", []string{"alert"}, "Name of the alert rule. Must be unique and descriptive."},
		// searchContext is authored natively in the jsonschema tag too
		{"create alert", []string{"searchContext"}, "The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results."},
		// deep path through a slice element (queries -> items -> spec -> filter -> expression)
		{"create alert", []string{"condition", "compositeQuery", "queries", "[]", "spec", "filter", "expression"}, "Filter expression using field operators. Example: service.name = frontend AND http.status_code >= 500. Use empty string for no filter."},
		// direct named field on UpdateAlertInput (id is canonical; ruleId is a legacy alias)
		{"update alert", []string{"id"}, "UUIDv7 of the alert rule to update (required). Obtain it from signoz_list_alert_rules or signoz_get_alert."},
	}

	for _, c := range checks {
		t.Run(c.tool+":"+joinPath(c.path), func(t *testing.T) {
			schema, ok := schemaByName[c.tool]
			if !ok {
				t.Fatalf("no schema for tool %q", c.tool)
			}
			node := descend(t, schema, c.path...)
			got, _ := node["description"].(string)
			if got != c.want {
				t.Fatalf("description at %v = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// TestTypedToolSchemasDescriptionCoverage guards against a *silent* loss of
// field descriptions (a tag-dialect regression, a botched edit, or an upstream
// generator change): each typed tool's schema must expose at least the number
// of descriptions it has today. Adding described fields only raises the count;
// dropping any drops below the floor and fails here. This closes the gap that
// the "never emit a 'required' description" guards leave open — descriptions
// vanishing entirely rather than turning into the word "required".
func TestTypedToolSchemasDescriptionCoverage(t *testing.T) {
	floors := map[string]int{
		"create alert": 89,
		"update alert": 90,
	}
	for _, tc := range typedToolSchemaCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var descriptions []string
			collectDescriptions(tc.schema, &descriptions)
			if floor := floors[tc.name]; len(descriptions) < floor {
				t.Fatalf("%s exposes %d field descriptions, want >= %d (were descriptions silently dropped?)", tc.name, len(descriptions), floor)
			}
		})
	}
}

type typedToolSchemaCase struct {
	name   string
	schema map[string]any
}

// typedToolSchemaCases builds each typed-struct tool's input schema the same way
// registration does (mcp.WithInputSchema -> normalizeToolSchemas) and parses it.
// Descriptions come natively from the `jsonschema` struct tags.
func typedToolSchemaCases(t *testing.T) []typedToolSchemaCase {
	t.Helper()
	return []typedToolSchemaCase{
		{"create alert", normalizedInputSchema(t, mcp.NewTool("signoz_create_alert", mcp.WithInputSchema[types.CreateAlertInput]()))},
		{"update alert", normalizedInputSchema(t, mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]()))},
		// Dashboards are no longer typed-struct tools — they use embedded raw JSON
		// Schemas (v2/Perses). Their schema is covered by TestWriteToolInputSchemasExposeSearchContext
		// and TestUpdateStructs_IDNotSchemaRequired instead.
	}
}

func normalizedInputSchema(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()
	normalizeToolSchemas(&tool)
	var schema map[string]any
	if err := json.Unmarshal(tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	return schema
}

// descend walks an object schema node. A step of "[]" descends into "items",
// "{}" into "additionalProperties", and any other step into properties[step].
func descend(t *testing.T, node map[string]any, steps ...string) map[string]any {
	t.Helper()
	cur := node
	for i, step := range steps {
		var next any
		switch step {
		case "[]":
			next = cur["items"]
		case "{}":
			next = cur["additionalProperties"]
		default:
			props, _ := cur["properties"].(map[string]any)
			next = props[step]
		}
		m, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("descend step %d (%q) in %v: not an object node (got %T)", i, step, steps, next)
		}
		cur = m
	}
	return cur
}

// collectDescriptions gathers every "description" string anywhere in the schema.
func collectDescriptions(node any, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if d, ok := v["description"].(string); ok {
			*out = append(*out, d)
		}
		for _, val := range v {
			collectDescriptions(val, out)
		}
	case []any:
		for _, val := range v {
			collectDescriptions(val, out)
		}
	}
}

func joinPath(steps []string) string {
	out := ""
	for i, s := range steps {
		if i > 0 {
			out += "."
		}
		out += s
	}
	return out
}
