package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
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
	if _, _, err := resolveTimestamps(map[string]any{"end": "soon"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed end = nil error, want loud validation error")
	}
	if _, _, err := resolveTimestamps(map[string]any{"timeRange": "24hours"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed timeRange = nil error, want loud validation error")
	}
	// Sanity: a valid explicit window still succeeds.
	if _, _, err := resolveTimestamps(map[string]any{"start": "1711123200000", "end": "1711130400000"}, "1h"); err != nil {
		t.Fatalf("resolveTimestamps with valid window: unexpected error: %v", err)
	}
	// Explicit start/end override timeRange, matching GetTimestampsWithDefaults.
	if _, _, err := resolveTimestamps(map[string]any{"timeRange": "24hours", "start": "1711123200000", "end": "1711130400000"}, "1h"); err != nil {
		t.Fatalf("resolveTimestamps with valid window and malformed timeRange: unexpected error: %v", err)
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

// TestResolveTimestampsNonStringTimeRangeErrorsLoudly pins that a present-but-
// non-string timeRange is rejected loudly (not silently defaulted) unless a
// complete valid start/end pair overrides it.
func TestResolveTimestampsNonStringTimeRangeErrorsLoudly(t *testing.T) {
	if _, _, err := resolveTimestamps(map[string]any{"timeRange": true}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with bool timeRange = nil error, want loud validation error")
	}
	if _, _, err := resolveTimestamps(map[string]any{"timeRange": 24}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with numeric timeRange = nil error, want loud validation error")
	}
	// A complete explicit window overrides the bad timeRange and still succeeds.
	if _, _, err := resolveTimestamps(map[string]any{"timeRange": true, "start": "1711123200000", "end": "1711130400000"}, "1h"); err != nil {
		t.Fatalf("resolveTimestamps with valid window and non-string timeRange: unexpected error: %v", err)
	}
}

func TestParseAggregateArgs_LimitClamped(t *testing.T) {
	over, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"limit":       "50000",
		"timeRange":   "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap aggregate: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"limit":       "25",
		"timeRange":   "1h",
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
		"aggregation": "count",
		"timeRange":   "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Limit != types.DefaultAggregateQueryLimit {
		t.Fatalf("default limit = %d, want %d", req.Limit, types.DefaultAggregateQueryLimit)
	}
	if req.OrderExpr != "count()" || req.OrderDir != "desc" {
		t.Fatalf("default order = %q %q, want count() desc", req.OrderExpr, req.OrderDir)
	}
}

// TestParseAggregateArgs_NonStringRequestTypeErrors (FIX A3) pins that a
// present-but-non-string requestType is rejected loudly at the parse layer
// instead of the failed type assertion coercing it to "" and silently
// defaulting to "scalar".
func TestParseAggregateArgs_NonStringRequestTypeErrors(t *testing.T) {
	for _, bad := range []any{true, 123, float64(1)} {
		if _, err := parseAggregateArgs(map[string]any{
			"aggregation": "count",
			"timeRange":   "1h",
			"requestType": bad,
		}, "logs", ""); err == nil {
			t.Fatalf("parseAggregateArgs with requestType=%v (%T) = nil error, want validation error", bad, bad)
		}
	}
	// A valid string requestType still parses.
	if _, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"timeRange":   "1h",
		"requestType": "time_series",
	}, "logs", ""); err != nil {
		t.Fatalf("parseAggregateArgs with valid requestType: unexpected error: %v", err)
	}
}

// TestHandleAggregateLogs_NonStringRequestTypeCoded (FIX A3) pins the end-to-end
// contract: a bool requestType yields a CodeValidationFailed result and never
// reaches the backend.
func TestHandleAggregateLogs_NonStringRequestTypeCoded(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_aggregate_logs", map[string]any{
		"aggregation": "count",
		"timeRange":   "1h",
		"requestType": true,
	})

	result, err := h.handleAggregateLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("requestType=true: code = %q, want %q", code, CodeValidationFailed)
	}
	if called {
		t.Fatal("backend QueryBuilderV5 was called despite invalid requestType")
	}
}

// TestParseMetricsQueryArgs_NonStringRequestTypeErrors (FIX A3) pins the same
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

// TestHandleQueryMetrics_NonStringRequestTypeCoded (FIX A3) pins the end-to-end
// contract for query_metrics: a numeric requestType yields a
// CodeValidationFailed result and never reaches the backend.
func TestHandleQueryMetrics_NonStringRequestTypeCoded(t *testing.T) {
	called := false
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_query_metrics", map[string]any{
		"metricName":  "system.cpu.time",
		"metricType":  "gauge",
		"timeRange":   "1h",
		"requestType": 123,
	})

	result, err := h.handleQueryMetrics(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("requestType=123: code = %q, want %q", code, CodeValidationFailed)
	}
	if called {
		t.Fatal("backend QueryBuilderV5 was called despite invalid requestType")
	}
}

// TestWarnUnparsedWarningEnvelope (FIX D1) pins that QB warning-envelope drift
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

// TestWarnRowCountUnknown (FIX D2) pins that an uncountable rows array on a
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
