package timeutil

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestGetTimestampsWithDefaultsExplicitStartEndWinsOverTimeRange(t *testing.T) {
	args := map[string]any{
		"timeRange": "1h",
		"start":     "1711123200000",
		"end":       "1711130400000",
	}

	start, end := GetTimestampsWithDefaults(args, "ms")

	if start != "1711123200000" {
		t.Fatalf("start = %q, want explicit start", start)
	}
	if end != "1711130400000" {
		t.Fatalf("end = %q, want explicit end", end)
	}
}

func TestGetTimestampsWithDefaultsNumericExplicitStartEndWinsOverTimeRange(t *testing.T) {
	args := map[string]any{
		"timeRange": "1h",
		"start":     json.Number("1711123200000"),
		"end":       float64(1711130400000),
	}

	start, end := GetTimestampsWithDefaults(args, "ms")

	if start != "1711123200000" {
		t.Fatalf("start = %q, want explicit numeric start", start)
	}
	if end != "1711130400000" {
		t.Fatalf("end = %q, want explicit numeric end", end)
	}
}

func TestGetTimestampsWithDefaultsUnsafeFloatTimestampDoesNotOverrideTimeRange(t *testing.T) {
	args := map[string]any{
		"timeRange": "1h",
		"start":     float64(1711123200000000000),
		"end":       float64(1711130400000000000),
	}

	start, end := GetTimestampsWithDefaults(args, "ns")

	if start == "1711123200000000000" || end == "1711130400000000000" {
		t.Fatalf("unsafe float timestamps should not be treated as exact explicit ns values: start=%s end=%s", start, end)
	}
	startInt, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		t.Fatalf("start should be numeric: %v", err)
	}
	endInt, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		t.Fatalf("end should be numeric: %v", err)
	}
	if delta := endInt - startInt; delta < int64(59*time.Minute) || delta > int64(61*time.Minute) {
		t.Fatalf("delta = %dns, want about 1h", delta)
	}
}

func TestGetTimestampsWithDefaultsTimeRangeUsedForIncompleteExplicitWindow(t *testing.T) {
	args := map[string]any{
		"timeRange": "1h",
		"start":     "1711123200000",
	}

	start, end := GetTimestampsWithDefaults(args, "ms")

	if start == "1711123200000" {
		t.Fatalf("start = %q, want timeRange-derived start for incomplete explicit window", start)
	}
	startInt, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		t.Fatalf("start should be numeric: %v", err)
	}
	endInt, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		t.Fatalf("end should be numeric: %v", err)
	}
	if delta := endInt - startInt; delta < 59*60*1000 || delta > 61*60*1000 {
		t.Fatalf("delta = %dms, want about 1h", delta)
	}
}

// TestNormalizeEpochToUnit pins the magnitude auto-detect bands directly. A
// fixed instant (2024-03-22T16:00:00Z) is expressed at every magnitude and must
// normalize back to the same canonical value.
func TestNormalizeEpochToUnit(t *testing.T) {
	const (
		sec    = int64(1711123200)          // seconds
		millis = int64(1711123200000)       // ms
		micros = int64(1711123200000000)    // µs
		nanos  = int64(1711123200000000000) // ns
	)

	tests := []struct {
		name    string
		raw     int64
		unit    string
		want    int64
		comment string
	}{
		{"seconds->ms", sec, UnitMillis, millis, "10-digit epoch is seconds"},
		{"millis->ms", millis, UnitMillis, millis, "13-digit epoch is already ms"},
		{"micros->ms", micros, UnitMillis, millis, "16-digit epoch is micros"},
		{"nanos->ms", nanos, UnitMillis, millis, "19-digit epoch is nanos"},
		{"seconds->ns", sec, UnitNanos, nanos, "seconds widen to ns"},
		{"millis->ns", millis, UnitNanos, nanos, "ms widen to ns"},
		{"micros->ns", micros, UnitNanos, nanos, "micros widen to ns"},
		{"nanos->ns", nanos, UnitNanos, nanos, "ns unchanged"},
		{"zero", 0, UnitMillis, 0, "non-positive unchanged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeEpochToUnit(tt.raw, tt.unit); got != tt.want {
				t.Fatalf("normalizeEpochToUnit(%d, %q) = %d, want %d (%s)", tt.raw, tt.unit, got, tt.want, tt.comment)
			}
		})
	}
}

