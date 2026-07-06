package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleGetTopMetrics_ReturnsSamples(t *testing.T) {
	calls := 0
	mock := &client.MockClient{
		GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"status":"success","data":{"samples":[
				{"metricName":"k8s.node.condition","percentage":8.13,"totalValue":45235627},
				{"metricName":"http_requests_total","percentage":5.20,"totalValue":28918400}
			]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_top_metrics", map[string]any{})

	result, err := h.handleGetTopMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 client call, got %d", calls)
	}
}

func TestHandleGetTopMetrics_DefaultTimeRange(t *testing.T) {
	var capturedStart, capturedEnd int64
	var capturedLimit int
	mock := &client.MockClient{
		GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			capturedLimit = limit
			return json.RawMessage(`{"status":"success","data":{"samples":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_top_metrics", map[string]any{})

	result, err := h.handleGetTopMetrics(testCtx(), req)
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
	if capturedLimit != 100 {
		t.Fatalf("expected limit=100, got %d", capturedLimit)
	}
}

func TestHandleGetTopMetrics_ExplicitStartEnd(t *testing.T) {
	var capturedStart, capturedEnd int64
	mock := &client.MockClient{
		GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`{"status":"success","data":{"samples":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_top_metrics", map[string]any{
		"start": "1711123200000",
		"end":   "1711130400000",
	})

	result, err := h.handleGetTopMetrics(testCtx(), req)
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
