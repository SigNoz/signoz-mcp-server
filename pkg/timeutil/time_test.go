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
