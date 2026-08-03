package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleListServices_AddsWebURL(t *testing.T) {
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			return json.RawMessage(`[{"serviceName":"cart service"}]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_services", map[string]any{"timeRange": "1h"})

	result, err := h.handleListServices(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/services/cart%20service"`) {
		t.Fatalf("expected encoded service webUrl, got: %s", body)
	}
}

func TestHandleListServices_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			return json.RawMessage(`[{"serviceName":"cart service"}]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_services", map[string]any{"timeRange": "1h"})

	result, err := h.handleListServices(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

func TestHandleListServices_ExplicitStartEndOverrideTimeRange(t *testing.T) {
	var capturedStart string
	var capturedEnd string
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`[]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_services", map[string]any{
		"timeRange": "1h",
		"start":     "1711123200000000000",
		"end":       "1711130400000000000",
	})

	result, err := h.handleListServices(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedStart != "1711123200000000000" {
		t.Fatalf("start = %q, want explicit start", capturedStart)
	}
	if capturedEnd != "1711130400000000000" {
		t.Fatalf("end = %q, want explicit end", capturedEnd)
	}
}

// TestHandleListServices_NanosecondBackwardCompat pins that a legacy caller
// passing nanosecond timestamps to list_services still gets nanoseconds at the
// client boundary after the ms→auto-detect migration.
func TestHandleListServices_NanosecondBackwardCompat(t *testing.T) {
	var capturedStart, capturedEnd string
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			capturedStart, capturedEnd = start, end
			return json.RawMessage(`[]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_services", map[string]any{
		"start": "1711123200000000000",
		"end":   "1711130400000000000",
	})
	if _, err := h.handleListServices(testCtx(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStart != "1711123200000000000" || capturedEnd != "1711130400000000000" {
		t.Fatalf("ns values must round-trip to the services client unchanged: start=%s end=%s", capturedStart, capturedEnd)
	}
}

// TestHandleGetServiceTopOperations_NanosecondBackwardCompat pins the same ns
// backward-compat contract for the top-operations service tool.
func TestHandleGetServiceTopOperations_NanosecondBackwardCompat(t *testing.T) {
	var capturedStart, capturedEnd string
	mock := &client.MockClient{
		GetServiceTopOperationsFn: func(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error) {
			capturedStart, capturedEnd = start, end
			return json.RawMessage(`[]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_service_top_operations", map[string]any{
		"service": "frontend",
		"start":   "1711123200000000000",
		"end":     "1711130400000000000",
	})
	if _, err := h.handleGetServiceTopOperations(testCtx(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStart != "1711123200000000000" || capturedEnd != "1711130400000000000" {
		t.Fatalf("ns values must round-trip to the top-operations client unchanged: start=%s end=%s", capturedStart, capturedEnd)
	}
}

const serviceMapFixture = `[
	{"parent":"frontend","child":"checkout","callCount":120,"callRate":3.3,"errorRate":1.2,"p99":420.5,"p50":120.4},
	{"parent":"checkout","child":"payments","callCount":80,"callRate":2.2,"errorRate":9.7,"p99":900.1,"p50":300.0},
	{"parent":"cron","child":"reporting","callCount":5,"callRate":0.1,"errorRate":0,"p99":50,"p50":20}
]`

func serviceMapHandler(t *testing.T, payload string) *Handler {
	t.Helper()
	return newTestHandler(&client.MockClient{
		GetServiceMapFn: func(ctx context.Context, start, end string, tags json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		},
	})
}

func TestHandleGetServiceMap_ReturnsAllEdgesUnfiltered(t *testing.T) {
	h := serviceMapHandler(t, serviceMapFixture)
	req := makeToolRequest("signoz_get_service_map", map[string]any{"timeRange": "1h"})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"total":3`) {
		t.Fatalf("expected all 3 edges, got: %s", body)
	}
	if !strings.Contains(body, `"parent":"frontend"`) || !strings.Contains(body, `"p99":420.5`) {
		t.Fatalf("expected upstream edge fields preserved verbatim, got: %s", body)
	}
}

func TestHandleGetServiceMap_FiltersByServiceAndDirection(t *testing.T) {
	tests := []struct {
		name       string
		direction  string
		wantTotal  string
		wantEdge   string
		unwantEdge string
	}{
		{"downstream keeps callees", "downstream", `"total":1`, `"child":"payments"`, `"parent":"frontend"`},
		{"upstream keeps callers", "upstream", `"total":1`, `"parent":"frontend"`, `"child":"payments"`},
		{"both keeps either side", "both", `"total":2`, `"child":"payments"`, `"parent":"cron"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := serviceMapHandler(t, serviceMapFixture)
			req := makeToolRequest("signoz_get_service_map", map[string]any{
				"service":   "checkout",
				"direction": tc.direction,
			})

			result, err := h.handleGetServiceMap(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handler returned error result: %s", textContent(t, result))
			}
			body := textContent(t, result)
			if !strings.Contains(body, tc.wantTotal) {
				t.Fatalf("expected %s, got: %s", tc.wantTotal, body)
			}
			if !strings.Contains(body, tc.wantEdge) {
				t.Fatalf("expected edge %s, got: %s", tc.wantEdge, body)
			}
			if strings.Contains(body, tc.unwantEdge) {
				t.Fatalf("expected %s to be filtered out, got: %s", tc.unwantEdge, body)
			}
		})
	}
}

// direction is meaningless without a service; silently ignoring it would let an
// agent believe it received a filtered graph.
func TestHandleGetServiceMap_RejectsDirectionWithoutService(t *testing.T) {
	h := serviceMapHandler(t, serviceMapFixture)
	req := makeToolRequest("signoz_get_service_map", map[string]any{"direction": "upstream"})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected validation error, got success: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "direction") {
		t.Fatalf("expected error to name the direction field, got: %s", textContent(t, result))
	}
}

func TestHandleGetServiceMap_RejectsUnknownDirection(t *testing.T) {
	h := serviceMapHandler(t, serviceMapFixture)
	req := makeToolRequest("signoz_get_service_map", map[string]any{
		"service":   "checkout",
		"direction": "sideways",
	})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected validation error for unknown direction, got: %s", textContent(t, result))
	}
}

// /api/v1/dependency_graph is undocumented and answers with a bare array today.
// If it ever starts using SigNoz's render.Success envelope we must still return
// edges rather than a silent empty graph.
func TestHandleGetServiceMap_AcceptsWrappedEnvelope(t *testing.T) {
	h := serviceMapHandler(t, `{"status":"success","data":[{"parent":"frontend","child":"checkout","callCount":9}]}`)
	req := makeToolRequest("signoz_get_service_map", map[string]any{})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"child":"checkout"`) {
		t.Fatalf("expected wrapped envelope to be unwrapped into edges, got: %s", body)
	}
}

func TestHandleGetServiceMap_ReportsUnparseableResponse(t *testing.T) {
	h := serviceMapHandler(t, `not json at all`)
	req := makeToolRequest("signoz_get_service_map", map[string]any{})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result for an unparseable response, got: %s", textContent(t, result))
	}
}

func TestHandleGetServiceMap_RejectsMalformedExplicitTimestamps(t *testing.T) {
	h := serviceMapHandler(t, serviceMapFixture)
	req := makeToolRequest("signoz_get_service_map", map[string]any{
		"start": "not-a-timestamp",
		"end":   "1641081600000",
	})

	result, err := h.handleGetServiceMap(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected validation error for malformed start, got: %s", textContent(t, result))
	}
}
