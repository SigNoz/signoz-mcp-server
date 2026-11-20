package timeutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Canonical units for normalizeEpochToUnit. An explicit numeric epoch is
// magnitude-auto-detected, so a caller can pass seconds, millis, micros, or
// nanos and get the right instant.
const (
	UnitMillis = "ms"
	UnitNanos  = "ns"
)

// Magnitude thresholds for auto-detecting the unit of a positive epoch value.
// A real "now" timestamp is ~1.7e9 s / ~1.7e12 ms / ~1.7e15 µs / ~1.7e18 ns.
// We classify by upper bound, leaving each band ~100x of headroom (the next
// band starts ~1000x up), so realistic timestamps for the next several
// millennia stay in the right band:
//
//	value <  1e11  → seconds   (1e11 s  ≈ year 5138)
//	value <  1e14  → millis    (1e14 ms ≈ year 5138)
//	value <  1e17  → micros    (1e17 µs ≈ year 5138)
//	otherwise      → nanos
const (
	secondsUpperBound = int64(1e11)
	millisUpperBound  = int64(1e14)
	microsUpperBound  = int64(1e17)

	maxInt64 = int64(^uint64(0) >> 1) // 9223372036854775807
)

// Window bounds checked before any query is sent.
var (
	// minNumericEpoch rejects numeric start/end values that read as a time before
	// 2001-09-09 (10^9 seconds). Such values are durations sent as epochs: a
	// 30-day window sent as start=0, end=2592000000 once reached SigNoz as
	// 1970 to 2052. RFC 3339 dates have no such floor.
	minNumericEpoch = time.Unix(1e9, 0).UTC()
	unixEpoch       = time.Unix(0, 0).UTC()
)

// maxSecondsEpochAge is how old a numeric epoch read as seconds (10 digits or
// fewer) may be. A duration in milliseconds such as 14 days (1209600000) reads as
// seconds in 2008, past the 2001 floor, so seconds-sized values must be recent.
// Older times stay available in milliseconds or RFC 3339.
const maxSecondsEpochAge = 366 * 24 * time.Hour

// maxFutureSkew is how far past the current time a bound may fall. It is
// generous so clock skew between the caller and this server never rejects a
// window; a bound beyond it is a wrong year or a duration sent as an epoch.
const maxFutureSkew = 24 * time.Hour

// Window is a resolved query window.
type Window struct {
	Start, End time.Time
}

// StartMs returns the window start in Unix milliseconds.
func (w Window) StartMs() int64 { return w.Start.UnixMilli() }

// EndMs returns the window end in Unix milliseconds.
func (w Window) EndMs() int64 { return w.End.UnixMilli() }

// StartNs returns the window start in Unix nanoseconds.
func (w Window) StartNs() int64 { return w.Start.UnixNano() }

// EndNs returns the window end in Unix nanoseconds.
func (w Window) EndNs() int64 { return w.End.UnixNano() }

// ResolveWindow resolves a tool's start, end and timeRange arguments against
// now, which the caller reads once per call. timeRange and start/end are two
// separate ways to give a window:
//
//   - start set: a fixed window from start to end, or to now. timeRange is not used.
//   - no start: the window is timeRange (or defaultRange) long and ends at now.
//   - end set without start: rejected, naming the start that gives the
//     timeRange-long window ending at end. An end of now counts as no end.
//
// start and end each accept an RFC 3339 date-time with Z or an offset, "now",
// "now-<duration>", or a Unix epoch of any magnitude (seconds to nanoseconds).
// Every rejection is returned before a query is sent.
func ResolveWindow(args map[string]any, defaultRange string, now time.Time) (Window, error) {
	start, hasStart, err := parseBound("start", args["start"], now)
	if err != nil {
		return Window{}, err
	}
	end, hasEnd, err := parseBound("end", args["end"], now)
	if err != nil {
		return Window{}, err
	}
	if !hasEnd {
		end = now
	}
	if !hasStart {
		length, label, err := windowLength(args["timeRange"], defaultRange)
		if err != nil {
			return Window{}, err
		}
		start = end.Add(-length)
		if hasEnd && !end.Equal(now) {
			return Window{}, endWithoutStart(Window{Start: start, End: end}, label, now)
		}
	}
	return checkWindow(Window{Start: start, End: end}, now)
}

