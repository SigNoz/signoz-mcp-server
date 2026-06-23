package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
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

func TestHandleQueryMetrics_BackendWarningsInDecisionsBlockAndWarnLog(t *testing.T) {
	const warningMessage = "Key http.status_code is ambiguous"
	var logs bytes.Buffer
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"meta":{"stepIntervals":{"A":60}},"warning":{"warnings":[{"message":"` + warningMessage + `"}]}}}`), nil
		},
	}
	h := newTestHandler(mock)
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	req := makeToolRequest("signoz_query_metrics", map[string]any{
		"metricName":  "system.cpu.time",
		"metricType":  "gauge",
		"timeRange":   "1h",
		"requestType": "time_series",
	})

	result, err := h.handleQueryMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	wantLine := "WARNING: backend: " + warningMessage
	if !strings.Contains(body, "[Decisions applied]\n") || !strings.Contains(body, wantLine) {
		t.Fatalf("metrics result missing backend warning in decisions block; want %q in:\n%s", wantLine, body)
	}
	if strings.Index(body, wantLine) > strings.Index(body, "---\n") {
		t.Fatalf("backend warning appears after decisions block delimiter; body:\n%s", body)
	}
	if gotLogs := logs.String(); !strings.Contains(gotLogs, "level=WARN") || !strings.Contains(gotLogs, "SigNoz query builder returned non-fatal warnings") || !strings.Contains(gotLogs, "warningCount=1") {
		t.Fatalf("expected WARN log with warningCount, got %q", gotLogs)
	}
}
