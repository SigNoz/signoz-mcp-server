package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// These tests pin the K5 contract: every CRUD resource id is read from the
// canonical "id" param, with the legacy key (ruleId/uuid/viewId) accepted
// forever as a silent alias. For each tool we assert the new "id" key works AND
// the legacy key still works (round-tripping to the same backend id).

const aliasUUIDv7 = "0196634d-5d66-75c4-b778-e317f49dab7a"

func TestGetAlert_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "ruleId"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
					captured = ruleID
					return json.RawMessage(`{"data":{"id":"r1"}}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_get_alert", map[string]any{key: "r1"})
			result, err := h.handleGetAlert(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "r1" {
				t.Fatalf("backend ruleID = %q, want r1 (via %q)", captured, key)
			}
		})
	}
}

func TestGetAlert_IDTakesPrecedenceOverLegacy(t *testing.T) {
	var captured string
	mock := &client.MockClient{
		GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
			captured = ruleID
			return json.RawMessage(`{}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{"id": "canonical", "ruleId": "legacy"})
	if _, err := h.handleGetAlert(testCtx(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured != "canonical" {
		t.Fatalf("canonical id should win, got %q", captured)
	}
}

func TestGetAlert_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, _ := h.handleGetAlert(testCtx(), makeToolRequest("signoz_get_alert", map[string]any{}))
	if result == nil || !result.IsError {
		t.Fatal("expected error result for missing id")
	}
	// The missing-id presence check returns errorWithCode(CodeValidationFailed, ...).
	// Pin the machine-readable code so a regression to an uncoded/wrong-class
	// error is caught here, not just the IsError flag.
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("missing id code = %q, want %q", code, CodeValidationFailed)
	}
}

func TestDeleteAlert_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "ruleId"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				DeleteAlertRuleFn: func(ctx context.Context, ruleID string) error {
					captured = ruleID
					return nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_delete_alert", map[string]any{key: aliasUUIDv7})
			result, err := h.handleDeleteAlert(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != aliasUUIDv7 {
				t.Fatalf("backend ruleID = %q, want %q (via %q)", captured, aliasUUIDv7, key)
			}
		})
	}
}

func TestGetAlertHistory_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "ruleId"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
					captured = ruleID
					return json.RawMessage(`{"data":{}}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_get_alert_history", map[string]any{key: "r1", "timeRange": "1h"})
			result, err := h.handleGetAlertHistory(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "r1" {
				t.Fatalf("backend ruleID = %q, want r1 (via %q)", captured, key)
			}
		})
	}
}

func TestUpdateAlert_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "ruleId"} {
		t.Run(key, func(t *testing.T) {
			var capturedID string
			var capturedJSON []byte
			mock := &client.MockClient{
				ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
					return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
				},
				UpdateAlertRuleFn: func(ctx context.Context, ruleID string, alertJSON []byte) error {
					capturedID = ruleID
					capturedJSON = alertJSON
					return nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_update_alert", map[string]any{
				key:         aliasUUIDv7,
				"alert":     "Updated Alert",
				"alertType": "METRIC_BASED_ALERT",
				"ruleType":  "threshold_rule",
				"condition": map[string]any{
					"compositeQuery": map[string]any{
						"queryType": "builder",
						"panelType": "graph",
						"queries": []any{
							map[string]any{
								"type": "builder_query",
								"spec": map[string]any{
									"name":         "A",
									"signal":       "metrics",
									"aggregations": []any{map[string]any{"expression": "count()"}},
									"filter":       map[string]any{"expression": ""},
								},
							},
						},
					},
					"thresholds": map[string]any{
						"kind": "basic",
						"spec": []any{
							map[string]any{
								"name":      "critical",
								"target":    float64(200),
								"op":        "1",
								"matchType": "1",
								"channels":  []any{"slack-alerts"},
							},
						},
					},
				},
			})
			result, err := h.handleUpdateAlert(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if capturedID != aliasUUIDv7 {
				t.Fatalf("backend ruleID = %q, want %q (via %q)", capturedID, aliasUUIDv7, key)
			}
			// Neither the canonical nor the legacy id key should leak into body.
			var parsed map[string]any
			if err := json.Unmarshal(capturedJSON, &parsed); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if _, present := parsed["id"]; present {
				t.Error(`"id" should be stripped from the rule body`)
			}
			if _, present := parsed["ruleId"]; present {
				t.Error(`"ruleId" should be stripped from the rule body`)
			}
		})
	}
}

func TestGetDashboard_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "uuid"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
					captured = uuid
					return json.RawMessage(`{"data":{"uuid":"d1"}}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_get_dashboard", map[string]any{key: "d1"})
			result, err := h.handleGetDashboard(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "d1" {
				t.Fatalf("backend uuid = %q, want d1 (via %q)", captured, key)
			}
		})
	}
}

