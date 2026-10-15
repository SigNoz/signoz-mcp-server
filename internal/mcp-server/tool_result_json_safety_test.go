package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGuardrail_ToolResultsRemainJSONSafeThroughProductionTransport(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		wantError bool
		wantJSON  string
	}{
		{name: "nan", value: math.NaN(), wantError: true},
		{name: "positive infinity", value: math.Inf(1), wantError: true},
		{name: "negative infinity", value: math.Inf(-1), wantError: true},
		{name: "large integer", value: int64(9007199254740993), wantJSON: "9007199254740993"},
		{name: "invalid utf8", value: string([]byte{'a', 0xff, 'b'})},
		{name: "control characters", value: "line\nnull\x00tab\t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			logger := logpkg.New("error")
			h := tools.NewHandler(logger, cfg)
			m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
			s := m.newSDKServer()

			h.AddTool(s, mcp.NewTool("json_safety_probe"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultStructured(map[string]any{"value": tt.value}, "ok"), nil
			})
			encoded := callToolWireForTest(t, s, "json_safety_probe", json.RawMessage(`{}`))
			if !json.Valid(encoded) {
				t.Fatalf("JSON-RPC response is invalid: %q", encoded)
			}

			if tt.wantError {
				if !bytes.Contains(encoded, []byte(`"isError":true`)) || !bytes.Contains(encoded, []byte(toolerrors.CodeInternalError)) {
					t.Fatalf("unsafe numeric result must become a coded tool error: %s", encoded)
				}
				return
			}
			if bytes.Contains(encoded, []byte(`"isError":true`)) {
				t.Fatalf("JSON-safe value was rejected: %s", encoded)
			}
			if tt.wantJSON != "" && !bytes.Contains(encoded, []byte(tt.wantJSON)) {
				t.Fatalf("serialized response lost exact value %s: %s", tt.wantJSON, encoded)
			}
		})
	}
}

func callToolWireForTest(t *testing.T, server *official.Server, name string, arguments json.RawMessage) []byte {
	t.Helper()
	handler := official.NewStreamableHTTPHandler(func(*http.Request) *official.Server { return server }, &official.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` + string(params) + `}`
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(call))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Body.Bytes()
}
