package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func TestDashboardReadWriteBackContract(t *testing.T) {
	const dashboardID = "dashboard-1"
	getFixture, err := os.ReadFile("../../../pkg/dashboard/dashboardbuilder/testdata/full.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboardData map[string]any
	if err := json.Unmarshal(getFixture, &dashboardData); err != nil {
		t.Fatal(err)
	}

	var gotID string
	var gotBody []byte
	h := newTestHandler(&client.MockClient{
		UpdateDashboardRawFn: func(_ context.Context, id string, body []byte) error {
			gotID = id
			gotBody = append([]byte(nil), body...)
			return nil
		},
	})
	result, err := h.handleUpdateDashboard(testCtx(), makeToolRequest("signoz_update_dashboard", map[string]any{
		"id":        dashboardID,
		"dashboard": dashboardData,
	}))
	if err != nil || result.IsError {
		t.Fatalf("write-back failed: result=%#v err=%v", result, err)
	}
	if gotID != dashboardID {
		t.Fatalf("update id = %q, want %q", gotID, dashboardID)
	}
	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "Full Dashboard" {
		t.Fatalf("dashboard body lost data subobject: %s", gotBody)
	}
	if _, wrapped := body["dashboard"]; wrapped {
		t.Fatalf("API body must be the dashboard subobject, not the MCP wrapper: %s", gotBody)
	}
	widgets := body["widgets"].([]any)
	query := widgets[1].(map[string]any)["query"].(map[string]any)
	builder := query["builder"].(map[string]any)
	spec := builder["queryData"].([]any)[0].(map[string]any)
	if spec["limit"] != float64(types.DefaultAggregateQueryLimit) {
		t.Fatalf("dashboard builder limit did not survive read-write-back: %s", gotBody)
	}
	orderBy := spec["orderBy"].([]any)
	if len(orderBy) != 1 || orderBy[0].(map[string]any)["columnName"] != "duration_nano" || orderBy[0].(map[string]any)["order"] != "desc" {
		t.Fatalf("dashboard builder orderBy did not survive read-write-back: %s", gotBody)
	}
}

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
			spec := body["condition"].(map[string]any)["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
			if spec["limit"] != float64(types.DefaultAggregateQueryLimit) {
				t.Fatalf("alert builder limit did not survive read-write-back: %s", gotBody)
			}
			order := spec["order"].([]any)
			if len(order) != 1 || order[0].(map[string]any)["key"].(map[string]any)["name"] != "__result" || order[0].(map[string]any)["direction"] != "desc" {
				t.Fatalf("alert builder order did not survive read-write-back: %s", gotBody)
			}
		})
	}
}

func TestViewReadWriteBackPreservesQueryBounds(t *testing.T) {
	const viewID = "view-1"
	view := map[string]any{
		"id":         viewID,
		"name":       "Error Logs",
		"sourcePage": "logs",
		"compositeQuery": map[string]any{
			"queryType": "builder",
			"panelType": "list",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{
					"name":   "A",
					"signal": "logs",
					"limit":  100,
					"order": []any{
						map[string]any{"key": map[string]any{"name": "timestamp"}, "direction": "desc"},
						map[string]any{"key": map[string]any{"name": "id"}, "direction": "desc"},
					},
					"filter": map[string]any{"expression": "severity_text = 'ERROR'"},
				},
			}},
		},
	}

	var gotBody []byte
	h := newTestHandler(&client.MockClient{
		UpdateViewFn: func(_ context.Context, id string, body []byte) (json.RawMessage, error) {
			if id != viewID {
				t.Fatalf("update id = %q, want %q", id, viewID)
			}
			gotBody = append([]byte(nil), body...)
			return json.RawMessage(`{"status":"success"}`), nil
		},
	})
	result, err := h.handleUpdateView(testCtx(), makeToolRequest("signoz_update_view", map[string]any{
		"id":   viewID,
		"view": view,
	}))
	if err != nil || result.IsError {
		t.Fatalf("write-back failed: result=%#v err=%v", result, err)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["id"]; present {
		t.Fatalf("server-populated id leaked into view update body: %s", gotBody)
	}
	spec := body["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	if spec["limit"] != float64(100) {
		t.Fatalf("view builder limit did not survive read-write-back: %s", gotBody)
	}
	order := spec["order"].([]any)
	if len(order) != 2 || order[0].(map[string]any)["key"].(map[string]any)["name"] != "timestamp" || order[1].(map[string]any)["key"].(map[string]any)["name"] != "id" {
		t.Fatalf("view builder order did not survive read-write-back: %s", gotBody)
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
						"name":   "A",
						"signal": "metrics",
						"limit":  types.DefaultAggregateQueryLimit,
						"order": []any{map[string]any{
							"key":       map[string]any{"name": "__result"},
							"direction": "desc",
						}},
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
