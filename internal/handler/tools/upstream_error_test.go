package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// TestUpstreamErrorPrefix_NonQueryBuilderHandlers locks in N9 (#364): every
// user-visible upstream client-call failure must surface the uniform
// "SigNoz API error:" prefix, not a bare or bespoke message. The e2e-tagged
// suite covers this against a live backend; this mock-driven test pins the
// contract per-PR for a representative spread of non-QueryBuilder handlers
// (list + single-get across alerts, dashboards, services, fields, channels).
func TestUpstreamErrorPrefix_NonQueryBuilderHandlers(t *testing.T) {
	const upstreamMsg = "connection refused"
	wantPrefix := "SigNoz API error:"

	cases := []struct {
		name   string
		mock   *client.MockClient
		invoke func(*Handler) (isErr bool, text string)
	}{
		{
			name: "list_dashboards",
			mock: &client.MockClient{ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleListDashboards(testCtx(), makeToolRequest("signoz_list_dashboards", map[string]any{}))
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "get_alert",
			mock: &client.MockClient{GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleGetAlert(testCtx(), makeToolRequest("signoz_get_alert", map[string]any{"ruleId": "01900000-0000-7000-8000-000000000000"}))
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "list_services",
			mock: &client.MockClient{ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleListServices(testCtx(), makeToolRequest("signoz_list_services", map[string]any{}))
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "get_field_keys",
			mock: &client.MockClient{GetFieldKeysFn: func(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleGetFieldKeys(testCtx(), makeToolRequest("signoz_get_field_keys", map[string]any{"signal": "logs"}))
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "get_notification_channel",
			mock: &client.MockClient{GetNotificationChannelFn: func(ctx context.Context, id string) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleGetNotificationChannel(testCtx(), makeToolRequest("signoz_get_notification_channel", map[string]any{"id": "abc"}))
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "list_alerts",
			mock: &client.MockClient{ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
				return nil, errors.New(upstreamMsg)
			}},
			invoke: func(h *Handler) (bool, string) {
				r, _ := h.handleListAlerts(testCtx(), makeToolRequest("signoz_list_alerts", map[string]any{}))
				return r.IsError, textContent(t, r)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(tc.mock)
			isErr, text := tc.invoke(h)
			if !isErr {
				t.Fatalf("%s: expected an error result, got success: %s", tc.name, text)
			}
			if !strings.HasPrefix(text, wantPrefix) {
				t.Fatalf("%s: error text = %q, want %q prefix", tc.name, text, wantPrefix)
			}
			if !strings.Contains(text, upstreamMsg) {
				t.Fatalf("%s: error text = %q, want it to preserve the underlying message %q", tc.name, text, upstreamMsg)
			}
		})
	}
}

// TestUpstreamErrorPrefix_FormulaMetadataFetchFailure pins the round-2 fix:
// resolveFormulaSubQuery's metadata auto-fetch (client.ListMetrics) is an
// upstream call, so when it fails the tool result must carry the uniform
// "SigNoz API error:" prefix — even though sibling error paths in the same
// function ("metric not found"/"validation error") are local and stay raw.
//
// The primary metric "A" provides metricType so it does NOT auto-fetch; only
// the formula sub-query "B" triggers the ListMetrics call, which we fail.
func TestUpstreamErrorPrefix_FormulaMetadataFetchFailure(t *testing.T) {
	mock := &client.MockClient{
		ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
			return nil, errors.New("connection refused")
		},
		// QueryBuilderV5 must never be reached — the sub-query resolution fails first.
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			t.Fatal("QueryBuilderV5 should not be called when formula metadata fetch fails")
			return nil, nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_query_metrics", map[string]any{
		"metricName":  "system.cpu.time",
		"metricType":  "gauge", // primary "A" provided -> no auto-fetch
		"timeRange":   "1h",
		"requestType": "time_series",
		"formula":     "A / B",
		"formulaQueries": []any{map[string]any{
			"name":       "B",
			"metricName": "system.memory.usage",
			// metricType omitted -> triggers the auto-fetch (ListMetrics) that fails
		}},
	})

	result, err := h.handleQueryMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	text := textContent(t, result)
	if !result.IsError {
		t.Fatalf("expected an error result, got success: %s", text)
	}
	if !strings.HasPrefix(text, "SigNoz API error:") {
		t.Fatalf("formula metadata-fetch failure should carry the upstream prefix; got %q", text)
	}
	if !strings.Contains(text, "connection refused") {
		t.Fatalf("error should preserve the underlying upstream message; got %q", text)
	}
}

// TestQueryMetrics_FormulaMetricNotFoundStaysLocal is the inverse guard: when
// the metadata fetch SUCCEEDS but the metric is absent from the response, that
// is a local "metric not found" validation error and must NOT be relabeled as
// an upstream failure.
func TestQueryMetrics_FormulaMetricNotFoundStaysLocal(t *testing.T) {
	mock := &client.MockClient{
		ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
			// Successful upstream response, but no metrics -> meta == nil -> local error.
			return json.RawMessage(`{"status":"success","data":{"metrics":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_query_metrics", map[string]any{
		"metricName":  "system.cpu.time",
		"metricType":  "gauge",
		"timeRange":   "1h",
		"requestType": "time_series",
		"formula":     "A / B",
		"formulaQueries": []any{map[string]any{
			"name":       "B",
			"metricName": "does.not.exist",
		}},
	})

	result, err := h.handleQueryMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	text := textContent(t, result)
	if !result.IsError {
		t.Fatalf("expected an error result, got success: %s", text)
	}
	if strings.HasPrefix(text, "SigNoz API error:") {
		t.Fatalf("a local 'metric not found' error must NOT carry the upstream prefix; got %q", text)
	}
	if !strings.Contains(text, "not found") {
		t.Fatalf("expected a 'metric not found' local error; got %q", text)
	}
}

// TestUpstreamErrorPrefix_NotForLocalErrors confirms the inverse: a local
// validation failure (missing required arg) does NOT get the upstream prefix —
// it stays on the "Parameter validation failed:" prefix so the LLM can tell a
// fixable parameter mistake apart from a retryable backend failure.
func TestUpstreamErrorPrefix_NotForLocalErrors(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	r, _ := h.handleGetAlert(testCtx(), makeToolRequest("signoz_get_alert", map[string]any{}))
	text := textContent(t, r)
	if !r.IsError {
		t.Fatalf("expected an error result, got success: %s", text)
	}
	if strings.HasPrefix(text, "SigNoz API error:") {
		t.Fatalf("local validation error must NOT carry the upstream prefix; got %q", text)
	}
	if !strings.HasPrefix(text, "Parameter validation failed:") {
		t.Fatalf("local validation error should keep the validation prefix; got %q", text)
	}
}