// TestNormalizeEpochToUnit_BandCeilingsNoOverflow pins that values near each
// band ceiling (1e11 seconds / 1e14 millis / 1e17 micros) convert without
// int64 overflow, for both ms and ns targets. The seconds-near-ceiling × 1e9
// case is the one that overflowed when conversion went through a nanosecond
// intermediate; it must now produce a correct, positive result (ms) or a
// clamped, still-positive value (ns).
func TestNormalizeEpochToUnit_BandCeilingsNoOverflow(t *testing.T) {
	// Just below each ceiling — these stay in the lower band.
	secNearCeil := secondsUpperBound - 1 // detected as seconds
	msNearCeil := millisUpperBound - 1   // detected as millis
	usNearCeil := microsUpperBound - 1   // detected as micros

	tests := []struct {
		name string
		raw  int64
		unit string
		want int64
	}{
		// seconds → ms: ×1000, no overflow.
		{"sec ceiling -> ms", secNearCeil, UnitMillis, secNearCeil * 1000},
		// millis → ms: identity.
		{"ms ceiling -> ms", msNearCeil, UnitMillis, msNearCeil},
		// micros → ms: /1000.
		{"us ceiling -> ms", usNearCeil, UnitMillis, usNearCeil / 1000},
		// millis → ns: ×1e6, stays in int64 (1e14×1e6 = 1e20 > maxInt64 → clamp).
		{"ms ceiling -> ns clamps", msNearCeil, UnitNanos, maxInt64},
		// micros → ns: ×1e3 (1e17×1e3 = 1e20 > maxInt64 → clamp).
		{"us ceiling -> ns clamps", usNearCeil, UnitNanos, maxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEpochToUnit(tt.raw, tt.unit)
			if got != tt.want {
				t.Fatalf("normalizeEpochToUnit(%d, %q) = %d, want %d", tt.raw, tt.unit, got, tt.want)
			}
			if got < 0 {
				t.Fatalf("normalizeEpochToUnit(%d, %q) = %d is negative (overflow wrap)", tt.raw, tt.unit, got)
			}
		})
	}

	// The specific reviewer case: a seconds value near the 1e11 ceiling, which
	// previously overflowed when scaled to nanos first. On the ms target it must
	// be exact; on the ns target it overflows the int64 range and must clamp
	// (never wrap negative).
	if got := normalizeEpochToUnit(secNearCeil, UnitMillis); got != secNearCeil*1000 || got < 0 {
		t.Fatalf("seconds-near-ceiling -> ms = %d, want %d (positive, exact)", got, secNearCeil*1000)
	}
	if got := normalizeEpochToUnit(secNearCeil, UnitNanos); got < 0 {
		t.Fatalf("seconds-near-ceiling -> ns = %d, must not wrap negative", got)
	}
}

// TestNormalizeEpochToUnit_ExactBoundaries pins that each band ceiling is
// EXCLUSIVE: a value EXACTLY equal to a boundary falls into the NEXT (finer)
// band, because the magnitude switch uses strict `<`.
//
//	raw == secondsUpperBound (1e11) → millis band (NOT seconds)
//	raw == millisUpperBound  (1e14) → micros band (NOT millis)
//	raw == microsUpperBound  (1e17) → nanos  band (NOT micros)
func TestNormalizeEpochToUnit_ExactBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  int64
		unit string
		want int64
	}{
		// 1e11 is treated as MILLIS → ms target is identity (NOT ×1000).
		{"1e11 is millis -> ms identity", secondsUpperBound, UnitMillis, secondsUpperBound},
		// If 1e11 were (wrongly) seconds, ms would be 1e14; assert it is NOT.
		// 1e14 is treated as MICROS → ms target divides by 1000.
		{"1e14 is micros -> ms /1000", millisUpperBound, UnitMillis, millisUpperBound / 1000},
		// 1e17 is treated as NANOS → ms target divides by 1e6, ns is identity.
		{"1e17 is nanos -> ms /1e6", microsUpperBound, UnitMillis, microsUpperBound / 1_000_000},
		{"1e17 is nanos -> ns identity", microsUpperBound, UnitNanos, microsUpperBound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeEpochToUnit(tt.raw, tt.unit); got != tt.want {
				t.Fatalf("normalizeEpochToUnit(%d, %q) = %d, want %d (boundary is exclusive → next band)", tt.raw, tt.unit, got, tt.want)
			}
		})
	}

	// Explicit anti-assertion: 1e11 must NOT be treated as seconds (×1000).
	if got := normalizeEpochToUnit(secondsUpperBound, UnitMillis); got == secondsUpperBound*1000 {
		t.Fatalf("1e11 was treated as seconds (got %d); boundary must be exclusive → millis band", got)
	}
}

