package timeutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

// ParseDateTimeString attempts to parse a human-readable date/time string into a time.Time
// Supports multiple formats:
//   - Numeric timestamps (milliseconds or seconds since epoch)
//   - ISO 8601 / RFC3339: "2006-01-02T15:04:05Z", "2006-01-02T15:04:05-07:00"
//   - Common formats: "2006-01-02 15:04:05", "2006-01-02"
//   - Relative times: "now", "today", "yesterday"
//   - Time ranges (relative to referenceTime): "24h", "7d", "30m" (calculates as referenceTime - duration)
//   - Natural language with context (requires current time): "Dec 3rd 5 PM", "5-6 PM on the 3rd"
func ParseDateTimeString(dateStr string, referenceTime time.Time) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}

	// Try parsing as numeric timestamp (milliseconds or seconds)
	if timestamp, err := strconv.ParseInt(dateStr, 10, 64); err == nil {
		// If it's less than year 2000 in milliseconds, assume it's seconds
		// Year 2000 in milliseconds: 946684800000
		if timestamp < 946684800000 {
			return time.Unix(timestamp, 0), nil
		}
		// Otherwise assume milliseconds
		return time.Unix(0, timestamp*int64(time.Millisecond)), nil
	}

	// Try relative time keywords
	lowerStr := strings.ToLower(strings.TrimSpace(dateStr))
	switch lowerStr {
	case "now":
		return referenceTime, nil
	case "today":
		// Start of today
		year, month, day := referenceTime.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, referenceTime.Location()), nil
	case "yesterday":
		yesterday := referenceTime.AddDate(0, 0, -1)
		year, month, day := yesterday.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, referenceTime.Location()), nil
	case "tomorrow":
		tomorrow := referenceTime.AddDate(0, 0, 1)
		year, month, day := tomorrow.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, referenceTime.Location()), nil
	}

	// Try parsing as time range (e.g., "24h", "7d", "30m")
	// This calculates the time as referenceTime - duration
	if duration, err := ParseTimeRange(dateStr); err == nil {
		return referenceTime.Add(-duration), nil
	}

	// Try standard time formats (in order of specificity)
	timeFormats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006 15:04",
		"01/02/2006",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006/01/02",
	}

	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, dateStr); err == nil {
			// If no timezone specified, use reference time's location
			if t.Location() == time.UTC && !strings.Contains(layout, "Z") && !strings.Contains(layout, "-07:00") {
				// Try parsing with reference time's location
				if t2, err := time.ParseInLocation(layout, dateStr, referenceTime.Location()); err == nil {
					return t2, nil
				}
			}
			return t, nil
		}
	}

	// Try natural language patterns (basic support)
	// Pattern: "Dec 3rd 5 PM" or "December 3rd 5 PM"
	naturalPattern := regexp.MustCompile(`(?i)(\w+)\s+(\d+)(?:st|nd|rd|th)?\s+(\d+)\s*(AM|PM)?`)
	if matches := naturalPattern.FindStringSubmatch(dateStr); matches != nil {
		monthStr := matches[1]
		dayStr := matches[2]
		hourStr := matches[3]
		ampm := matches[4]

		// Parse month
		var month time.Month
		monthNames := map[string]time.Month{
			"january": time.January, "jan": time.January,
			"february": time.February, "feb": time.February,
			"march": time.March, "mar": time.March,
			"april": time.April, "apr": time.April,
			"may":  time.May,
			"june": time.June, "jun": time.June,
			"july": time.July, "jul": time.July,
			"august": time.August, "aug": time.August,
			"september": time.September, "sep": time.September,
			"october": time.October, "oct": time.October,
			"november": time.November, "nov": time.November,
			"december": time.December, "dec": time.December,
		}
		if m, ok := monthNames[strings.ToLower(monthStr)]; ok {
			month = m
		} else {
			return time.Time{}, fmt.Errorf("invalid month: %s", monthStr)
		}

		day, _ := strconv.Atoi(dayStr)
		hour, _ := strconv.Atoi(hourStr)

		// Handle AM/PM
		if strings.ToUpper(ampm) == "PM" && hour < 12 {
			hour += 12
		} else if strings.ToUpper(ampm) == "AM" && hour == 12 {
			hour = 0
		}

		year := referenceTime.Year()
		// If the date is in the future relative to reference time, assume previous year
		// (This is a heuristic - could be improved)
		t := time.Date(year, month, day, hour, 0, 0, 0, referenceTime.Location())
		if t.After(referenceTime) && t.Sub(referenceTime) > 30*24*time.Hour {
			t = t.AddDate(-1, 0, 0)
		}

		return t, nil
	}

	// Pattern: "5 PM" or "17:00" (time only, use today's date)
	timeOnlyPattern := regexp.MustCompile(`(?i)(\d+):?(\d+)?\s*(AM|PM)?`)
	if matches := timeOnlyPattern.FindStringSubmatch(dateStr); matches != nil {
		hourStr := matches[1]
		minuteStr := matches[2]
		ampm := matches[3]

		hour, _ := strconv.Atoi(hourStr)
		minute := 0
		if minuteStr != "" {
			minute, _ = strconv.Atoi(minuteStr)
		}

		// Handle AM/PM
		if strings.ToUpper(ampm) == "PM" && hour < 12 {
			hour += 12
		} else if strings.ToUpper(ampm) == "AM" && hour == 12 {
			hour = 0
		}

		year, month, day := referenceTime.Date()
		return time.Date(year, month, day, hour, minute, 0, 0, referenceTime.Location()), nil
	}

	return time.Time{}, fmt.Errorf("unable to parse date/time string: %s (supported formats: ISO 8601, RFC3339, 'YYYY-MM-DD HH:MM:SS', numeric timestamps, time ranges like '24h' or '7d', or natural language like 'Dec 3rd 5 PM')", dateStr)
}

