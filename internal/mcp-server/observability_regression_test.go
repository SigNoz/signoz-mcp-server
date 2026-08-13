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
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpcontract "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
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

func TestReceivingMiddlewareAttributesModernRequestMetadataWithoutMetricCardinality(t *testing.T) {
	server, reader, exporter := testServerWithTelemetry(t, logpkg.New("error"))
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

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	for key, want := range map[attribute.Key]string{
		otelpkg.MCPProtocolVersionKey: modernProtocolVersion,
		otelpkg.MCPClientNameKey:      strings.Repeat("c", util.CallerCorrelationHeaderMaxLen),
		otelpkg.MCPClientVersionKey:   "2.3.4",
	} {
		got, ok := spanAttrValue(spans[0].Attributes, key)
		if !ok || got.AsString() != want {
			t.Fatalf("span %s = %v, want %q", key, got, want)
		}
	}
	for key, want := range map[attribute.Key]bool{
		otelpkg.MCPClientRootsKey:       false,
		otelpkg.MCPClientSamplingKey:    true,
		otelpkg.MCPClientElicitationKey: true,
	} {
		got, ok := spanAttrValue(spans[0].Attributes, key)
		if !ok || got.AsBool() != want {
			t.Fatalf("span %s = %v, want %t", key, got, want)
		}
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

func TestRegisteredToolObservabilityComposition(t *testing.T) {
	const (
		toolName      = "observability_probe"
		secretCanary  = "request-secret-canary"
		errorTail     = "error-tail-canary"
		requestLogCap = 1 << 20
		errorLogCap   = 4 << 10
	)
	largeError := strings.Repeat("upstream failure ", 400) + errorTail
	arguments, err := json.Marshal(map[string]any{
		"apiKey": secretCanary,
		"filter": strings.Repeat("x", requestLogCap+256),
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		handler       mcpcontract.ToolHandlerFunc
		wantCallError bool
		wantLevel     string
		wantIsError   bool
		wantErrorType string
		wantErrorCode string
		logErrorField string
	}{
		{
			name: "success",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				return mcpcontract.NewToolResultText("ok"), nil
			},
			wantLevel: "DEBUG",
		},
		{
			name: "coded error",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				result := mcpcontract.NewToolResultError(largeError)
				result.StructuredContent = map[string]any{"code": tools.CodePermissionDenied}
				return result, nil
			},
			wantLevel:     "WARN",
			wantIsError:   true,
			wantErrorType: "tool_error",
			wantErrorCode: tools.CodePermissionDenied,
			logErrorField: "error_message",
		},
		{
			name: "Go error",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				return nil, errors.New(largeError)
			},
			wantCallError: true,
			wantLevel:     "ERROR",
			wantIsError:   true,
			wantErrorType: "internal",
			logErrorField: "error",
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
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			handler := tools.NewHandler(logger, cfg)
			server := NewMCPServer(logger, handler, cfg, noopanalytics.New(), meters)
			sdkServer := server.newSDKServer()

			var correlatedTool string
			handler.AddTool(sdkServer, mcpcontract.NewTool(toolName), func(ctx context.Context, req mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				correlatedTool, _ = util.GetToolName(ctx)
				return tt.handler(ctx, req)
			})
			client, err := newIntegrationClient(t, sdkServer)
			if err != nil {
				t.Fatal(err)
			}
			_, callErr := client.client.CallTool(context.Background(), &mcp.CallToolParams{Name: toolName, Arguments: json.RawMessage(arguments)})
			if (callErr != nil) != tt.wantCallError {
				t.Fatalf("CallTool error = %v, want error=%t", callErr, tt.wantCallError)
			}
			if correlatedTool != toolName {
				t.Fatalf("handler tool correlation = %q, want %q", correlatedTool, toolName)
			}

			var terminal []map[string]any
			for _, record := range parseJSONLogLines(t, &logs) {
				if _, ok := record["mcp.tool.is_error"]; ok {
					terminal = append(terminal, record)
				}
			}
			if len(terminal) != 1 {
				t.Fatalf("terminal lifecycle logs = %d, want exactly one; all logs=%s", len(terminal), strings.TrimSpace(logs.String()))
			}
			record := terminal[0]
			if record["level"] != tt.wantLevel || record["mcp.tool.is_error"] != tt.wantIsError {
				t.Fatalf("terminal severity/error = %v/%v, want %s/%t", record["level"], record["mcp.tool.is_error"], tt.wantLevel, tt.wantIsError)
			}
			if record["gen_ai.tool.name"] != toolName {
				t.Fatalf("terminal tool correlation = %v, want %q", record["gen_ai.tool.name"], toolName)
			}
			if strings.Contains(logs.String(), secretCanary) {
				t.Fatal("registered pipeline logs leaked the credential-shaped argument")
			}
			if tt.wantIsError {
				requestText, ok := record["mcp.request"].(string)
				if !ok || len(requestText) > requestLogCap || !strings.HasSuffix(requestText, "...(truncated)") || !strings.Contains(requestText, "[REDACTED]") {
					t.Fatalf("bounded/redacted request = %T len=%d", record["mcp.request"], len(requestText))
				}
				errorText, ok := record[tt.logErrorField].(string)
				if !ok || len(errorText) > errorLogCap || !strings.HasSuffix(errorText, "...(truncated)") || strings.Contains(errorText, errorTail) {
					t.Fatalf("bounded %s = %T len=%d", tt.logErrorField, record[tt.logErrorField], len(errorText))
				}
			}

			var metrics metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &metrics); err != nil {
				t.Fatal(err)
			}
			toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
			if !found || len(toolCalls.DataPoints) != 1 || toolCalls.DataPoints[0].Value != 1 {
				t.Fatalf("mcp.tool.calls = %#v, found=%t; want one datapoint with value 1", toolCalls.DataPoints, found)
			}
			toolDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.tool.call.duration")
			if !found || len(toolDuration.DataPoints) != 1 || toolDuration.DataPoints[0].Count != 1 {
				t.Fatalf("mcp.tool.call.duration = %#v, found=%t; want one datapoint with count 1", toolDuration.DataPoints, found)
			}
			attrs := toolCalls.DataPoints[0].Attributes
			if got, _ := attrs.Value(otelpkg.GenAIToolNameKey); got.AsString() != toolName {
				t.Fatalf("metric tool correlation = %v, want %q", got, toolName)
			}
			if got, _ := attrs.Value(otelpkg.MCPToolIsErrorKey); got.AsBool() != tt.wantIsError {
				t.Fatalf("metric is_error = %v, want %t", got, tt.wantIsError)
			}
			gotErrorType, hasErrorType := attrs.Value(attribute.Key("error.type"))
			if tt.wantErrorType == "" {
				if hasErrorType {
					t.Fatalf("successful metric has error.type=%v", gotErrorType)
				}
			} else if !hasErrorType || gotErrorType.AsString() != tt.wantErrorType {
				t.Fatalf("metric error.type = %v, found=%t, want %q", gotErrorType, hasErrorType, tt.wantErrorType)
			}
			gotErrorCode, hasErrorCode := attrs.Value(otelpkg.MCPToolErrorCodeKey)
			if tt.wantErrorCode == "" {
				if hasErrorCode {
					t.Fatalf("metric has unexpected error code %v", gotErrorCode)
				}
			} else if !hasErrorCode || gotErrorCode.AsString() != tt.wantErrorCode {
				t.Fatalf("metric error code = %v, found=%t, want %q", gotErrorCode, hasErrorCode, tt.wantErrorCode)
			}
		})
	}
}

