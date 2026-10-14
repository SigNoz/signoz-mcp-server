package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestInputMismatchServedBestEffortLogsRedactedRequestAndAttributedMetric(t *testing.T) {
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
	s := newMCPTestServer()
	tool := mcp.NewTool("shadow_probe")
	tool.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"webhook_password": map[string]any{"type": "number"}},
	}
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})

	ctx := util.SetClientSource(context.Background(), "ai-assistant")
	response := callTestToolFromJSONRPC(ctx, t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shadow_probe","arguments":{"searchContext":"create a webhook","webhook_password":"super-secret-value"}}}`))
	if !called {
		t.Fatal("input mismatch must not block the handler call")
	}
	callTestToolFromJSONRPC(ctx, t, s, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shadow_probe","arguments":{"searchContext":"second attempt","webhook_password":"another-secret-value"}}}`))
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	const wantNotice = `Input validation notice: parameter "webhook_password" did not fully match its advertised schema. The call still ran best-effort: mismatched values may have been ignored or replaced with defaults. Adjust the flagged parameter(s) and re-call if the results look off.`
	encodedNotice, err := json.Marshal(wantNotice)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, encodedNotice[1:len(encodedNotice)-1]) {
		t.Fatalf("successful result missing exact in-band validation notice: %s", encoded)
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
	if count := strings.Count(logs.String(), "tool schema validation mismatch"); count != 1 {
		t.Fatalf("mismatch warning count = %d, want one representative request per rate-limit window; logs=%s", count, logs.String())
	}
	if strings.Contains(logs.String(), "super-secret-value") || strings.Contains(logs.String(), "another-secret-value") {
		t.Fatalf("validation telemetry leaked raw argument values: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"mcp.request":`) || !strings.Contains(logs.String(), `create a webhook`) || !strings.Contains(logs.String(), `[REDACTED]`) {
		t.Fatalf("validation telemetry missing reproducible redacted requests: %s", logs.String())
	}
	if strings.Contains(logs.String(), `second attempt`) {
		t.Fatalf("identical mismatch inside rate-limit window should be suppressed: %s", logs.String())
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	sum, ok := oteltest.FindInt64SumMetric(collected, "mcp.tool.validation.mismatches")
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 2 {
		t.Fatalf("shadow mismatch counter = %#v, found=%t", sum, ok)
	}
	for key, want := range map[attribute.Key]string{
		attribute.Key("gen_ai.tool.name"):      "shadow_probe",
		attribute.Key("validation.direction"):  "input",
		attribute.Key("validation.path"):       "webhook_password",
		attribute.Key("validation.constraint"): "schema",
		otelpkg.MCPClientSourceKey:             "ai-assistant",
	} {
		got, present := sum.DataPoints[0].Attributes.Value(key)
		if !present || got.AsString() != want {
			t.Fatalf("mismatch metric %s = %v, present=%t; want %q", key, got, present, want)
		}
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
	s := newMCPTestServer()
	tool := mcp.NewTool("output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
	})

	response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"output_probe","arguments":{}}}`))
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
	s := newMCPTestServer()
	tool := mcp.NewTool("nil_output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("text-only"), nil
	})

	response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nil_output_probe","arguments":{}}}`))
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
	s := newMCPTestServer()
	tool := mcp.Tool{
		Name:        "broken_schema_probe",
		Description: "probe",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"$ref":"missing.json"}}}`),
	}
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"broken_schema_probe","arguments":{"secret":"do-not-log"}}}`))
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