// GetTimestampsWithDefaults returns start and end timestamps as strings
// Supports "timeRange" (e.g., "2h", "2d") which takes precedence over start/end.
// Also supports human-readable date/time strings for start/end parameters:
//   - Numeric timestamps (milliseconds or seconds since epoch)
//   - ISO 8601 / RFC3339: "2006-01-02T15:04:05Z"
//   - Common formats: "2006-01-02 15:04:05", "2006-01-02"
//   - Relative: "now", "today", "yesterday"
//   - Natural language: "Dec 3rd 5 PM", "5 PM" (assumes today)
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

	// Check for timeRange parameter first (takes precedence)
	if timeRange, ok := args["timeRange"].(string); ok && timeRange != "" {
		if duration, err := ParseTimeRange(timeRange); err == nil {
			startTime := toUnix(now.Add(-duration))
			endTime := toUnix(now)
			return fmt.Sprintf("%d", startTime), fmt.Sprintf("%d", endTime)
		}
	}

	// Parse start timestamp (supports both numeric and human-readable formats)
	startStr, startOk := args["start"].(string)
	if !startOk || startStr == "" {
		start = fmt.Sprintf("%d", defaultStart)
	} else {
		// Try parsing as human-readable date/time string
		if parsedTime, err := ParseDateTimeString(startStr, now); err == nil {
			start = fmt.Sprintf("%d", toUnix(parsedTime))
		} else {
			// If parsing fails, assume it's already a numeric timestamp string
			// Validate it's numeric
			if _, err := strconv.ParseInt(startStr, 10, 64); err == nil {
				start = startStr
			} else {
				// Invalid format - use default but log the error
				start = fmt.Sprintf("%d", defaultStart)
			}
		}
	}

	// Parse end timestamp (supports both numeric and human-readable formats)
	endStr, endOk := args["end"].(string)
	if !endOk || endStr == "" {
		end = fmt.Sprintf("%d", defaultEnd)
	} else {
		// Try parsing as human-readable date/time string
		if parsedTime, err := ParseDateTimeString(endStr, now); err == nil {
			end = fmt.Sprintf("%d", toUnix(parsedTime))
		} else {
			// If parsing fails, assume it's already a numeric timestamp string
			// Validate it's numeric
			if _, err := strconv.ParseInt(endStr, 10, 64); err == nil {
				end = endStr
			} else {
				// Invalid format - use default but log the error
				end = fmt.Sprintf("%d", defaultEnd)
			}
		}
	}

	return start, end
}