func TestDeleteDashboard_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "uuid"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				DeleteDashboardFn: func(ctx context.Context, id string) error {
					captured = id
					return nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_delete_dashboard", map[string]any{key: "d1"})
			result, err := h.handleDeleteDashboard(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "d1" {
				t.Fatalf("backend id = %q, want d1 (via %q)", captured, key)
			}
		})
	}
}

func TestUpdateDashboard_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "uuid"} {
		t.Run(key, func(t *testing.T) {
			var capturedID string
			mock := &client.MockClient{
				UpdateDashboardRawFn: func(ctx context.Context, id string, dashboardJSON []byte) error {
					capturedID = id
					return nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_update_dashboard", map[string]any{
				key: "d1",
				"dashboard": map[string]any{
					"title":   "My Dashboard",
					"layout":  []any{},
					"widgets": []any{},
				},
			})
			result, err := h.handleUpdateDashboard(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if capturedID != "d1" {
				t.Fatalf("backend id = %q, want d1 (via %q)", capturedID, key)
			}
		})
	}
}

func TestGetView_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "viewId"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				GetViewFn: func(ctx context.Context, viewID string) (json.RawMessage, error) {
					captured = viewID
					return json.RawMessage(`{"data":{"id":"v1"}}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_get_view", map[string]any{key: "v1"})
			result, err := h.handleGetView(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "v1" {
				t.Fatalf("backend viewID = %q, want v1 (via %q)", captured, key)
			}
		})
	}
}

func TestDeleteView_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "viewId"} {
		t.Run(key, func(t *testing.T) {
			var captured string
			mock := &client.MockClient{
				DeleteViewFn: func(ctx context.Context, viewID string) (json.RawMessage, error) {
					captured = viewID
					return json.RawMessage(`{"status":"success"}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_delete_view", map[string]any{key: "v1"})
			result, err := h.handleDeleteView(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if captured != "v1" {
				t.Fatalf("backend viewID = %q, want v1 (via %q)", captured, key)
			}
		})
	}
}

