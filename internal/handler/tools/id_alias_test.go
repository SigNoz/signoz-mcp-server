package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
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
			// The top-level id/viewId path param must not leak into the body.
			var parsed map[string]any
			if err := json.Unmarshal(capturedBody, &parsed); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if _, present := parsed["viewId"]; present {
				t.Error(`"viewId" should not leak into the view body`)
			}
		})
	}
}
