package mcp_server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpcontract "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const modernProtocolVersion = "2026-07-28"

type protocolHTTPResponse struct {
	status int
	header http.Header
	body   []byte
	wire   map[string]any
}

func protocolPOST(t *testing.T, handler http.Handler, body string, headers http.Header) protocolHTTPResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, values := range headers {
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	response := protocolHTTPResponse{status: rr.Code, header: rr.Header().Clone(), body: rr.Body.Bytes()}
	if strings.Contains(rr.Header().Get("Content-Type"), "application/json") && len(response.body) > 0 {
		if err := json.Unmarshal(response.body, &response.wire); err != nil {
			t.Fatalf("decode JSON response (status %d): %v\nbody: %s", rr.Code, err, rr.Body.String())
		}
	}
	return response
}

func modernParams(meta map[string]any, fields map[string]any) map[string]any {
	if meta == nil {
		meta = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    modernProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "protocol-test", "version": "1"},
		}
	}
	params := map[string]any{"_meta": meta}
	for key, value := range fields {
		params[key] = value
	}
	return params
}

func protocolRequestJSON(t *testing.T, id int, method string, params map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func modernHeaders(method, name string) http.Header {
	header := http.Header{
		"Mcp-Protocol-Version": {modernProtocolVersion},
		"Mcp-Method":           {method},
	}
	if name != "" {
		header.Set("Mcp-Name", name)
	}
	return header
}

func requireProtocolError(t *testing.T, response protocolHTTPResponse, status int, id int, code int, messageContains string) {
	t.Helper()
	if response.status != status {
		t.Fatalf("status = %d, want %d; body=%s", response.status, status, response.body)
	}
	if got := response.header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("Mcp-Session-Id = %q, want absent", got)
	}
	if response.wire["id"] != float64(id) {
		t.Fatalf("response id = %#v, want %d; body=%s", response.wire["id"], id, response.body)
	}
	errorObject, ok := response.wire["error"].(map[string]any)
	if !ok {
		t.Fatalf("response error = %#v, want object; body=%s", response.wire["error"], response.body)
	}
	if errorObject["code"] != float64(code) {
		t.Fatalf("error code = %#v, want %d; body=%s", errorObject["code"], code, response.body)
	}
	if message, _ := errorObject["message"].(string); !strings.Contains(message, messageContains) {
		t.Fatalf("error message = %q, want substring %q", message, messageContains)
	}
}

func TestHTTPServesCorrectModernHeadersAndHeaderlessLegacyRequests(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	const id = 41
	baseBody := protocolRequestJSON(t, id, "tools/call", modernParams(nil, map[string]any{
		"name":      "signoz_search_docs",
		"arguments": map[string]any{"searchText": "docker"},
	}))

	t.Run("correct modern headers are served", func(t *testing.T) {
		response := protocolPOST(t, oracle.handler, baseBody, modernHeaders("tools/call", "signoz_search_docs"))
		if response.status != http.StatusOK || response.wire["result"] == nil {
			t.Fatalf("correct modern request failed: status=%d body=%s", response.status, response.body)
		}
		if got := response.header.Get("Mcp-Session-Id"); got != "" {
			t.Fatalf("Mcp-Session-Id = %q, want absent", got)
		}
	})

	t.Run("legacy request needs no modern headers", func(t *testing.T) {
		body := protocolRequestJSON(t, 42, "tools/list", map[string]any{})
		response := protocolPOST(t, oracle.handler, body, http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
		if response.status != http.StatusOK || response.wire["result"] == nil {
			t.Fatalf("legacy request failed: status=%d body=%s", response.status, response.body)
		}
	})
}

func TestModernHTTPHeaderMismatchRejectsBeforeToolHandler(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20}
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	server := m.newSDKServer()
	called := false
	h.AddTool(server, mcpcontract.NewTool("header_probe"), func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
		called = true
		return mcpcontract.NewToolResultText("called"), nil
	})

	body := protocolRequestJSON(t, 61, "tools/call", modernParams(nil, map[string]any{"name": "header_probe", "arguments": map[string]any{}}))
	headers := modernHeaders("tools/call", "wrong-name")
	response := protocolPOST(t, m.buildHTTP(server).Handler, body, headers)
	requireProtocolError(t, response, http.StatusBadRequest, 61, -32020, "Mcp-Name header value")
	if called {
		t.Fatal("tool handler ran after standardized header rejection")
	}
}
