package mcp_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestGuardrail_RowSearchResultsFitTheirSizeBudget catches a raw row tool
// whose delivered text outgrows its result size budget: what MCP clients
// truncate, drop, or move to a file. Both tools go through the production HTTP
// handler against a SigNoz stub that returns about 400 KB of rows; the text
// blocks on the wire, notes and decorator notices included, must fit.
func TestGuardrail_RowSearchResultsFitTheirSizeBudget(t *testing.T) {
	rows := make([]string, 200)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"timestamp":"2026-11-21T10:00:00Z","data":{"id":"row-%03d","trace_id":"trace-%03d","duration_nano":9007199254740993,"body":%q}}`,
			i, i, strings.Repeat("payload ", 250))
	}
	body := `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","nextCursor":"c","rows":[` + strings.Join(rows, ",") + `]}]}}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{
		URL:             upstream.URL,
		APIKey:          "budget-guardrail-key",
		TransportMode:   "http",
		Host:            "127.0.0.1",
		Port:            "0",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
		MaxRequestBytes: 1 << 20,
	}
	handler := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, handler, cfg, nil, nil)
	sdkServer := server.newSDKServer()
	handler.RegisterLogsHandlers(sdkServer)
	handler.RegisterTracesHandlers(sdkServer)
	httpHandler := server.buildHTTP(sdkServer).Handler

	for index, tc := range []struct {
		name      string
		arguments map[string]any
		budget    int
	}{
		{name: "server default", arguments: map[string]any{"limit": 200}, budget: config.DefaultMaxResultBytes},
		{name: "per-call budget", arguments: map[string]any{"limit": 200, "maxResultBytes": "5000"}, budget: 5000},
		{name: "with decorator notices", arguments: map[string]any{"limit": 200, "maxResultBytes": 5000, "stray_argument_one": 1, "stray_argument_two": 2}, budget: 5000},
	} {
		for _, tool := range []string{"signoz_search_logs", "signoz_search_traces"} {
			t.Run(tool+" "+tc.name, func(t *testing.T) {
				response := protocolPOST(t, httpHandler, protocolRequestJSON(t, 900+index, "tools/call", map[string]any{
					"name": tool, "arguments": tc.arguments,
				}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
				if response.status != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%.400s", response.status, response.body)
				}
				result, _ := response.wire["result"].(map[string]any)
				if result == nil || result["isError"] == true {
					t.Fatalf("want a successful tool result, got %.400s", response.body)
				}
				content, _ := result["content"].([]any)
				textBytes := 0
				for _, block := range content {
					text, _ := block.(map[string]any)["text"].(string)
					textBytes += len(text)
				}
				if textBytes > tc.budget {
					t.Fatalf("%s delivered %d bytes of text, over its %d-byte result size budget. Fit whole rows into the budget in rawSearchResult and count every note the decorators append.", tool, textBytes, tc.budget)
				}

				block0, _ := content[0].(map[string]any)["text"].(string)
				var page struct {
					Pagination struct {
						Returned   int  `json:"returned"`
						HasMore    bool `json:"hasMore"`
						NextOffset int  `json:"nextOffset"`
						Truncated  bool `json:"truncated"`
					} `json:"pagination"`
				}
				if err := json.Unmarshal([]byte(block0), &page); err != nil {
					t.Fatalf("block 0 is not valid JSON: %v", err)
				}
				if got := page.Pagination; got.Returned < 1 || !got.HasMore || !got.Truncated || got.NextOffset != got.Returned {
					t.Fatalf("pagination = %+v, want at least one row, hasMore and truncated true, and nextOffset = returned", got)
				}
			})
		}
	}
}
