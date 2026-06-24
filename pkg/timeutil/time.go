package timeutil

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Canonical units accepted by GetTimestampsWithDefaults. Callers advertise "ms"
// in tool descriptions uniformly; "ns" is kept for the two service tools whose
// existing callers may still pass nanosecond windows (backward-compat). Either
// way, an explicit numeric epoch is magnitude-auto-detected and normalized to
// the requested canonical unit, so a caller can pass seconds, millis, micros,
// or nanos and get the right window.
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

// ParseTimeRange parses time range strings like "2h", "2d", "30m", "7d"
// Returns duration or error
func ParseTimeRange(timeRange string) (time.Duration, error) {
	duration, err := time.ParseDuration(timeRange)
	if err == nil {
		return duration, nil
	}

	if len(timeRange) > 1 && timeRange[len(timeRange)-1] == 'd' {
		days := timeRange[:len(timeRange)-1]
		if numDays, err := strconv.Atoi(days); err == nil {
			return time.Duration(numDays) * 24 * time.Hour, nil
		}
	}

	return 0, fmt.Errorf("invalid time range format: use formats like '2h', '30m', '2d', '7d'")
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

// GetTimestampsWithDefaults returns start and end timestamps as strings in the
// requested canonical unit ("ms" by default, "ns" for the legacy service
// tools). A complete explicit start/end pair takes precedence over timeRange.
//
// Explicit start/end MUST be string-typed numerics (the JSON-Schema declares
// them as strings). A numeric JSON value > 2^53 loses precision as a float64,
// so the float path is deliberately NOT trusted as an exact timestamp — such
// values fall through to the timeRange/default window instead of being silently
// truncated. Any accepted explicit value is magnitude-auto-detected (s/ms/µs/ns)
// and normalized to the canonical unit, so callers can pass any magnitude.
func GetTimestampsWithDefaults(args map[string]any, unit string) (start, end string) {
	now := time.Now()

	var toUnix func(time.Time) int64
	switch unit {
	case UnitNanos:
		toUnix = func(t time.Time) int64 { return t.UnixNano() }
	default:
		toUnix = func(t time.Time) int64 { return t.UnixMilli() }
	}

	defaultEnd := toUnix(now)
	defaultStart := toUnix(now.Add(-6 * time.Hour))

	startRaw, hasStart := timestampArgInt(args, "start")
	endRaw, hasEnd := timestampArgInt(args, "end")
	if hasStart && hasEnd {
		return formatInt(normalizeEpochToUnit(startRaw, unit)),
			formatInt(normalizeEpochToUnit(endRaw, unit))
	}

	if timeRange, ok := args["timeRange"].(string); ok && timeRange != "" {
		if duration, err := ParseTimeRange(timeRange); err == nil {
			startTime := toUnix(now.Add(-duration))
			endTime := toUnix(now)
			return formatInt(startTime), formatInt(endTime)
		}
	}

	if hasStart {
		start = formatInt(normalizeEpochToUnit(startRaw, unit))
	} else {
		start = formatInt(defaultStart)
	}

	if hasEnd {
		end = formatInt(normalizeEpochToUnit(endRaw, unit))
	} else {
		end = formatInt(defaultEnd)
	}

	return start, end
}

func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

// timestampArgInt extracts an epoch value as an int64 from args[key]. It accepts
// string-typed numerics (the canonical schema form) and json.Number / integer
// types. A float64 is accepted ONLY when it is integral and within the exact
// float53 range — a larger float has already lost precision, so we treat it as
// absent rather than emit a silently-truncated timestamp.
//
// NOTE: this returns (0, false) for BOTH "absent" and "present-but-unparseable".
// GetTimestampsWithDefaults intentionally collapses both into "use the default
// window" so it can never fail. Callers that must reject a present-but-malformed
// value LOUDLY (the silent-failure anti-pattern) should pre-validate with
// ValidateExplicitTimestamps, which distinguishes the two cases.
func timestampArgInt(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	n, _, ok := parseEpochArg(v)
	return n, ok
}

// parseEpochArg classifies a single timestamp argument value into three states:
//   - present=false: the value is absent (nil) or an empty string → caller uses default
//   - present=true, ok=false: the value is present but cannot be parsed as an
//     exact integer epoch → malformed, caller should error
//   - present=true, ok=true: the value parsed to a valid epoch int64
//
// This is the single parse used by BOTH timestampArgInt (which ignores the
// present/malformed distinction and defaults) and ValidateExplicitTimestamps
// (which surfaces present+malformed as an error), so the two can never drift.
func parseEpochArg(v any) (value int64, present bool, ok bool) {
	switch value := v.(type) {
	case nil:
		return 0, false, false
	case string:
		if value == "" {
			return 0, false, false
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, true, false
		}
		return n, true, true
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

// ValidateExplicitTimestamps returns a descriptive error when args["start"] or
// args["end"] is PRESENT and non-empty but cannot be parsed as an exact integer
// epoch (e.g. {"start":"yesterday"} or a non-integral / precision-lost float).
// Absent keys and empty strings return nil — those legitimately mean "use the
// default window", which GetTimestampsWithDefaults handles.
//
// This exists because GetTimestampsWithDefaults has no error channel and
// silently falls back to the default window for a malformed start/end, handing
// the user the WRONG time range with no signal. Handlers call this first to fail
// loudly. It lives in pkg/timeutil (not the tools package) and returns a plain
// error so the handler can wrap it in the tools package's validation-error
// shape; timeutil must not import the tools package.
func ValidateExplicitTimestamps(args map[string]any) error {
	for _, key := range []string{"start", "end"} {
		v, ok := args[key]
		if !ok {
			continue
		}
		if _, present, parsed := parseEpochArg(v); present && !parsed {
			return fmt.Errorf(
				"invalid %q timestamp %v: must be a unix epoch integer (e.g. milliseconds like \"1711130400000\") or omitted; for a relative window use timeRange (e.g. \"1h\", \"24h\")",
				key, v)
		}
	}
	return nil
}

// NowMillis returns the current time in unix milliseconds.
func NowMillis() int64 {
	return time.Now().UnixMilli()
}

// HoursAgoMillis returns unix milliseconds for the given number of hours ago.
func HoursAgoMillis(hours int) int64 {
	return time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
}
