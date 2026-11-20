package timeutil

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestResolveWindowAcceptsEquivalentForms pins that every accepted way of
// writing the same hour resolves to the same bounds against a fixed clock.
func TestResolveWindowAcceptsEquivalentForms(t *testing.T) {
	now := mustTime(t, "2026-11-19T10:00:00Z")
	want := Window{Start: mustTime(t, "2026-11-19T09:00:00Z"), End: now}

	tests := []struct {
		name string
		args map[string]any
	}{
		{"default window", map[string]any{}},
		{"empty strings mean default", map[string]any{"start": "", "end": "", "timeRange": ""}},
		{"null values mean default", map[string]any{"start": nil, "end": nil, "timeRange": nil}},
		{"timeRange", map[string]any{"timeRange": "1h"}},
		{"timeRange written as now-1h", map[string]any{"timeRange": "now-1h"}},
		{"compound timeRange", map[string]any{"timeRange": "30m30m"}},
		{"relative start", map[string]any{"start": "now-1h"}},
		{"relative start and end", map[string]any{"start": "now-60m", "end": "now"}},
		{"start wins over timeRange", map[string]any{"start": "now-1h", "timeRange": "24h"}},
		{"RFC 3339 with Z", map[string]any{"start": "2026-11-19T09:00:00Z", "end": "2026-11-19T10:00:00Z"}},
		{"RFC 3339 with offsets", map[string]any{"start": "2026-11-19T14:30:00+05:30", "end": "2026-11-19T05:00:00-05:00"}},
		{"RFC 3339 with fraction", map[string]any{"start": "2026-11-19T09:00:00.000Z", "end": "2026-11-19T10:00:00.000Z"}},
		{"end of now counts as no end", map[string]any{"end": "now", "timeRange": "1h"}},
		{"epoch milliseconds", map[string]any{"start": "1795078800000", "end": "1795082400000"}},
		{"epoch seconds", map[string]any{"start": "1795078800", "end": "1795082400"}},
		{"epoch nanoseconds", map[string]any{"start": "1795078800000000000", "end": "1795082400000000000"}},
		{"json.Number epoch", map[string]any{"start": json.Number("1795078800000"), "end": json.Number("1795082400000")}},
		{"float64 epoch", map[string]any{"start": float64(1795078800000), "end": float64(1795082400000)}},
		{"int epoch", map[string]any{"start": 1795078800000, "end": 1795082400000}},
		{"mixed forms", map[string]any{"start": "1795078800000", "end": "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveWindow(tt.args, "1h", now)
			if err != nil {
				t.Fatalf("ResolveWindow(%v) error: %v", tt.args, err)
			}
			if !got.Start.Equal(want.Start) || !got.End.Equal(want.End) {
				t.Fatalf("ResolveWindow(%v) = [%s, %s], want [%s, %s]", tt.args, got.Start, got.End, want.Start, want.End)
			}
			if got.StartMs() != 1795078800000 || got.EndNs() != 1795082400000000000 {
				t.Fatalf("unit conversion: StartMs = %d, EndNs = %d", got.StartMs(), got.EndNs())
			}
		})
	}
}

