package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// serveTopOperations points a handler at a fake SigNoz whose v2 top-operations
// endpoint returns rows operations, slowest p99 first, and records each request body.
func serveTopOperations(t *testing.T, rows int) (*Handler, context.Context, *[]map[string]any) {
	t.Helper()
	var sent []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/service/top_operations" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		sent = append(sent, body)
		ops := make([]map[string]any, rows)
		for i := range ops {
			ops[i] = map[string]any{"name": fmt.Sprintf("op-%04d", i), "p50": 1.0, "p95": 2.0, "p99": float64(rows - i), "numCalls": 10, "errorCount": 0}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": ops})
	}))
	t.Cleanup(backend.Close)
	t.Setenv(config.SignozURL, strings.Replace(backend.URL, "127.0.0.1", "localhost", 1))
	t.Setenv(config.SignozApiKey, "test-key")
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	h := NewHandler(logpkg.New("error"), cfg)
	return h, util.SetAPIKey(util.SetSigNozURL(testCtx(), cfg.URL), cfg.APIKey), &sent
}

func TestGetServiceTopOperations_LimitKeepsTheSlowestAndCountsTheRest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rows     int
		args     map[string]any
		wantRows int
		wantNote string // "" means the result carries no note
	}{
		{name: "default keeps the 50 slowest", rows: 120, args: map[string]any{}, wantRows: 50, wantNote: "returned the 50 slowest operations by p99 of 120; 70 were omitted"},
		{name: "string limit", rows: 120, args: map[string]any{"limit": "10"}, wantRows: 10, wantNote: "110 were omitted"},
		{name: "limit above the row count returns every row", rows: 30, args: map[string]any{"limit": 200}, wantRows: 30},
		{name: "limit above the SigNoz maximum is clamped", rows: 10, args: map[string]any{"limit": "9000"}, wantRows: 10, wantNote: "limit clamped to 5000"},
		{name: "a full SigNoz response makes the omitted count a lower bound", rows: signozclient.TopOperationsLimit, args: map[string]any{"limit": 100}, wantRows: 100, wantNote: "at least 4900 more were omitted"},
		{name: "a full SigNoz response at the maximum limit says more may exist", rows: signozclient.TopOperationsLimit, args: map[string]any{"limit": 5000}, wantRows: signozclient.TopOperationsLimit, wantNote: "more results likely exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, ctx, sent := serveTopOperations(t, tc.rows)
			args := map[string]any{"service": "checkout"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations", args))
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)

			first, ok := mcp.AsTextContent(result.Content[0])
			require.True(t, ok)
			var ops []map[string]any
			require.NoError(t, json.Unmarshal([]byte(first.Text), &ops))
			require.Len(t, ops, tc.wantRows)
			require.Equal(t, "op-0000", ops[0]["name"], "the slowest operation comes first")
			require.Equal(t, fmt.Sprintf("op-%04d", tc.wantRows-1), ops[len(ops)-1]["name"], "the cut drops the fastest operations")

			var notes []string
			for _, c := range result.Content[1:] {
				if block, ok := mcp.AsTextContent(c); ok {
					notes = append(notes, block.Text)
				}
			}
			if tc.wantNote == "" {
				require.Empty(t, notes)
			} else {
				require.Contains(t, strings.Join(notes, "\n"), tc.wantNote)
			}
			// The handler, not SigNoz, applies the limit, so it can count the omitted rows.
			require.Len(t, *sent, 1)
			require.Equal(t, float64(signozclient.TopOperationsLimit), (*sent)[0]["limit"])
		})
	}
}

func TestGetServiceTopOperations_RejectsANonNumericLimit(t *testing.T) {
	h, ctx, sent := serveTopOperations(t, 5)
	result, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations",
		map[string]any{"service": "checkout", "limit": "all"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, CodeValidationFailed, resultCode(t, result))
	text, _ := mcp.AsTextContent(result.Content[0])
	require.Contains(t, text.Text, `"limit"`)
	require.Empty(t, *sent, "an invalid limit must not reach SigNoz")
}
