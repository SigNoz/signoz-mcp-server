package timeutil

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

const maxExactJSONInteger = 1<<53 - 1

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

// GetTimestampsWithDefaults returns start and end timestamps as strings
// Supports "timeRange" (e.g., "2h", "2d"). A complete explicit start/end
// pair takes precedence over timeRange.
func GetTimestampsWithDefaults(args map[string]any, unit string) (start, end string) {
	now := time.Now()

	var toUnix func(time.Time) int64
	switch unit {
	case "ns":
		toUnix = func(t time.Time) int64 { return t.UnixNano() }
	default:
		toUnix = func(t time.Time) int64 { return t.UnixMilli() }
	}

	defaultEnd := toUnix(now)
	defaultStart := toUnix(now.Add(-6 * time.Hour))

	start, hasStart := timestampArgString(args, "start")
	end, hasEnd := timestampArgString(args, "end")
	if hasStart && hasEnd {
		return start, end
	}

	if timeRange, ok := args["timeRange"].(string); ok && timeRange != "" {
		if duration, err := ParseTimeRange(timeRange); err == nil {
			startTime := toUnix(now.Add(-duration))
			endTime := toUnix(now)
			return fmt.Sprintf("%d", startTime), fmt.Sprintf("%d", endTime)
		}
	}

	if !hasStart {
		start = fmt.Sprintf("%d", defaultStart)
	}

	if !hasEnd {
		end = fmt.Sprintf("%d", defaultEnd)
	}

	return start, end
}

func timestampArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}

	switch value := v.(type) {
	case string:
		return value, value != ""
	case json.Number:
		return value.String(), value.String() != ""
	case int:
		return fmt.Sprintf("%d", value), true
	case int8:
		return fmt.Sprintf("%d", value), true
	case int16:
		return fmt.Sprintf("%d", value), true
	case int32:
		return fmt.Sprintf("%d", value), true
	case int64:
		return fmt.Sprintf("%d", value), true
	case uint:
		return fmt.Sprintf("%d", value), true
	case uint8:
		return fmt.Sprintf("%d", value), true
	case uint16:
		return fmt.Sprintf("%d", value), true
	case uint32:
		return fmt.Sprintf("%d", value), true
	case uint64:
		return fmt.Sprintf("%d", value), true
	case float64:
		if value < 0 || value > maxExactJSONInteger || math.Trunc(value) != value {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	default:
		return "", false
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
