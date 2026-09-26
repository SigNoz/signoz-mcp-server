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
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestModernHTTPStandardHeaderValidation(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	const id = 41
	baseBody := protocolRequestJSON(t, id, "tools/call", modernParams(nil, map[string]any{
		"name":      "signoz_search_docs",
		"arguments": map[string]any{"searchText": "docker"},
	}))

	tests := []struct {
		name            string
		headers         http.Header
		wantMessagePart string
	}{
		{
			name: "protocol header and meta mismatch",
			headers: http.Header{
				"Mcp-Protocol-Version": {"2026-08-01"},
				"Mcp-Method":           {"tools/call"},
				"Mcp-Name":             {"signoz_search_docs"},
			},
			wantMessagePart: "does not match request",
		},
		{
			name: "method mismatch",
			headers: http.Header{
				"Mcp-Protocol-Version": {modernProtocolVersion},
				"Mcp-Method":           {"prompts/get"},
				"Mcp-Name":             {"signoz_search_docs"},
			},
			wantMessagePart: "Mcp-Method header value",
		},
		{
			name: "name mismatch",
			headers: http.Header{
				"Mcp-Protocol-Version": {modernProtocolVersion},
				"Mcp-Method":           {"tools/call"},
				"Mcp-Name":             {"signoz_fetch_doc"},
			},
			wantMessagePart: "Mcp-Name header value",
		},
		{
			name: "missing method",
			headers: http.Header{
				"Mcp-Protocol-Version": {modernProtocolVersion},
				"Mcp-Name":             {"signoz_search_docs"},
			},
			wantMessagePart: "missing required Mcp-Method",
		},
		{
			name: "missing name",
			headers: http.Header{
				"Mcp-Protocol-Version": {modernProtocolVersion},
				"Mcp-Method":           {"tools/call"},
			},
			wantMessagePart: "missing required Mcp-Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := protocolPOST(t, oracle.handler, baseBody, tt.headers)
			requireProtocolError(t, response, http.StatusBadRequest, id, -32020, tt.wantMessagePart)
		})
	}

	t.Run("correct headers are a positive control", func(t *testing.T) {
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

func TestModernHTTPMetadataValidation(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	tests := []struct {
		name        string
		headerVer   string
		meta        map[string]any
		wantCode    int
		wantMessage string
	}{
		{
			name:      "missing capabilities",
			headerVer: modernProtocolVersion,
			meta: map[string]any{
				"io.modelcontextprotocol/protocolVersion": modernProtocolVersion,
			},
			wantCode:    -32602,
			wantMessage: "clientCapabilities",
		},
		{
			name:      "invalid capabilities",
			headerVer: modernProtocolVersion,
			meta: map[string]any{
				"io.modelcontextprotocol/protocolVersion":    modernProtocolVersion,
				"io.modelcontextprotocol/clientCapabilities": "invalid",
			},
			wantCode:    -32602,
			wantMessage: "clientCapabilities",
		},
		{
			name:      "unsupported protocol",
			headerVer: "2026-08-01",
			meta: map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-08-01",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
			wantCode:    -32022,
			wantMessage: "unsupported protocol version",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := 50 + index
			body := protocolRequestJSON(t, id, "tools/list", modernParams(tt.meta, nil))
			headers := modernHeaders("tools/list", "")
			headers.Set("Mcp-Protocol-Version", tt.headerVer)
			response := protocolPOST(t, oracle.handler, body, headers)
			requireProtocolError(t, response, http.StatusBadRequest, id, tt.wantCode, tt.wantMessage)
		})
	}
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

type capabilityProbeParams struct{ mcp.ParamsBase }
type capabilityProbeResult struct{ mcp.ResultBase }

func TestModernHTTPMissingRequiredCapability(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20}
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	server := m.newSDKServer()
	called := false
	if err := mcp.AddReceivingCustomMethod(server, "test/capability-probe", func(context.Context, *mcp.ServerSession, *capabilityProbeParams) (*capabilityProbeResult, error) {
		called = true
		return &capabilityProbeResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "test/capability-probe" {
				data, err := json.Marshal(mcp.MissingRequiredClientCapabilityData{
					RequiredCapabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return nil, &jsonrpc.Error{Code: mcp.CodeMissingRequiredClientCapabilities, Message: "missing required client capability", Data: data}
			}
			return next(ctx, method, request)
		}
	})

	body := protocolRequestJSON(t, 62, "test/capability-probe", modernParams(nil, nil))
	response := protocolPOST(t, m.buildHTTP(server).Handler, body, modernHeaders("test/capability-probe", ""))
	requireProtocolError(t, response, http.StatusBadRequest, 62, -32021, "missing required client capability")
	if called {
		t.Fatal("capability-gated handler ran without its required capability")
	}
}
