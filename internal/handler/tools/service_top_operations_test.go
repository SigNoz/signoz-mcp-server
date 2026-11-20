package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// topOperationsUpstream answers like SigNoz's top-operations route: it returns
// up to the requested limit of n operations and records each limit it was sent.
func topOperationsUpstream(t *testing.T, n int) (*Handler, *[]int) {
	t.Helper()
	var limits []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/service/top_operations", r.URL.Path)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body struct {
			Limit int `json:"limit"`
		}
		require.NoError(t, json.Unmarshal(raw, &body))
		limits = append(limits, body.Limit)
		ops := make([]string, 0, min(n, body.Limit))
		for i := range cap(ops) {
			ops = append(ops, fmt.Sprintf(`{"name":"op-%d","p50":1000,"p95":2000,"p99":%d,"numCalls":3,"errorCount":0}`, i, 9000-i))
		}
		_, _ = w.Write([]byte(`{"status":"success","data":[` + strings.Join(ops, ",") + `]}`))
	}))
	t.Cleanup(server.Close)
	return newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)), &limits
}

func callTopOperations(t *testing.T, h *Handler, args map[string]any) (int, []string) {
	t.Helper()
	res, err := h.handleGetServiceTopOperations(testCtx(), makeToolRequest("signoz_get_service_top_operations", args))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", textContent(t, res))
	var ops []map[string]any
	require.NoError(t, json.Unmarshal([]byte(textContent(t, res)), &ops), "the result stays a bare array")
	// The window report every time-windowed result carries is pinned in
	// time_window_test.go; these tests cover the limit notes.
	var notes []string
	for _, block := range res.Content[1:] {
		if text, ok := mcp.AsTextContent(block); ok && !strings.HasPrefix(text.Text, "window queried: ") {
			notes = append(notes, text.Text)
		}
	}
	return len(ops), notes
}

// SigNoz ranks by p99 before applying the limit, so the tool sends the limit
// upstream instead of trimming a larger result.
func TestTopOperationsSendsTheLimitToSigNoz(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit any
		want  int
	}{
		{"default", nil, 100},
		{"string", "3", 3},
		{"number", 7, 7},
		{"above the maximum", "9000", 5000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, limits := topOperationsUpstream(t, 2)
			args := map[string]any{"service": "checkout"}
			if tc.limit != nil {
				args["limit"] = tc.limit
			}
			callTopOperations(t, h, args)
			assert.Equal(t, []int{tc.want}, *limits)
		})
	}
}

// A result that fills the limit must say that more operations exist, and below
// the maximum it must say how to see them.
func TestTopOperationsNotesWhenTheLimitIsFilled(t *testing.T) {
	h, _ := topOperationsUpstream(t, 5)

	count, notes := callTopOperations(t, h, map[string]any{"service": "checkout", "limit": "3"})
	assert.Equal(t, 3, count)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "Raise limit, up to 5000")
	assert.Contains(t, notes[0], "signoz_aggregate_traces")

	count, notes = callTopOperations(t, h, map[string]any{"service": "checkout", "limit": "10"})
	assert.Equal(t, 5, count)
	assert.Empty(t, notes, "every operation fit under the limit")

	h, _ = topOperationsUpstream(t, signozclient.TopOperationsLimit+1)
	count, notes = callTopOperations(t, h, map[string]any{"service": "checkout", "limit": "9000"})
	assert.Equal(t, signozclient.TopOperationsLimit, count)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "narrow the result set (a shorter time range or tags)", "a higher limit cannot help at the maximum")
}

func TestTopOperationsRejectsALimitThatIsNotANumber(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	t.Cleanup(server.Close)
	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil))

	res, err := h.handleGetServiceTopOperations(testCtx(), makeToolRequest("signoz_get_service_top_operations", map[string]any{"service": "checkout", "limit": "ten"}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Equal(t, CodeValidationFailed, resultCode(t, res))
	assert.True(t, strings.HasPrefix(textContent(t, res), `Parameter validation failed: "limit"`), textContent(t, res))
	assert.Zero(t, calls.Load(), "a rejected call must not reach SigNoz")
}
