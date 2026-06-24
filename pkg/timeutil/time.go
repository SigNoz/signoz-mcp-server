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
// converts it to the requested canonical unit ("ms" or "ns"). Non-positive
// values are returned unchanged.
func normalizeEpochToUnit(raw int64, unit string) int64 {
	if raw <= 0 {
		return raw
	}

	// Convert the detected magnitude to nanoseconds first, then down to the
	// requested unit. This keeps the band logic in one place.
	var nanos int64
	switch {
	case raw < secondsUpperBound:
		nanos = raw * int64(time.Second)
	case raw < millisUpperBound:
		nanos = raw * int64(time.Millisecond)
	case raw < microsUpperBound:
		nanos = raw * int64(time.Microsecond)
	default:
		nanos = raw
	}

	switch unit {
	case UnitNanos:
		return nanos
	default: // UnitMillis
		return nanos / int64(time.Millisecond)
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
func timestampArgInt(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}

	switch value := v.(type) {
	case string:
		if value == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return n, true
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case uint:
		return int64(value), true
	case uint8:
		return int64(value), true
	case uint16:
		return int64(value), true
	case uint32:
		return int64(value), true
	case uint64:
		return int64(value), true
	case float64:
		// Reject values that have lost precision as a float64 (> 2^53) or are
		// non-integral. The canonical schema is string-typed; this path only
		// guards stray numeric JSON.
		const maxExactFloat = float64(1<<53 - 1)
		if value < 0 || value > maxExactFloat || float64(int64(value)) != value {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}

// NowMillis returns the current time in unix milliseconds.
func NowMillis() int64 {
	return time.Now().UnixMilli()
}

// HoursAgoMillis returns unix milliseconds for the given number of hours ago.
func HoursAgoMillis(hours int) int64 {
	return time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
}
