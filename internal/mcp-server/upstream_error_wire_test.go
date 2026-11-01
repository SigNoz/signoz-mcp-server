package mcp_server

import (
	"net/http"
	"net/http/httptest"
	"os"
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

	// The full guidance and field mapping are pinned on the same fixture in
	// internal/client and internal/handler/tools; this test pins that each
	// protocol era delivers them over the production HTTP transport.
	const unrecognizedCanary = "wire-proxy-secret-canary"
	tests := []struct {
		name         string
		modern       bool
		upstreamBody string
		wantText     string
	}{
		{
			name:         "legacy recognized renderer",
			upstreamBody: string(recognizedBody),
			wantText:     "did you mean: `service.name`",
		},
		{
			name:         "modern recognized renderer",
			modern:       true,
			upstreamBody: string(recognizedBody),
			wantText:     "did you mean: `service.name`",
		},
		{
			name:         "legacy unrecognized proxy",
			upstreamBody: `{"status":"error","error":"` + unrecognizedCanary + `"}`,
			wantText:     "SigNoz API error: unexpected status 400",
		},
		{
			name:         "modern unrecognized proxy",
			modern:       true,
			upstreamBody: `{"status":"error","error":"` + unrecognizedCanary + `"}`,
			wantText:     "SigNoz API error: unexpected status 400",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v2/services" || r.Header.Get("SIGNOZ-API-KEY") != "wire-test-key" {
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
			structured, _ := result["structuredContent"].(map[string]any)
			if structured["code"] != tools.CodeValidationFailed {
				t.Fatalf("structuredContent = %#v, want code %s", result["structuredContent"], tools.CodeValidationFailed)
			}
			content, ok := result["content"].([]any)
			if !ok || len(content) != 1 {
				t.Fatalf("content = %#v, want one text block", result["content"])
			}
			block, _ := content[0].(map[string]any)
			if text, _ := block["text"].(string); !strings.Contains(text, tt.wantText) {
				t.Fatalf("text = %q, want it to carry %q", text, tt.wantText)
			}
			if strings.Contains(string(response.body), unrecognizedCanary) {
				t.Fatalf("unrecognized upstream canary leaked through MCP: %s", response.body)
			}
		})
	}
}
