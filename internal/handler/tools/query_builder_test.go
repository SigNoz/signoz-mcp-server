package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

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
