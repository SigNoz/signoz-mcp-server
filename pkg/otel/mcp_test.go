package otel

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcptracing "github.com/mark3labs/mcp-go/tracing"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestMCPTracerNormalizesRequestSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	baseTracer := provider.Tracer("test")
	transportCtx, transportSpan := baseTracer.Start(context.Background(), "HTTP POST",
		trace.WithSpanKind(trace.SpanKindServer))

	mcpTracer := NewMCPTracer(baseTracer)
	mcpCtx, mcpSpan := mcpTracer.Start(transportCtx, "mcp.tools/list", mcptracing.SpanKindServer,
		mcptracing.String(mcpSDKMethodAttr, "tools/list"),
		mcptracing.String(mcpSDKProtocolVersionAttr, "2025-11-25"),
		mcptracing.String(mcpSDKSessionIDAttr, "caller-controlled"),
	)
	mcptracing.SpanFromContext(mcpCtx).SetAttributes(mcptracing.String("custom", "value"))
	mcpSpan.End()
	transportSpan.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("span count = %d, want 2", len(spans))
	}

	var requestSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "tools/list" {
			requestSpan = &spans[i]
			break
		}
	}
	if requestSpan == nil {
		t.Fatal("normalized MCP request span not found")
	}
	if requestSpan.SpanKind != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want SERVER", requestSpan.SpanKind)
	}
	if requestSpan.Parent.SpanID() != transportSpan.SpanContext().SpanID() {
		t.Fatalf("parent span ID = %s, want transport span %s",
			requestSpan.Parent.SpanID(), transportSpan.SpanContext().SpanID())
	}
	if len(requestSpan.Links) != 0 {
		t.Fatalf("links = %d, want 0", len(requestSpan.Links))
	}

	attrs := make(map[attribute.Key]attribute.Value)
	for _, attr := range requestSpan.Attributes {
		attrs[attr.Key] = attr.Value
	}
	if got := attrs[MCPMethodKey].AsString(); got != "tools/list" {
		t.Fatalf("%s = %q, want tools/list", MCPMethodKey, got)
	}
	if got := attrs[MCPProtocolVersionKey].AsString(); got != "2025-11-25" {
		t.Fatalf("%s = %q, want 2025-11-25", MCPProtocolVersionKey, got)
	}
	if got := attrs[attribute.Key("custom")].AsString(); got != "value" {
		t.Fatalf("custom = %q, want value", got)
	}
	if _, ok := attrs[attribute.Key(mcpSDKSessionIDAttr)]; ok {
		t.Fatal("MCP session ID must not be emitted")
	}
}

func TestMCPTracerSuppressesDuplicateInternalToolSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	tracer := NewMCPTracer(provider.Tracer("test"))
	inputCtx := context.Background()
	ctx, span := tracer.Start(inputCtx, "tool.signoz_query", mcptracing.SpanKindInternal,
		mcptracing.String(mcpSDKToolNameAttr, "signoz_query"),
	)
	span.End()

	if got := len(exporter.GetSpans()); got != 0 {
		t.Fatalf("span count = %d, want 0", got)
	}
	if ctx != inputCtx {
		t.Fatal("suppressed tool span changed the context")
	}
}

func TestMCPTracerBoundsUnknownMethodTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	tracer := NewMCPTracer(provider.Tracer("test"))
	_, span := tracer.Start(context.Background(), "mcp.attacker/generated-method", mcptracing.SpanKindServer,
		mcptracing.String(mcpSDKMethodAttr, "attacker/generated-method"),
	)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != UnknownMCPMethod {
		t.Fatalf("span name = %q, want %q", spans[0].Name, UnknownMCPMethod)
	}
	attrs := make(map[attribute.Key]attribute.Value)
	for _, attr := range spans[0].Attributes {
		attrs[attr.Key] = attr.Value
	}
	if got := attrs[MCPMethodKey].AsString(); got != UnknownMCPMethod {
		t.Fatalf("%s = %q, want %q", MCPMethodKey, got, UnknownMCPMethod)
	}
}

func TestMCPTracerTracksWhetherToolMiddlewareRan(t *testing.T) {
	tracer := NewMCPTracer(nil)
	ctx, span := tracer.Start(context.Background(), "mcp.tools/call", mcptracing.SpanKindServer,
		mcptracing.String(mcpSDKMethodAttr, "tools/call"),
	)
	defer span.End()

	started, observed, ok := MCPToolRequestObservation(ctx)
	if !ok || started.IsZero() || observed {
		t.Fatalf("initial observation = (%v, %t, %t), want started/unobserved/present", started, observed, ok)
	}
	MarkMCPToolHandlerObserved(ctx)
	_, observed, ok = MCPToolRequestObservation(ctx)
	if !ok || !observed {
		t.Fatalf("marked observation = (%t, %t), want observed/present", observed, ok)
	}
}

func TestNormalizeMCPMethodPreservesKnownNotifications(t *testing.T) {
	if got := NormalizeMCPMethod(mcp.MethodNotificationToolsListChanged); got != mcp.MethodNotificationToolsListChanged {
		t.Fatalf("NormalizeMCPMethod() = %q, want %q", got, mcp.MethodNotificationToolsListChanged)
	}
}
