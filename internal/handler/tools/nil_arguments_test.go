package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// requestWithNilArguments builds a CallToolRequest whose Arguments field is an
// untyped nil interface — exactly what the MCP framework delivers when a client
// invokes a tool with no "arguments" object at all. This is distinct from an
// empty map[string]any{}: an untyped nil fails a bare
// req.Params.Arguments.(map[string]any) assertion and previously panicked with
// "interface conversion: interface {} is nil, not map[string]interface {}".
func requestWithNilArguments(toolName string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: toolName},
	}
}

// TestHandlers_NilArguments_NoPanic is a regression guard for the nil-Arguments
// panic observed in production (signoz_get_alert / signoz_get_dashboard tool
// handlers). Every handler that requires at least one argument must return a
// validation error result — never panic — when invoked with no arguments.
func TestHandlers_NilArguments_NoPanic(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	cases := []struct {
		tool    string
		handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"signoz_get_alert", h.handleGetAlert},
		{"signoz_get_alert_history", h.handleGetAlertHistory},
		{"signoz_delete_alert", h.handleDeleteAlert},
		{"signoz_get_dashboard", h.handleGetDashboard},
		{"signoz_delete_dashboard", h.handleDeleteDashboard},
		{"signoz_get_trace_details", h.handleGetTraceDetails},
		{"signoz_get_service_top_operations", h.handleGetServiceTopOperations},
		{"signoz_query_metrics", h.handleQueryMetrics},
		{"signoz_create_notification_channel", h.handleCreateNotificationChannel},
		{"signoz_get_notification_channel", h.handleGetNotificationChannel},
		{"signoz_update_notification_channel", h.handleUpdateNotificationChannel},
		{"signoz_delete_notification_channel", h.handleDeleteNotificationChannel},
		{"signoz_check_metric_usage", h.handleCheckMetricUsage},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			// A panic here fails the test, which is the primary thing we guard.
			result, err := tc.handler(testCtx(), requestWithNilArguments(tc.tool))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if result == nil {
				t.Fatal("expected a non-nil result")
			}
			if !result.IsError {
				t.Fatalf("expected a validation error result for missing arguments, got a success result")
			}
			// These tools all require at least one argument, so a no-arguments
			// call must surface the SPECIFIC missing-parameter message (e.g.
			// `"ruleId" cannot be empty`), NOT the generic JSON-object guard.
			// requireArgsMap maps the untyped-nil payload to an empty map so the
			// per-field check owns the diagnosis; this assertion locks that in
			// (a bare IsError check passes for the generic guard too, which is
			// why the production regression slipped past it).
			if msg := resultText(t, result); msg == notAJSONObjectMessage {
				t.Fatalf("no-arguments call returned the generic JSON-object guard %q; want a specific per-field validation message", msg)
			}
		})
	}
}

// TestListHandlers_NilArguments_UseDefaults locks in the behavior that list-style
// tools (whose arguments are all optional) succeed via their defaults when called
// with no arguments — they must NOT be turned into a validation error by the
// nil-Arguments fix. Same untyped-nil Arguments that previously panicked.
func TestListHandlers_NilArguments_UseDefaults(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"labels":{"alertname":"A","ruleId":"1","severity":"critical"},"startsAt":"","endsAt":"","status":{"state":"firing"}}]}`), nil
		},
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			return json.RawMessage(`[{"serviceName":"svc"}]`), nil
		},
		ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[]}`), nil
		},
		GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			return json.RawMessage(`{"metrics":[]}`), nil
		},
	}
	h := newTestHandler(mock)

	cases := []struct {
		tool    string
		handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"signoz_list_alerts", h.handleListAlerts},
		{"signoz_list_services", h.handleListServices},
		{"signoz_list_metrics", h.handleListMetrics},
		{"signoz_get_top_metrics", h.handleGetTopMetrics},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := tc.handler(testCtx(), requestWithNilArguments(tc.tool))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if result == nil {
				t.Fatal("expected a non-nil result")
			}
			if result.IsError {
				t.Fatalf("list tool with no arguments must succeed via defaults, got error result: %v", result.Content)
			}
		})
	}
}
