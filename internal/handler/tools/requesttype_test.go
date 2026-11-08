package tools

import (
	"strings"
	"testing"
)

func TestAggregateTools_RejectUnknownRequestTypeAsValidationFailed(t *testing.T) {
	server := newToolServerWithoutSigNoz(t)
	for tool, arguments := range map[string]string{
		"signoz_aggregate_logs": `{"aggregations":"count()","timeRange":"1h","requestType":"totally-bogus"}`,
		"signoz_query_metrics":  `{"metricName":"system.cpu.time","timeRange":"1h","requestType":"totally-bogus"}`,
	} {
		t.Run(tool, func(t *testing.T) {
			result := callRegisteredTool(t, server, tool, arguments)
			if !result.IsError {
				t.Fatalf("unknown requestType must be an error result, got %+v", result.Content)
			}
			if got := resultCode(t, result); got != CodeValidationFailed {
				t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
			}
		})
	}
}

// TestParseAggregateArgs_RejectsUnknownRequestType pins that the aggregate tools
// reject an unknown requestType at the arg layer instead of passing it through.
func TestParseAggregateArgs_RejectsUnknownRequestType(t *testing.T) {
	_, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"requestType":  "raw",
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
	req, err := parseAggregateArgs(map[string]any{"aggregations": "count()"}, "logs", "")
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
	req, err := parseAggregateArgs(map[string]any{"aggregations": "count()", "requestType": "time_series"}, "traces", "")
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
