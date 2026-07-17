package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
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
	var parsed types.QueryPayload
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("failed to parse captured query: %v", err)
	}
	spec := parsed.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
	if spec.Limit != types.DefaultRawQueryLimit {
		t.Fatalf("limit = %d, want %d", spec.Limit, types.DefaultRawQueryLimit)
	}
	if len(spec.Order) != 1 || spec.Order[0].Key.Name != "timestamp" || spec.Order[0].Direction != "desc" {
		t.Fatalf("order = %#v, want timestamp desc", spec.Order)
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
	want := "has_error = true AND duration_nano >= 500000000 AND duration_nano <= 2000000000"
	if got := payloadFilterExpression(t, captured); got != want {
		t.Fatalf("payload filter = %q, want %q", got, want)
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
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
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
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
	if got := payloadFilterExpression(t, captured); got != "has_error = true" {
		t.Fatalf("payload filter = %q, want has_error = true", got)
	}
	groupBy := payloadGroupByFields(t, captured)
	if len(groupBy) != 1 {
		t.Fatalf("groupBy count = %d, want 1: %#v", len(groupBy), groupBy)
	}
	if got := groupBy[0]; got.Name != "service.name" || got.FieldContext != "resource" || got.FieldDataType != "string" {
		t.Fatalf("groupBy[0] = %#v, want service.name resource string", got)
	}
	var parsed types.QueryPayload
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("failed to parse captured query: %v", err)
	}
	spec := parsed.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
	if spec.Limit != types.DefaultAggregateQueryLimit {
		t.Fatalf("limit = %d, want %d", spec.Limit, types.DefaultAggregateQueryLimit)
	}
	if len(spec.Order) != 1 || spec.Order[0].Key.Name != "count()" || spec.Order[0].Direction != "desc" {
		t.Fatalf("order = %#v, want count() desc", spec.Order)
	}
}

func TestHandleAggregateTraces_P99Latency(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "p99",
		"aggregateOn": "duration_nano",
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
	if captured == nil {
		t.Fatal("QueryBuilderV5 was not called")
	}
	payload := string(captured)
	if !strings.Contains(payload, `"expression":"p99(duration_nano)"`) {
		t.Fatalf("expected canonical p99 latency aggregation, got: %s", payload)
	}
	if !strings.Contains(payload, `"expression":"service.name = 'checkout-svc'"`) {
		t.Fatalf("expected service shortcut filter, got: %s", payload)
	}
}

func TestHandleAggregateTraces_LegacyFreeFormFieldsPassThrough(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_traces", map[string]any{
		"aggregation": "avg",
		"aggregateOn": "durationNano",
		"filter":      "hasError = true",
		"orderBy":     "avg(durationNano) asc",
		"timeRange":   "6h",
	})

	result, err := h.handleAggregateTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	payload := string(captured)
	for _, want := range []string{"avg(durationNano)", "hasError = true"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("legacy free-form value %q was not preserved in payload: %s", want, payload)
		}
	}
}

func TestHandleSearchTraces_LegacyFreeFormFilterPassesThrough(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{
		"filter":    "hasError = true AND durationNano > 1000",
		"timeRange": "1h",
	})

	result, err := h.handleSearchTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	want := "hasError = true AND durationNano > 1000"
	if got := payloadFilterExpression(t, captured); got != want {
		t.Fatalf("payload filter = %q, want %q", got, want)
	}
}

type payloadSelectField struct {
	Name          string `json:"name"`
	FieldDataType string `json:"fieldDataType"`
	Signal        string `json:"signal"`
	FieldContext  string `json:"fieldContext"`
}

