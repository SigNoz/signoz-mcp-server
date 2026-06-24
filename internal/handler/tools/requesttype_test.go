package tools

import (
	"strings"
	"testing"
)

// TestParseAggregateArgs_RejectsUnknownRequestType pins K4: the aggregate tools
// reject an unknown requestType at the arg layer instead of passing it through.
func TestParseAggregateArgs_RejectsUnknownRequestType(t *testing.T) {
	_, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"requestType": "raw",
	}, "logs", "")
	if err == nil {
		t.Fatal("expected error for unknown requestType, got nil")
	}
	if !strings.Contains(err.Error(), "requestType") {
		t.Fatalf("error should mention requestType, got: %v", err)
	}
}

// TestParseAggregateArgs_DefaultsToScalar pins the per-signal default is kept.
func TestParseAggregateArgs_DefaultsToScalar(t *testing.T) {
	req, err := parseAggregateArgs(map[string]any{"aggregation": "count"}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.RequestType != "scalar" {
		t.Fatalf("aggregate default requestType = %q, want scalar", req.RequestType)
	}
}

// TestParseAggregateArgs_AcceptsValidRequestTypes pins both valid values pass.
func TestParseAggregateArgs_AcceptsValidRequestTypes(t *testing.T) {
	for _, v := range []string{"scalar", "time_series"} {
		req, err := parseAggregateArgs(map[string]any{"aggregation": "count", "requestType": v}, "traces", "")
		if err != nil {
			t.Fatalf("requestType %q: unexpected error: %v", v, err)
		}
		if req.RequestType != v {
			t.Fatalf("requestType = %q, want %q", req.RequestType, v)
		}
	}
}

// TestParseMetricsQueryArgs_RejectsUnknownRequestType pins K4 for query_metrics.
func TestParseMetricsQueryArgs_RejectsUnknownRequestType(t *testing.T) {
	_, err := parseMetricsQueryArgs(map[string]any{
		"metricName":  "system.cpu.time",
		"requestType": "raw",
	})
	if err == nil {
		t.Fatal("expected error for unknown requestType, got nil")
	}
	if !strings.Contains(err.Error(), "requestType") {
		t.Fatalf("error should mention requestType, got: %v", err)
	}
}

// TestParseMetricsQueryArgs_DefaultsToTimeSeries pins the per-signal default.
func TestParseMetricsQueryArgs_DefaultsToTimeSeries(t *testing.T) {
	req, err := parseMetricsQueryArgs(map[string]any{"metricName": "system.cpu.time"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.RequestType != "time_series" {
		t.Fatalf("metrics default requestType = %q, want time_series", req.RequestType)
	}
}
