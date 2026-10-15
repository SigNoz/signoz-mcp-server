package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpcontract "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func testServerWithTelemetry(t *testing.T, logger *slog.Logger) (*MCPServer, *sdkmetric.ManualReader, *tracetest.InMemoryExporter) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(meterProvider)
	if err != nil {
		t.Fatal(err)
	}

	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tracerProvider.Shutdown(context.Background())
	})

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	return NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters), reader, exporter
}

func assertToolResultBytes(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()

	server, _, exporter := testServerWithTelemetry(t, logpkg.New("error"))
	got, err := callReceiving(t, server, context.Background(), "tools/call", toolRequest("probe", `{}`), result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != result {
		t.Fatal("receiving middleware changed the result")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	size, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPToolResultBytesKey)
	if !ok {
		t.Fatalf("span missing %s", otelpkg.MCPToolResultBytesKey)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := size.AsInt64(), int64(len(encoded)); got != want {
		t.Fatalf("%s = %d, want exact serialized size %d for %s", otelpkg.MCPToolResultBytesKey, got, want, encoded)
	}
}

func TestGuardrail_ToolCallSpanHasSerializedResultBytes(t *testing.T) {
	body := strings.Repeat("x", 512)
	assertToolResultBytes(t, &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: body}},
		StructuredContent: map[string]any{"body": body},
	})
}

func TestGuardrail_EmptyToolResultIncludesSerializedEnvelopeBytes(t *testing.T) {
	assertToolResultBytes(t, &mcp.CallToolResult{})
}

func TestReceivingMiddlewareKeepsClientMetadataOutOfMetricLabels(t *testing.T) {
	server, reader, _ := testServerWithTelemetry(t, logpkg.New("error"))
	clientName := strings.Repeat("c", util.CallerCorrelationHeaderMaxLen+32)
	request := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{Meta: mcp.Meta{
		mcp.MetaKeyProtocolVersion: modernProtocolVersion,
		mcp.MetaKeyClientInfo: map[string]any{
			"name": clientName, "version": "2.3.4",
		},
		mcp.MetaKeyClientCapabilities: map[string]any{
			"sampling":    map[string]any{},
			"elicitation": map[string]any{},
		},
	}}}
	if _, err := callReceiving(t, server, context.Background(), "tools/list", request, &mcp.ListToolsResult{}, nil); err != nil {
		t.Fatal(err)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found || len(methodCalls.DataPoints) != 1 {
		t.Fatalf("method calls = %#v, found=%t", methodCalls.DataPoints, found)
	}
	encoded := methodCalls.DataPoints[0].Attributes.Encoded(attribute.DefaultEncoder())
	for _, forbidden := range []string{clientName, "2.3.4", modernProtocolVersion, "sampling", "elicitation"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("metric attributes contain span-only client metadata %q: %s", forbidden, encoded)
		}
	}
}

func TestReceivingMiddlewareErrorLogsAreRedactedAndBounded(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		err      error
	}{
		{
			name:     "handler error",
			toolName: "probe",
			err:      errors.New(strings.Repeat("upstream failure ", 400)),
		},
		{
			name:     "unknown tool",
			toolName: strings.Repeat("untrusted-name-", 400),
			err:      &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: strings.Repeat("unknown tool ", 400)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), nil)
			arguments, err := json.Marshal(map[string]any{
				"apiKey": "secret-canary",
				"filter": strings.Repeat("x", 1024*1024+128),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = callReceiving(t, server, context.Background(), "tools/call", toolRequest(tt.toolName, string(arguments)), nil, tt.err)

			methodLog, _ := logRecordByMessage(t, &logs, "mcp error")
			errorText, ok := methodLog["error"].(string)
			if !ok || len(errorText) > 4*1024 || !strings.HasSuffix(errorText, "...(truncated)") {
				t.Fatalf("bounded method error = %T len=%d", methodLog["error"], len(errorText))
			}
			terminal, _ := logRecordByMessage(t, &logs, "tool call failed")
			requestText, ok := terminal["mcp.request"].(string)
			if !ok || len(requestText) > 1024*1024 || !strings.HasSuffix(requestText, "...(truncated)") {
				t.Fatalf("bounded request = %T len=%d", terminal["mcp.request"], len(requestText))
			}
			if strings.Contains(logs.String(), "secret-canary") || !strings.Contains(requestText, "[REDACTED]") {
				t.Fatal("tool failure log did not redact the credential-shaped argument")
			}
		})
	}
}