// endWithoutStart rejects an end sent without start. An end that checkWindow
// rejects keeps that more specific error, such as a duration sent as an epoch;
// otherwise the message names the start for the window the caller likely meant.
func endWithoutStart(w Window, length string, now time.Time) error {
	if _, err := checkWindow(w, now); err != nil {
		return err
	}
	return fmt.Errorf(`invalid window: "end" %s needs "start". For the %s before it, add start %q; for a window ending now, omit end and use timeRange`,
		formatTime(w.End), length, formatTime(w.Start))
}

func checkWindow(w Window, now time.Time) (Window, error) {
	limit := now.Add(maxFutureSkew)
	if w.End.After(limit) {
		return Window{}, fmt.Errorf(`invalid "end" %s: it is more than 24h after the current time, %s. Check the year, or omit end to end the window now`,
			formatTime(w.End), formatTime(now))
	}
	if w.Start.After(limit) {
		return Window{}, fmt.Errorf(`invalid "start" %s: it is more than 24h after the current time, %s. Check the year`,
			formatTime(w.Start), formatTime(now))
	}
	if w.Start.Before(unixEpoch) {
		return Window{}, fmt.Errorf(`invalid window: it starts at %s, before 1970-01-01T00:00:00Z. Shorten timeRange or move start later`,
			formatTime(w.Start))
	}
	if w.Start.Equal(w.End) {
		return Window{}, fmt.Errorf(`invalid window: "start" and "end" are both %s, so it is empty`, formatTime(w.Start))
	}
	if w.Start.After(w.End) {
		return Window{}, fmt.Errorf(`invalid window: "start" %s is after "end" %s. Swap them, or omit end to end the window now`,
			formatTime(w.Start), formatTime(w.End))
	}
	return w, nil
}

// parseBound reads one start or end argument. present is false for an absent,
// null or empty value, which means "use the default".
func parseBound(key string, v any, now time.Time) (t time.Time, present bool, err error) {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, false, nil
		}
		t, err := parseBoundString(key, s, now)
		return t, true, err
	}
	n, present, ok := parseEpochArg(v)
	if !present {
		return time.Time{}, false, nil
	}
	if !ok {
		return time.Time{}, true, fmt.Errorf("invalid %q %v: %s", key, v, boundForms(now))
	}
	t, err = epochTime(key, n, strconv.FormatInt(n, 10), now)
	return t, true, err
}

// boundForms lists the accepted start and end forms, with an RFC 3339 example
// built from now so an agent copying it does not pick up a stale year.
func boundForms(now time.Time) string {
	return fmt.Sprintf(`use an RFC 3339 date-time with Z or an offset (%q), "now", "now-<duration>" ("now-2h"), or Unix epoch milliseconds`,
		now.UTC().Truncate(time.Hour).Format(time.RFC3339))
}

func parseBoundString(key, s string, now time.Time) (time.Time, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return epochTime(key, n, s, now)
	}
	if s == "now" {
		return now, nil
	}
	if rest, ok := strings.CutPrefix(s, "now-"); ok {
		d, err := ParseDuration(rest)
		if err != nil {
			return time.Time{}, fmt.Errorf(`invalid %q %q: after "now-" give a duration such as "30m", "2h" or "7d"`, key, s)
		}
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if _, err := time.Parse("2006-01-02T15:04:05", strings.SplitN(s, ".", 2)[0]); err == nil {
		return time.Time{}, fmt.Errorf(`invalid %q %q: add Z for UTC or an offset such as +05:30`, key, s)
	}
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		if _, err := ParseDuration(rest); err == nil {
			return time.Time{}, fmt.Errorf(`invalid %q %q: write a relative time as "now%s"`, key, s, s)
		}
	}
	return time.Time{}, fmt.Errorf("invalid %q %q: %s", key, s, boundForms(now))
}

