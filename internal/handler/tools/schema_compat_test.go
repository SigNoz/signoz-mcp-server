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
			tool:       mcp.NewTool("signoz_create_dashboard", mcp.WithInputSchema[types.CreateDashboardInput]()),
			wantFields: []string{"title", "layout", "widgets", "searchContext"},
		},
		{
			name:       "update dashboard",
			tool:       mcp.NewTool("signoz_update_dashboard", mcp.WithInputSchema[types.UpdateDashboardInput]()),
			wantFields: []string{"uuid", "dashboard", "searchContext"},
		},
		{
			name:       "create alert",
			tool:       mcp.NewTool("signoz_create_alert", mcp.WithInputSchema[types.CreateAlertInput]()),
			wantFields: []string{"alert", "alertType", "ruleType", "condition", "searchContext"},
		},
		{
			name:       "update alert",
			tool:       mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]()),
			wantFields: []string{"ruleId", "alert", "alertType", "ruleType", "condition", "searchContext"},
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
