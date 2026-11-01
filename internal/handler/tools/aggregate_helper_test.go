package tools

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

func TestResolveTimestampsEndOnlyUsesDefaultRange(t *testing.T) {
	start, end, err := resolveTimestamps(map[string]any{
		"end": "1711130400000",
	}, "1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if start >= end {
		t.Fatalf("start = %d, end = %d, want non-inverted default window", start, end)
	}
	if delta := end - start; delta < int64(59*time.Minute/time.Millisecond) || delta > int64(61*time.Minute/time.Millisecond) {
		t.Fatalf("delta = %dms, want about 1h", delta)
	}
}

// TestResolveTimestampsMalformedStartErrorsLoudly pins that present-but-malformed
// timestamp inputs propagate an error out of resolveTimestamps instead of
// silently falling back to the default window (the silent-failure anti-pattern).
func TestResolveTimestampsMalformedStartErrorsLoudly(t *testing.T) {
	if _, _, err := resolveTimestamps(map[string]any{"start": "yesterday"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed start = nil error, want loud validation error")
	}
}

// TestResolveTimestampsEmptyOrAbsentUsesDefaultRange pins that empty-string / absent
// time input — including a present-but-empty start, the case GetTimestampsWithDefaults
// treats as absent — injects the tool's advertised default window (1h) rather than
// silently falling through to the generic 6h fallback. The present-but-empty start
// case is the one that regressed when the guard checked raw key presence.
func TestResolveTimestampsEmptyOrAbsentUsesDefaultRange(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty-string timeRange", map[string]any{"timeRange": ""}},
		{"empty timeRange and empty start", map[string]any{"timeRange": "", "start": ""}},
		{"empty start only", map[string]any{"start": ""}},
		{"absent", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := resolveTimestamps(tc.args, "1h")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			delta := end - start
			if delta < int64(59*time.Minute/time.Millisecond) || delta > int64(61*time.Minute/time.Millisecond) {
				t.Fatalf("delta = %dms, want about 1h (the tool default), not the generic 6h fallback", delta)
			}
		})
	}
}

func TestParseAggregateArgs_LimitClamped(t *testing.T) {
	over, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"limit":        "50000",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap aggregate: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"limit":        "25",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if under.Limit != 25 || under.LimitClamped {
		t.Fatalf("under-cap aggregate: Limit=%d Clamped=%v, want 25 false", under.Limit, under.LimitClamped)
	}
}

func TestParseAggregateArgs_DefaultLimit(t *testing.T) {
	req, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Limit != querybuilder.DefaultAggregateQueryLimit {
		t.Fatalf("default limit = %d, want %d", req.Limit, querybuilder.DefaultAggregateQueryLimit)
	}
	if req.OrderExpr != "count()" || req.OrderDir != "desc" {
		t.Fatalf("default order = %q %q, want count() desc", req.OrderExpr, req.OrderDir)
	}
}

// TestParseAggregateArgs_NonStringRequestTypeErrors pins that a
// present-but-non-string requestType is rejected loudly at the parse layer
// instead of the failed type assertion coercing it to "" and silently
// defaulting to "scalar".
func TestParseAggregateArgs_NonStringRequestTypeErrors(t *testing.T) {
	for _, bad := range []any{true, 123, float64(1)} {
		if _, err := parseAggregateArgs(map[string]any{
			"aggregations": "count()",
			"timeRange":    "1h",
			"requestType":  bad,
		}, "logs", ""); err == nil {
			t.Fatalf("parseAggregateArgs with requestType=%v (%T) = nil error, want validation error", bad, bad)
		}
	}
	// A valid string requestType still parses.
	if _, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"timeRange":    "1h",
		"requestType":  "time_series",
	}, "logs", ""); err != nil {
		t.Fatalf("parseAggregateArgs with valid requestType: unexpected error: %v", err)
	}
}

// TestParseMetricsQueryArgs_NonStringRequestTypeErrors pins the same
// loud-rejection contract for the metrics arg parser (stringArg previously
// dropped a non-string value to "").
func TestParseMetricsQueryArgs_NonStringRequestTypeErrors(t *testing.T) {
	if _, err := parseMetricsQueryArgs(map[string]any{
		"metricName":  "system.cpu.time",
		"requestType": 123,
	}); err == nil {
		t.Fatal("parseMetricsQueryArgs with requestType=123 = nil error, want validation error")
	}
}

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
