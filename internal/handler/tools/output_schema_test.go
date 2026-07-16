package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAllowlistedOutputToolsReturnStructuredContentOnSuccess(t *testing.T) {
	client := &signozclient.MockClient{
		ListAlertsFn: func(context.Context, types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
		ListAlertRulesFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
		CheckMetricUsageFn: func(context.Context, []string) (map[string]signozclient.MetricUsage, error) {
			return map[string]signozclient.MetricUsage{
				"cpu": {Dashboards: []string{"infra"}, Alerts: []string{}},
			}, nil
		},
	}
	h := newTestHandler(client)

	tests := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{"signoz_list_alerts", func() (*mcp.CallToolResult, error) {
			return h.handleListAlerts(ctxWithURL(), makeToolRequest("signoz_list_alerts", map[string]any{}))
		}},
		{"signoz_list_alert_rules", func() (*mcp.CallToolResult, error) {
			return h.handleListAlertRules(ctxWithURL(), makeToolRequest("signoz_list_alert_rules", map[string]any{}))
		}},
		{"signoz_check_metric_usage", func() (*mcp.CallToolResult, error) {
			return h.handleCheckMetricUsage(testCtx(), makeToolRequest("signoz_check_metric_usage", map[string]any{"metricNames": []any{"cpu"}}))
		}},
	}

	docsHandler, cleanup := newDocsTestHandler(t)
	defer cleanup()
	tests = append(tests,
		struct {
			name string
			call func() (*mcp.CallToolResult, error)
		}{"signoz_search_docs", func() (*mcp.CallToolResult, error) {
			return docsHandler.handleSearchDocs(testCtx(), makeToolRequest("signoz_search_docs", map[string]any{"searchText": "docker"}))
		}},
		struct {
			name string
			call func() (*mcp.CallToolResult, error)
		}{"signoz_search_docs zero hits", func() (*mcp.CallToolResult, error) {
			return docsHandler.handleSearchDocs(testCtx(), makeToolRequest("signoz_search_docs", map[string]any{"searchText": "zzqxjvkwpmnohitszz"}))
		}},
		struct {
			name string
			call func() (*mcp.CallToolResult, error)
		}{"signoz_fetch_doc", func() (*mcp.CallToolResult, error) {
			return docsHandler.handleFetchDoc(testCtx(), makeToolRequest("signoz_fetch_doc", map[string]any{"url": "/docs/install/docker/"}))
		}},
	)

	registered := registeredTestTools(t)
	outputValidators := map[string]*compiledToolSchema{}
	for name, entry := range registered {
		raw := outputSchemaJSON(entry.Tool)
		if len(raw) == 0 {
			continue
		}
		compiled, err := compileToolSchema(name, "output", raw)
		if err != nil {
			t.Fatalf("compile %s output schema: %v", name, err)
		}
		outputValidators[name] = compiled
	}

	for _, tt := range tests {
		toolName := strings.Fields(tt.name)[0]
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.call()
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("unexpected tool error: %#v", result)
			}
			if result.StructuredContent == nil {
				t.Fatal("successful allowlisted result has nil StructuredContent")
			}
			compiled := outputValidators[toolName]
			if compiled == nil {
				t.Fatalf("no advertised output schema for %s", toolName)
			}
			if err := validateSchemaValue(compiled.validator, result.StructuredContent, false); err != nil {
				t.Fatalf("successful %s result violates its advertised output schema: %v", toolName, err)
			}
		})
	}
}

func TestInputMismatchServedBestEffortAndNeverLogsArgumentValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(provider)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{logger: logger, meters: meters}
	s := server.NewMCPServer("test", "0.0.0")
	tool := mcp.NewTool("shadow_probe", mcp.WithNumber("webhook_password"))
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})

	response := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shadow_probe","arguments":{"webhook_password":"super-secret-value"}}}`))
	if !called {
		t.Fatal("input mismatch must not block the handler call")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), inputValidationNoticePrefix) {
		t.Fatalf("successful result missing in-band validation notice: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"ok"`) {
		t.Fatalf("original handler content must be preserved alongside the notice: %s", encoded)
	}
	if strings.Contains(string(encoded), "super-secret-value") {
		t.Fatalf("validation notice leaked raw argument values: %s", encoded)
	}
	if !strings.Contains(logs.String(), "tool schema validation mismatch") || !strings.Contains(logs.String(), `"validation.direction":"input"`) {
		t.Fatalf("missing shadow mismatch warning: %s", logs.String())
	}
	if strings.Contains(logs.String(), "super-secret-value") {
		t.Fatalf("validation telemetry leaked raw argument values: %s", logs.String())
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.validation.mismatches")
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("shadow mismatch counter = %#v, found=%t", sum, ok)
	}
}

func TestOutputMismatchPassesOriginalResultAndCounts(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(provider)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{logger: logger, meters: meters}
	s := server.NewMCPServer("test", "0.0.0")
	tool := mcp.NewTool("output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
	})

	response := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"output_probe","arguments":{}}}`))
	b, _ := json.Marshal(response)
	if !strings.Contains(string(b), `"count":"wrong"`) || strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("output mismatch did not pass the original result through: %s", b)
	}
	if !strings.Contains(logs.String(), `"validation.direction":"output"`) {
		t.Fatalf("missing bounded output mismatch warning: %s", logs.String())
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.validation.mismatches")
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("output shadow mismatch counter = %#v, found=%t", sum, ok)
	}
	direction, ok := sum.DataPoints[0].Attributes.Value(attribute.Key("validation.direction"))
	if !ok || direction.AsString() != "output" {
		t.Fatalf("output mismatch direction = %v, found=%t", direction, ok)
	}
}

func TestOutputSchemaSuccessWithoutStructuredContentWarnsAndCounts(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(provider)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{logger: logger, meters: meters}
	s := server.NewMCPServer("test", "0.0.0")
	tool := mcp.NewTool("nil_output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("text-only"), nil
	})

	response := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nil_output_probe","arguments":{}}}`))
	b, _ := json.Marshal(response)
	if !strings.Contains(string(b), "text-only") || strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("nil StructuredContent should fail open with the original result: %s", b)
	}
	if !strings.Contains(logs.String(), "successful schema-declaring tool returned no structured content") {
		t.Fatalf("missing nil-StructuredContent warning: %s", logs.String())
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.output.missing_structured_content")
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("missing structured-content counter = %#v, found=%t", sum, ok)
	}
}

func TestSchemaCompileFailureRegistersFailOpenAndCounts(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(provider)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{logger: logger, meters: meters}
	s := server.NewMCPServer("test", "0.0.0")
	tool := mcp.NewToolWithRawSchema("broken_schema_probe", "probe", json.RawMessage(`{"type":`))
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"broken_schema_probe","arguments":{"secret":"do-not-log"}}}`))
	if !called {
		t.Fatal("compile failure prevented fail-open handler registration")
	}
	if !strings.Contains(logs.String(), "tool schema compilation failed; validation disabled for schema") {
		t.Fatalf("missing compile-failure error log: %s", logs.String())
	}
	if strings.Contains(logs.String(), "do-not-log") {
		t.Fatalf("compile-failure telemetry leaked arguments: %s", logs.String())
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.schema.compile_failures")
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("compile-failure counter = %#v, found=%t", sum, ok)
	}
}
