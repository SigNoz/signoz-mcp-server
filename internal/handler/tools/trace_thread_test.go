package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// threadServer serves one canned thread response with the given status and
// records each thread request's query string.
func threadServer(t *testing.T, status int, response string) (*Handler, *[]string) {
	t.Helper()
	var queries []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/traces/trace-1/thread" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(upstream.Close)
	return newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil)), &queries
}

func TestGetTraceDetails_ThreadForwardsPagingAndPreviewsLongMessages(t *testing.T) {
	long := strings.Repeat("x", 1500)
	response := fmt.Sprintf(`{"status":"success","data":{"nextCursor":"cursor-2","prevCursor":"cursor-0","spans":[
		{"span_id":"llm-1","duration_nano":1234567890123456789,
		 "attributes":{"gen_ai.request.model":"gpt-4o","gen_ai.input.messages":"[raw]","gen_ai.output.messages":"[raw]"},
		 "formattedInput":[{"role":"user","parts":[{"type":"text","content":%q}]}],
		 "formattedOutput":[{"role":"assistant","parts":[{"type":"tool_call","name":"search_docs","arguments":{"q":"x"}}]}]},
		{"span_id":"tool-1","attributes":{"gen_ai.tool.name":"search_docs"},"formattedInput":[],"formattedOutput":[]}]}}`, long)
	h, queries := threadServer(t, http.StatusOK, response)

	result, err := h.handleGetTraceDetails(testCtx(), makeToolRequest("signoz_get_trace_details",
		map[string]any{"traceId": "trace-1", "view": "thread", "limit": "2", "after": "cursor-1"}))
	require.NoError(t, err)
	require.False(t, result.IsError, textContent(t, result))
	require.Len(t, *queries, 1)
	sent, err := url.ParseQuery((*queries)[0])
	require.NoError(t, err)
	require.Equal(t, url.Values{"limit": {"2"}, "after": {"cursor-1"}}, sent)

	text := textContent(t, result)
	require.Contains(t, text, `"duration_nano":1234567890123456789`, "nanosecond values must stay exact")
	var out struct {
		Spans []struct {
			SpanID         string          `json:"span_id"`
			Attributes     map[string]any  `json:"attributes"`
			FormattedInput json.RawMessage `json:"formattedInput"`
			Truncated      bool            `json:"truncated"`
		} `json:"spans"`
		NextCursor string `json:"nextCursor"`
		PrevCursor string `json:"prevCursor"`
		Truncated  bool   `json:"truncated"`
		NextAction string `json:"nextAction"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	require.Len(t, out.Spans, 2)
	llm := out.Spans[0]
	require.Equal(t, map[string]any{"gen_ai.request.model": "gpt-4o"}, llm.Attributes, "raw message attributes repeat formattedInput/formattedOutput")
	require.Contains(t, string(llm.FormattedInput), `"content":"`+strings.Repeat("x", threadPreviewChars)+`"`)
	require.True(t, llm.Truncated)
	require.False(t, out.Spans[1].Truncated)
	require.True(t, out.Truncated)
	require.Equal(t, "cursor-2", out.NextCursor)
	require.Equal(t, "cursor-0", out.PrevCursor)
	require.Contains(t, out.NextAction, "after set to nextCursor")
	require.Contains(t, out.NextAction, "signoz_search_traces")
}

func TestGetTraceDetails_ThreadRejectionsKeepSigNozGuidance(t *testing.T) {
	h, _ := threadServer(t, http.StatusBadRequest,
		`{"status":"error","error":{"type":"invalid-input","code":"trace_thread_invalid_anchor","message":"only one of after, before and spanId can be set"}}`)

	result, err := h.handleGetTraceDetails(testCtx(), makeToolRequest("signoz_get_trace_details",
		map[string]any{"traceId": "trace-1", "view": "thread", "after": "a", "before": "b"}))
	require.NoError(t, err)
	require.Equal(t, CodeValidationFailed, resultCode(t, result))
	require.Equal(t, "only one of after, before and spanId can be set", resultStructuredMap(t, result)["upstreamMessage"])
}

func TestGetTraceDetails_ThreadUnparseableLimitNeverReachesSigNoz(t *testing.T) {
	h, queries := threadServer(t, http.StatusOK, `{"status":"success","data":{"spans":[]}}`)

	result, err := h.handleGetTraceDetails(testCtx(), makeToolRequest("signoz_get_trace_details",
		map[string]any{"traceId": "trace-1", "view": "thread", "limit": "ten"}))
	require.NoError(t, err)
	require.Equal(t, CodeValidationFailed, resultCode(t, result))
	require.Contains(t, resultText(t, result), `"limit"`)
	require.Empty(t, *queries)
}

func TestGetTraceDetails_ThreadWithoutSpansIsAnUpstreamError(t *testing.T) {
	h, _ := threadServer(t, http.StatusOK, `{"status":"success","data":{}}`)

	result, err := h.handleGetTraceDetails(testCtx(), makeToolRequest("signoz_get_trace_details",
		map[string]any{"traceId": "trace-1", "view": "thread"}))
	require.NoError(t, err)
	require.Equal(t, CodeUpstreamError, resultCode(t, result))
}
