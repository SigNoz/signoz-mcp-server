package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

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

// TestHandleQueryMetrics_JSONFirstWithSeparateDecisionsNote pins the JSON-first
// contract for query_metrics: the raw backend payload is
// content block 0 (independently json.Unmarshal-able, matching the
// search/aggregate siblings), and the decisions + backend-warning advisory is a
// SEPARATE trailing block — never prepended into the JSON. query_metrics is a
// raw QB passthrough, so block 0 must stay text-only (no structuredContent).
func TestHandleQueryMetrics_JSONFirstWithSeparateDecisionsNote(t *testing.T) {
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

	// Two content blocks: JSON payload, then the decisions/warnings note.
	if len(result.Content) != 2 {
		t.Fatalf("want 2 content blocks (JSON + note), got %d: %#v", len(result.Content), result.Content)
	}

	// Passthrough stays text-only — no structuredContent.
	if result.StructuredContent != nil {
		t.Fatalf("query_metrics is a raw passthrough; want no structuredContent, got %#v", result.StructuredContent)
	}

	// Block 0 must be independently parseable JSON, with no prose preamble.
	block0, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("block 0 is %T, want text content", result.Content[0])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(block0.Text), &parsed); err != nil {
		t.Fatalf("block 0 must be valid JSON, got %q (err: %v)", block0.Text, err)
	}
	if strings.Contains(block0.Text, "[Decisions applied]") || strings.Contains(block0.Text, "---") {
		t.Fatalf("block 0 must not contain the decisions preamble; got %q", block0.Text)
	}

	// Block 1 is the decisions/warnings note carrying the backend warning.
	block1, ok := mcp.AsTextContent(result.Content[1])
	if !ok {
		t.Fatalf("block 1 is %T, want text content", result.Content[1])
	}
	wantLine := "WARNING: backend: " + warningMessage
	if !strings.Contains(block1.Text, "[Decisions applied]") || !strings.Contains(block1.Text, wantLine) {
		t.Fatalf("note block missing decisions header or backend warning; want %q in:\n%s", wantLine, block1.Text)
	}

	if gotLogs := logs.String(); !strings.Contains(gotLogs, "level=WARN") || !strings.Contains(gotLogs, "SigNoz query builder returned non-fatal warnings") || !strings.Contains(gotLogs, "warningCount=1") {
		t.Fatalf("expected WARN log with warningCount, got %q", gotLogs)
	}
}
