package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestAlertReadWriteBackContractAcrossServerVersions(t *testing.T) {
	for _, versionFields := range []map[string]any{
		{"createdAt": "yesterday", "updatedAt": "today", "createdBy": "user", "updatedBy": "user"},
		{"createAt": "yesterday", "updateAt": "today", "createBy": "user", "updateBy": "user"},
	} {
		t.Run(func() string {
			if _, ok := versionFields["createdAt"]; ok {
				return "canonical audit fields"
			}
			return "legacy audit fields"
		}(), func(t *testing.T) {
			rule := validAlertWriteBackFixture()
			for key, value := range versionFields {
				rule[key] = value
			}
			rule["id"] = validRuleUUIDv7

			var gotBody []byte
			h := newTestHandler(&client.MockClient{
				ListNotificationChannelsFn: func(context.Context) (json.RawMessage, error) {
					return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
				},
				UpdateAlertRuleFn: func(_ context.Context, id string, body []byte) error {
					if id != validRuleUUIDv7 {
						t.Fatalf("update id = %q, want %q", id, validRuleUUIDv7)
					}
					gotBody = append([]byte(nil), body...)
					return nil
				},
			})
			result, err := h.handleUpdateAlert(testCtx(), makeToolRequest("signoz_update_alert", rule))
			if err != nil || result.IsError {
				t.Fatalf("write-back failed: result=%#v err=%v", result, err)
			}
			var body map[string]any
			if err := json.Unmarshal(gotBody, &body); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"id", "ruleId", "createdAt", "updatedAt", "createdBy", "updatedBy", "createAt", "updateAt", "createBy", "updateBy"} {
				if _, present := body[field]; present {
					t.Fatalf("read-only field %q leaked into alert update body: %s", field, gotBody)
				}
			}
			if body["alert"] != "Updated Alert" {
				t.Fatalf("alert body lost get response fields: %s", gotBody)
			}
		})
	}
}

func validAlertWriteBackFixture() map[string]any {
	return map[string]any{
		"alert":     "Updated Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "graph",
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{
						"name":         "A",
						"signal":       "metrics",
						"aggregations": []any{map[string]any{"expression": "count()"}},
						"filter":       map[string]any{"expression": ""},
					},
				}},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{map[string]any{
					"name":      "critical",
					"target":    float64(200),
					"op":        "1",
					"matchType": "1",
					"channels":  []any{"slack-alerts"},
				}},
			},
		},
	}
}