func TestProductionHTTPFailureLogsOmitArgumentAndHeaderSecrets(t *testing.T) {
	for _, tt := range []struct {
		name       string
		handler    mcpcontract.ToolHandlerFunc
		wantStatus int
	}{
		{
			name: "coded result",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				result := mcpcontract.NewToolResultError("denied")
				result.StructuredContent = map[string]any{"code": tools.CodePermissionDenied}
				return result, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Go error",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				return nil, errors.New("handler failed")
			},
			wantStatus: http.StatusOK,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs lockedBuffer
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			cfg := &config.Config{
				URL:             "https://tenant.example.com",
				APIKey:          "test-key",
				ClientCacheSize: 1,
				ClientCacheTTL:  time.Minute,
				MaxRequestBytes: 1 << 20,
			}
			handler := tools.NewHandler(logger, cfg)
			server := NewMCPServer(logger, handler, cfg, noopanalytics.New(), nil)
			sdkServer := server.newSDKServer()
			handler.AddTool(sdkServer, mcpcontract.NewTool("http_failure_probe"), tt.handler)

			body := protocolRequestJSON(t, 302, "tools/call", map[string]any{
				"name": "http_failure_probe",
				"arguments": map[string]any{
					"apiKey": "argument-secret-canary",
					"filter": "service.name = checkout",
				},
			})
			response := protocolPOST(t, server.buildHTTP(sdkServer).Handler, body, http.Header{
				"Cookie":               {"session=header-secret-canary"},
				"Mcp-Protocol-Version": {"2025-11-25"},
			})
			if response.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.status, tt.wantStatus, response.body)
			}

			for _, secret := range []string{"argument-secret-canary", "header-secret-canary"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("failure logs leaked %q", secret)
				}
			}
		})
	}
}

func TestAnalyticsIsOptInAsyncAndOmitsSearchContext(t *testing.T) {
	t.Run("disabled skips identity lookup", func(t *testing.T) {
		var requests atomic.Int32
		identity := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
		defer identity.Close()
		cfg := &config.Config{URL: identity.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
		spy := &spyAnalytics{enabled: false}
		server := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, spy, nil)
		ctx := util.SetSigNozURL(context.Background(), identity.URL)
		_, _ = callReceiving(t, server, ctx, "tools/call", toolRequest("probe", `{}`), &mcp.CallToolResult{}, nil)
		if requests.Load() != 0 {
			t.Fatalf("identity requests = %d, want 0", requests.Load())
		}
	})

	t.Run("identity lookup does not block tool completion", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1"}}`))
		}))
		defer identity.Close()
		cfg := &config.Config{URL: identity.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
		spy := &spyAnalytics{enabled: true}
		server := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, spy, nil)
		ctx := util.SetAPIKey(context.Background(), "key")
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		ctx = util.SetSigNozURL(ctx, identity.URL)

		done := make(chan error, 1)
		go func() {
			_, err := callReceiving(t, server, ctx, "tools/call", toolRequest("probe", `{}`), &mcp.CallToolResult{}, nil)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-started:
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				close(release)
				t.Fatal("tool completion waited for analytics identity lookup")
			}
		case <-time.After(time.Second):
			close(release)
			t.Fatal("analytics identity lookup did not start")
		}
		close(release)
		if err := server.WaitForAnalytics(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("tool events omit the caller's searchContext", func(t *testing.T) {
		identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1"}}`))
		}))
		defer identity.Close()
		cfg := &config.Config{URL: identity.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
		spy := &spyAnalytics{enabled: true}
		server := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, spy, nil)
		ctx := util.SetAPIKey(context.Background(), "key")
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		ctx = util.SetSigNozURL(ctx, identity.URL)
		if _, err := callReceiving(t, server, ctx, "tools/call", toolRequest("probe", `{"searchContext":"search-context-canary"}`), &mcp.CallToolResult{}, nil); err != nil {
			t.Fatal(err)
		}
		if err := server.WaitForAnalytics(context.Background()); err != nil {
			t.Fatal(err)
		}

		_, tracked := spy.snapshot()
		if len(tracked) != 1 {
			t.Fatalf("analytics calls = %d, want 1", len(tracked))
		}
		if encoded, err := json.Marshal(tracked[0].attrs); err != nil || bytes.Contains(encoded, []byte("search-context-canary")) {
			t.Fatalf("analytics event carried the searchContext: %s (err=%v)", encoded, err)
		}
	})
}

