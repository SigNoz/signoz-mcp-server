package metricsrules

import (
	"testing"
)

func TestApplyDefaults_TimeSeriesAggregationByMetricType(t *testing.T) {
	tests := []struct {
		name          string
		params        MetricQueryParams
		wantTime      string
		wantSpace     string
		wantDecisions bool
		wantWarnings  bool
	}{
		{name: "gauge defaults", params: MetricQueryParams{MetricType: "gauge"}, wantTime: "avg", wantSpace: "sum", wantDecisions: true},
		{name: "gauge overrides", params: MetricQueryParams{MetricType: "gauge", TimeAggregation: "max", SpaceAggregation: "avg"}, wantTime: "max", wantSpace: "avg"},
		{name: "counter defaults", params: MetricQueryParams{MetricType: "sum", IsMonotonic: true}, wantTime: "rate", wantSpace: "sum"},
		{name: "counter increase", params: MetricQueryParams{MetricType: "sum", IsMonotonic: true, TimeAggregation: "increase"}, wantTime: "increase", wantSpace: "sum"},
		{name: "non-monotonic sum defaults", params: MetricQueryParams{MetricType: "sum", IsMonotonic: false}, wantTime: "avg", wantSpace: "sum"},
		{name: "histogram defaults", params: MetricQueryParams{MetricType: "histogram"}, wantTime: "", wantSpace: "p99"},
		{name: "histogram p50", params: MetricQueryParams{MetricType: "histogram", SpaceAggregation: "p50"}, wantTime: "", wantSpace: "p50"},
		{name: "histogram ignores timeAggregation with a warning", params: MetricQueryParams{MetricType: "histogram", TimeAggregation: "avg"}, wantTime: "", wantSpace: "p99", wantWarnings: true},
		{name: "exponential histogram defaults", params: MetricQueryParams{MetricType: "exponential_histogram"}, wantTime: "", wantSpace: "p99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ApplyDefaults(tt.params, "time_series")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.TimeAggregation != tt.wantTime {
				t.Errorf("timeAggregation = %q, want %q", r.TimeAggregation, tt.wantTime)
			}
			if r.SpaceAggregation != tt.wantSpace {
				t.Errorf("spaceAggregation = %q, want %q", r.SpaceAggregation, tt.wantSpace)
			}
			if r.ReduceTo != "" {
				t.Errorf("reduceTo = %q, want empty for time_series", r.ReduceTo)
			}
			if tt.wantDecisions && len(r.Decisions) == 0 {
				t.Error("expected non-empty decisions")
			}
			if tt.wantWarnings && len(r.Warnings) == 0 {
				t.Error("expected a warning about the ignored timeAggregation")
			}
		})
	}
}

func TestApplyDefaults_ScalarReduceTo(t *testing.T) {
	tests := []struct {
		name       string
		params     MetricQueryParams
		wantReduce string
	}{
		{"gauge scalar", MetricQueryParams{MetricType: "gauge"}, "avg"},
		{"counter scalar", MetricQueryParams{MetricType: "sum", IsMonotonic: true}, "sum"},
		{"non-monotonic scalar", MetricQueryParams{MetricType: "sum", IsMonotonic: false}, "avg"},
		{"histogram scalar", MetricQueryParams{MetricType: "histogram"}, "avg"},
		{"explicit reduceTo", MetricQueryParams{MetricType: "gauge", ReduceTo: "max"}, "max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ApplyDefaults(tt.params, "scalar")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.ReduceTo != tt.wantReduce {
				t.Errorf("expected reduceTo=%q, got %q", tt.wantReduce, r.ReduceTo)
			}
		})
	}
}

func TestApplyDefaults_UnknownType(t *testing.T) {
	_, err := ApplyDefaults(MetricQueryParams{MetricType: "unknown"}, "time_series")
	if err == nil {
		t.Error("expected error for unknown metricType")
	}
}