func TestUpdateView_IDAndLegacyAlias(t *testing.T) {
	for _, key := range []string{"id", "viewId"} {
		t.Run(key, func(t *testing.T) {
			var capturedID string
			var capturedBody []byte
			mock := &client.MockClient{
				GetViewFn: func(ctx context.Context, viewID string) (json.RawMessage, error) {
					return json.RawMessage(`{"data":{"sourcePage":"logs"}}`), nil
				},
				UpdateViewFn: func(ctx context.Context, viewID string, body []byte) (json.RawMessage, error) {
					capturedID = viewID
					capturedBody = body
					return json.RawMessage(`{"status":"success"}`), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_update_view", map[string]any{
				key: "v1",
				"view": map[string]any{
					// Server-populated "id" is included to prove it gets stripped
					// from the outgoing body (matches the signoz_get_view shape a
					// caller is told to paste back under "view").
					"id":         "v1",
					"name":       "My View",
					"sourcePage": "logs",
					"compositeQuery": map[string]any{
						"queryType": "builder",
						"queries": []any{
							map[string]any{
								"type": "builder_query",
								"spec": map[string]any{"name": "A", "signal": "logs"},
							},
						},
					},
				},
			})
			result, err := h.handleUpdateView(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("error result: %v", result.Content)
			}
			if capturedID != "v1" {
				t.Fatalf("backend viewID = %q, want v1 (via %q)", capturedID, key)
			}
			// Neither the canonical "id" nor the legacy "viewId" path param may
			// leak into the marshalled view body (id is also a server-populated
			// SavedView field that must be stripped).
			var parsed map[string]any
			if err := json.Unmarshal(capturedBody, &parsed); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if _, present := parsed["id"]; present {
				t.Error(`"id" should not leak into the view body`)
			}
			if _, present := parsed["viewId"]; present {
				t.Error(`"viewId" should not leak into the view body`)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Neither id nor legacy key supplied → a clean "id is required" validation
// error (NOT a panic, NOT a silent backend call). Pins the handler-side
// presence check that replaces the dropped schema-level `required`.
// ---------------------------------------------------------------------------

func TestResourceID_MissingBothKeys_Errors(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	cases := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{"get_alert", func() (*mcp.CallToolResult, error) {
			return h.handleGetAlert(testCtx(), makeToolRequest("signoz_get_alert", map[string]any{}))
		}},
		{"delete_alert", func() (*mcp.CallToolResult, error) {
			return h.handleDeleteAlert(testCtx(), makeToolRequest("signoz_delete_alert", map[string]any{}))
		}},
		{"get_alert_history", func() (*mcp.CallToolResult, error) {
			return h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{"timeRange": "1h"}))
		}},
		{"update_alert", func() (*mcp.CallToolResult, error) {
			return h.handleUpdateAlert(testCtx(), makeToolRequest("signoz_update_alert", map[string]any{"alert": "x"}))
		}},
		{"get_dashboard", func() (*mcp.CallToolResult, error) {
			return h.handleGetDashboard(testCtx(), makeToolRequest("signoz_get_dashboard", map[string]any{}))
		}},
		{"delete_dashboard", func() (*mcp.CallToolResult, error) {
			return h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{}))
		}},
		{"update_dashboard", func() (*mcp.CallToolResult, error) {
			return h.handleUpdateDashboard(testCtx(), makeToolRequest("signoz_update_dashboard", map[string]any{
				"dashboard": map[string]any{"title": "T", "layout": []any{}, "widgets": []any{}},
			}))
		}},
		{"get_view", func() (*mcp.CallToolResult, error) {
			return h.handleGetView(testCtx(), makeToolRequest("signoz_get_view", map[string]any{}))
		}},
		{"delete_view", func() (*mcp.CallToolResult, error) {
			return h.handleDeleteView(testCtx(), makeToolRequest("signoz_delete_view", map[string]any{}))
		}},
		{"update_view", func() (*mcp.CallToolResult, error) {
			return h.handleUpdateView(testCtx(), makeToolRequest("signoz_update_view", map[string]any{
				"view": map[string]any{"name": "n", "sourcePage": "logs", "compositeQuery": map[string]any{}},
			}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call()
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if res == nil || !res.IsError {
				t.Fatalf("expected an error result when neither id nor legacy key supplied, got: %+v", res)
			}
			// Every missing-id presence check returns
			// errorWithCode(CodeValidationFailed, ...). Pin the code so a
			// regression to an uncoded error (e.g. plain NewToolResultError) is
			// caught, not just the IsError flag.
			if code := resultCode(t, res); code != CodeValidationFailed {
				t.Fatalf("%s: missing-id code = %q, want %q", tc.name, code, CodeValidationFailed)
			}
			text, ok := mcp.AsTextContent(res.Content[0])
			if !ok {
				t.Fatalf("error content is not text")
			}
			if !strings.Contains(text.Text, `"id"`) {
				t.Fatalf("error should mention the canonical \"id\" param, got: %s", text.Text)
			}
			// The legacy alias is a SILENT back-compat fallback: the error must
			// steer callers to the canonical "id" and must NOT advertise the alias.
			if strings.Contains(strings.ToLower(text.Text), "legacy") {
				t.Fatalf("error must not advertise a legacy alias, got: %s", text.Text)
			}
		})
	}
}

// TestUpdateStructs_IDNotSchemaRequired pins that the typed update_alert /
// update_dashboard input schemas advertise BOTH the canonical "id" and the
// legacy alias key (ruleId/uuid) as OPTIONAL (non-required) properties. This is
// the schema-aware-client contract: WithInputSchema emits
// additionalProperties:false, so unless the legacy key is itself an advertised
// property, a legacy-only call would be rejected before readResourceID's
// runtime fallback ever runs. Neither key may be in the required list (exactly
// one is supplied; the handler validates presence).
func TestUpdateStructs_IDNotSchemaRequired(t *testing.T) {
	cases := []struct {
		name      string
		legacyKey string
		tool      mcp.Tool
	}{
		{"update_alert", "ruleId", mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]())},
		{"update_dashboard", "uuid", mcp.NewTool("signoz_update_dashboard", mcp.WithInputSchema[types.UpdateDashboardInput]())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := inputSchemaProperties(t, tc.tool)
			if _, ok := props["id"]; !ok {
				t.Fatalf("%s: id must remain an advertised property: %#v", tc.name, props)
			}
			if _, ok := props[tc.legacyKey]; !ok {
				t.Fatalf("%s: legacy alias %q must be an advertised property so additionalProperties:false does not reject a legacy-only call: %#v", tc.name, tc.legacyKey, props)
			}
			required := inputSchemaRequiredFields(t, tc.tool)
			if containsString(required, "id") {
				t.Fatalf("%s: id must NOT be in the required list (legacy-only calls must stay schema-valid), got: %#v", tc.name, required)
			}
			if containsString(required, tc.legacyKey) {
				t.Fatalf("%s: legacy alias %q must NOT be required, got: %#v", tc.name, tc.legacyKey, required)
			}
		})
	}
}
