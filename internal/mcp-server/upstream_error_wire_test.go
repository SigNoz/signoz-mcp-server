package mcp_server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func TestProductionHTTPUpstreamErrorGuidanceWire(t *testing.T) {
	recognizedBody, err := os.ReadFile("../client/testdata/current-renderer-error.json")
	if err != nil {
		t.Fatal(err)
	}

	const unrecognizedCanary = "wire-proxy-secret-canary"
	recognizedText := []string{
		"The query is invalid.",
		"field `service.nam` was not found",
		"Documentation: https://signoz.io/docs/userguide/search-troubleshooting/",
		"Suggestions: Use an existing field key.",
		"Suggestions for \"field `service.nam` was not found\": did you mean: `service.name`",
		"Retry delay: 5s (5000000000 ns)",
	}
	recognizedStructured := map[string]any{
		"code":                tools.CodeValidationFailed,
		"status":              float64(http.StatusBadRequest),
		"upstreamCode":        "invalid_input",
		"upstreamType":        "invalid-input",
		"upstreamMessage":     "The query is invalid.",
		"upstreamURL":         "https://signoz.io/docs/userguide/search-troubleshooting/",
		"upstreamSuggestions": []any{"Use an existing field key."},
		"upstreamDetails": []any{map[string]any{
			"message":     "field `service.nam` was not found",
			"suggestions": []any{"did you mean: `service.name`"},
		}},
		"upstreamRetry": map[string]any{"delay": float64(5_000_000_000)},
	}
	unrecognizedStructured := map[string]any{
		"code": tools.CodeValidationFailed, "status": float64(http.StatusBadRequest),
	}

	tests := []struct {
		name           string
		modern         bool
		upstreamBody   string
		wantStructured map[string]any
		wantText       []string
		wantExactText  string
	}{
		{
			name:           "legacy recognized renderer",
			upstreamBody:   string(recognizedBody),
			wantStructured: recognizedStructured,
			wantText:       recognizedText,
		},
		{
			name:           "modern recognized renderer",
			modern:         true,
			upstreamBody:   string(recognizedBody),
			wantStructured: recognizedStructured,
			wantText:       recognizedText,
		},
		{
			name:           "legacy unrecognized proxy",
			upstreamBody:   `{"status":"error","error":"` + unrecognizedCanary + `"}`,
			wantStructured: unrecognizedStructured,
			wantExactText:  "SigNoz API error: unexpected status 400",
		},
		{
			name:           "modern unrecognized proxy",
			modern:         true,
			upstreamBody:   `{"status":"error","error":"` + unrecognizedCanary + `"}`,
			wantStructured: unrecognizedStructured,
			wantExactText:  "SigNoz API error: unexpected status 400",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/services" || r.Header.Get("SIGNOZ-API-KEY") != "wire-test-key" {
					http.Error(w, "unexpected services request", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tt.upstreamBody))
			}))
			t.Cleanup(upstream.Close)

			logger := logpkg.New("error")
			cfg := &config.Config{
				URL:             upstream.URL,
				APIKey:          "wire-test-key",
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
			handler.RegisterServiceHandlers(sdkServer)
			httpHandler := server.buildHTTP(sdkServer).Handler

			var response protocolHTTPResponse
			if tt.modern {
				response = protocolPOST(t, httpHandler, protocolRequestJSON(t, 700+index, "tools/call", modernParams(nil, map[string]any{
					"name": "signoz_list_services", "arguments": map[string]any{},
				})), modernHeaders("tools/call", "signoz_list_services"))
			} else {
				initialize := protocolPOST(t, httpHandler, protocolRequestJSON(t, 600+index, "initialize", map[string]any{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]any{},
					"clientInfo":      map[string]any{"name": "upstream-wire-test", "version": "1"},
				}), nil)
				if initialize.status != http.StatusOK || initialize.wire["result"] == nil {
					t.Fatalf("initialize = status %d body %s, want JSON-RPC result", initialize.status, initialize.body)
				}
				if got := initialize.header.Get("Mcp-Session-Id"); got != "" {
					t.Fatalf("initialize Mcp-Session-Id = %q, want absent", got)
				}
				initialized := protocolPOST(t, httpHandler, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, http.Header{
					"Mcp-Protocol-Version": {"2025-11-25"},
				})
				if initialized.status != http.StatusAccepted || len(initialized.body) != 0 {
					t.Fatalf("initialized notification = status %d body %q, want 202 with empty body", initialized.status, initialized.body)
				}
				if got := initialized.header.Get("Mcp-Session-Id"); got != "" {
					t.Fatalf("initialized notification Mcp-Session-Id = %q, want absent", got)
				}
				response = protocolPOST(t, httpHandler, protocolRequestJSON(t, 700+index, "tools/call", map[string]any{
					"name": "signoz_list_services", "arguments": map[string]any{},
				}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
			}

			if response.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", response.status, response.body)
			}
			if got := response.header.Get("Mcp-Session-Id"); got != "" {
				t.Fatalf("Mcp-Session-Id = %q, want absent", got)
			}
			result, ok := response.wire["result"].(map[string]any)
			if !ok || result["isError"] != true {
				t.Fatalf("result = %#v, want isError=true", response.wire["result"])
			}
			structured, ok := result["structuredContent"].(map[string]any)
			if !ok || !reflect.DeepEqual(structured, tt.wantStructured) {
				t.Fatalf("structuredContent = %#v, want %#v", result["structuredContent"], tt.wantStructured)
			}
			content, ok := result["content"].([]any)
			if !ok || len(content) != 1 {
				t.Fatalf("content = %#v, want one text block", result["content"])
			}
			block, ok := content[0].(map[string]any)
			text, textOK := block["text"].(string)
			if !ok || !textOK || block["type"] != "text" {
				t.Fatalf("content[0] = %#v, want text block", content[0])
			}
			if tt.wantExactText != "" && text != tt.wantExactText {
				t.Fatalf("text = %q, want %q", text, tt.wantExactText)
			}
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Fatalf("text omitted %q: %q", want, text)
				}
			}
			if strings.Contains(string(response.body), unrecognizedCanary) {
				t.Fatalf("unrecognized upstream canary leaked through MCP: %s", response.body)
			}
		})
	}
}