// TestResolveWindowCalendarEdges pins relative ranges across a year boundary and
// explicit offsets across a daylight-saving change.
func TestResolveWindowCalendarEdges(t *testing.T) {
	tests := []struct {
		name      string
		now       string
		args      map[string]any
		wantStart string
		wantEnd   string
	}{
		{"now-1h across new year", "2027-01-01T00:30:00Z", map[string]any{"start": "now-1h"}, "2026-12-31T23:30:00Z", "2027-01-01T00:30:00Z"},
		{"timeRange across new year", "2027-01-01T00:30:00Z", map[string]any{"timeRange": "1d"}, "2026-12-31T00:30:00Z", "2027-01-01T00:30:00Z"},
		{"local midnight with offset", "2026-03-09T00:00:00Z", map[string]any{"start": "2026-03-08T00:00:00-05:00", "end": "2026-03-08T12:00:00-04:00"}, "2026-03-08T05:00:00Z", "2026-03-08T16:00:00Z"},
		{"offsets across a DST change", "2026-03-09T00:00:00Z", map[string]any{"start": "2026-03-08T01:00:00-05:00", "end": "2026-03-08T03:00:00-04:00"}, "2026-03-08T06:00:00Z", "2026-03-08T07:00:00Z"},
		{"old millisecond epochs stay valid", "2026-11-19T10:00:00Z", map[string]any{"start": "1262304000000", "end": "1262390400000"}, "2010-01-01T00:00:00Z", "2010-01-02T00:00:00Z"},
		{"old RFC 3339 dates stay valid", "2026-11-19T10:00:00Z", map[string]any{"start": "1999-12-31T00:00:00Z", "end": "2000-01-01T00:00:00Z"}, "1999-12-31T00:00:00Z", "2000-01-01T00:00:00Z"},
		{"end a little ahead of this clock", "2026-11-19T10:00:00Z", map[string]any{"start": "now-1h", "end": "2026-11-19T10:05:00Z"}, "2026-11-19T09:00:00Z", "2026-11-19T10:05:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveWindow(tt.args, "1h", mustTime(t, tt.now))
			if err != nil {
				t.Fatalf("ResolveWindow(%v) error: %v", tt.args, err)
			}
			if !got.Start.Equal(mustTime(t, tt.wantStart)) || !got.End.Equal(mustTime(t, tt.wantEnd)) {
				t.Fatalf("ResolveWindow(%v) = [%s, %s], want [%s, %s]", tt.args, got.Start, got.End, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// TestResolveWindowRejectsBeforeQuerying pins each rejection and the fix its
// message names.
func TestResolveWindowRejectsBeforeQuerying(t *testing.T) {
	now := mustTime(t, "2026-11-19T10:00:00Z")
	const maxExactFloat = float64(1<<53 - 1)

	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"unparseable start", map[string]any{"start": "yesterday"}, `invalid "start" "yesterday": use an RFC 3339 date-time with Z or an offset ("2026-11-19T10:00:00Z")`},
		{"date-time without a zone", map[string]any{"start": "2026-10-27T00:00:00"}, "add Z for UTC or an offset"},
		{"date-time with a zone name", map[string]any{"start": "2026-10-24 09:30 IST"}, "use an RFC 3339 date-time"},
		{"relative time without now", map[string]any{"start": "-24h"}, `write a relative time as "now-24h"`},
		{"bad duration after now-", map[string]any{"start": "now-1x"}, `after "now-" give a duration`},
		{"duration sent as epochs", map[string]any{"start": "0", "end": "2592000000"}, `invalid "start" 0: read as Unix seconds it is 1970-01-01T00:00:00Z`},
		{"14 days in ms sent as start", map[string]any{"start": "1209600000"}, `read as Unix seconds it is 2008-05-01T00:00:00Z, more than a year ago`},
		{"20 days in ms sent as end", map[string]any{"end": "1728000000"}, "more than a year ago, so it looks like a duration"},
		{"millisecond epoch before 2001", map[string]any{"start": "500000000000"}, `as a Unix epoch it is 1985-11-05T00:53:20Z, so it looks like a duration`},
		{"one hour sent as start", map[string]any{"start": "3600"}, "looks like a duration"},
		{"one day sent as start", map[string]any{"start": "86400"}, "looks like a duration"},
		{"one hour in ms sent as start", map[string]any{"start": "3600000"}, "looks like a duration"},
		{"one day in ms sent as start", map[string]any{"start": 86400000}, "looks like a duration"},
		{"negative epoch", map[string]any{"start": "-1711123200000"}, "looks like a duration"},
		{"duration sent as end", map[string]any{"end": "2592000000"}, `invalid "end" 2052-02-20T00:00:00Z: it is more than 24h after the current time`},
		{"wrong year", map[string]any{"start": "now-1h", "end": "2027-11-21T00:27:26Z"}, "Check the year"},
		{"future start", map[string]any{"start": "2027-11-19T09:00:00Z"}, `invalid "start" 2027-11-19T09:00:00Z`},
		{"reversed", map[string]any{"start": "2026-11-19T11:00:00Z", "end": "2026-11-19T10:30:00Z"}, `"start" 2026-11-19T11:00:00Z is after "end" 2026-11-19T10:30:00Z`},
		{"start after now with no end", map[string]any{"start": "2026-11-19T11:00:00Z"}, "is after"},
		{"empty", map[string]any{"start": "now", "end": "now"}, "so it is empty"},
		{"end without start", map[string]any{"end": "2026-11-19T09:00:00Z", "timeRange": "2h"}, `invalid window: "end" 2026-11-19T09:00:00Z needs "start". For the 2h before it, send start "2026-11-19T07:00:00Z" and end "2026-11-19T09:00:00Z"`},
		{"relative end without start or timeRange", map[string]any{"end": "now-1h"}, `For the 1h before it, send start "2026-11-19T08:00:00Z" and end "2026-11-19T09:00:00Z"; for a window ending now, omit end`},
		{"unparseable timeRange", map[string]any{"timeRange": "24hours"}, `invalid "timeRange" "24hours": give one or more`},
		{"zero timeRange", map[string]any{"timeRange": "0h"}, `invalid "timeRange" "0h"`},
		{"negative timeRange", map[string]any{"timeRange": "-1h"}, `invalid "timeRange" "-1h"`},
		{"two timeRanges", map[string]any{"timeRange": "6h-4h"}, `start "now-6h" and end "now-4h"`},
		{"timeRange object", map[string]any{"timeRange": map[string]any{"from": "now-24h", "to": "now"}}, `give a duration string such as "24h"`},
		{"timeRange number", map[string]any{"timeRange": 24}, `give a duration string such as "24h"`},
		{"timeRange reaching before 1970", map[string]any{"timeRange": "100000d"}, "before 1970-01-01T00:00:00Z"},
		{"precision-lost float", map[string]any{"start": maxExactFloat + 2}, `invalid "start"`},
		{"non-integral float", map[string]any{"start": 1711130400000.5}, `invalid "start"`},
		{"bool", map[string]any{"start": true}, `invalid "start" true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveWindow(tt.args, "1h", now)
			if err == nil {
				t.Fatalf("ResolveWindow(%v) = [%s, %s], want error containing %q", tt.args, got.Start, got.End, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ResolveWindow(%v) error = %q, want it to contain %q", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	valid := map[string]time.Duration{
		"90s":   90 * time.Second,
		"30m":   30 * time.Minute,
		"6h":    6 * time.Hour,
		"1h30m": 90 * time.Minute,
		"7d":    7 * 24 * time.Hour,
		"1.5d":  36 * time.Hour,
		"2d12h": 60 * time.Hour,
		"2w":    14 * 24 * time.Hour,
		"500ms": 500 * time.Millisecond,
		"+2d":   48 * time.Hour,
	}
	for in, want := range valid {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	// Positive time.ParseDuration inputs, which timeRange took before d and w
	// were added, keep their meaning.
	for _, in := range []string{"+1h", "+1.5h30m", "1.001s", "1000000μs", "1000000µs", "1000000us", "1500ns", ".5h", "5.h", "2h45m0.5s"} {
		want, err := time.ParseDuration(in)
		if err != nil {
			t.Fatalf("time.ParseDuration(%q): %v", in, err)
		}
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v as time.ParseDuration gives", in, got, err, want)
		}
	}
	for _, in := range []string{"", "+", "++1h", "0h", "-1h", "1x", "24hours", "h", "5", "1h30", "1 h", "1..5h"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want error", in, got)
		}
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
		// seconds → ns: ×1e9 leaves int64 range (1e11×1e9 = 1e20) → clamp, never wrap.
		{"sec ceiling -> ns clamps", secNearCeil, UnitNanos, maxInt64},
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
}
