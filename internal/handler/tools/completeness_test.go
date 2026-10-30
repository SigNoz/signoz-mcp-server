package tools

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestWarnRowCountUnknown pins that an uncountable rows array on a
// non-trivial body emits a WARN with the distinctive marker, while a countable
// body or a trivially-empty body stays silent.
func TestWarnRowCountUnknown(t *testing.T) {
	t.Run("uncountable non-empty body emits WARN", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		warnRowCountUnknown(context.Background(), logger, "signoz_search_traces", []byte(`{"data":{"data":{"renamed":[]}}}`), false)
		got := logs.String()
		if !strings.Contains(got, "level=WARN") || !strings.Contains(got, "row count unknown on non-empty body") || !strings.Contains(got, "signoz_search_traces") {
			t.Fatalf("expected row-count WARN with marker + tool, got %q", got)
		}
	})
	t.Run("rowsKnown stays silent", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		warnRowCountUnknown(context.Background(), logger, "signoz_search_traces", []byte(`{"data":{"data":{"results":[]}}}`), true)
		if got := logs.String(); got != "" {
			t.Fatalf("expected no WARN when rowsKnown, got %q", got)
		}
	})
	t.Run("trivial body stays silent", func(t *testing.T) {
		for _, body := range []string{"", "{}", "[]", "null", "  {}  "} {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			warnRowCountUnknown(context.Background(), logger, "signoz_search_traces", []byte(body), false)
			if got := logs.String(); got != "" {
				t.Fatalf("expected no WARN for trivial body %q, got %q", body, got)
			}
		}
	})
}
