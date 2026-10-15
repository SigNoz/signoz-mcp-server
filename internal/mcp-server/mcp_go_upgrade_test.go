package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
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

func TestStreamableHTTPDNSRebindingProtection(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	m := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), nil)
	handler := official.NewStreamableHTTPHandler(func(*http.Request) *official.Server { return m.newSDKServer() }, m.streamableHTTPOptions())

	tests := []struct {
		name      string
		localAddr net.Addr
		host      string
		want403   bool
	}{
		{"loopback rejects public host", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}, "attacker.example", true},
		{"loopback allows localhost host", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}, "localhost:8000", false},
		{"pod ip allows public host", &net.TCPAddr{IP: net.ParseIP("10.42.1.7"), Port: 8000}, "mcp.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://unused/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Host = tt.host
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, tt.localAddr))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if got := res.Code == http.StatusForbidden; got != tt.want403 {
				t.Fatalf("status = %d, body=%q, want403=%t", res.Code, res.Body.String(), tt.want403)
			}
		})
	}
}

func TestSDKLoggerBoundsPersistentAttrs(t *testing.T) {
	var logs bytes.Buffer
	logger := newBufferedLogger(&logs, slog.LevelDebug)
	sdkLogger := slog.New(&sdkLogHandler{next: logger.Handler()}).With(
		slog.String("session_id", strings.Repeat("x", 8*1024)),
	)
	sdkLogger.Error("transport rejection")

	record, _ := logRecordByMessage(t, &logs, "transport rejection")
	value, ok := record["session_id"].(string)
	if !ok || len(value) > 4*1024 || !strings.HasSuffix(value, "...(truncated)") {
		t.Fatalf("persistent SDK attr = %T len=%d, want bounded string", record["session_id"], len(value))
	}
}

func TestInputMismatchServedWithNoticeThroughProductionPipeline(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
	s := m.newSDKServer()

	called := false
	h.AddTool(s, mcp.NewTool("notice_probe", mcp.WithString("value", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	response := callToolForTest(t, s, "notice_probe", json.RawMessage(`{"value":42}`))
	if !called {
		t.Fatal("input mismatch must be served best-effort, never rejected")
	}
	b, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"isError":true`)) {
		t.Fatalf("mismatched input must not produce an error result: %s", b)
	}
	if !bytes.Contains(b, []byte(`"ok"`)) {
		t.Fatalf("handler result must be preserved: %s", b)
	}
	// The appended notice tells self-correcting agents what to fix.
	for _, want := range []string{"input validation notice", `parameter \"value\"`, "best-effort", "re-call"} {
		if !bytes.Contains(bytes.ToLower(b), []byte(want)) {
			t.Fatalf("notice is not actionable (missing %q): %s", want, b)
		}
	}
}

func TestProductionOutputMismatchPassesOriginalThrough(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
	s := m.newSDKServer()
	tool := mcp.NewTool("probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.AddTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
	})
	response := callToolForTest(t, s, "probe", json.RawMessage(`{}`))
	b, _ := json.Marshal(response)
	if bytes.Contains(b, []byte(`"isError":true`)) || !bytes.Contains(b, []byte(`"count":"wrong"`)) {
		t.Fatalf("output mismatch must pass the original result through: %s", b)
	}
}

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

func callToolForTest(t *testing.T, server *official.Server, name string, arguments json.RawMessage) *official.CallToolResult {
	t.Helper()
	client, err := newIntegrationClient(t, server)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.client.CallTool(context.Background(), &official.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil
	}
	return result
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
