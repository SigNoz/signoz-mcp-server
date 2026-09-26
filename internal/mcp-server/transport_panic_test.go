package mcp_server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpcontract "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const transportPanicCanary = "secret-transport-panic-canary"

func newPanicTransportServer(t *testing.T) (*MCPServer, *mcp.Server) {
	t.Helper()
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20}
	h := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, h, cfg, nil, nil)
	sdkServer := server.newSDKServer()

	h.AddTool(sdkServer, mcpcontract.NewTool("panic_probe"), func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
		panic(transportPanicCanary)
	})
	sdkServer.AddResource(&mcp.Resource{Name: "panic resource", URI: "test://panic"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		panic(transportPanicCanary)
	})
	sdkServer.AddPrompt(&mcp.Prompt{Name: "panic_prompt"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		panic(transportPanicCanary)
	})
	return server, sdkServer
}

func TestProductionHTTPPanicsAreContainedAtTransport(t *testing.T) {
	server, sdkServer := newPanicTransportServer(t)
	handler := server.buildHTTP(sdkServer).Handler

	t.Run("tool returns coded result", func(t *testing.T) {
		response := protocolPOST(t, handler, protocolRequestJSON(t, 401, "tools/call", map[string]any{
			"name": "panic_probe", "arguments": map[string]any{},
		}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
		if response.status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", response.status, response.body)
		}
		result, ok := response.wire["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("tool result = %#v, want isError=true; body=%s", response.wire["result"], response.body)
		}
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok || structured["code"] != toolerrors.CodeInternalError {
			t.Fatalf("structuredContent = %#v, want code %s", result["structuredContent"], toolerrors.CodeInternalError)
		}
		assertNoTransportPanicCanary(t, response.body)
		assertHTTPTransportStillUsable(t, handler, 402)
	})

	tests := []struct {
		name   string
		id     int
		method string
		params map[string]any
	}{
		{name: "resource returns generic JSON-RPC error", id: 411, method: "resources/read", params: map[string]any{"uri": "test://panic"}},
		{name: "prompt returns generic JSON-RPC error", id: 421, method: "prompts/get", params: map[string]any{"name": "panic_prompt", "arguments": map[string]string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := protocolPOST(t, handler, protocolRequestJSON(t, tt.id, tt.method, tt.params), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
			requireProtocolError(t, response, http.StatusOK, tt.id, -32603, "Internal error")
			assertNoTransportPanicCanary(t, response.body)
			assertHTTPTransportStillUsable(t, handler, tt.id+1)
		})
	}
}

func TestProductionIOTransportToolPanicIsContained(t *testing.T) {
	_, sdkServer := newPanicTransportServer(t)
	session := newRawIOProtocolSession(t, sdkServer)

	initialize := protocolRequestJSON(t, 501, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "panic-io", "version": "1"},
	})
	if response := session.send(initialize, true); response["result"] == nil {
		t.Fatalf("initialize response = %#v, want result", response)
	}
	session.send(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, false)

	response := session.send(protocolRequestJSON(t, 502, "tools/call", map[string]any{
		"name": "panic_probe", "arguments": map[string]any{},
	}), true)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("tool result = %#v, want isError=true", response["result"])
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["code"] != toolerrors.CodeInternalError {
		t.Fatalf("structuredContent = %#v, want code %s", result["structuredContent"], toolerrors.CodeInternalError)
	}
	assertNoTransportPanicCanary(t, encoded)

	listed := session.send(protocolRequestJSON(t, 503, "tools/list", map[string]any{}), true)
	if listed["result"] == nil {
		t.Fatalf("tools/list after panic = %#v, want usable connection", listed)
	}
}

func assertHTTPTransportStillUsable(t *testing.T, handler http.Handler, id int) {
	t.Helper()
	response := protocolPOST(t, handler, protocolRequestJSON(t, id, "tools/list", map[string]any{}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
	if response.status != http.StatusOK || response.wire["result"] == nil {
		t.Fatalf("tools/list after panic = status %d body %s, want usable transport", response.status, response.body)
	}
}

func assertNoTransportPanicCanary(t *testing.T, body []byte) {
	t.Helper()
	if strings.Contains(string(body), transportPanicCanary) {
		t.Fatalf("panic canary leaked through transport: %s", body)
	}
}
