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

const stacktraceDepth = 32

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
	if sessionID, ok := util.GetSessionID(ctx); ok && sessionID != "" {
		r.AddAttrs(slog.String("mcp.session.id", sessionID))
	}
	if searchContext, ok := util.GetSearchContext(ctx); ok && searchContext != "" {
		r.AddAttrs(slog.String("mcp.search_context", searchContext))
	}

	if r.Level >= slog.LevelError {
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetStatus(codes.Error, r.Message)
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
