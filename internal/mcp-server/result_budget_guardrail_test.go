package mcp_server

import (
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

// TestGuardrail_RowSearchResultsFitTheirSizeBudget catches a raw row tool that
// the production server, as configured by default, lets outgrow the result size
// budget. The handler tests pin how a page is cut; this is the wiring.
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

	for index, tool := range []string{"signoz_search_logs", "signoz_search_traces"} {
		t.Run(tool, func(t *testing.T) {
			response := protocolPOST(t, httpHandler, protocolRequestJSON(t, 900+index, "tools/call", map[string]any{
				"name": tool, "arguments": map[string]any{"limit": 200},
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
			if textBytes > config.DefaultMaxResultBytes {
				t.Fatalf("%s delivered %d bytes of text for a %d-byte upstream page, over the default %d-byte result size budget. Return the page through rawSearchResult, which fits whole rows into the budget.", tool, textBytes, len(body), config.DefaultMaxResultBytes)
			}
		})
	}
}
