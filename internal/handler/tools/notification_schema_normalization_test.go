package tools

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
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

func TestCanonicalNotificationSchemasExposeClosedTenProviderUnion(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	server := newMCPTestServer()
	h.RegisterNotificationChannelHandlers(server)
	registered := listTestTools(t, server)
	wantRequired := map[string][]string{
		"slack": {"apiUrl"}, "email": {"to"}, "webhook": {"url"},
		"pagerduty": {"routingKey"}, "opsgenie": {"apiKey"}, "msteams": {"webhookUrl"},
		"googlechat": {"webhookUrl"}, "jira": {"apiToken", "email", "issueType", "project", "site"},
		"jsmops": {"apiKey"}, "incidentio": {"token", "url"},
	}
	for _, name := range []string{"signoz_create_notification_channel", "signoz_update_notification_channel"} {
		t.Run(name, func(t *testing.T) {
			raw := inputSchemaJSON(registered[name].Tool)
			var root map[string]any
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			if root["additionalProperties"] != false {
				t.Fatal("root schema is open")
			}
			required := stringSlice(root["required"])
			if notificationContainsString(required, "searchContext") {
				t.Fatal("searchContext must not be required")
			}
			config := root["properties"].(map[string]any)["config"].(map[string]any)
			if config["additionalProperties"] != false {
				t.Fatal("config schema is open")
			}
			branches := config["oneOf"].([]any)
			if len(branches) != 10 {
				t.Fatalf("provider branches = %d, want 10", len(branches))
			}
			seen := map[string]bool{}
			for _, rawBranch := range branches {
				branch := rawBranch.(map[string]any)
				if branch["additionalProperties"] != false {
					t.Fatal("provider config branch is open")
				}
				properties := branch["properties"].(map[string]any)
				kind := properties["kind"].(map[string]any)["const"].(string)
				seen[kind] = true
				spec := properties["spec"].(map[string]any)
				if spec["additionalProperties"] != false {
					t.Fatalf("%s spec is open", kind)
				}
				gotRequired := stringSlice(spec["required"])
				sort.Strings(gotRequired)
				if !reflect.DeepEqual(gotRequired, wantRequired[kind]) {
					t.Fatalf("%s required = %v, want %v", kind, gotRequired, wantRequired[kind])
				}
			}
			if len(seen) != 10 {
				t.Fatalf("seen providers = %v", seen)
			}
		})
	}
}

func TestCanonicalNotificationRootRequiredSerialization(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	server := newMCPTestServer()
	h.RegisterNotificationChannelHandlers(server)
	registered := listTestTools(t, server)
	wantRequired := map[string][]string{
		"signoz_create_notification_channel": {"config"},
		"signoz_update_notification_channel": {"id", "config"},
		"signoz_get_notification_channel":    {"id"},
		"signoz_delete_notification_channel": {"id"},
	}

	for _, name := range []string{
		"signoz_list_notification_channels",
		"signoz_create_notification_channel",
		"signoz_update_notification_channel",
		"signoz_get_notification_channel",
		"signoz_delete_notification_channel",
	} {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(inputSchemaJSON(registered[name].Tool), &root); err != nil {
				t.Fatal(err)
			}
			properties := root["properties"].(map[string]any)
			if _, ok := properties["searchContext"]; !ok {
				t.Fatal("searchContext property is missing")
			}

			requiredValue, present := root["required"]
			want, hasRequired := wantRequired[name]
			if !hasRequired {
				if present {
					t.Fatalf("required = %#v, want omitted", requiredValue)
				}
				return
			}
			if _, ok := requiredValue.([]any); !ok {
				t.Fatalf("required has type %T, want JSON array", requiredValue)
			}
			got := stringSlice(requiredValue)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("required = %v, want %v", got, want)
			}
			if notificationContainsString(got, "searchContext") {
				t.Fatal("searchContext must not be required")
			}
		})
	}
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	if direct, ok := value.([]string); ok {
		return append([]string(nil), direct...)
	}
	return result
}

func notificationContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
