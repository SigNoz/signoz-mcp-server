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
