package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleSearchTraces_BasicQuery(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success","result":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{
		"service":   "checkout-svc",
		"timeRange": "1h",
	})

	result, err := h.handleSearchTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
	payload := string(captured)
	if !strings.Contains(payload, "checkout-svc") {
		t.Errorf("expected payload to contain service name, got: %s", payload)
	}
}

func TestHandleSearchTraces_ErrorAndDurationFilters(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{
		"error":       "true",
		"minDuration": "500000000",
		"maxDuration": "2000000000",
		"timeRange":   "30m",
	})

	result, err := h.handleSearchTraces(testCtx(), req)
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

func TestHandleSearchTraces_OperationFilter(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{
		"service":   "api-gw",
		"operation": "GET /users",
		"timeRange": "1h",
	})

	result, err := h.handleSearchTraces(testCtx(), req)
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

func TestHandleAggregateTraces_CountByService(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success","result":[{"value":100}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "count",
		"groupBy":     "service.name",
		"error":       "true",
		"timeRange":   "1h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
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

func TestHandleAggregateTraces_P99Latency(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "p99",
		"aggregateOn": "durationNano",
		"service":     "checkout-svc",
		"timeRange":   "6h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
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

func TestHandleAggregateTraces_MissingAggregation(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"timeRange": "1h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing aggregation")
	}
}

func TestHandleAggregateTraces_InvalidAggregation(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "invalid_agg",
		"timeRange":   "1h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid aggregation")
	}
}

func TestHandleAggregateTraces_TimeSeries(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "count",
		"requestType": "time_series",
		"timeRange":   "24h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
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

func TestHandleGetTraceDetails(t *testing.T) {
	var capturedTraceID string
	var capturedIncludeSpans bool
	mock := &client.MockClient{
		GetTraceDetailsFn: func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
			capturedTraceID = traceID
			capturedIncludeSpans = includeSpans
			return json.RawMessage(`{"traceId":"abc123","spans":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId":      "abc123",
		"includeSpans": "true",
		"timeRange":    "1h",
	})

	result, err := h.handleGetTraceDetails(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedTraceID != "abc123" {
		t.Errorf("expected traceId=abc123, got %q", capturedTraceID)
	}
	if !capturedIncludeSpans {
		t.Error("expected includeSpans=true")
	}
}

func TestHandleGetTraceDetails_ExplicitStartEndOverrideTimeRange(t *testing.T) {
	var capturedStart int64
	var capturedEnd int64
	mock := &client.MockClient{
		GetTraceDetailsFn: func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
			capturedStart = startTime
			capturedEnd = endTime
			return json.RawMessage(`{"traceId":"abc123","spans":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId":      "abc123",
		"includeSpans": "true",
		"timeRange":    "1h",
		"start":        "1711123200000",
		"end":          "1711130400000",
	})

	result, err := h.handleGetTraceDetails(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedStart != 1711123200000 {
		t.Fatalf("start = %d, want explicit start", capturedStart)
	}
	if capturedEnd != 1711130400000 {
		t.Fatalf("end = %d, want explicit end", capturedEnd)
	}
}

func TestHandleGetTraceDetails_EmptyTraceId(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId": "",
	})

	result, err := h.handleGetTraceDetails(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty traceId")
	}
}
