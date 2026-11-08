package tools

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestWarnUnparsedWarningEnvelope pins that QB warning-envelope drift
// is detectable: a body that mentions "warning" but from which we extract zero
// structured messages emits a WARN with the distinctive marker. A body with no
// "warning" substring, or one we parsed successfully, stays silent.
func TestWarnUnparsedWarningEnvelope(t *testing.T) {
	t.Run("entry present but message field drifted emits WARN", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		// The warnings array carries an entry, but its message field was renamed
		// (message -> msg), so extraction returns nothing — genuine drift.
		drifted := []byte(`{"data":{"warning":{"warnings":[{"msg":"deprecated key"}]}}}`)
		warnUnparsedWarningEnvelope(context.Background(), logger, "signoz_search_logs", drifted, 0)
		got := logs.String()
		if !strings.Contains(got, "level=WARN") || !strings.Contains(got, "qb warning envelope unparsed") || !strings.Contains(got, "signoz_search_logs") {
			t.Fatalf("expected drift WARN with marker + tool, got %q", got)
		}
	})
	t.Run("no warning envelope stays silent", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		warnUnparsedWarningEnvelope(context.Background(), logger, "signoz_search_logs", []byte(`{"data":{"results":[]}}`), 0)
		if got := logs.String(); got != "" {
			t.Fatalf("expected no WARN for warning-free body, got %q", got)
		}
	})
	t.Run("empty or degenerate envelope stays silent", func(t *testing.T) {
		// A normal no-warnings response (empty array), an empty warning object, or
		// degenerate empty entries must NOT be mistaken for drift.
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		for _, body := range []string{
			`{"data":{"warning":{"warnings":[]}}}`,
			`{"data":{"warning":{}}}`,
			`{"data":{"warning":{"warnings":[{}]}}}`,
		} {
			logs.Reset()
			warnUnparsedWarningEnvelope(context.Background(), logger, "signoz_search_logs", []byte(body), 0)
			if got := logs.String(); got != "" {
				t.Fatalf("expected no WARN for empty envelope %q, got %q", body, got)
			}
		}
	})
	t.Run("extracted messages stay silent", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		warnUnparsedWarningEnvelope(context.Background(), logger, "signoz_search_logs", []byte(`{"data":{"warning":{"warnings":[{"message":"x"}]}}}`), 1)
		if got := logs.String(); got != "" {
			t.Fatalf("expected no WARN when messages were extracted, got %q", got)
		}
	})
}
