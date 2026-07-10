package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestStreamableHTTPDNSRebindingProtection(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	m := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), nil)
	handler := server.NewStreamableHTTPServer(m.newSDKServer(), m.streamableHTTPOptions()...)

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

func TestStreamableHTTPLoggerUsesServerSlogLevelAndFields(t *testing.T) {
	var logs bytes.Buffer
	logger := newBufferedLogger(&logs, 0)
	m := NewMCPServer(logger, nil, &config.Config{}, noopanalytics.New(), nil)
	options := append(m.streamableHTTPOptions(), server.WithDisableStreaming(true))
	handler := server.NewStreamableHTTPServer(server.NewMCPServer("test", "0.0.0"), options...)

	req := httptest.NewRequest(http.MethodGet, "http://unused/mcp", nil)
	req.Host = "mcp.example.com"
	req.Header.Set(server.HeaderKeySessionID, "session-1")
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("10.42.1.7"), Port: 8000}))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	// Only the wiring is asserted (SDK transport events reach our slog
	// handler); exact upstream wording is the SDK's to change.
	if len(parseJSONLogLines(t, &logs)) == 0 {
		t.Fatal("SDK transport rejection produced no records through the server slog logger")
	}
}

func TestEnforceModeInputRejectionUsesCodedErrorContract(t *testing.T) {
	cfg := &config.Config{InputValidationMode: config.InputValidationEnforce}
	logger := logpkg.New("error")
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
	s := m.newSDKServer()

	called := false
	h.AddTool(s, mcp.NewTool("coded_probe", mcp.WithString("value", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	response := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"coded_probe","arguments":{"value":42}}}`))
	if called {
		t.Fatal("enforce mode allowed invalid input to reach the handler")
	}
	b, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"code":"VALIDATION_FAILED"`)) {
		t.Fatalf("input rejection must carry the stable coded-error contract: %s", b)
	}
	for _, want := range []string{"coded_probe", "/value", "string"} {
		if !bytes.Contains(bytes.ToLower(b), []byte(want)) {
			t.Fatalf("rejection is not actionable (missing %q): %s", want, b)
		}
	}
}

func TestProductionOutputValidationModeWiring(t *testing.T) {
	for _, tt := range []struct {
		mode       config.InputValidationMode
		wantReject bool
	}{
		{mode: config.InputValidationOff},
		{mode: config.InputValidationShadow},
		{mode: config.InputValidationEnforce, wantReject: true},
	} {
		t.Run(string(tt.mode), func(t *testing.T) {
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute, InputValidationMode: tt.mode}
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
			response := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":{}}}`))
			b, _ := json.Marshal(response)
			if got := bytes.Contains(b, []byte(tools.OutputValidationErrorCode)); got != tt.wantReject {
				t.Fatalf("output rejected = %t, want %t: %s", got, tt.wantReject, b)
			}
			if !tt.wantReject && !bytes.Contains(b, []byte(`"count":"wrong"`)) {
				t.Fatalf("mode %s did not pass original result through: %s", tt.mode, b)
			}
		})
	}
}

