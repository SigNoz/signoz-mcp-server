package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewContextHandler(base))
}

func TestContextHandler_InjectsTenantSessionSearchContext(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	ctx := context.Background()
	ctx = util.SetSigNozURL(ctx, "https://tenant.example.com")
	ctx = util.SetSessionID(ctx, "sess-42")
	ctx = util.SetSearchContext(ctx, "root-cause")

	logger.InfoContext(ctx, "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	if got := rec["mcp.tenant_url"]; got != "https://tenant.example.com" {
		t.Fatalf("mcp.tenant_url = %v, want tenant url", got)
	}
	if got := rec["mcp.session.id"]; got != "sess-42" {
		t.Fatalf("mcp.session.id = %v, want sess-42", got)
	}
	if got := rec["mcp.search_context"]; got != "root-cause" {
		t.Fatalf("mcp.search_context = %v, want root-cause", got)
	}
}

func TestContextHandler_InjectsTraceAndSpanIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()

	logger.InfoContext(ctx, "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	if got := rec["trace_id"]; got != span.SpanContext().TraceID().String() {
		t.Fatalf("trace_id = %v, want %s", got, span.SpanContext().TraceID())
	}
	if got := rec["span_id"]; got != span.SpanContext().SpanID().String() {
		t.Fatalf("span_id = %v, want %s", got, span.SpanContext().SpanID())
	}
}

func TestContextHandler_ErrorLevelAttachesStacktraceByDefault(t *testing.T) {
	t.Setenv("LOG_STACKTRACE", "")
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	logger.ErrorContext(context.Background(), "boom")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	exception, ok := rec["exception"].(map[string]any)
	if !ok {
		t.Fatalf("exception group missing in %v", rec)
	}
	if trace, ok := exception["stacktrace"].(string); !ok || !strings.Contains(trace, "handler_test.go") {
		t.Fatalf("stacktrace missing or unexpected: %v", exception["stacktrace"])
	}
}

func TestContextHandler_StacktraceDisabledViaEnv(t *testing.T) {
	t.Setenv("LOG_STACKTRACE", "false")
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	logger.ErrorContext(context.Background(), "boom")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	if _, present := rec["exception"]; present {
		t.Fatalf("expected no exception group when LOG_STACKTRACE=false, got %v", rec["exception"])
	}
}

func TestTruncBody(t *testing.T) {
	short := []byte("hello")
	if got := TruncBody(short); got != "hello" {
		t.Fatalf("TruncBody(short) = %q, want hello", got)
	}

	big := bytes.Repeat([]byte("a"), 5000)
	got := TruncBody(big)
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("TruncBody(big) does not end in truncation suffix: %q", got[len(got)-20:])
	}
	if len(got) > 4*1024 {
		t.Fatalf("TruncBody(big) len = %d, want <= 4096", len(got))
	}
}