func TestAnalyticsIdentityModes(t *testing.T) {
	tests := []struct {
		name          string
		authHeader    string
		apiKey        string
		wantPath      string
		wantHeader    string
		response      string
		wantPrincipal string
		wantUserID    string
	}{
		{
			name:          "service account",
			authHeader:    "SIGNOZ-API-KEY",
			apiKey:        "service-key",
			wantPath:      "/api/v1/service_accounts/me",
			wantHeader:    "service-key",
			response:      `{"status":"success","data":{"id":"sa-1","name":"ingest-bot","email":"service@example.com","orgId":"org-1"}}`,
			wantPrincipal: "service_account",
			wantUserID:    "sa-1",
		},
		{
			name:          "JWT user",
			authHeader:    "Authorization",
			apiKey:        "Bearer jwt-token",
			wantPath:      "/api/v2/users/me",
			wantHeader:    "Bearer jwt-token",
			response:      `{"status":"success","data":{"id":"user-1","displayName":"Ada","email":"user@example.com","orgId":"org-1"}}`,
			wantPrincipal: "user",
			wantUserID:    "user-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("identity path = %q, want %q", r.URL.Path, tt.wantPath)
				}
				if got := r.Header.Get(tt.authHeader); got != tt.wantHeader {
					t.Errorf("%s = %q, want %q", tt.authHeader, got, tt.wantHeader)
				}
				_, _ = w.Write([]byte(tt.response))
			}))
			defer identity.Close()

			cfg := &config.Config{URL: identity.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			spy := &spyAnalytics{enabled: true}
			server := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, spy, nil)
			ctx := util.SetAPIKey(context.Background(), tt.apiKey)
			ctx = util.SetAuthHeader(ctx, tt.authHeader)
			ctx = util.SetSigNozURL(ctx, identity.URL)
			_, err := callReceiving(t, server, ctx, "tools/call", toolRequest("probe", `{}`), &mcp.CallToolResult{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := server.WaitForAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, calls := spy.snapshot()
			if len(calls) != 1 {
				t.Fatalf("analytics calls = %d, want 1", len(calls))
			}
			if calls[0].groupID != "org-1" || calls[0].userID != tt.wantUserID || calls[0].attrs[analytics.AttrPrincipal] != tt.wantPrincipal {
				t.Fatalf("analytics identity = %#v", calls[0])
			}
		})
	}
}