func epochTime(key string, n int64, raw string, now time.Time) (time.Time, error) {
	t := time.Unix(0, normalizeEpochToUnit(n, UnitNanos)).UTC()
	hint := fmt.Sprintf(`For a relative window use timeRange ("1h") or "now-1h"; for an absolute time use Unix epoch milliseconds or RFC 3339 (%q)`,
		now.UTC().Truncate(time.Hour).Format(time.RFC3339))
	switch {
	case n < secondsUpperBound && t.Before(now.Add(-maxSecondsEpochAge)):
		return time.Time{}, fmt.Errorf("invalid %q %s: read as Unix seconds it is %s, more than a year ago, so it looks like a duration. %s",
			key, raw, formatTime(t), hint)
	case t.Before(minNumericEpoch):
		return time.Time{}, fmt.Errorf("invalid %q %s: as a Unix epoch it is %s, so it looks like a duration. %s",
			key, raw, formatTime(t), hint)
	}
	return t, nil
}

// windowLength reads timeRange, falling back to defaultRange when it is absent,
// null or empty. It also returns the length as written, for messages.
func windowLength(v any, defaultRange string) (time.Duration, string, error) {
	s, isString := v.(string)
	if v != nil && !isString {
		return 0, "", fmt.Errorf(`invalid "timeRange" %v: give a duration string such as "24h". For explicit bounds use start and end, for example start "now-24h" and end "now"`, v)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = defaultRange
	}
	// "now-24h" names the same window as "24h".
	label := strings.TrimPrefix(s, "now-")
	d, err := ParseDuration(label)
	if err == nil {
		return d, label, nil
	}
	if a, b, ok := strings.Cut(s, "-"); ok {
		if _, errA := ParseDuration(a); errA == nil {
			if _, errB := ParseDuration(b); errB == nil {
				return 0, "", fmt.Errorf(`invalid "timeRange" %q: give one duration. For a window that ends in the past use start "now-%s" and end "now-%s"`, s, a, b)
			}
		}
	}
	return 0, "", fmt.Errorf(`invalid "timeRange" %q: %v`, s, err)
}

// durationUnits are the units ParseDuration accepts. Sub-second units, including
// both micro signs, are kept for inputs time.ParseDuration accepted before;
// descriptions advertise s to w.
var durationUnits = []struct {
	name string
	d    time.Duration
}{
	{"ns", time.Nanosecond},
	{"us", time.Microsecond},
	{"µs", time.Microsecond}, // micro sign, U+00B5
	{"μs", time.Microsecond}, // Greek mu, U+03BC
	{"ms", time.Millisecond},
	{"s", time.Second},
	{"m", time.Minute},
	{"h", time.Hour},
	{"d", 24 * time.Hour},
	{"w", 7 * 24 * time.Hour},
}

var errDuration = errors.New(`give one or more <number><unit> terms with unit s, m, h, d or w, such as "30m", "6h", "7d", "2w" or "1h30m"`)

// ParseDuration parses a window length: one or more <number><unit> terms, such
// as "90s", "6h", "1h30m", "1.5d" or "2w". d is 24 hours and w is 7 days. The
// result must be greater than zero. A leading "+" is accepted, as
// time.ParseDuration accepts it.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "+")
	if s == "" {
		return 0, errDuration
	}
	var total float64
	for s != "" {
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, errDuration
		}
		n, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, errDuration
		}
		s = s[i:]
		matched := false
		for _, u := range durationUnits {
			if rest, ok := strings.CutPrefix(s, u.name); ok {
				total += n * float64(u.d)
				s = rest
				matched = true
				break
			}
		}
		if !matched {
			return 0, errDuration
		}
	}
	if total <= 0 || total > math.MaxInt64 {
		return 0, errDuration
	}
	return time.Duration(total), nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// normalizeEpochToUnit takes a raw positive epoch integer, auto-detects whether
