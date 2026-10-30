package tools

import (
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

func TestParseAggregateArgs_LimitClamped(t *testing.T) {
	over, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"limit":        "50000",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap aggregate: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"limit":        "25",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if under.Limit != 25 || under.LimitClamped {
		t.Fatalf("under-cap aggregate: Limit=%d Clamped=%v, want 25 false", under.Limit, under.LimitClamped)
	}
}

func TestParseAggregateArgs_DefaultLimit(t *testing.T) {
	req, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"timeRange":    "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Limit != querybuilder.DefaultAggregateQueryLimit {
		t.Fatalf("default limit = %d, want %d", req.Limit, querybuilder.DefaultAggregateQueryLimit)
	}
	if req.OrderExpr != "count()" || req.OrderDir != "desc" {
		t.Fatalf("default order = %q %q, want count() desc", req.OrderExpr, req.OrderDir)
	}
}

// TestParseAggregateArgs_NonStringRequestTypeErrors pins that a
// present-but-non-string requestType is rejected loudly at the parse layer
// instead of the failed type assertion coercing it to "" and silently
// defaulting to "scalar".
func TestParseAggregateArgs_NonStringRequestTypeErrors(t *testing.T) {
	for _, bad := range []any{true, 123, float64(1)} {
		if _, err := parseAggregateArgs(map[string]any{
			"aggregations": "count()",
			"timeRange":    "1h",
			"requestType":  bad,
		}, "logs", ""); err == nil {
			t.Fatalf("parseAggregateArgs with requestType=%v (%T) = nil error, want validation error", bad, bad)
		}
	}
	// A valid string requestType still parses.
	if _, err := parseAggregateArgs(map[string]any{
		"aggregations": "count()",
		"timeRange":    "1h",
		"requestType":  "time_series",
	}, "logs", ""); err != nil {
		t.Fatalf("parseAggregateArgs with valid requestType: unexpected error: %v", err)
	}
}

// TestParseMetricsQueryArgs_NonStringRequestTypeErrors pins the same
// loud-rejection contract for the metrics arg parser (stringArg previously
// dropped a non-string value to "").
func TestParseMetricsQueryArgs_NonStringRequestTypeErrors(t *testing.T) {
	if _, err := parseMetricsQueryArgs(map[string]any{
		"metricName":  "system.cpu.time",
		"requestType": 123,
	}); err == nil {
		t.Fatal("parseMetricsQueryArgs with requestType=123 = nil error, want validation error")
	}
}