func TestTelemetryMetricsExcludeHighCardinalityCorrelation(t *testing.T) {
	server, reader, _ := testServerWithTelemetry(t, logpkg.New("error"))
	ctx := util.SetSigNozURL(context.Background(), "https://tenant.example.com")
	ctx = util.SetClientSource(ctx, "ai-assistant")
	ctx = util.SetAssistantThreadID(ctx, "thread-uuid")
	ctx = util.SetAssistantExecutionID(ctx, "execution-uuid")
	_, err := callReceiving(t, server, ctx, "tools/call", toolRequest("probe", `{"searchContext":"customer question"}`), &mcp.CallToolResult{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
	if !found || len(toolCalls.DataPoints) != 1 {
		t.Fatalf("tool calls = %#v, found=%t", toolCalls.DataPoints, found)
	}
	toolDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.tool.call.duration")
	if !found || len(toolDuration.DataPoints) != 1 {
		t.Fatalf("tool duration = %#v, found=%t", toolDuration.DataPoints, found)
	}
	for name, attrs := range map[string]attribute.Set{
		"mcp.tool.calls":         toolCalls.DataPoints[0].Attributes,
		"mcp.tool.call.duration": toolDuration.DataPoints[0].Attributes,
	} {
		if got, _ := attrs.Value(otelpkg.MCPClientSourceKey); got.AsString() != "ai-assistant" {
			t.Fatalf("%s client source = %v", name, got)
		}
		for _, key := range []attribute.Key{otelpkg.MCPAssistantThreadIDKey, otelpkg.MCPAssistantExecutionIDKey, otelpkg.MCPSearchContextKey} {
			if _, ok := attrs.Value(key); ok {
				t.Fatalf("%s contains high-cardinality attribute %s", name, key)
			}
		}
	}
}

func TestSecretBearingToolTelemetryScrubsSearchContextAndErrorText(t *testing.T) {
	const secret = "https://hooks.example.test/credential-url-canary"
	for _, toolName := range []string{"signoz_create_notification_channel", "signoz_update_notification_channel"} {
		t.Run(toolName, func(t *testing.T) {
			identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1"}}`))
			}))
			defer identity.Close()

			var logs bytes.Buffer
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			server, _, exporter := testServerWithTelemetry(t, logger)
			spy := &spyAnalytics{enabled: true}
			server.analytics = spy

			ctx := util.SetAPIKey(context.Background(), "test-key")
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			ctx = util.SetSigNozURL(ctx, identity.URL)
			result := &mcp.CallToolResult{
				IsError:           true,
				Content:           []mcp.Content{&mcp.TextContent{Text: "upstream rejected " + secret}},
				StructuredContent: map[string]any{"code": tools.CodeValidationFailed, "status": http.StatusBadRequest},
			}
			request := toolRequest(toolName, `{"searchContext":"`+secret+`","config":{"kind":"webhook","spec":{"url":"`+secret+`"}}}`)
			got, err := server.receivingMiddleware(func(name string) bool { return name == toolName })(func(context.Context, string, mcp.Request) (mcp.Result, error) {
				return result, nil
			})(ctx, "tools/call", request)
			if err != nil || got != result {
				t.Fatalf("tool observation changed result: got=%v err=%v", got, err)
			}
			if err := server.WaitForAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}

			if strings.Contains(logs.String(), secret) {
				t.Fatalf("logs leaked secret-bearing tool text: %s", logs.String())
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("span count = %d, want 1", len(spans))
			}
			if _, present := spanAttrValue(spans[0].Attributes, otelpkg.MCPSearchContextKey); present {
				t.Fatal("secret-bearing tool span retained mcp.search_context")
			}
			if strings.Contains(fmt.Sprintf("%#v", spans[0]), secret) {
				t.Fatal("span events or status leaked secret-bearing error text")
			}

			_, tracked := spy.snapshot()
			if len(tracked) != 1 {
				t.Fatalf("analytics calls = %#v", tracked)
			}
			if encoded, marshalErr := json.Marshal(tracked[0].attrs); marshalErr != nil || bytes.Contains(encoded, []byte(secret)) {
				t.Fatalf("analytics leaked secret-bearing text: %s (err=%v)", encoded, marshalErr)
			}
		})
	}
}
