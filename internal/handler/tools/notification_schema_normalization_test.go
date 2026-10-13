package tools

import (
	"encoding/json"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

func TestChannelV2SchemaRetainsClosedConfig(t *testing.T) {
	for _, name := range []string{"signoz_create_notification_channel", "signoz_update_notification_channel"} {
		t.Run(name, func(t *testing.T) {
			tool := mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object","properties":{"config":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string"}}}},"additionalProperties":false}`)}
			normalizeToolSchemas(&tool)
			var schema map[string]any
			if err := json.Unmarshal(inputSchemaJSON(tool), &schema); err != nil {
				t.Fatal(err)
			}
			if schema["additionalProperties"] != false {
				t.Fatal("canonical channel root became open")
			}
			config := schema["properties"].(map[string]any)["config"].(map[string]any)
			if config["additionalProperties"] != false {
				t.Fatal("canonical channel config became open")
			}
		})
	}
}
