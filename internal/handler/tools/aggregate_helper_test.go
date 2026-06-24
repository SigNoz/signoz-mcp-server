package tools

import (
	"testing"
	"time"
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

// TestResolveTimestampsMalformedStartErrorsLoudly pins that a present-but-malformed
// start/end propagates an error out of resolveTimestamps instead of silently
// falling back to the default window (the silent-failure anti-pattern).
func TestResolveTimestampsMalformedStartErrorsLoudly(t *testing.T) {
	if _, _, err := resolveTimestamps(map[string]any{"start": "yesterday"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed start = nil error, want loud validation error")
	}
	if _, _, err := resolveTimestamps(map[string]any{"end": "soon"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed end = nil error, want loud validation error")
	}
	// Sanity: a valid explicit window still succeeds.
	if _, _, err := resolveTimestamps(map[string]any{"start": "1711123200000", "end": "1711130400000"}, "1h"); err != nil {
		t.Fatalf("resolveTimestamps with valid window: unexpected error: %v", err)
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