func payloadGroupByFields(t *testing.T, payload []byte) []payloadSelectField {
	t.Helper()
	if len(payload) == 0 {
		t.Fatal("payload was not captured")
	}
	var decoded struct {
		CompositeQuery struct {
			Queries []struct {
				Spec struct {
					GroupBy []payloadSelectField `json:"groupBy"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(decoded.CompositeQuery.Queries) != 1 {
		t.Fatalf("query count = %d, want 1", len(decoded.CompositeQuery.Queries))
	}
	return decoded.CompositeQuery.Queries[0].Spec.GroupBy
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

func TestHandleGetTraceDetails_WrappedBodyGetsWebURL(t *testing.T) {
	mock := &client.MockClient{
		GetTraceDetailsFn: func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"spans":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId": "e4dfc429fd5655656d46a0e9db386296",
	})

	result, err := h.handleGetTraceDetails(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	inner, ok := obj["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected wrapped data object, got: %s", body)
	}
	if inner["webUrl"] != "https://signoz.example.com/trace/e4dfc429fd5655656d46a0e9db386296" {
		t.Fatalf("expected webUrl on inner object, got: %v", inner["webUrl"])
	}
}

func TestHandleGetTraceDetails_BareBodyGetsWebURL(t *testing.T) {
	mock := &client.MockClient{
		GetTraceDetailsFn: func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
			return json.RawMessage(`{"spans":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{"traceId": "abc-123"})

	result, err := h.handleGetTraceDetails(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("expected top-level webUrl, got: %s", body)
	}
}

func TestHandleGetTraceDetails_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		GetTraceDetailsFn: func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"spans":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_trace_details", map[string]any{"traceId": "abc-123"})

	result, err := h.handleGetTraceDetails(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

// rawSearchTracesBody is a realistic query-builder v5 "raw" response (a
// render.Success envelope wrapping QueryRangeResponse) with two rows. The second
// row's duration_nano exceeds float64's exact-integer range to guard precision.
const rawSearchTracesBody = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
	`{"timestamp":"2026-06-19T10:00:00Z","data":{"trace_id":"abc-123","duration_nano":9007199254740993,"name":"GET /cart"}},` +
	`{"timestamp":"2026-06-19T10:00:01Z","data":{"trace_id":"def-456","duration_nano":42,"name":"POST /checkout"}}` +
	`]}]},"meta":{}}}`

func TestHandleSearchTraces_RowsGetWebURL(t *testing.T) {
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return json.RawMessage(rawSearchTracesBody), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{"service": "cart-svc", "timeRange": "1h"})

	result, err := h.handleSearchTraces(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("expected first row webUrl, got: %s", body)
	}
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/trace/def-456"`) {
		t.Fatalf("expected second row webUrl, got: %s", body)
	}
	// duration_nano (> 2^53) must survive the enrichment round-trip exactly.
	if !strings.Contains(body, "9007199254740993") {
		t.Fatalf("duration_nano lost precision: %s", body)
	}
}

func TestHandleSearchTraces_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return json.RawMessage(rawSearchTracesBody), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_traces", map[string]any{"service": "cart-svc", "timeRange": "1h"})

	result, err := h.handleSearchTraces(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

// driftSearchTracesBody is a v5 raw response whose rows carry no supported trace
// id key — i.e. a simulated upstream shape change. Rows are present but none are
// enrichable.
const driftSearchTracesBody = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
	`{"timestamp":"t","data":{"trace_identifier":"abc-123","name":"GET /cart"}}` +
	`]}]},"meta":{}}}`

// emptySearchTracesBody is an ordinary "no data" response: results present, zero rows.
const emptySearchTracesBody = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}},"meta":{}}`

func searchTracesWithCapturedLogs(t *testing.T, responseBody string) string {
	t.Helper()
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return json.RawMessage(responseBody), nil
		},
	}
	h := newTestHandler(mock)
	var logs bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	req := makeToolRequest("signoz_search_traces", map[string]any{"service": "cart-svc", "timeRange": "1h"})

	if _, err := h.handleSearchTraces(ctxWithURL(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return logs.String()
}

func TestHandleSearchTraces_WarnsOnProbableShapeDrift(t *testing.T) {
	// Rows present but none enrichable -> WARN so the silent fail-open is detectable.
	out := searchTracesWithCapturedLogs(t, driftSearchTracesBody)
	if !strings.Contains(out, "rows without supported trace id") || !strings.Contains(out, "rowsSeen=1") || !strings.Contains(out, "rowsEnriched=0") {
		t.Fatalf("expected a shape-drift WARN with rowsSeen, got logs: %q", out)
	}
}

func TestHandleSearchTraces_WarnsOnPartialRowEnrichment(t *testing.T) {
	// Mixed rows where only some carry supported trace-id aliases should warn
	// because fail-open enrichment would otherwise drop links silently.
	body := `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
		`{"timestamp":"t","data":{"trace_id":"abc-123"}},` +
		`{"timestamp":"t","data":{"trace_identifier":"missing"}}` +
		`]}]},"meta":{}}}`
	out := searchTracesWithCapturedLogs(t, body)
	if !strings.Contains(out, "rows without supported trace id") || !strings.Contains(out, "rowsSeen=2") || !strings.Contains(out, "rowsEnriched=1") {
		t.Fatalf("expected a partial-enrichment WARN with row counts, got logs: %q", out)
	}
}

func TestHandleSearchTraces_NoWarnWhenNoRows(t *testing.T) {
	// Present-but-empty rows[] -> ordinary "no data" -> no drift warning of any mode.
	out := searchTracesWithCapturedLogs(t, emptySearchTracesBody)
	if strings.Contains(out, "webUrl enrichment") {
		t.Fatalf("expected NO drift WARN for an empty result, got logs: %q", out)
	}
}

// rowsKeyDriftSearchTracesBody is a 2xx v5 response whose results[] is reachable
// and non-empty, but the per-result "rows" key is renamed ("records") so no rows
// array can be read — a simulated per-result shape change.
const rowsKeyDriftSearchTracesBody = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","records":[` +
	`{"timestamp":"t","data":{"trace_id":"abc-123"}}` +
	`]}]},"meta":{}}}`

func TestHandleSearchTraces_WarnsWhenRowsKeyMissing(t *testing.T) {
	// results[] reachable but no readable rows[] -> rows-key drift -> WARN.
	out := searchTracesWithCapturedLogs(t, rowsKeyDriftSearchTracesBody)
	if !strings.Contains(out, "no readable rows") {
		t.Fatalf("expected a rows-key-drift WARN, got logs: %q", out)
	}
}

// envelopeDriftSearchTracesBody is a 2xx v5 response whose results[] array is
// renamed ("rezults"), so the envelope can't be walked even though it carries
// rows — a simulated upstream envelope-shape change.
const envelopeDriftSearchTracesBody = `{"status":"success","data":{"type":"raw","data":{"rezults":[{"queryName":"A","rows":[` +
	`{"timestamp":"t","data":{"trace_id":"abc-123"}}` +
	`]}]},"meta":{}}}`

func TestHandleSearchTraces_WarnsWhenEnvelopeUnwalkable(t *testing.T) {
	// results[] not reachable -> envelope drift -> WARN (distinct from no-data).
	out := searchTracesWithCapturedLogs(t, envelopeDriftSearchTracesBody)
	if !strings.Contains(out, "locate results") {
		t.Fatalf("expected an envelope-drift WARN, got logs: %q", out)
	}
}
