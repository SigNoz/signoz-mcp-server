package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewContextHandler(base))
}

func TestContextHandler_InjectsTenantAndSearchContext(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	ctx := context.Background()
	ctx = util.SetSigNozURL(ctx, "https://tenant.example.com")
	ctx = util.SetSearchContext(ctx, "root-cause")

	logger.InfoContext(ctx, "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	if got := rec["mcp.tenant_url"]; got != "https://tenant.example.com" {
		t.Fatalf("mcp.tenant_url = %v, want tenant url", got)
	}
	if got := rec["mcp.search_context"]; got != "root-cause" {
		t.Fatalf("mcp.search_context = %v, want root-cause", got)
	}
}

func TestContextHandler_InjectsAssistantCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	ctx := context.Background()
	ctx = util.SetClientSource(ctx, "ai-assistant")
	ctx = util.SetAssistantThreadID(ctx, "thread-abc")
	ctx = util.SetAssistantExecutionID(ctx, "exec-xyz")

	logger.InfoContext(ctx, "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	if got := rec["mcp.client_source"]; got != "ai-assistant" {
		t.Fatalf("mcp.client_source = %v, want ai-assistant", got)
	}
	if got := rec["mcp.assistant.thread_id"]; got != "thread-abc" {
		t.Fatalf("mcp.assistant.thread_id = %v, want thread-abc", got)
	}
	if got := rec["mcp.assistant.execution_id"]; got != "exec-xyz" {
		t.Fatalf("mcp.assistant.execution_id = %v, want exec-xyz", got)
	}
}

func TestContextHandler_OmitsAssistantCorrelationWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	logger.InfoContext(context.Background(), "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	for _, key := range []string{"mcp.client_source", "mcp.assistant.thread_id", "mcp.assistant.execution_id"} {
		if _, ok := rec[key]; ok {
			t.Fatalf("%s should not be present when ctx carries no value, got %v", key, rec[key])
		}
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

func TestContextHandler_SpanStatusPrefersErrorAttrText(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")

	upstream := errors.New("dial tcp 10.0.0.1:443: connection refused")
	logger.ErrorContext(ctx, "mcp error", ErrAttr(upstream))
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("status code = %v, want Error", spans[0].Status.Code)
	}
	if spans[0].Status.Description != upstream.Error() {
		t.Fatalf("status description = %q, want %q (error attr text, not log msg)", spans[0].Status.Description, upstream.Error())
	}
}

func TestContextHandler_SpanStatusFallsBackToMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")

	logger.ErrorContext(ctx, "something broke")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Description != "something broke" {
		t.Fatalf("status description = %q, want log message fallback", spans[0].Status.Description)
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

func TestRedactedTruncAny(t *testing.T) {
	slackURL := "https://hooks.slack.com/services/T1/B2/secret-slack"
	webhookURL := "https://alerts.example.com/hook/secret-webhook"
	teamsURL := "https://teams.example.com/webhook/secret-teams"
	routingKey := "pagerduty-routing-secret"
	payload := map[string]any{
		"name": "signoz_create_notification_channel",
		"arguments": map[string]any{
			"webhook_password":      "secret-canary",
			"slack_api_url":         slackURL,
			"webhook_url":           webhookURL,
			"msteams_webhook_url":   teamsURL,
			"pagerduty_routing_key": routingKey,
			"searchContext":         "configure notification channels",
			"nested": []any{
				map[string]any{"clientSecret": "nested-canary", "filter": "service.name = 'api'"},
			},
		},
	}

	got := RedactedTruncAny(payload)
	for _, secret := range []string{"secret-canary", "nested-canary", slackURL, webhookURL, teamsURL, routingKey} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactedTruncAny leaked credential %q: %s", secret, got)
		}
	}
	for _, want := range []string{
		`"name":"signoz_create_notification_channel"`,
		`"webhook_password":"[REDACTED]"`,
		`"slack_api_url":"[REDACTED]"`,
		`"webhook_url":"[REDACTED]"`,
		`"msteams_webhook_url":"[REDACTED]"`,
		`"pagerduty_routing_key":"[REDACTED]"`,
		`"searchContext":"configure notification channels"`,
		`"clientSecret":"[REDACTED]"`,
		`"filter":"service.name = 'api'"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RedactedTruncAny = %s, want %s", got, want)
		}
	}

	big := RedactedTruncAny(map[string]any{"query": strings.Repeat("x", truncBodyLimit*2)})
	if len(big) > truncBodyLimit || !strings.HasSuffix(big, truncBodySuffix) {
		t.Fatalf("RedactedTruncAny oversized payload len/suffix = %d/%q", len(big), big[len(big)-len(truncBodySuffix):])
	}
}

func TestBoundedErrAttr(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	logger.ErrorContext(ctx, "failed", BoundedErrAttr(errors.New(strings.Repeat("x", truncBodyLimit*2))))
	span.End()

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	got, ok := record["error"].(string)
	if !ok || len(got) > truncBodyLimit || !strings.HasSuffix(got, truncBodySuffix) {
		t.Fatalf("bounded error = %T len=%d suffix=%t", record["error"], len(got), strings.HasSuffix(got, truncBodySuffix))
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error || spans[0].Status.Description != got {
		t.Fatalf("bounded error span status = %#v, want Error with bounded description", spans)
	}
}
