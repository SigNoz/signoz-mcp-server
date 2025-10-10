package timeutil

import (
	"fmt"
	"strconv"
	"time"
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

// GetTimestampsWithDefaults returns start and end timestamps as strings
// Supports "timeRange" (e.g., "2h", "2d") which takes precedence over start/end.
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
	defaultStart := toUnix(now.Add(-24 * time.Hour))

	if timeRange, ok := args["timeRange"].(string); ok && timeRange != "" {
		if duration, err := ParseTimeRange(timeRange); err == nil {
			startTime := toUnix(now.Add(-duration))
			endTime := toUnix(now)
			return fmt.Sprintf("%d", startTime), fmt.Sprintf("%d", endTime)
		}
	}

	start, ok := args["start"].(string)
	if !ok || start == "" {
		start = fmt.Sprintf("%d", defaultStart)
	}

	end, ok = args["end"].(string)
	if !ok || end == "" {
		end = fmt.Sprintf("%d", defaultEnd)
	}

	return start, end
}
