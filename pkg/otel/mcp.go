package otel

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcptracing "github.com/mark3labs/mcp-go/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	mcpSDKMethodAttr          = "mcp.method"
	mcpSDKToolNameAttr        = "mcp.tool.name"
	mcpSDKSessionIDAttr       = "mcp.session.id"
	mcpSDKProtocolVersionAttr = "mcp.protocol.version"
	UnknownMCPMethod          = "unknown"
)

type mcpToolRequestStateKey struct{}

type mcpToolRequestState struct {
	started         time.Time
	handlerObserved atomic.Bool
}

// NewMCPTracer adapts an OpenTelemetry tracer to mcp-go while normalizing the
// SDK's generic tracing names and attributes to the MCP semantic conventions.
func NewMCPTracer(tracer trace.Tracer) mcptracing.Tracer {
	return &mcpTracer{tracer: tracer}
}

type mcpTracer struct {
	tracer trace.Tracer
}

func (t *mcpTracer) Start(ctx context.Context, name string, kind mcptracing.SpanKind, attrs ...mcptracing.Attribute) (context.Context, mcptracing.Span) {
	// mcp-go also starts an internal tool.<name> span. The enclosing MCP
	// tools/call server span already covers execution, so suppress the duplicate.
	if kind == mcptracing.SpanKindInternal && strings.HasPrefix(name, "tool.") {
		return ctx, noopMCPSpan{}
	}
	if mcpMethodAttribute(attrs) == string(mcp.MethodToolsCall) {
		ctx = context.WithValue(ctx, mcpToolRequestStateKey{}, &mcpToolRequestState{started: time.Now()})
	}
	if t == nil || t.tracer == nil {
		return ctx, noopMCPSpan{}
	}

	ctx, span := t.tracer.Start(ctx, mcpSpanName(name),
		trace.WithSpanKind(mcpSpanKind(kind)),
		trace.WithAttributes(mcpAttributes(attrs...)...),
	)
	wrapped := &mcpSpan{span: span}
	ctx = mcptracing.ContextWithSpan(ctx, wrapped)
	return ctx, wrapped
}

func mcpSpanName(name string) string {
	if method, ok := strings.CutPrefix(name, "mcp."); ok {
		return NormalizeMCPMethod(method)
	}
	return name
}

// NormalizeMCPMethod bounds client-controlled JSON-RPC method values to the
// methods supported by this MCP SDK version. Unknown values share one bucket.
func NormalizeMCPMethod(method string) string {
	switch mcp.MCPMethod(method) {
	case mcp.MethodInitialize,
		mcp.MethodPing,
		mcp.MethodResourcesList,
		mcp.MethodResourcesTemplatesList,
		mcp.MethodResourcesRead,
		mcp.MethodResourcesSubscribe,
		mcp.MethodResourcesUnsubscribe,
		mcp.MethodPromptsList,
		mcp.MethodPromptsGet,
		mcp.MethodToolsList,
		mcp.MethodToolsCall,
		mcp.MethodSetLogLevel,
		mcp.MethodElicitationCreate,
		mcp.MethodNotificationElicitationComplete,
		mcp.MethodListRoots,
		mcp.MethodTasksGet,
		mcp.MethodTasksList,
		mcp.MethodTasksResult,
		mcp.MethodTasksCancel,
		mcp.MethodNotificationInitialized,
		mcp.MethodNotificationCancelled,
		mcp.MethodNotificationProgress,
		mcp.MethodNotificationMessage,
		mcp.MethodNotificationResourcesListChanged,
		mcp.MethodNotificationResourceUpdated,
		mcp.MethodNotificationPromptsListChanged,
		mcp.MethodNotificationToolsListChanged,
		mcp.MethodNotificationRootsListChanged,
		mcp.MethodNotificationTasksStatus,
		mcp.MethodCompletionComplete,
		mcp.MethodSamplingCreateMessage:
		return method
	default:
		return UnknownMCPMethod
	}
}

func mcpMethodAttribute(attrs []mcptracing.Attribute) string {
	for _, attr := range attrs {
		if attr.Key == mcpSDKMethodAttr {
			return attr.Value
		}
	}
	return ""
}

// MarkMCPToolHandlerObserved records that registered tool middleware handled
// the request, so the lifecycle hook does not emit fallback telemetry too.
func MarkMCPToolHandlerObserved(ctx context.Context) {
	if state, ok := ctx.Value(mcpToolRequestStateKey{}).(*mcpToolRequestState); ok {
		state.handlerObserved.Store(true)
	}
}

// MCPToolRequestObservation returns request-span state used to observe
// tools/call failures that occur before registered tool middleware runs.
func MCPToolRequestObservation(ctx context.Context) (started time.Time, handlerObserved bool, ok bool) {
	state, ok := ctx.Value(mcpToolRequestStateKey{}).(*mcpToolRequestState)
	if !ok || state == nil {
		return time.Time{}, false, false
	}
	return state.started, state.handlerObserved.Load(), true
}

func mcpSpanKind(kind mcptracing.SpanKind) trace.SpanKind {
	switch kind {
	case mcptracing.SpanKindServer:
		return trace.SpanKindServer
	case mcptracing.SpanKindClient:
		return trace.SpanKindClient
	case mcptracing.SpanKindInternal:
		return trace.SpanKindInternal
	default:
		return trace.SpanKindUnspecified
	}
}

func mcpAttributes(attrs ...mcptracing.Attribute) []attribute.KeyValue {
	converted := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		switch attr.Key {
		case mcpSDKMethodAttr:
			converted = append(converted, MCPMethodKey.String(NormalizeMCPMethod(attr.Value)))
		case mcpSDKToolNameAttr:
			converted = append(converted, GenAIToolNameKey.String(attr.Value))
		case mcpSDKProtocolVersionAttr:
			converted = append(converted, MCPProtocolVersionKey.String(attr.Value))
		case mcpSDKSessionIDAttr:
			// Session telemetry is deliberately omitted by this stateless server.
		default:
			converted = append(converted, attribute.String(attr.Key, attr.Value))
		}
	}
	return converted
}

type mcpSpan struct {
	span trace.Span
}

func (s *mcpSpan) SetAttributes(attrs ...mcptracing.Attribute) {
	if s != nil && s.span != nil {
		s.span.SetAttributes(mcpAttributes(attrs...)...)
	}
}

func (s *mcpSpan) RecordError(err error) {
	if s != nil && s.span != nil && err != nil {
		s.span.RecordError(err)
	}
}

func (s *mcpSpan) SetStatus(code mcptracing.StatusCode, description string) {
	if s == nil || s.span == nil {
		return
	}
	switch code {
	case mcptracing.StatusOK:
		s.span.SetStatus(codes.Ok, description)
	case mcptracing.StatusError:
		s.span.SetStatus(codes.Error, description)
	default:
		s.span.SetStatus(codes.Unset, description)
	}
}

func (s *mcpSpan) End() {
	if s != nil && s.span != nil {
		s.span.End()
	}
}

type noopMCPSpan struct{}

func (noopMCPSpan) SetAttributes(...mcptracing.Attribute)   {}
func (noopMCPSpan) RecordError(error)                       {}
func (noopMCPSpan) SetStatus(mcptracing.StatusCode, string) {}
func (noopMCPSpan) End()                                    {}