func TestAnalyticsLifecycleEventsAndCorrelation(t *testing.T) {
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","name":"bot","email":"svc@example.com","orgId":"org-1"}}`))
	}))
	defer identity.Close()

	cfg := &config.Config{URL: identity.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	spy := &spyAnalytics{enabled: true}
	server := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, spy, nil)
	ctx := util.SetAPIKey(context.Background(), "key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, identity.URL)
	ctx = util.SetClientSource(ctx, "ai-assistant")
	ctx = util.SetAssistantThreadID(ctx, "thread-1")
	ctx = util.SetAssistantExecutionID(ctx, "execution-1")

	calls := []struct {
		method string
		req    mcp.Request
		result mcp.Result
	}{
		{
			method: "initialize",
			req: &mcp.ServerRequest[*mcp.InitializeParams]{Params: &mcp.InitializeParams{
				ProtocolVersion: "2025-11-25",
				ClientInfo:      &mcp.Implementation{Name: "claude-desktop", Version: "1.2.3"},
			}},
			result: &mcp.InitializeResult{ProtocolVersion: "2025-11-25"},
		},
		{method: "prompts/get", req: &mcp.GetPromptRequest{Params: &mcp.GetPromptParams{Name: "rca"}}, result: &mcp.GetPromptResult{}},
		{method: "resources/read", req: &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: "signoz://docs/index"}}, result: &mcp.ReadResourceResult{}},
		{
			method: "tools/call",
			req:    toolRequest("probe", `{"searchContext":"find failures"}`),
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "denied"}}, StructuredContent: map[string]any{"code": tools.CodePermissionDenied}},
		},
	}
	for _, call := range calls {
		if _, err := callReceiving(t, server, ctx, call.method, call.req, call.result, nil); err != nil {
			t.Fatalf("%s: %v", call.method, err)
		}
	}
	if err := server.WaitForAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, tracked := spy.snapshot()
	if len(tracked) != len(calls) {
		t.Fatalf("analytics calls = %d, want %d", len(tracked), len(calls))
	}
	byEvent := make(map[string]analyticsCall, len(tracked))
	for _, call := range tracked {
		byEvent[call.event] = call
		for key, want := range map[string]any{
			analytics.AttrClientSource:         "ai-assistant",
			analytics.AttrAssistantThreadID:    "thread-1",
			analytics.AttrAssistantExecutionID: "execution-1",
		} {
			if got := call.attrs[key]; got != want {
				t.Fatalf("%s %s = %v, want %v", call.event, key, got, want)
			}
		}
		if _, exists := call.attrs["searchContext"]; exists {
			t.Fatalf("%s analytics leaked searchContext", call.event)
		}
	}
	if got := byEvent[analytics.EventClientInitialized].attrs[analytics.AttrClientName]; got != "claude-desktop" {
		t.Fatalf("initialize clientName = %v", got)
	}
	if got := byEvent[analytics.EventPromptFetched].attrs[analytics.AttrPromptName]; got != "rca" {
		t.Fatalf("promptName = %v", got)
	}
	if got := byEvent[analytics.EventResourceFetched].attrs[analytics.AttrResourceURI]; got != "signoz://docs/index" {
		t.Fatalf("resourceUri = %v", got)
	}
	if got := byEvent[analytics.EventToolCalled].attrs[analytics.AttrErrorType]; got != "permission_denied" {
		t.Fatalf("tool errorType = %v", got)
	}
}

