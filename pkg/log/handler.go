package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const stacktraceDepth = 64

type ContextHandler struct {
	handler           slog.Handler
	stacktraceEnabled bool
}

func NewContextHandler(base slog.Handler) slog.Handler {
	return &ContextHandler{
		handler:           base,
		stacktraceEnabled: !strings.EqualFold(os.Getenv("LOG_STACKTRACE"), "false"),
	}
}

func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
		)
	}

	if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
		r.AddAttrs(slog.String("mcp.tenant_url", signozURL))
	}
	if searchContext, ok := util.GetSearchContext(ctx); ok && searchContext != "" {
		r.AddAttrs(slog.String("mcp.search_context", searchContext))
	}
	if toolName, ok := util.GetToolName(ctx); ok && toolName != "" {
		r.AddAttrs(
			slog.String("gen_ai.tool.name", toolName),
			slog.String("gen_ai.operation.name", "execute_tool"),
		)
	}
	if clientSource, ok := util.GetClientSource(ctx); ok && clientSource != "" {
		r.AddAttrs(slog.String("mcp.client_source", clientSource))
	}
	if threadID, ok := util.GetAssistantThreadID(ctx); ok && threadID != "" {
		r.AddAttrs(slog.String("mcp.assistant.thread_id", threadID))
	}
	if executionID, ok := util.GetAssistantExecutionID(ctx); ok && executionID != "" {
		r.AddAttrs(slog.String("mcp.assistant.execution_id", executionID))
	}

	if r.Level >= slog.LevelError {
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			// Prefer an attached error's text so the span status carries
			// the specific failure reason (e.g. "dial tcp: connection
			// refused") rather than the generic log prefix ("mcp error").
			// Callers attach errors via pkg/log.ErrAttr(err) which keys
			// on "error" and stores the error as slog.Any.
			msg := r.Message
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "error" {
					if err, ok := a.Value.Any().(error); ok && err != nil {
						msg = err.Error()
					}
					return false
				}
				return true
			})
			span.SetStatus(codes.Error, msg)
		}
		if h.stacktraceEnabled {
			if stacktrace := captureStacktrace(4); stacktrace != "" {
				r.AddAttrs(slog.Group("exception", slog.String("stacktrace", stacktrace)))
			}
		}
	}

	return h.handler.Handle(ctx, r)
}

func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{
		handler:           h.handler.WithAttrs(attrs),
		stacktraceEnabled: h.stacktraceEnabled,
	}
}

func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{
		handler:           h.handler.WithGroup(name),
		stacktraceEnabled: h.stacktraceEnabled,
	}
}

func captureStacktrace(skip int) string {
	var pcs [stacktraceDepth]uintptr
	n := runtime.Callers(skip+1, pcs[:])
	if n == 0 {
		return ""
	}

	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		frame, more := frames.Next()
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(frame.Function)
		b.WriteString("\n\t")
		b.WriteString(frame.File)
		b.WriteString(":")
		b.WriteString(fmt.Sprintf("%d", frame.Line))
		if !more {
			break
		}
	}

	return b.String()
}