func TestToolTerminalTelemetryIsExactlyOnce(t *testing.T) {
	tests := []struct {
		name           string
		tool           mcp.Tool
		arguments      string
		handler        server.ToolHandlerFunc
		wantCalled     bool
		wantLog        string
		wantLevel      string
		wantToolCalls  int64
		wantRejections int64
		wantDirection  string
	}{
		{
			name:      "input rejection",
			tool:      mcp.NewTool("probe", mcp.WithString("value", mcp.Required())),
			arguments: `{"value":42}`,
			// The decorator rejects before the inner handler, and the
			// middleware logs the error result like any other tool failure.
			wantLog:        "tool call returned error result",
			wantLevel:      "WARN",
			wantToolCalls:  1,
			wantRejections: 1,
			wantDirection:  "input",
		},
		{
			name: "output rejection",
			tool: mcp.NewTool("probe", mcp.WithOutputSchema[struct {
				Count int `json:"count"`
			}]()),
			arguments: `{}`,
			handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
			},
			wantCalled:     true,
			wantLog:        "tool call returned error result",
			wantLevel:      "WARN",
			wantToolCalls:  1,
			wantRejections: 1,
			wantDirection:  "output",
		},
		{
			name:      "normal success",
			tool:      mcp.NewTool("probe"),
			arguments: `{}`,
			handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			},
			wantCalled:    true,
			wantLog:       "tool call finished",
			wantLevel:     "DEBUG",
			wantToolCalls: 1,
		},
		{
			name:      "handler error",
			tool:      mcp.NewTool("probe"),
			arguments: `{}`,
			handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errors.New("handler failed")
			},
			wantCalled:    true,
			wantLog:       "tool call failed",
			wantLevel:     "ERROR",
			wantToolCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			meters, err := otelpkg.NewMeters(provider)
			if err != nil {
				t.Fatal(err)
			}
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute, InputValidationMode: config.InputValidationEnforce}
			h := tools.NewHandler(logger, cfg)
			m := NewMCPServer(logger, h, cfg, noopanalytics.New(), meters)
			s := m.newSDKServer()
			called := false
			next := tt.handler
			if next == nil {
				next = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultText("unexpected"), nil
				}
			}
			h.AddTool(s, tt.tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return next(ctx, req)
			})
			raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":` + tt.arguments + `}}`
			s.HandleMessage(context.Background(), json.RawMessage(raw))
			if called != tt.wantCalled {
				t.Fatalf("handler called = %t, want %t", called, tt.wantCalled)
			}

			classified := map[string]struct{}{
				"tool call rejected by schema validation": {},
				"tool call returned error result":         {},
				"tool call finished":                      {},
				"tool call failed":                        {},
			}
			var terminal []map[string]any
			for _, record := range parseJSONLogLines(t, &logs) {
				if _, ok := classified[record["msg"].(string)]; ok {
					terminal = append(terminal, record)
				}
			}
			if len(terminal) != 1 || terminal[0]["msg"] != tt.wantLog || terminal[0]["level"] != tt.wantLevel {
				t.Fatalf("classified terminal logs = %#v, want one %s/%s; all logs=%s", terminal, tt.wantLevel, tt.wantLog, strings.TrimSpace(logs.String()))
			}

			var collected metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &collected); err != nil {
				t.Fatal(err)
			}
			if got := int64MetricTotal(collected, "mcp.tool.calls"); got != tt.wantToolCalls {
				t.Fatalf("mcp.tool.calls = %d, want %d", got, tt.wantToolCalls)
			}
			if got := int64MetricTotal(collected, "mcp.tool.validation.rejections"); got != tt.wantRejections {
				t.Fatalf("mcp.tool.validation.rejections = %d, want %d", got, tt.wantRejections)
			}
			if got := int64MetricTotal(collected, "mcp.tool.validation.mismatches"); got != 0 {
				t.Fatalf("mcp.tool.validation.mismatches = %d, want 0", got)
			}
			if tt.wantDirection != "" {
				sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.validation.rejections")
				if !ok || len(sum.DataPoints) != 1 {
					t.Fatalf("validation rejection datapoints = %#v, found=%t", sum, ok)
				}
				direction, ok := sum.DataPoints[0].Attributes.Value(attribute.Key("validation.direction"))
				if !ok || direction.AsString() != tt.wantDirection {
					t.Fatalf("validation direction = %v, found=%t, want %s", direction, ok, tt.wantDirection)
				}
			}
		})
	}
}

func int64MetricTotal(metrics metricdata.ResourceMetrics, name string) int64 {
	sum, ok := oteltest.FindInt64SumMetric(metrics, name)
	if !ok {
		return 0
	}
	var total int64
	for _, point := range sum.DataPoints {
		total += point.Value
	}
	return total
}
