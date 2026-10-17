package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

func TestHandleExecuteBuilderQuery_SourceIsolationAndRequestLocalDedup(t *testing.T) {
	var ordinaryLookups, meterLookups, executions atomic.Int32
	var round atomic.Int32
	h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/metrics":
			assert.Equal(t, "shared_metric", r.URL.Query().Get("searchText"))
			meter := r.URL.Query().Get("source") == "meter"
			if meter {
				meterLookups.Add(1)
			} else {
				assert.Empty(t, r.URL.Query().Get("source"))
				ordinaryLookups.Add(1)
			}
			if meter == (round.Load() == 0) {
				_, _ = w.Write([]byte(`{"data":{"metrics":[{"metricName":"shared_metric","type":"sum","isMonotonic":true}]}}`))
			} else {
				_, _ = w.Write([]byte(`{"data":{"metrics":[{"metricName":"shared_metric","type":"gauge"}]}}`))
			}
		case "POST /api/v5/query_range":
			executions.Add(1)
			var payload map[string]any
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
				return
			}
			assert.Equal(t, "retained", payload["futureRequestField"])
			composite := payload["compositeQuery"].(map[string]any)
			assert.Equal(t, true, composite["futureCompositeField"])
			queries := composite["queries"].([]any)
			if !assert.Len(t, queries, 4) {
				return
			}
			for i, source := range []string{"", "meter", " Meter "} {
				query := queries[i].(map[string]any)
				assert.Equal(t, "retained", query["futureQueryField"])
				spec := query["spec"].(map[string]any)
				sourceKey := "source"
				if i == 2 {
					sourceKey = "Source"
				}
				assert.Equal(t, source, spec[sourceKey])
				assert.Equal(t, true, spec["disabled"], "hidden formula inputs must still get reducers")
				assert.Equal(t, "retained", spec["futureSpecField"])
				aggs := spec["aggregations"].([]any)
				if !assert.Len(t, aggs, 3) {
					return
				}
				want := "avg"
				if (i > 0) == (round.Load() == 0) {
					want = "sum"
				}
				for j, raw := range aggs {
					if j == 2 {
						want = "max"
					}
					metricKey := "metricName"
					if i == 2 {
						metricKey = "MetricName"
					}
					assert.Equal(t, map[string]any{metricKey: "shared_metric", "timeAggregation": "sum", "spaceAggregation": "sum", "reduceTo": want, "futureAggregationField": "retained"}, raw)
				}
			}
			formula := queries[3].(map[string]any)["spec"].(map[string]any)
			assert.Equal(t, "A / (B + C)", formula["expression"])
			assert.Equal(t, "retained", formula["futureFormulaField"])
			_, _ = w.Write([]byte(`{"data":{"results":[{"queryName":"F","value":2}]}}`))
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for invocation := range 2 {
		round.Store(int32(invocation))
		query := scalarMetricBuilderQuery()
		query["futureRequestField"] = "retained"
		var queries []any
		for i, source := range []string{"", "meter", " Meter "} {
			var aggs []any
			for j := range 3 {
				agg := map[string]any{"metricName": "shared_metric", "timeAggregation": "sum", "spaceAggregation": "sum", "futureAggregationField": "retained"}
				if j == 2 {
					agg["reduceTo"] = "max"
				}
				if i == 2 {
					delete(agg, "metricName")
					agg["MetricName"] = "shared_metric"
				}
				aggs = append(aggs, agg)
			}
			queries = append(queries, map[string]any{"type": "builder_query", "futureQueryField": "retained", "spec": map[string]any{
				"name": []string{"A", "B", "C"}[i], "signal": "metrics", "source": source, "disabled": true, "aggregations": aggs, "futureSpecField": "retained",
			}})
		}
		aliasedSpec := queries[2].(map[string]any)["spec"].(map[string]any)
		aliasedSpec["Source"] = aliasedSpec["source"]
		delete(aliasedSpec, "source")
		queries = append(queries, map[string]any{"type": "builder_formula", "spec": map[string]any{"name": "F", "expression": "A / (B + C)", "futureFormulaField": "retained"}})
		query["compositeQuery"] = map[string]any{"queries": queries, "futureCompositeField": true}
		result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		assert.JSONEq(t, `{"data":{"results":[{"queryName":"F","value":2}]}}`, textContent(t, result))
		var notes string
		for i := range result.Content {
			notes += noteText(t, result, i) + "\n"
		}
		for i, name := range []string{"A", "B", "C"} {
			want := "avg"
			if (i > 0) == (invocation == 0) {
				want = "sum"
			}
			for agg := 1; agg <= 2; agg++ {
				assert.Contains(t, notes, fmt.Sprintf("query %s aggregation #%d (shared_metric): reduceTo=%s", name, agg, want))
			}
		}
		assert.Equal(t, int32(invocation+1), ordinaryLookups.Load(), "deduplicate ordinary metadata only within each request")
		assert.Equal(t, int32(invocation+1), meterLookups.Load(), "deduplicate meter metadata only within each request")
		assert.Equal(t, int32(invocation+1), executions.Load())
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
		{"case_variant", "scalar", "metrics", map[string]any{"MetricName": "test_metric", "ReduceTo": "max"}},
		{"case_variant_null", "scalar", "metrics", map[string]any{"metricName": "test_metric", "ReduceTo": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantAgg := maps.Clone(tc.agg)
			var executions atomic.Int32
			h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v5/query_range" {
					t.Errorf("authored reducers must bypass metadata: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				executions.Add(1)
				var payload map[string]any
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
					return
				}
				assert.Equal(t, tc.requestType, payload["requestType"])
				spec := payload["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
				assert.Equal(t, tc.signal, spec["signal"])
				assert.Equal(t, []any{wantAgg}, spec["aggregations"], "caller-supplied values belong to upstream validation")
				_, _ = w.Write([]byte(`{"data":{"results":[{"queryName":"A","value":7}]}}`))
			})
			query := scalarMetricBuilderQuery()
			query["requestType"] = tc.requestType
			query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{"name": "A", "signal": tc.signal, "aggregations": []any{tc.agg}}}}
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			assert.JSONEq(t, `{"data":{"results":[{"queryName":"A","value":7}]}}`, textContent(t, result))
			for i := range result.Content {
				assert.NotContains(t, noteText(t, result, i), "reduceTo=", "authored aggregations must not produce reducer decisions")
			}
			assert.Equal(t, int32(1), executions.Load())
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
		{name: "missing_metric_name", code: CodeValidationFailed},
		{name: "missing", body: `{"data":{"metrics":[]}}`, code: CodeValidationFailed},
		{name: "different_metric", body: `{"data":{"metrics":[{"metricName":"test_metric_other","type":"gauge"}]}}`, code: CodeValidationFailed},
		{name: "missing_type", body: `{"data":{"metrics":[{"metricName":"test_metric"}]}}`, code: CodeValidationFailed},
		{name: "missing_monotonicity", body: `{"data":{"metrics":[{"metricName":"test_metric","type":"sum"}]}}`, code: CodeValidationFailed},
		{name: "unknown_type", body: `{"data":{"metrics":[{"metricName":"test_metric","type":"new_type"}]}}`, code: CodeValidationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups atomic.Int32
			h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/metrics":
					lookups.Add(1)
					assert.Equal(t, "test_metric", r.URL.Query().Get("searchText"))
					if tc.status != 0 {
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(`{"error":"metadata unavailable"}`))
						return
					}
					_, _ = w.Write([]byte(tc.body))
				case "GET /api/v2/metrics/metadata":
					assert.Contains(t, []string{"missing", "different_metric"}, tc.name, "only an absent catalog match needs fallback")
					assert.Equal(t, "test_metric", r.URL.Query().Get("metricName"))
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"status":"error","error":{"code":"not_found","message":"metadata not found for metric \"test_metric\""}}`))
				default:
					t.Errorf("unsafe metadata must not execute a query: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			query := scalarMetricBuilderQuery()
			query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{"name": "A", "signal": "metrics", "aggregations": []any{map[string]any{"metricName": "test_metric"}}}}}
			if tc.name == "missing_metric_name" {
				query["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)["aggregations"] = []any{map[string]any{}}
			}
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Equal(t, tc.code, resultCode(t, result))
			if tc.name == "missing_metric_name" {
				assert.Contains(t, resultText(t, result), "aggregations[0].metricName")
				assert.Contains(t, resultText(t, result), "signoz_list_metrics")
				assert.Zero(t, lookups.Load(), "missing metricName must fail before discovery")
				return
			}
			assert.Contains(t, resultText(t, result), "reduceTo")
			assert.Contains(t, resultText(t, result), "explicitly", "agent must be told how to bypass unresolved metadata")
			if tc.name == "missing" || tc.name == "different_metric" {
				assert.Contains(t, resultText(t, result), "check metricName and source with signoz_list_metrics")
			}
			assert.Positive(t, lookups.Load(), "failure must come from metadata discovery")
		})
	}
}

func TestHandleExecuteBuilderQuery_MetricMetadataBeyondFirstPage(t *testing.T) {
	for _, tc := range []struct {
		name, source, body, code string
		status                   int
	}{
		{name: "exact_name_omitted", status: http.StatusOK, body: `{"data":{"type":"sum","isMonotonic":true,"temporality":"cumulative"}}`},
		{name: "exact_name_mismatch", status: http.StatusOK, body: `{"data":{"metricName":"different_metric","type":"sum","isMonotonic":true}}`, code: CodeValidationFailed},
		{name: "meter_store", source: "meter", status: http.StatusOK},
		{name: "meter_outside_requested_window", source: "meter", status: http.StatusOK},
		{name: "meter_missing", source: "meter", status: http.StatusOK, code: CodeValidationFailed},
		{name: "exact_lookup_unauthorized", status: http.StatusUnauthorized, code: CodeUnauthorized},
		{name: "exact_lookup_forbidden", status: http.StatusForbidden, code: CodePermissionDenied},
		{name: "meter_lookup_unauthorized", source: "meter", status: http.StatusUnauthorized, code: CodeUnauthorized},
		{name: "meter_lookup_forbidden", source: "meter", status: http.StatusForbidden, code: CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const metricName = "test+metric&name"
			var initialLookups, fallbacks, executions atomic.Int32
			var firstPageLimit atomic.Int64
			h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/metrics":
					assert.Equal(t, tc.source, r.URL.Query().Get("source"))
					assert.Equal(t, metricName, r.URL.Query().Get("searchText"))
					limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
					if !assert.NoError(t, err) || !assert.Positive(t, limit) {
						return
					}
					var rows []map[string]any
					if firstPageLimit.CompareAndSwap(0, int64(limit)) {
						initialLookups.Add(1)
						assert.Equal(t, "1711123200000", r.URL.Query().Get("start"))
						assert.Equal(t, "1711130400000", r.URL.Query().Get("end"))
						if tc.name == "meter_outside_requested_window" {
							_, _ = w.Write([]byte(`{"data":{"metrics":[]}}`))
							return
						}
						for i := range limit {
							rows = append(rows, map[string]any{"metricName": fmt.Sprintf("a%d_%s", i, metricName), "type": "gauge"})
						}
					} else {
						fallbacks.Add(1)
						assert.Equal(t, "meter", tc.source, "ordinary metrics must use exact-name fallback")
						assert.Greater(t, int64(limit), firstPageLimit.Load(), "meter fallback must search beyond the first catalog page")
						assert.False(t, r.URL.Query().Has("start"), "meter metadata fallback must include hourly buckets outside the requested window")
						assert.False(t, r.URL.Query().Has("end"))
						w.WriteHeader(tc.status)
						if tc.name != "meter_missing" {
							rows = append(rows, map[string]any{"metricName": metricName, "type": "sum", "isMonotonic": true})
						}
					}
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"metrics": rows}}))
				case "GET /api/v2/metrics/metadata":
					fallbacks.Add(1)
					assert.Empty(t, tc.source, "meter must never call the ordinary-store exact endpoint")
					assert.Equal(t, metricName, r.URL.Query().Get("metricName"))
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				case "POST /api/v5/query_range":
					executions.Add(1)
					assert.Empty(t, tc.code, "unresolved metadata must prevent query execution")
					var query map[string]any
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&query)) {
						return
					}
					spec := query["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
					assert.Equal(t, tc.source, spec["source"])
					assert.Equal(t, []any{map[string]any{"metricName": metricName, "reduceTo": "sum"}}, spec["aggregations"], "reducer must come from the exact metric")
					_, _ = w.Write([]byte(`{"data":{"results":[{"queryName":"A","value":12}]}}`))
				default:
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			query := scalarMetricBuilderQuery()
			query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{
				"name": "A", "signal": "metrics", "source": tc.source, "aggregations": []any{map[string]any{"metricName": metricName}},
			}}}
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			require.NoError(t, err)
			assert.Equal(t, int32(1), initialLookups.Load())
			assert.Equal(t, int32(1), fallbacks.Load())
			if tc.code != "" {
				require.True(t, result.IsError)
				assert.Equal(t, tc.code, resultCode(t, result))
				assert.Contains(t, resultText(t, result), "reduceTo")
				assert.Contains(t, resultText(t, result), "explicitly")
				assert.Zero(t, executions.Load())
				if tc.name == "meter_missing" {
					assert.Contains(t, resultText(t, result), "check metricName and source with signoz_list_metrics")
				}
			} else {
				require.False(t, result.IsError, "%v", result.Content)
				assert.JSONEq(t, `{"data":{"results":[{"queryName":"A","value":12}]}}`, textContent(t, result))
				var notes string
				for i := range result.Content {
					notes += noteText(t, result, i) + "\n"
				}
				assert.Contains(t, notes, "reduceTo=sum")
				assert.Equal(t, int32(1), executions.Load())
			}
		})
	}
}

func TestHandleExecuteBuilderQuery_MetricMetadataBudget(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		metrics, sources, wantCalls int
		explicit, wantError         bool
	}{
		{name: "at_limit_with_duplicates", metrics: 16, sources: 1, wantCalls: 16},
		{name: "at_limit_across_sources", metrics: 8, sources: 2, wantCalls: 16},
		{name: "over_limit", metrics: 17, sources: 1, wantError: true},
		{name: "sources_count_separately", metrics: 9, sources: 2, wantError: true},
		{name: "explicit_reducers_bypass_budget", metrics: 17, sources: 2, explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups, executions atomic.Int32
			h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
				assert.False(t, tc.wantError, "over-budget discovery must fail before any upstream request")
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/metrics":
					lookups.Add(1)
					_, _ = fmt.Fprintf(w, `{"data":{"metrics":[{"metricName":%q,"type":"gauge"}]}}`, r.URL.Query().Get("searchText"))
				case "POST /api/v5/query_range":
					executions.Add(1)
					var query map[string]any
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&query)) {
						return
					}
					queries := query["compositeQuery"].(map[string]any)["queries"].([]any)
					if !assert.Len(t, queries, tc.sources) {
						return
					}
					for source, raw := range queries {
						spec := raw.(map[string]any)["spec"].(map[string]any)
						assert.Equal(t, []string{"", "meter"}[source], spec["source"])
						aggs := spec["aggregations"].([]any)
						assert.Len(t, aggs, tc.metrics*2)
						for i, agg := range aggs {
							want := "avg"
							if tc.explicit {
								want = "last"
							}
							assert.Equal(t, map[string]any{"metricName": fmt.Sprintf("metric_%d", i/2), "reduceTo": want}, agg)
						}
					}
					_, _ = w.Write([]byte(`{"data":{"results":[]}}`))
				default:
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			query := scalarMetricBuilderQuery()
			var queries []any
			for source := 0; source < tc.sources; source++ {
				var aggregations []any
				for metric := 0; metric < tc.metrics; metric++ {
					for range 2 {
						agg := map[string]any{"metricName": fmt.Sprintf("metric_%d", metric)}
						if tc.explicit {
							agg["reduceTo"] = "last"
						}
						aggregations = append(aggregations, agg)
					}
				}
				queries = append(queries, map[string]any{"type": "builder_query", "spec": map[string]any{
					"name": fmt.Sprintf("Q%d", source), "signal": "metrics", "source": []string{"", "meter"}[source], "aggregations": aggregations,
				}})
			}
			query["compositeQuery"].(map[string]any)["queries"] = queries
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
			require.NoError(t, err)
			assert.Equal(t, int32(tc.wantCalls), lookups.Load())
			if tc.wantError {
				require.True(t, result.IsError)
				assert.Equal(t, CodeValidationFailed, resultCode(t, result))
				assert.Contains(t, resultText(t, result), "reduceTo explicitly")
				assert.Zero(t, executions.Load())
			} else {
				require.False(t, result.IsError, "%v", result.Content)
				assert.JSONEq(t, `{"data":{"results":[]}}`, textContent(t, result))
				assert.Equal(t, int32(1), executions.Load())
			}
		})
	}
}

func TestHandleExecuteBuilderQuery_CancelsMetricDiscovery(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	var requests atomic.Int32
	ctx, cancel := context.WithCancel(testCtx())
	defer cancel()
	h := newBuilderMetricTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/metrics" || r.URL.Query().Get("searchText") != "first" {
			t.Errorf("canceled discovery must not advance or execute: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-time.After(5 * time.Second):
			t.Error("upstream request did not observe cancellation")
		}
	})
	query := scalarMetricBuilderQuery()
	query["compositeQuery"].(map[string]any)["queries"] = []any{map[string]any{"type": "builder_query", "spec": map[string]any{
		"name": "A", "signal": "metrics", "aggregations": []any{map[string]any{"metricName": "first"}, map[string]any{"metricName": "second"}},
	}}}
	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := h.handleExecuteBuilderQuery(ctx, makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
		done <- outcome{result, err}
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("metadata request never reached upstream")
	}
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.True(t, got.result.IsError)
		assert.Equal(t, CodeCanceled, resultCode(t, got.result))
		assert.Contains(t, resultText(t, got.result), "reduceTo explicitly")
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after cancellation")
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not reach the HTTP request")
	}
	assert.Equal(t, int32(1), requests.Load(), "no fallback, retry, second discovery, or query after cancellation")
}

func newBuilderMetricTestHandler(t *testing.T, serve http.HandlerFunc) *Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		serve(w, r)
	}))
	t.Cleanup(upstream.Close)
	return newTestHandler(client.NewClient(slog.Default(), upstream.URL, "test-key", "SIGNOZ-API-KEY", nil))
}

func scalarMetricBuilderQuery() map[string]any {
	return map[string]any{"schemaVersion": "v1", "start": 1711123200000, "end": 1711130400000, "requestType": "scalar", "compositeQuery": map[string]any{"queries": []any{}}}
}