func TestProductionHTTPInitializeEmitsClientAnalytics(t *testing.T) {
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			t.Fatalf("identity path = %q, want /api/v1/service_accounts/me", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","name":"bot","email":"svc@example.com","orgId":"org-1"}}`))
	}))
	defer identity.Close()

	logger := logpkg.New("error")
	cfg := &config.Config{
		URL:              identity.URL,
		APIKey:           "key",
		ClientCacheSize:  1,
		ClientCacheTTL:   time.Minute,
		MaxRequestBytes:  1 << 20,
		AnalyticsEnabled: true,
		TransportMode:    "http",
	}
	spy := &spyAnalytics{enabled: true}
	handler := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, handler, cfg, spy, nil)
	httpHandler := server.buildHTTP(server.newSDKServer()).Handler
	body := protocolRequestJSON(t, 301, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "real-http-client", "version": "9.1"},
	})
	response := protocolPOST(t, httpHandler, body, http.Header{
		"X-SigNoz-Client-Source":          {"ai-assistant"},
		"X-SigNoz-Assistant-Thread-Id":    {"thread-http"},
		"X-SigNoz-Assistant-Execution-Id": {"execution-http"},
	})
	if response.status != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200; body=%s", response.status, response.body)
	}
	if err := server.WaitForAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, tracked := spy.snapshot()
	if len(tracked) != 1 || tracked[0].event != analytics.EventClientInitialized {
		t.Fatalf("initialize analytics = %#v, want one %q event", tracked, analytics.EventClientInitialized)
	}
	for key, want := range map[string]any{
		analytics.AttrClientName:           "real-http-client",
		analytics.AttrClientVersion:        "9.1",
		analytics.AttrProtocolVersion:      "2025-11-25",
		analytics.AttrClientSource:         "ai-assistant",
		analytics.AttrAssistantThreadID:    "thread-http",
		analytics.AttrAssistantExecutionID: "execution-http",
	} {
		if got := tracked[0].attrs[key]; got != want {
			t.Fatalf("initialize analytics %s = %v, want %v", key, got, want)
		}
	}
}

func TestProductionHTTPFailureLogsProjectRequestParams(t *testing.T) {
	for _, tt := range []struct {
		name       string
		handler    mcpcontract.ToolHandlerFunc
		message    string
		wantStatus int
	}{
		{
			name: "coded result",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				result := mcpcontract.NewToolResultError("denied")
				result.StructuredContent = map[string]any{"code": tools.CodePermissionDenied}
				return result, nil
			},
			message:    "tool call returned error result",
			wantStatus: http.StatusOK,
		},
		{
			name: "Go error",
			handler: func(context.Context, mcpcontract.CallToolRequest) (*mcpcontract.CallToolResult, error) {
				return nil, errors.New("handler failed")
			},
			message:    "tool call failed",
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

			record, _ := logRecordByMessage(t, &logs, tt.message)
			requestText, ok := record["mcp.request"].(string)
			if !ok || requestText == "<unmarshalable>" {
				t.Fatalf("mcp.request = %#v, want marshalable params", record["mcp.request"])
			}
			for _, want := range []string{"http_failure_probe", "service.name = checkout", "[REDACTED]"} {
				if !strings.Contains(requestText, want) {
					t.Fatalf("mcp.request = %q, want %q", requestText, want)
				}
			}
			for _, secret := range []string{"argument-secret-canary", "header-secret-canary"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("failure logs leaked %q", secret)
				}
			}
		})
	}
}

