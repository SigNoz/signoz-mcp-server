package tools

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// These tests pin the K5 contract: every CRUD resource id is read from the
// canonical "id" param. Alert and view legacy keys remain aliases; dashboard
// tools make the v0.142.0 hard cut and reject uuid with a correction to id.

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
				ListNotificationChannelsV2Fn: func(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
					return listedNotificationChannels("slack-alerts"), nil
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

func TestDashboardCanonicalIDAndLegacyAliasRejection(t *testing.T) {
	called := false
	mock := &client.MockClient{
		GetDashboardFn:       func(context.Context, string) (json.RawMessage, error) { called = true; return nil, nil },
		UpdateDashboardRawFn: func(context.Context, string, []byte) (json.RawMessage, error) { called = true; return nil, nil },
		PatchDashboardRawFn:  func(context.Context, string, []byte) (json.RawMessage, error) { called = true; return nil, nil },
		DeleteDashboardFn:    func(context.Context, string) error { called = true; return nil },
	}
	h := newTestHandler(mock)

	tests := []struct {
		name string
		call func(map[string]any) (*mcp.CallToolResult, error)
		args map[string]any
	}{
		{"get", func(args map[string]any) (*mcp.CallToolResult, error) {
			return h.handleGetDashboard(testCtx(), makeToolRequest("signoz_get_dashboard", args))
		}, map[string]any{}},
		{"update", func(args map[string]any) (*mcp.CallToolResult, error) {
			return h.handleUpdateDashboard(testCtx(), makeToolRequest("signoz_update_dashboard", args))
		}, map[string]any{"schemaVersion": "v6", "name": "d1", "tags": []any{}, "spec": map[string]any{}}},
		{"patch", func(args map[string]any) (*mcp.CallToolResult, error) {
			return h.handlePatchDashboard(testCtx(), makeToolRequest("signoz_patch_dashboard", args))
		}, map[string]any{"patch": []any{}}},
		{"delete", func(args map[string]any) (*mcp.CallToolResult, error) {
			return h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", args))
		}, map[string]any{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := maps.Clone(tc.args)
			args["id"] = "d1"
			args["uuid"] = "legacy"
			result, err := tc.call(args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError || !strings.Contains(resultText(t, result), `"uuid" is no longer accepted`) {
				t.Fatalf("expected canonical-id correction, got: %v", result.Content)
			}
		})
	}
	if called {
		t.Fatal("legacy dashboard uuid must be rejected before any upstream call")
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
					return json.RawMessage(`{"data":{"source":"logs"}}`), nil
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
					"id":     "v1",
					"name":   "My View",
					"source": "logs",
					"spec": map[string]any{
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
				"view": map[string]any{"name": "n", "source": "logs", "spec": map[string]any{}},
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

// TestUpdateStructs_IDNotSchemaRequired pins the remaining alert alias and the
// dashboard hard cut. Dashboard update advertises only canonical id; both
// handlers validate id presence at runtime.
func TestUpdateStructs_IDNotSchemaRequired(t *testing.T) {
	cases := []struct {
		name        string
		legacyKey   string
		allowLegacy bool
		tool        mcp.Tool
	}{
		{"update_alert", "ruleId", true, mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]())},
		{"update_dashboard", "uuid", false, mcp.NewTool("signoz_update_dashboard", rawInputSchema(updateDashboardSchema))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := inputSchemaProperties(t, tc.tool)
			if _, ok := props["id"]; !ok {
				t.Fatalf("%s: id must remain an advertised property: %#v", tc.name, props)
			}
			_, hasLegacy := props[tc.legacyKey]
			if hasLegacy != tc.allowLegacy {
				t.Fatalf("%s: legacy alias %q advertised=%v, want %v: %#v", tc.name, tc.legacyKey, hasLegacy, tc.allowLegacy, props)
			}
			required := inputSchemaRequiredFields(t, tc.tool)
			if containsString(required, "id") {
				t.Fatalf("%s: id must NOT be in the required list (legacy-only calls must stay schema-valid), got: %#v", tc.name, required)
			}
			if tc.allowLegacy && containsString(required, tc.legacyKey) {
				t.Fatalf("%s: legacy alias %q must NOT be required, got: %#v", tc.name, tc.legacyKey, required)
			}
		})
	}
}
