package tools

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func TestExecuteBuilderQuery_DropsMisplacedSearchContext(t *testing.T) {
	const query = `{
		"schemaVersion":"v1", "start":1700000000000, "end":1700003600000, "requestType":"scalar",
		"compositeQuery":{"queries":[{"type":"builder_query", "spec":{
			"name":"A", "signal":"logs", "aggregations":[{"expression":"count()"}],
			"limit":100, "order":[{"key":{"name":"count()"}, "direction":"desc"}]
		}}]}
	}`
	var withContext map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(query), &withContext))
	withContext["searchContext"] = json.RawMessage(`"Count logs in that window"`)
	queryJSON, err := json.Marshal(withContext)
	require.NoError(t, err)

	const reply = `{"status":"success","data":{"resultType":"scalar","result":[]}}`
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(reply))
	})
	var logs bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	server := newMCPTestServer()
	h.RegisterQueryBuilderV5Handlers(server)
	result := callTestToolFromJSONRPC(testCtx(), t, server, json.RawMessage(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"signoz_execute_builder_query","arguments":{"query":`+string(queryJSON)+`}}}`))

	require.False(t, result.IsError, "the misplaced metadata must not block the query")
	require.JSONEq(t, reply, textContent(t, result))
	calls := requests()
	require.Len(t, calls, 1)
	require.Equal(t, "/api/v5/query_range", calls[0].Path)
	require.JSONEq(t, query, calls[0].Body, "strip only searchContext from the upstream query")
	require.Contains(t, logs.String(), "level=WARN")
	require.Contains(t, logs.String(), "validation.path=query.searchContext")
	require.NotContains(t, logs.String(), "Count logs in that window")
}

// SigNoz exhausts its memory on a raw query whose start is missing or 0
// (SigNoz/nerve-pod#439), so the guard has to stop the request before it leaves.
func TestExecuteBuilderQuery_RejectsMissingStartBeforeCallingSigNoz(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamCalls.Add(1) }))
	defer upstream.Close()
	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil))

	for name, arguments := range map[string]string{
		"missing start": `{"query":{"end":1700003600000,"requestType":"raw","compositeQuery":{"queries":[]}}}`,
		"zero start":    `{"query":{"start":0,"end":1700003600000,"requestType":"raw","compositeQuery":{"queries":[]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var args map[string]any
			require.NoError(t, json.Unmarshal([]byte(arguments), &args))
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", args))
			require.NoError(t, err)
			require.Equal(t, CodeValidationFailed, resultCode(t, result))
			require.Contains(t, textContent(t, result), `"query.start" is required`)
		})
	}
	require.Zero(t, upstreamCalls.Load(), "the guard must stop the request before it reaches SigNoz")
}