// it is expressed in seconds / millis / micros / nanos by magnitude, and
// converts it DIRECTLY to the requested canonical unit ("ms" or "ns").
// Non-positive values are returned unchanged.
//
// We convert source→target directly (no nanosecond intermediate) so a large
// in-band value cannot overflow int64. For example a seconds value near the
// 1e11 band ceiling, scaled by 1e9 to reach nanos, would overflow; converting
// straight to ms (×1e3) or, for a ns target, scaling by the exact source→ns
// factor keeps every realistic timestamp well within int64.
func normalizeEpochToUnit(raw int64, unit string) int64 {
	if raw <= 0 {
		return raw
	}

	// Detect the source unit by magnitude. Each band boundary is EXCLUSIVE on
	// the upper end (strict <): a value exactly equal to a band ceiling falls
	// into the NEXT (finer) band — e.g. raw == secondsUpperBound (1e11) is
	// treated as millis, == millisUpperBound (1e14) as micros, == microsUpperBound
	// (1e17) as nanos.
	var sourceUnitNanos int64 // how many nanoseconds one unit of the source equals
	switch {
	case raw < secondsUpperBound:
		sourceUnitNanos = int64(time.Second) // seconds → 1e9 ns
	case raw < millisUpperBound:
		sourceUnitNanos = int64(time.Millisecond) // millis  → 1e6 ns
	case raw < microsUpperBound:
		sourceUnitNanos = int64(time.Microsecond) // micros  → 1e3 ns
	default:
		sourceUnitNanos = 1 // nanos → 1 ns
	}

	switch unit {
	case UnitNanos:
		// Scale source→ns by the exact factor, guarding against int64 overflow
		// for absurdly far-future inputs (clamp rather than wrap negative).
		if raw <= maxInt64/sourceUnitNanos {
			return raw * sourceUnitNanos
		}
		return maxInt64
	default: // UnitMillis — divide ns-per-source by ns-per-ms to get the ms factor.
		const nsPerMs = int64(time.Millisecond)
		if sourceUnitNanos >= nsPerMs {
			// source is coarser than (or equal to) ms: multiply.
			factor := sourceUnitNanos / nsPerMs // 1 (ms) or 1000 (s)
			if raw <= maxInt64/factor {
				return raw * factor
			}
			return maxInt64
		}
		// source is finer than ms (micros or nanos): divide.
		return raw / (nsPerMs / sourceUnitNanos)
	}
}

// parseEpochArg classifies a non-string timestamp argument value (parseBound
// reads strings itself) into three states:
//   - present=false: the value is absent (nil) → caller uses default
//   - present=true, ok=false: the value is present but cannot be parsed as an
//     exact integer epoch → malformed, caller should error
//   - present=true, ok=true: the value parsed to a valid epoch int64
func parseEpochArg(v any) (value int64, present bool, ok bool) {
	switch value := v.(type) {
	case nil:
		return 0, false, false
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, true, false
		}
		return n, true, true
	case int:
		return int64(value), true, true
	case int8:
		return int64(value), true, true
	case int16:
		return int64(value), true, true
	case int32:
		return int64(value), true, true
	case int64:
		return value, true, true
	case uint:
		return int64(value), true, true
	case uint8:
		return int64(value), true, true
	case uint16:
		return int64(value), true, true
	case uint32:
		return int64(value), true, true
	case uint64:
		return int64(value), true, true
	case float64:
		// Reject values that have lost precision as a float64 (> 2^53) or are
		// non-integral. The canonical schema is string-typed; this path only
		// guards stray numeric JSON.
		const maxExactFloat = float64(1<<53 - 1)
		if value < 0 || value > maxExactFloat || float64(int64(value)) != value {
			return 0, true, false
		}
		return int64(value), true, true
	default:
		return 0, true, false
	}
}