// TestGetTimestampsWithDefaultsAutoDetectsMagnitude verifies that an explicit
// start/end pair given in any magnitude is normalized to the canonical unit.
func TestGetTimestampsWithDefaultsAutoDetectsMagnitude(t *testing.T) {
	tests := []struct {
		name               string
		start, end         any
		unit               string
		wantStart, wantEnd string
	}{
		{"seconds in ms tool", "1711123200", "1711130400", UnitMillis, "1711123200000", "1711130400000"},
		{"ms in ms tool", "1711123200000", "1711130400000", UnitMillis, "1711123200000", "1711130400000"},
		{"nanos in ms tool", "1711123200000000000", "1711130400000000000", UnitMillis, "1711123200000", "1711130400000"},
		{"seconds in ns tool", "1711123200", "1711130400", UnitNanos, "1711123200000000000", "1711130400000000000"},
		{"nanos in ns tool", "1711123200000000000", "1711130400000000000", UnitNanos, "1711123200000000000", "1711130400000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := GetTimestampsWithDefaults(map[string]any{"start": tt.start, "end": tt.end}, tt.unit)
			if start != tt.wantStart {
				t.Fatalf("start = %q, want %q", start, tt.wantStart)
			}
			if end != tt.wantEnd {
				t.Fatalf("end = %q, want %q", end, tt.wantEnd)
			}
		})
	}
}

// TestGetTimestampsWithDefaultsNanoBackwardCompat pins the contract that the two
// service tools (which historically passed ns) keep resolving ns values
// correctly through the shared helper after the ms→auto-detect migration.
func TestGetTimestampsWithDefaultsNanoBackwardCompat(t *testing.T) {
	args := map[string]any{
		"start": "1711123200000000000",
		"end":   "1711130400000000000",
	}
	start, end := GetTimestampsWithDefaults(args, UnitNanos)
	if start != "1711123200000000000" || end != "1711130400000000000" {
		t.Fatalf("ns values should round-trip unchanged on a ns tool: start=%s end=%s", start, end)
	}
}

// TestValidateExplicitTimestamps pins that a PRESENT-but-malformed start/end is
// rejected loudly, while ABSENT and EMPTY-STRING (both mean "use the default
// window") return nil. This guards the silent-failure regression where
// GetTimestampsWithDefaults silently falls back to the default window for a
// malformed value, handing back the wrong time range with no error.
func TestValidateExplicitTimestamps(t *testing.T) {
	const maxExactFloat = float64(1<<53 - 1)

	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{"malformed string start", map[string]any{"start": "yesterday"}, true},
		{"malformed string end", map[string]any{"end": "soon"}, true},
		{"valid string start", map[string]any{"start": "1711130400000"}, false},
		{"valid string pair", map[string]any{"start": "1711123200000", "end": "1711130400000"}, false},
		{"absent", map[string]any{}, false},
		{"empty string start", map[string]any{"start": ""}, false},
		{"empty string end", map[string]any{"end": ""}, false},
		{"nil value", map[string]any{"start": nil}, false},
		{"valid json.Number", map[string]any{"start": json.Number("1711130400000")}, false},
		{"malformed json.Number", map[string]any{"start": json.Number("12.5")}, true},
		{"valid float64", map[string]any{"start": float64(1711130400000)}, false},
		{"non-integral float64", map[string]any{"start": float64(1711130400000.5)}, true},
		{"precision-lost float64", map[string]any{"start": maxExactFloat + 2}, true},
		{"valid int", map[string]any{"start": 1711130400000}, false},
		{"unsupported type bool", map[string]any{"start": true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExplicitTimestamps(tt.args)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateExplicitTimestamps(%#v) = nil, want error", tt.args)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateExplicitTimestamps(%#v) = %v, want nil", tt.args, err)
			}
		})
	}
}
