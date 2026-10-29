package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const emptyQueryRangeResponse = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}}}`

// queryRangeRecorder serves one canned query_range response and records the
// request body each query_range call sent upstream. Other paths, such as the
// global config read behind webUrl links, get a 404.
func queryRangeRecorder(t *testing.T, response string) (*Handler, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(upstream.Close)
	return newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil)), &bodies
}

func requireJSONEqual(t *testing.T, want string, got any) {
	t.Helper()
	body, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, want, string(body))
}

// sentEnvelope returns the request type and the single query envelope a call sent.
func sentEnvelope(t *testing.T, body map[string]any) (string, map[string]any) {
	t.Helper()
	queries := body["compositeQuery"].(map[string]any)["queries"].([]any)
	require.Len(t, queries, 1)
	return body["requestType"].(string), queries[0].(map[string]any)
}

// queryType picks the envelope and requestType the row shape: span rows default
// to timestamp desc, trace rows leave the order to SigNoz unless orderBy is set,
// and unknown values of either run as builder_query span rows.
func TestSearchTraces_QueryTypeRequestTypeAndOrderByReachSigNoz(t *testing.T) {
	const newestFirst = `[{"key":{"name":"timestamp"},"direction":"desc"}]`
	tests := []struct {
		name            string
		args            map[string]any
		wantType        string
		wantRequestType string
		wantOrder       string // JSON, or "" when no order is sent
	}{
		{"default is a span search newest first", map[string]any{}, "builder_query", "raw", newestFirst},
		{"span rows with orderBy", map[string]any{"orderBy": "duration_nano asc"}, "builder_query", "raw",
			`[{"key":{"name":"duration_nano"},"direction":"asc"}]`},
		{"builder_ai_query span rows", map[string]any{"queryType": "builder_ai_query", "filter": "trace.tool_call_count > 10"}, "builder_ai_query", "raw", newestFirst},
		{"plain trace rows", map[string]any{"requestType": "trace"}, "builder_query", "trace", ""},
		{"AI trace rows leave the order to SigNoz", map[string]any{"requestType": "trace", "queryType": "builder_ai_query"}, "builder_ai_query", "trace", ""},
		{"AI trace rows with a per-trace orderBy", map[string]any{"requestType": "trace", "queryType": "builder_ai_query", "orderBy": "trace.estimated_total_cost desc"}, "builder_ai_query", "trace",
			`[{"key":{"name":"trace.estimated_total_cost"},"direction":"desc"}]`},
		{"an unknown requestType runs as span rows", map[string]any{"requestType": "spans"}, "builder_query", "raw", newestFirst},
		{"an unknown queryType runs as builder_query", map[string]any{"queryType": "builder_llm_query"}, "builder_query", "raw", newestFirst},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, bodies := queryRangeRecorder(t, emptyQueryRangeResponse)
			args := map[string]any{"timeRange": "1h"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleSearchTraces(testCtx(), makeToolRequest("signoz_search_traces", args))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			require.Len(t, *bodies, 1)

			requestType, envelope := sentEnvelope(t, (*bodies)[0])
			require.Equal(t, tc.wantType, envelope["type"])
			require.Equal(t, tc.wantRequestType, requestType)
			spec := envelope["spec"].(map[string]any)
			require.Equal(t, "traces", spec["signal"], "SigNoz requires the signal on every trace spec")
			if tc.wantOrder == "" {
				require.NotContains(t, spec, "order")
			} else {
				requireJSONEqual(t, tc.wantOrder, spec["order"])
			}
		})
	}
}

// queryType picks the aggregate envelope; the default order stays the
// aggregation descending, which SigNoz accepts for per-trace aggregations.
func TestAggregateTraces_QueryTypeReachesSigNoz(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		wantType string
	}{
		{"default aggregates spans", map[string]any{}, "builder_query"},
		{"builder_ai_query", map[string]any{"queryType": "builder_ai_query"}, "builder_ai_query"},
		{"an unknown queryType runs as builder_query", map[string]any{"queryType": "builder_llm_query"}, "builder_query"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, bodies := queryRangeRecorder(t, emptyQueryRangeResponse)
			args := map[string]any{"timeRange": "1h", "aggregations": "avg(trace.estimated_total_cost)"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleAggregateTraces(testCtx(), makeToolRequest("signoz_aggregate_traces", args))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			require.Len(t, *bodies, 1)

			requestType, envelope := sentEnvelope(t, (*bodies)[0])
			require.Equal(t, tc.wantType, envelope["type"])
			require.Equal(t, "scalar", requestType)
			spec := envelope["spec"].(map[string]any)
			require.Equal(t, "traces", spec["signal"])
			requireJSONEqual(t, `[{"expression":"avg(trace.estimated_total_cost)"}]`, spec["aggregations"])
			requireJSONEqual(t, `[{"key":{"name":"avg(trace.estimated_total_cost)"},"direction":"desc"}]`, spec["order"])
		})
	}
}

func TestSearchTraces_AINotesExplainAIResultsOnly(t *testing.T) {
	const oneRow = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"data":{"trace_id":"t1"}}]}]}}}`
	tests := []struct {
		name     string
		args     map[string]any
		response string
		want     []string
		notWant  []string
	}{
		{"AI trace rows explain window and values", map[string]any{"queryType": "builder_ai_query", "requestType": "trace"}, oneRow,
			[]string{aiTraceRowsNote}, []string{aiNoRowsNote}},
		{"empty AI results suggest a wider window", map[string]any{"queryType": "builder_ai_query"}, emptyQueryRangeResponse,
			[]string{aiNoRowsNote}, []string{aiTraceRowsNote}},
		{"builder_query results carry no AI notes", map[string]any{"requestType": "trace"}, emptyQueryRangeResponse,
			nil, []string{aiTraceRowsNote, aiNoRowsNote}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := queryRangeRecorder(t, tc.response)
			args := map[string]any{"timeRange": "1h"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleSearchTraces(testCtx(), makeToolRequest("signoz_search_traces", args))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			text := allTextBlocks(result)
			for _, note := range tc.want {
				require.Contains(t, text, note)
			}
			for _, note := range tc.notWant {
				require.NotContains(t, text, note)
			}
		})
	}
}

