package tools

import (
	"encoding/json"
	"testing"
)

// TestParseStepIntervalSeconds pins that query_metrics' stepInterval parser is
// STRICT on strings: only an entirely-numeric positive integer of seconds is
// honored. Unit-suffixed ("1h"/"60s") and non-numeric ("abc") strings are
// rejected (valid=false) and reported via raw rather than silently coerced.
func TestParseStepIntervalSeconds(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		wantN     int64
		wantRaw   string
		wantValid bool
	}{
		{"numeric string", "60", 60, "", true},
		{"json number", json.Number("300"), 300, "", true},
		{"float whole", float64(120), 120, "", true},
		{"int", 90, 90, "", true},
		{"hours suffix rejected", "1h", 0, "1h", false},
		{"seconds suffix rejected", "60s", 0, "60s", false},
		{"trailing junk rejected", "60abc", 0, "60abc", false},
		{"non-numeric rejected", "abc", 0, "abc", false},
		{"empty -> absent", "", 0, "", false},
		{"whitespace trimmed numeric", "  45 ", 45, "", true},
		{"zero rejected", "0", 0, "0", false},
		{"negative rejected", "-5", 0, "-5", false},
		{"float fraction rejected", float64(1.5), 0, "1.5", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, raw, valid := parseStepIntervalSeconds(tt.in)
			if n != tt.wantN || raw != tt.wantRaw || valid != tt.wantValid {
				t.Fatalf("parseStepIntervalSeconds(%#v) = (%d, %q, %v), want (%d, %q, %v)",
					tt.in, n, raw, valid, tt.wantN, tt.wantRaw, tt.wantValid)
			}
		})
	}
}

// TestParseMetricsQueryArgs_StepIntervalStrict pins the end-to-end contract on
// query_metrics: a numeric stepInterval is honored; "1h"/"60s"/"abc" are NOT
// coerced (StepInterval stays 0 → backend auto-select) and the rejected raw
// value is captured for the [Decisions] note.
func TestParseMetricsQueryArgs_StepIntervalStrict(t *testing.T) {
	t.Run("numeric honored", func(t *testing.T) {
		req, err := parseMetricsQueryArgs(map[string]any{
			"metricName":   "system.cpu.time",
			"stepInterval": "60",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.StepInterval != 60 {
			t.Fatalf("StepInterval = %d, want 60", req.StepInterval)
		}
		if req.StepIntervalInvalid != "" {
			t.Fatalf("StepIntervalInvalid = %q, want empty", req.StepIntervalInvalid)
		}
	})

	t.Run("json number honored", func(t *testing.T) {
		req, err := parseMetricsQueryArgs(map[string]any{
			"metricName":   "system.cpu.time",
			"stepInterval": float64(300),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.StepInterval != 300 {
			t.Fatalf("StepInterval = %d, want 300", req.StepInterval)
		}
	})

	for _, bad := range []string{"1h", "60s", "abc"} {
		t.Run("rejected_"+bad, func(t *testing.T) {
			req, err := parseMetricsQueryArgs(map[string]any{
				"metricName":   "system.cpu.time",
				"stepInterval": bad,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.StepInterval != 0 {
				t.Fatalf("StepInterval = %d for %q, want 0 (backend auto-select)", req.StepInterval, bad)
			}
			if req.StepIntervalInvalid != bad {
				t.Fatalf("StepIntervalInvalid = %q, want %q", req.StepIntervalInvalid, bad)
			}
		})
	}
}
