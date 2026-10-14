package tools

import (
	"strings"
	"testing"
)

// NOTE: this file exercises the PURE parser functions (parseAggregateArgs,
// parseMetricsQueryArgs) only — it asserts the returned error, never a
// *mcp.CallToolResult, so there is no StructuredContent.code to pin here.
// Handler-level coverage that the unknown-requestType rejection carries
// CodeValidationFailed lives where a handler result is available: the live
// e2e suite (tests/e2e/tests/test_param_validation.py) asserts the code on
// aggregate_logs/query_metrics results through the MCP transport.

// TestParseAggregateArgs_RejectsUnknownRequestType pins that the aggregate tools
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

// TestParseAggregateArgs_ExplicitRequestTypeReachesRequest pins that a valid
// non-default requestType is carried into the request rather than defaulted.
func TestParseAggregateArgs_ExplicitRequestTypeReachesRequest(t *testing.T) {
	req, err := parseAggregateArgs(map[string]any{"aggregation": "count", "requestType": "time_series"}, "traces", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.RequestType != "time_series" {
		t.Fatalf("requestType = %q, want time_series", req.RequestType)
	}
}

// TestParseMetricsQueryArgs_RejectsUnknownRequestType pins the same rejection for query_metrics.
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
