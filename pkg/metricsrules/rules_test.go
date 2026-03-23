package metricsrules

import (
	"strings"
	"testing"
)

func TestApplyDefaults_Gauge(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{MetricType: "gauge"}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "avg" {
		t.Errorf("expected timeAggregation=avg, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "sum" {
		t.Errorf("expected spaceAggregation=sum, got %q", r.SpaceAggregation)
	}
	if r.ReduceTo != "" {
		t.Errorf("expected empty reduceTo for time_series, got %q", r.ReduceTo)
	}
	if len(r.Decisions) == 0 {
		t.Error("expected non-empty decisions")
	}
}

func TestApplyDefaults_GaugeWithOverrides(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:       "gauge",
		TimeAggregation:  "max",
		SpaceAggregation: "avg",
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "max" {
		t.Errorf("expected timeAggregation=max, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "avg" {
		t.Errorf("expected spaceAggregation=avg, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_Counter(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:  "sum",
		IsMonotonic: true,
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "rate" {
		t.Errorf("expected timeAggregation=rate, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "sum" {
		t.Errorf("expected spaceAggregation=sum, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_CounterIncrease(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:      "sum",
		IsMonotonic:     true,
		TimeAggregation: "increase",
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "increase" {
		t.Errorf("expected timeAggregation=increase, got %q", r.TimeAggregation)
	}
}

func TestApplyDefaults_NonMonotonicSum(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:  "sum",
		IsMonotonic: false,
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "avg" {
		t.Errorf("expected timeAggregation=avg, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "sum" {
		t.Errorf("expected spaceAggregation=sum, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_Histogram(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{MetricType: "histogram"}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "" {
		t.Errorf("expected empty timeAggregation for histogram, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "p99" {
		t.Errorf("expected spaceAggregation=p99, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_HistogramP50(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:       "histogram",
		SpaceAggregation: "p50",
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.SpaceAggregation != "p50" {
		t.Errorf("expected spaceAggregation=p50, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_HistogramTimeAggWarning(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{
		MetricType:      "histogram",
		TimeAggregation: "avg",
	}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "" {
		t.Errorf("expected empty timeAggregation, got %q", r.TimeAggregation)
	}
	if len(r.Warnings) == 0 {
		t.Error("expected warning about ignored timeAggregation")
	}
}

func TestApplyDefaults_ExponentialHistogram(t *testing.T) {
	r, err := ApplyDefaults(MetricQueryParams{MetricType: "exponential_histogram"}, "time_series")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TimeAggregation != "" {
		t.Errorf("expected empty timeAggregation, got %q", r.TimeAggregation)
	}
	if r.SpaceAggregation != "p99" {
		t.Errorf("expected spaceAggregation=p99, got %q", r.SpaceAggregation)
	}
}

func TestApplyDefaults_ScalarReduceTo(t *testing.T) {
	tests := []struct {
		name        string
		params      MetricQueryParams
		wantReduce  string
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

func TestValidateAggregation_RateOnGauge(t *testing.T) {
	err := ValidateAggregation(MetricQueryParams{
		MetricType:      "gauge",
		TimeAggregation: "rate",
	})
	if err == nil {
		t.Error("expected error for rate on gauge")
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("expected descriptive error, got: %v", err)
	}
}

func TestValidateAggregation_IncreaseOnGauge(t *testing.T) {
	err := ValidateAggregation(MetricQueryParams{
		MetricType:      "gauge",
		TimeAggregation: "increase",
	})
	if err == nil {
		t.Error("expected error for increase on gauge")
	}
}

func TestValidateAggregation_SumSpaceOnHistogram(t *testing.T) {
	err := ValidateAggregation(MetricQueryParams{
		MetricType:       "histogram",
		SpaceAggregation: "sum",
	})
	if err == nil {
		t.Error("expected error for sum spaceAggregation on histogram")
	}
	if !strings.Contains(err.Error(), "percentile") {
		t.Errorf("expected error mentioning percentile, got: %v", err)
	}
}

func TestValidateAggregation_AvgOnCounter(t *testing.T) {
	err := ValidateAggregation(MetricQueryParams{
		MetricType:      "sum",
		IsMonotonic:     true,
		TimeAggregation: "avg",
	})
	if err == nil {
		t.Error("expected error for avg timeAggregation on monotonic counter")
	}
}

func TestValidateAggregation_ValidCombinations(t *testing.T) {
	tests := []struct {
		name   string
		params MetricQueryParams
	}{
		{"gauge avg/sum", MetricQueryParams{MetricType: "gauge", TimeAggregation: "avg", SpaceAggregation: "sum"}},
		{"gauge latest/max", MetricQueryParams{MetricType: "gauge", TimeAggregation: "latest", SpaceAggregation: "max"}},
		{"counter rate/sum", MetricQueryParams{MetricType: "sum", IsMonotonic: true, TimeAggregation: "rate", SpaceAggregation: "sum"}},
		{"counter increase/avg", MetricQueryParams{MetricType: "sum", IsMonotonic: true, TimeAggregation: "increase", SpaceAggregation: "avg"}},
		{"non-mono sum/sum", MetricQueryParams{MetricType: "sum", IsMonotonic: false, TimeAggregation: "sum", SpaceAggregation: "sum"}},
		{"histogram p99", MetricQueryParams{MetricType: "histogram", SpaceAggregation: "p99"}},
		{"histogram p50", MetricQueryParams{MetricType: "histogram", SpaceAggregation: "p50"}},
		{"exp histogram p95", MetricQueryParams{MetricType: "exponential_histogram", SpaceAggregation: "p95"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateAggregation(tt.params); err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
		})
	}
}

func TestValidateAggregation_InvalidReduceTo(t *testing.T) {
	err := ValidateAggregation(MetricQueryParams{
		MetricType: "gauge",
		ReduceTo:   "invalid",
	})
	if err == nil {
		t.Error("expected error for invalid reduceTo")
	}
}
