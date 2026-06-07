package tools

import (
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func builderPayload(requestType string, limit int) types.QueryPayload {
	return types.QueryPayload{
		RequestType: requestType,
		CompositeQuery: types.CompositeQuery{
			Queries: []types.Query{
				{Type: "builder_query", Spec: types.QuerySpec{Name: "A", Signal: "logs", Limit: limit}},
			},
		},
	}
}

func specLimit(t *testing.T, qp types.QueryPayload) int {
	t.Helper()
	spec, ok := qp.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
	if !ok {
		t.Fatalf("query 0 spec is %T, want types.QuerySpec", qp.CompositeQuery.Queries[0].Spec)
	}
	return spec.Limit
}

// TestClampBuilderQueryLimits_OverCapAllRequestTypes verifies the cap applies
// to every requestType — "raw"/"trace" (rows) and "scalar"/"time_series"
// (groups) — so neither the trace bypass nor manual aggregate group limits can
// request an unbounded response via execute_builder_query.
func TestClampBuilderQueryLimits_OverCapAllRequestTypes(t *testing.T) {
	for _, rt := range []string{"raw", "trace", "scalar", "time_series"} {
		qp := builderPayload(rt, 50000)
		if !clampBuilderQueryLimits(&qp) {
			t.Fatalf("%s: expected clamp to report true for over-cap limit", rt)
		}
		if got := specLimit(t, qp); got != MaxRawResultLimit {
			t.Fatalf("%s: limit=%d, want clamped to %d", rt, got, MaxRawResultLimit)
		}
	}
}

func TestClampBuilderQueryLimits_UnderCapAndUnset(t *testing.T) {
	under := builderPayload("raw", 500)
	if clampBuilderQueryLimits(&under) {
		t.Fatal("under-cap limit should not be clamped")
	}
	if got := specLimit(t, under); got != 500 {
		t.Fatalf("under-cap limit=%d, want 500 (unchanged)", got)
	}

	// A zero/unset limit is left for the backend to default — not clamped.
	unset := builderPayload("raw", 0)
	if clampBuilderQueryLimits(&unset) {
		t.Fatal("unset limit should not be clamped")
	}
	if got := specLimit(t, unset); got != 0 {
		t.Fatalf("unset limit=%d, want 0 (unchanged)", got)
	}
}

// TestClampBuilderQueryLimits_NonBuilderSpecSkipped verifies non-builder_query
// specs (which carry no row limit) are skipped via the type assertion rather
// than panicking.
func TestClampBuilderQueryLimits_NonBuilderSpecSkipped(t *testing.T) {
	qp := types.QueryPayload{
		RequestType: "time_series",
		CompositeQuery: types.CompositeQuery{
			Queries: []types.Query{
				{Type: "promql", Spec: types.PromQLSpec{Name: "A", Query: "up"}},
				{Type: "clickhouse_sql", Spec: types.ClickHouseSQLSpec{Name: "B", Query: "SELECT 1"}},
			},
		},
	}
	if clampBuilderQueryLimits(&qp) {
		t.Fatal("non-builder_query specs must be skipped (no clamp)")
	}
}

func TestParseAggregateArgs_LimitClamped(t *testing.T) {
	over, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"limit":       "50000",
		"timeRange":   "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap aggregate: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseAggregateArgs(map[string]any{
		"aggregation": "count",
		"limit":       "25",
		"timeRange":   "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if under.Limit != 25 || under.LimitClamped {
		t.Fatalf("under-cap aggregate: Limit=%d Clamped=%v, want 25 false", under.Limit, under.LimitClamped)
	}
}