func TestModernRequestAnalyticsCarryClientIdentityWithoutInitializedEvent(t *testing.T) {
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
	request := &mcp.GetPromptRequest{Params: &mcp.GetPromptParams{
		Name: "rca",
		Meta: mcp.Meta{
			mcp.MetaKeyProtocolVersion:    modernProtocolVersion,
			mcp.MetaKeyClientInfo:         map[string]any{"name": "modern-client", "version": "7"},
			mcp.MetaKeyClientCapabilities: map[string]any{},
		},
	}}
	if _, err := callReceiving(t, server, ctx, "prompts/get", request, &mcp.GetPromptResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := server.WaitForAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, tracked := spy.snapshot()
	if len(tracked) != 1 || tracked[0].event != analytics.EventPromptFetched {
		t.Fatalf("modern analytics events = %#v, want one prompt event and no initialized event", tracked)
	}
	for key, want := range map[string]any{
		analytics.AttrProtocolVersion: modernProtocolVersion,
		analytics.AttrClientName:      "modern-client",
		analytics.AttrClientVersion:   "7",
	} {
		if got := tracked[0].attrs[key]; got != want {
			t.Fatalf("prompt analytics %s = %v, want %v", key, got, want)
		}
	}
}

func TestAnalyticsDisabledAndDispatchIsAsync(t *testing.T) {
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
}

func TestNonToolMethodTerminalTelemetry(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		err       error
		wantError string
	}{
		{name: "success", method: "resources/list"},
		{name: "internal error", method: "prompts/list", err: errors.New("boom"), wantError: "internal"},
		{name: "cancelled", method: "resources/read", err: context.Canceled, wantError: "cancelled"},
		{name: "deadline", method: "prompts/get", err: context.DeadlineExceeded, wantError: "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, reader, exporter := testServerWithTelemetry(t, logpkg.New("error"))
			ctx := util.SetSigNozURL(context.Background(), "https://tenant.example.com")
			ctx = util.SetClientSource(ctx, "ai-assistant")
			_, gotErr := callReceiving(t, server, ctx, tt.method, nil, nil, tt.err)
			if !errors.Is(gotErr, tt.err) {
				t.Fatalf("error = %v, want %v", gotErr, tt.err)
			}

			var metrics metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &metrics); err != nil {
				t.Fatal(err)
			}
			methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
			if !found || len(methodCalls.DataPoints) != 1 || methodCalls.DataPoints[0].Value != 1 {
				t.Fatalf("method calls = %#v, found=%t", methodCalls.DataPoints, found)
			}
			methodDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.method.duration")
			if !found || len(methodDuration.DataPoints) != 1 || methodDuration.DataPoints[0].Count != 1 {
				t.Fatalf("method duration = %#v, found=%t", methodDuration.DataPoints, found)
			}
			attrs := methodCalls.DataPoints[0].Attributes
			if got, _ := attrs.Value(attribute.Key("mcp.method.name")); got.AsString() != tt.method {
				t.Fatalf("method = %v, want %s", got, tt.method)
			}
			if tt.wantError == "" {
				if _, ok := attrs.Value(attribute.Key("error.type")); ok {
					t.Fatal("successful method has error.type")
				}
			} else if got, _ := attrs.Value(attribute.Key("error.type")); got.AsString() != tt.wantError {
				t.Fatalf("metric error.type = %v, want %s", got, tt.wantError)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 || spans[0].Name != tt.method {
				t.Fatalf("spans = %#v, want one %s span", spans, tt.method)
			}
			if tt.wantError == "" {
				if spans[0].Status.Code == codes.Error {
					t.Fatal("successful method span is error")
				}
			} else {
				if spans[0].Status.Code != codes.Error {
					t.Fatal("failed method span is not error")
				}
				if got, _ := spanAttrValue(spans[0].Attributes, attribute.Key("error.type")); got.AsString() != tt.wantError {
					t.Fatalf("span error.type = %v, want %s", got, tt.wantError)
				}
			}
		})
	}
}

func TestTelemetryMetricsExcludeHighCardinalityCorrelation(t *testing.T) {
	server, reader, exporter := testServerWithTelemetry(t, logpkg.New("error"))
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
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want one tool span", len(spans))
	}
	searchContext, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPSearchContextKey)
	if !ok || searchContext.AsString() != "customer question" {
		t.Fatalf("span mcp.search_context = %v, want customer question", searchContext)
	}
}

func TestUnknownMethodIsNormalizedInMetrics(t *testing.T) {
	server, reader, _ := testServerWithTelemetry(t, logpkg.New("error"))
	err := &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: fmt.Sprintf("unknown method %q", strings.Repeat("x", 256))}
	_, _ = callReceiving(t, server, context.Background(), strings.Repeat("untrusted-", 128), nil, nil, err)
	var metrics metricdata.ResourceMetrics
	if collectErr := reader.Collect(context.Background(), &metrics); collectErr != nil {
		t.Fatal(collectErr)
	}
	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found || len(methodCalls.DataPoints) != 1 {
		t.Fatalf("method calls = %#v, found=%t", methodCalls.DataPoints, found)
	}
	if got, _ := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("mcp.method.name")); got.AsString() != otelpkg.UnknownMCPMethod {
		t.Fatalf("unknown method metric = %v, want %q", got, otelpkg.UnknownMCPMethod)
	}
}