func TestAggregateTraces_AINoteOnlyForBuilderAIQuery(t *testing.T) {
	for _, tc := range []struct {
		queryType string
		want      bool
	}{{"builder_ai_query", true}, {"builder_query", false}} {
		t.Run(tc.queryType, func(t *testing.T) {
			h, _ := queryRangeRecorder(t, emptyQueryRangeResponse)
			result, err := h.handleAggregateTraces(testCtx(), makeToolRequest("signoz_aggregate_traces",
				map[string]any{"timeRange": "1h", "aggregations": "count()", "queryType": tc.queryType}))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			require.Equal(t, tc.want, strings.Contains(allTextBlocks(result), aiAggregateNote))
		})
	}
}

func TestAggregateTraces_CostAggregationsPointAtPricing(t *testing.T) {
	for _, tc := range []struct {
		aggregations string
		want         bool
	}{
		{"avg(trace.estimated_total_cost)", true},
		{"sum(signoz.gen_ai.usage.tokens.cost)", true},
		{"sum(trace.total_tokens)", false},
	} {
		t.Run(tc.aggregations, func(t *testing.T) {
			h, _ := queryRangeRecorder(t, emptyQueryRangeResponse)
			result, err := h.handleAggregateTraces(testCtx(), makeToolRequest("signoz_aggregate_traces",
				map[string]any{"timeRange": "1h", "aggregations": tc.aggregations, "queryType": "builder_ai_query"}))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			require.Equal(t, tc.want, strings.Contains(allTextBlocks(result), aiCostAggregateNote))
		})
	}
}
