package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

func TestHandleExecuteBuilderQuery_ScalarMetricReducers(t *testing.T) {
	for _, tc := range []struct {
		name, metricType, reducer string
		monotonic                 bool
	}{
		{"gauge", "gauge", "avg", false},
		{"counter", "sum", "sum", true},
		{"non_monotonic_sum", "sum", "avg", false},
		{"histogram", "histogram", "avg", false},
		{"exponential_histogram", "exponentialhistogram", "avg", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := make(map[string]int)
			mock := &client.MockClient{
				ListMetricsFn: func(_ context.Context, _, _ int64, _ int, name, source string) (json.RawMessage, error) {
					if name != "test_metric" {
						t.Fatalf("unexpected metric %q", name)
					}
					calls[source]++
					return json.RawMessage(fmt.Sprintf(`{"data":{"metrics":[{"metricName":"test_metric","type":%q,"isMonotonic":%t,"temporality":"delta"}]}}`, tc.metricType, tc.monotonic)), nil
				},
				QueryBuilderV5Fn: func(_ context.Context, body []byte) (json.RawMessage, error) {
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Fatal(err)
					}
					queries := payload["compositeQuery"].(map[string]any)["queries"].([]any)
					for _, index := range []int{0, 1} {
						spec := queries[index].(map[string]any)["spec"].(map[string]any)
						aggs := spec["aggregations"].([]any)
						for i, raw := range aggs {
							agg := raw.(map[string]any)
							want := tc.reducer
							if i == 2 {
								want = "max"
							}
							if agg["reduceTo"] != want {
								t.Fatalf("query %d aggregation %d: %v", index, i, agg)
							}
							if agg["timeAggregation"] != "sum" || agg["spaceAggregation"] != "sum" {
								t.Fatalf("authored aggregation changed: %v", agg)
							}
						}
					}
					return json.RawMessage(`{"data":{"results":[]}}`), nil
				},
			}
			query := scalarMetricBuilderQuery()
			queries := query["compositeQuery"].(map[string]any)["queries"].([]any)
			for _, source := range []string{"", "meter"} {
				spec := map[string]any{"name": "A", "signal": "metrics", "source": source, "disabled": true,
					"aggregations": []any{
						map[string]any{"metricName": "test_metric", "timeAggregation": "sum", "spaceAggregation": "sum"},
						map[string]any{"metricName": "test_metric", "timeAggregation": "sum", "spaceAggregation": "sum"},
						map[string]any{"metricName": "test_metric", "timeAggregation": "sum", "spaceAggregation": "sum", "reduceTo": "max"},
					},
				}
				if source == "meter" {
					spec["name"] = "B"
				}
				queries = append(queries, map[string]any{"type": "builder_query", "spec": spec})
			}
			queries = append(queries, map[string]any{"type": "builder_formula", "spec": map[string]any{"name": "F", "expression": "A / B"}})
			query["compositeQuery"].(map[string]any)["queries"] = queries
			result, err := newTestHandler(mock).handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			if err != nil || result.IsError {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if !reflect.DeepEqual(calls, map[string]int{"": 1, "meter": 1}) {
				t.Fatalf("source-scoped metadata lookups: %v", calls)
			}
			if !resultNotesContain(result, "reduceTo="+tc.reducer) {
				t.Fatalf("missing decisions: %v", allTextBlocks(result))
			}
			if block, ok := mcp.AsTextContent(result.Content[0]); !ok || block.Text != `{"data":{"results":[]}}` {
				t.Fatalf("raw response changed: %v", result.Content)
			}
		})
	}
}

func TestHandleExecuteBuilderQuery_PreservesAuthoredReducers(t *testing.T) {
	for _, tc := range []struct {
		name, requestType, signal string
		agg                       map[string]any
	}{
		{"time_series", "time_series", "metrics", map[string]any{"metricName": "test_metric", "timeAggregation": "avg", "spaceAggregation": "sum"}},
		{"logs", "scalar", "logs", map[string]any{"expression": "count()"}},
		{"traces", "scalar", "traces", map[string]any{"expression": "count()"}},
		{"explicit", "scalar", "metrics", map[string]any{"metricName": "test_metric", "reduceTo": "last"}},
		{"invalid", "scalar", "metrics", map[string]any{"metricName": "test_metric", "reduceTo": "not_a_reducer"}},
		{"empty", "scalar", "metrics", map[string]any{"metricName": "test_metric", "reduceTo": ""}},
		{"null", "scalar", "metrics", map[string]any{"metricName": "test_metric", "reduceTo": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &client.MockClient{
				ListMetricsFn: func(context.Context, int64, int64, int, string, string) (json.RawMessage, error) {
					t.Fatal("unexpected metadata lookup")
					return nil, nil
				},
				QueryBuilderV5Fn: func(_ context.Context, body []byte) (json.RawMessage, error) {
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Fatal(err)
					}
					got := payload["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)["aggregations"].([]any)[0]
					if !reflect.DeepEqual(got, tc.agg) {
						t.Fatalf("aggregation changed: %v", got)
					}
					return json.RawMessage(`{}`), nil
				},
			}
			query := scalarMetricBuilderQuery()
			query["requestType"] = tc.requestType
			query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{"name": "A", "signal": tc.signal, "aggregations": []any{tc.agg}}}}
			result, err := newTestHandler(mock).handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			if err != nil || result.IsError {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if resultNotesContain(result, "reduceTo=") {
				t.Fatal("unexpected reducer decision")
			}
		})
	}
}

func TestHandleExecuteBuilderQuery_MetricMetadataFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, code: CodeUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, code: CodePermissionDenied},
		{name: "unavailable", status: http.StatusServiceUnavailable, code: CodeUpstreamError},
		{name: "missing", body: `{"data":{"metrics":[]}}`, code: CodeValidationFailed},
		{name: "different_metric", body: `{"data":{"metrics":[{"metricName":"test_metric_other","type":"gauge"}]}}`, code: CodeValidationFailed},
		{name: "missing_monotonicity", body: `{"data":{"metrics":[{"metricName":"test_metric","type":"sum"}]}}`, code: CodeValidationFailed},
		{name: "unknown_type", body: `{"data":{"metrics":[{"metricName":"test_metric","type":"new_type"}]}}`, code: CodeValidationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &client.MockClient{
				ListMetricsFn: func(context.Context, int64, int64, int, string, string) (json.RawMessage, error) {
					if tc.status != 0 {
						return nil, &client.HTTPStatusError{StatusCode: tc.status, Body: `{}`}
					}
					return json.RawMessage(tc.body), nil
				},
				QueryBuilderV5Fn: func(context.Context, []byte) (json.RawMessage, error) {
					t.Fatal("must not execute without a safe reducer")
					return nil, nil
				},
			}
			query := scalarMetricBuilderQuery()
			query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{"name": "A", "signal": "metrics", "aggregations": []any{map[string]any{"metricName": "test_metric"}}}}}
			result, err := newTestHandler(mock).handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			if err != nil || !result.IsError || resultCode(t, result) != tc.code {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if tc.status == 0 && !strings.Contains(resultText(t, result), "reduceTo") {
				t.Fatalf("missing recovery guidance: %v", result.Content)
			}
		})
	}
}

func scalarMetricBuilderQuery() map[string]any {
	return map[string]any{"schemaVersion": "v1", "start": 1711123200000, "end": 1711130400000, "requestType": "scalar", "compositeQuery": map[string]any{"queries": []any{}}}
}
