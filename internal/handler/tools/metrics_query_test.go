package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleQueryMetrics_ExplicitStartEndOverrideTimeRange(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success","data":{"meta":{"stepIntervals":{"A":60}}}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_query_metrics", map[string]any{
		"metricName":  "system.cpu.time",
		"metricType":  "gauge",
		"timeRange":   "1h",
		"start":       "1711123200000",
		"end":         "1711130400000",
		"requestType": "time_series",
	})

	result, err := h.handleQueryMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var payload struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	}
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("failed to parse captured query: %v", err)
	}
	if payload.Start != 1711123200000 {
		t.Fatalf("start = %d, want explicit start", payload.Start)
	}
	if payload.End != 1711130400000 {
		t.Fatalf("end = %d, want explicit end", payload.End)
	}
}
