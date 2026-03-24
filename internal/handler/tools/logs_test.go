package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleSearchLogs_BasicQuery(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success","result":[{"logs":"data"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_logs", map[string]any{
		"query":     "status_code >= 400",
		"timeRange": "1h",
	})

	result, err := h.handleSearchLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
}

func TestHandleSearchLogs_ServiceFilter(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_logs", map[string]any{
		"service":   "payment-svc",
		"severity":  "ERROR",
		"timeRange": "30m",
	})

	result, err := h.handleSearchLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
	// The filter should include both service.name and severity_text
	payload := string(captured)
	if !strings.Contains(payload, "payment-svc") {
		t.Errorf("expected payload to contain service name, got: %s", payload)
	}
}

func TestHandleSearchLogs_SearchText(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_logs", map[string]any{
		"searchText": "timeout",
		"timeRange":  "1h",
	})

	result, err := h.handleSearchLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
}

func TestHandleSearchLogs_InvalidLimit(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_logs", map[string]any{
		"limit":     "not-a-number",
		"timeRange": "1h",
	})

	result, err := h.handleSearchLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid limit")
	}
}

func TestHandleAggregateLogs_Count(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success","result":[{"value":42}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_logs", map[string]any{
		"aggregation": "count",
		"service":     "auth-svc",
		"timeRange":   "1h",
	})

	result, err := h.handleAggregateLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if !called {
		t.Fatal("QueryBuilderV5 was not called")
	}
}

func TestHandleAggregateLogs_MissingAggregation(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_logs", map[string]any{
		"timeRange": "1h",
	})

	result, err := h.handleAggregateLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing aggregation")
	}
}

func TestHandleAggregateLogs_AvgRequiresAggregateOn(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_logs", map[string]any{
		"aggregation": "avg",
		"timeRange":   "1h",
	})

	result, err := h.handleAggregateLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when aggregateOn is missing for avg")
	}
}

func TestHandleAggregateLogs_WithGroupBy(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_logs", map[string]any{
		"aggregation": "count",
		"groupBy":     "service.name, severity_text",
		"timeRange":   "1h",
	})

	result, err := h.handleAggregateLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if !called {
		t.Fatal("QueryBuilderV5 was not called")
	}
}

func TestHandleListLogViews(t *testing.T) {
	mock := &client.MockClient{
		ListLogViewsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"id":"v1","name":"Error Logs"},{"id":"v2","name":"Info Logs"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_log_views", map[string]any{})

	result, err := h.handleListLogViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
}

func TestHandleGetLogView(t *testing.T) {
	var capturedID string
	mock := &client.MockClient{
		GetLogViewFn: func(ctx context.Context, viewID string) (json.RawMessage, error) {
			capturedID = viewID
			return json.RawMessage(`{"id":"view-123","name":"Error Logs"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_log_view", map[string]any{
		"viewId": "view-123",
	})

	result, err := h.handleGetLogView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedID != "view-123" {
		t.Errorf("expected viewId=view-123, got %q", capturedID)
	}
}

func TestHandleGetLogView_EmptyViewId(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_log_view", map[string]any{
		"viewId": "",
	})

	result, err := h.handleGetLogView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty viewId")
	}
}
