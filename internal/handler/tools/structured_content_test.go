package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

// Family C (#365) two-tier rule:
//   - Code-controlled tools (paginate.Wrap list/summary, single-resource get_*,
//     and synthesized-JSON mutations) carry structuredContent.
//   - Raw QB passthrough tools (search/aggregate/query_metrics) do NOT, because
//     their upstream JSON shape is variable and an outputSchema would be brittle.
//
// These tests pin both halves of that rule so a future change can't silently
// add a brittle outputSchema to a passthrough tool or drop it from a
// code-controlled one.

// handlerFn is the common shape of every MCP tool handler method.
type handlerFn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

func runHandler(t *testing.T, fn handlerFn, req mcp.CallToolRequest) *mcp.CallToolResult {
	t.Helper()
	res, err := fn(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error result: %v", res.Content)
	}
	return res
}

// --- structuredResult helper unit test ---

func TestStructuredResult_CarriesSameJSONInBothRepresentations(t *testing.T) {
	payload := []byte(`{"pagination":{"total":2},"data":[{"id":"a"},{"id":"b"}]}`)
	res := structuredResult(payload)

	if res.IsError {
		t.Fatalf("structuredResult should not be an error result")
	}
	if res.StructuredContent == nil {
		t.Fatalf("structuredResult must populate StructuredContent")
	}
	// Block 0 carries the exact JSON string for back-compat clients.
	if got := textContent(t, res); got != string(payload) {
		t.Fatalf("text block = %q, want %q", got, string(payload))
	}
	// StructuredContent decodes to the same value as the original payload.
	// Compare via re-decode (not marshaled bytes) so map key ordering doesn't
	// make an equal value look different.
	var wantVal, gotVal any
	if err := json.Unmarshal(payload, &wantVal); err != nil {
		t.Fatalf("failed to decode want payload: %v", err)
	}
	gotBytes, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	if err := json.Unmarshal(gotBytes, &gotVal); err != nil {
		t.Fatalf("failed to decode structured content: %v", err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Fatalf("structured content = %#v, want %#v", gotVal, wantVal)
	}
}

func TestStructuredResult_FailsOpenOnInvalidJSON(t *testing.T) {
	// Should never happen for a code-controlled tool, but must not error out.
	res := structuredResult([]byte(`not json`))
	if res.IsError {
		t.Fatalf("structuredResult should fail open, not error")
	}
	if res.StructuredContent != nil {
		t.Fatalf("invalid JSON should not populate StructuredContent, got %#v", res.StructuredContent)
	}
	if got := textContent(t, res); got != "not json" {
		t.Fatalf("text block = %q, want raw payload", got)
	}
}

// --- structuredContent PRESENT on code-controlled tools ---

func TestStructuredContent_PresentOnCodeControlledTools(t *testing.T) {
	const ruleID = "0196634d-5d66-75c4-b778-e317f49dab7a"
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			return json.RawMessage(`[{"serviceName":"frontend"}]`), nil
		},
		ListDashboardsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"uuid":"d1","title":"X"}]}`), nil
		},
		GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"uuid":"d1","title":"X"}}`), nil
		},
		ListViewsFn: func(ctx context.Context, sourcePage, name, category string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"id":"v1","name":"V"}]}`), nil
		},
		GetViewFn: func(ctx context.Context, viewID string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"v1","name":"V"}}`), nil
		},
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"id":"c1","type":"slack","name":"N"}]}`), nil
		},
		GetNotificationChannelFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"c1","type":"slack"}}`), nil
		},
		DeleteNotificationChannelFn: func(ctx context.Context, id string) error { return nil },
		GetAlertByRuleIDFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"` + ruleID + `","alert":"A"}}`), nil
		},
		DeleteAlertRuleFn: func(ctx context.Context, id string) error { return nil },
	}
	h := newTestHandler(mock)

	cases := []struct {
		name string
		fn   handlerFn
		req  mcp.CallToolRequest
	}{
		{"list_services", h.handleListServices, makeToolRequest("signoz_list_services", map[string]any{})},
		{"list_dashboards", h.handleListDashboards, makeToolRequest("signoz_list_dashboards", map[string]any{})},
		{"get_dashboard", h.handleGetDashboard, makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": "d1"})},
		{"list_views", h.handleListViews, makeToolRequest("signoz_list_views", map[string]any{"sourcePage": "logs"})},
		{"get_view", h.handleGetView, makeToolRequest("signoz_get_view", map[string]any{"viewId": "v1"})},
		{"list_notification_channels", h.handleListNotificationChannels, makeToolRequest("signoz_list_notification_channels", map[string]any{})},
		{"get_notification_channel", h.handleGetNotificationChannel, makeToolRequest("signoz_get_notification_channel", map[string]any{"id": "c1"})},
		{"delete_notification_channel", h.handleDeleteNotificationChannel, makeToolRequest("signoz_delete_notification_channel", map[string]any{"id": "c1"})},
		{"get_alert", h.handleGetAlert, makeToolRequest("signoz_get_alert", map[string]any{"ruleId": ruleID})},
		{"delete_alert", h.handleDeleteAlert, makeToolRequest("signoz_delete_alert", map[string]any{"ruleId": ruleID})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runHandler(t, tc.fn, tc.req)
			if res.StructuredContent == nil {
				t.Fatalf("%s: code-controlled tool must populate structuredContent", tc.name)
			}
			// Block 0 must remain valid, independently-parseable JSON.
			var parsed any
			if err := json.Unmarshal([]byte(textContent(t, res)), &parsed); err != nil {
				t.Fatalf("%s: block 0 must be valid JSON: %v", tc.name, err)
			}
		})
	}
}

// --- structuredContent ABSENT on raw QB passthrough tools ---

func TestStructuredContent_AbsentOnPassthroughTools(t *testing.T) {
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"results":[]}}`), nil
		},
	}
	h := newTestHandler(mock)

	cases := []struct {
		name string
		fn   handlerFn
		req  mcp.CallToolRequest
	}{
		{"search_logs", h.handleSearchLogs, makeToolRequest("signoz_search_logs", map[string]any{"timeRange": "1h"})},
		{"aggregate_logs", h.handleAggregateLogs, makeToolRequest("signoz_aggregate_logs", map[string]any{"aggregation": "count", "timeRange": "1h"})},
		{"search_traces", h.handleSearchTraces, makeToolRequest("signoz_search_traces", map[string]any{"timeRange": "1h"})},
		{"aggregate_traces", h.handleAggregateTraces, makeToolRequest("signoz_aggregate_traces", map[string]any{"aggregation": "count", "timeRange": "1h"})},
		{"query_metrics", h.handleQueryMetrics, makeToolRequest("signoz_query_metrics", map[string]any{"metricName": "m", "metricType": "gauge", "timeRange": "1h"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runHandler(t, tc.fn, tc.req)
			if res.StructuredContent != nil {
				t.Fatalf("%s: raw passthrough must NOT populate structuredContent, got %#v", tc.name, res.StructuredContent)
			}
		})
	}
}
