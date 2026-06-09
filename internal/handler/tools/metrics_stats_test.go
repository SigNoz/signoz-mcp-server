package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleGetMetricsStats_FetchesAllViaProbe(t *testing.T) {
	calls := 0
	mock := &client.MockClient{
		GetMetricsStatsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			calls++
			if calls == 1 {
				// probe call — return total count
				return json.RawMessage(`{"data":{"metrics":[{"metricName":"http_requests_total","samples":1000000,"type":"sum"}],"total":3}}`), nil
			}
			// full fetch — return all metrics
			return json.RawMessage(`{"data":{"metrics":[
				{"metricName":"http_requests_total","samples":1000000,"type":"sum"},
				{"metricName":"system.cpu.time","samples":500000,"type":"gauge"},
				{"metricName":"k8s.pod.phase","samples":200000,"type":"gauge"}
			],"total":3}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_metrics_stats", map[string]any{})

	result, err := h.handleGetMetricsStats(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if calls != 2 {
		t.Fatalf("expected 2 client calls (probe + full fetch), got %d", calls)
	}
}

func TestHandleGetMetricsStats_DefaultTimeRange(t *testing.T) {
	var capturedStart, capturedEnd int64
	calls := 0
	mock := &client.MockClient{
		GetMetricsStatsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			calls++
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`{"data":{"metrics":[],"total":0}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_metrics_stats", map[string]any{})

	result, err := h.handleGetMetricsStats(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedStart == 0 || capturedEnd == 0 {
		t.Fatalf("expected non-zero start/end from default timeRange; got start=%d end=%d", capturedStart, capturedEnd)
	}
	if capturedEnd <= capturedStart {
		t.Fatalf("end (%d) must be after start (%d)", capturedEnd, capturedStart)
	}
}

func TestHandleGetMetricsStats_ExplicitStartEnd(t *testing.T) {
	var capturedStart, capturedEnd int64
	mock := &client.MockClient{
		GetMetricsStatsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`{"data":{"metrics":[],"total":0}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_metrics_stats", map[string]any{
		"start": "1711123200000",
		"end":   "1711130400000",
	})

	result, err := h.handleGetMetricsStats(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedStart != 1711123200000 {
		t.Fatalf("start = %d, want 1711123200000", capturedStart)
	}
	if capturedEnd != 1711130400000 {
		t.Fatalf("end = %d, want 1711130400000", capturedEnd)
	}
}
