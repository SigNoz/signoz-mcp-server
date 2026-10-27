package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const emptyQueryRangeResponse = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}}}`

// queryRangeRecorder serves one canned query_range response and records the
// request body each query_range call sent upstream. Other paths, such as the
// global config read behind webUrl links, get a 404.
func queryRangeRecorder(t *testing.T, status int, response string) (*Handler, *[]map[string]any) {
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
		w.WriteHeader(status)
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

// queryType is copied into the envelope type. requestType picks the row shape:
// span rows default to timestamp desc, trace rows send no order or selectFields
// unless orderBy is set, and an unknown requestType runs as span rows.
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
		{"an unknown queryType is forwarded for SigNoz to judge", map[string]any{"queryType": "builder_llm_query"}, "builder_llm_query", "raw", newestFirst},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, bodies := queryRangeRecorder(t, http.StatusOK, emptyQueryRangeResponse)
			args := map[string]any{"timeRange": "1h", "limit": "5"}
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
			require.Equal(t, float64(5), spec["limit"])
			if tc.wantOrder == "" {
				require.NotContains(t, spec, "order")
			} else {
				requireJSONEqual(t, tc.wantOrder, spec["order"])
			}
			if tc.wantRequestType == "trace" {
				require.NotContains(t, spec, "selectFields", "trace rows have fixed columns")
			}
			if filter, ok := tc.args["filter"]; ok {
				require.Equal(t, filter, spec["filter"].(map[string]any)["expression"])
			}
		})
	}
}
