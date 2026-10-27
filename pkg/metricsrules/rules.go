package metricsrules

import (
	"fmt"
	"strings"
)

// MetricQueryParams carries the caller-supplied (or auto-fetched) metadata
// plus optional aggregation overrides for a single metric query.
type MetricQueryParams struct {
	MetricType       string // gauge, sum, histogram, exponential_histogram
	IsMonotonic      bool
	Temporality      string // cumulative, delta, unspecified
	TimeAggregation  string // user-provided or "" for auto-default
	SpaceAggregation string // user-provided or "" for auto-default
	ReduceTo         string // user-provided or "" for auto-default (scalar only)
}

// ResolvedAggregation is the defaults-applied result. SigNoz validates the
// combination; this package only fills what the caller left empty.
type ResolvedAggregation struct {
	TimeAggregation  string
	SpaceAggregation string
	ReduceTo         string   // populated when requestType=scalar
	Decisions        []string // human-readable explanation of each default applied
	Warnings         []string // non-fatal issues
}

// ApplyDefaults fills timeAggregation, spaceAggregation, and (for scalar)
// reduceTo from the metric type when the caller left them empty. It never
// rejects a caller-chosen combination: SigNoz validates it, and accepts more
// than a per-type table would (for example rate with sum on histogram buckets).
func ApplyDefaults(p MetricQueryParams, requestType string) (ResolvedAggregation, error) {
	r := ResolvedAggregation{}

	metricType := strings.ToLower(p.MetricType)

	switch metricType {
	case "gauge":
		applyGaugeDefaults(p, &r)
	case "sum":
		if p.IsMonotonic {
			applyCounterDefaults(p, &r)
		} else {
			applyNonMonotonicSumDefaults(p, &r)
		}
	case "histogram", "exponential_histogram":
		applyHistogramDefaults(p, metricType, &r)
	case "summary":
		if p.TimeAggregation == "" || p.SpaceAggregation == "" {
			return r, fmt.Errorf("summary metrics have no default aggregation; pass timeAggregation and spaceAggregation, for example timeAggregation avg and spaceAggregation max on the .quantile series")
		}
		r.TimeAggregation, r.SpaceAggregation = p.TimeAggregation, p.SpaceAggregation
		r.Decisions = append(r.Decisions,
			fmt.Sprintf("timeAggregation: %s (caller-provided)", p.TimeAggregation),
			fmt.Sprintf("spaceAggregation: %s (caller-provided)", p.SpaceAggregation))
	default:
		return r, fmt.Errorf("unknown metricType %q — valid values: gauge, sum, histogram, exponential_histogram, summary", p.MetricType)
	}

	// reduceTo for scalar
	if requestType == "scalar" {
		applyReduceToDefaults(metricType, p.IsMonotonic, p.ReduceTo, &r)
	}

	return r, nil
}

// --- per-type default application ---

func applyGaugeDefaults(p MetricQueryParams, r *ResolvedAggregation) {
	if p.TimeAggregation != "" {
		r.TimeAggregation = p.TimeAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: %s (caller-provided)", p.TimeAggregation))
	} else {
		r.TimeAggregation = "avg"
		r.Decisions = append(r.Decisions, "timeAggregation: avg (default for gauge — averages samples within each time bucket)")
	}

	if p.SpaceAggregation != "" {
		r.SpaceAggregation = p.SpaceAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("spaceAggregation: %s (caller-provided)", p.SpaceAggregation))
	} else {
		r.SpaceAggregation = "sum"
		r.Decisions = append(r.Decisions, "spaceAggregation: sum (default for gauge — sums across series/dimensions)")
	}
}

func applyCounterDefaults(p MetricQueryParams, r *ResolvedAggregation) {
	if p.TimeAggregation != "" {
		r.TimeAggregation = p.TimeAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: %s (caller-provided)", p.TimeAggregation))
	} else {
		r.TimeAggregation = "rate"
		r.Decisions = append(r.Decisions, "timeAggregation: rate (default for monotonic counter — per-second rate of change)")
	}

	if p.SpaceAggregation != "" {
		r.SpaceAggregation = p.SpaceAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("spaceAggregation: %s (caller-provided)", p.SpaceAggregation))
	} else {
		r.SpaceAggregation = "sum"
		r.Decisions = append(r.Decisions, "spaceAggregation: sum (default for counter — sums rates across series)")
	}
}

func applyNonMonotonicSumDefaults(p MetricQueryParams, r *ResolvedAggregation) {
	if p.TimeAggregation != "" {
		r.TimeAggregation = p.TimeAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: %s (caller-provided)", p.TimeAggregation))
	} else {
		r.TimeAggregation = "avg"
		r.Decisions = append(r.Decisions, "timeAggregation: avg (default for non-monotonic sum)")
	}

	if p.SpaceAggregation != "" {
		r.SpaceAggregation = p.SpaceAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("spaceAggregation: %s (caller-provided)", p.SpaceAggregation))
	} else {
		r.SpaceAggregation = "sum"
		r.Decisions = append(r.Decisions, "spaceAggregation: sum (default for non-monotonic sum)")
	}
}

func applyHistogramDefaults(p MetricQueryParams, metricType string, r *ResolvedAggregation) {
	// Percentiles need no time aggregation; a caller who reads the buckets
	// (for example rate with sum, grouped by le) sets one.
	if p.TimeAggregation != "" {
		r.TimeAggregation = p.TimeAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: %s (caller-provided)", p.TimeAggregation))
	} else {
		r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: (empty) — automatic for %s percentiles", metricType))
	}

	if p.SpaceAggregation != "" {
		r.SpaceAggregation = p.SpaceAggregation
		r.Decisions = append(r.Decisions, fmt.Sprintf("spaceAggregation: %s (caller-provided)", p.SpaceAggregation))
	} else {
		r.SpaceAggregation = "p99"
		r.Decisions = append(r.Decisions, fmt.Sprintf("spaceAggregation: p99 (default for %s — 99th percentile)", metricType))
	}
}

func applyReduceToDefaults(metricType string, isMonotonic bool, userReduceTo string, r *ResolvedAggregation) {
	if userReduceTo != "" {
		r.ReduceTo = userReduceTo
		r.Decisions = append(r.Decisions, fmt.Sprintf("reduceTo: %s (caller-provided)", userReduceTo))
		return
	}

	switch metricType {
	case "gauge":
		r.ReduceTo = "avg"
		r.Decisions = append(r.Decisions, "reduceTo: avg (default for gauge scalar)")
	case "sum":
		if isMonotonic {
			r.ReduceTo = "sum"
			r.Decisions = append(r.Decisions, "reduceTo: sum (default for counter scalar)")
		} else {
			r.ReduceTo = "avg"
			r.Decisions = append(r.Decisions, "reduceTo: avg (default for non-monotonic sum scalar)")
		}
	case "histogram", "exponential_histogram":
		r.ReduceTo = "avg"
		r.Decisions = append(r.Decisions, "reduceTo: avg (default for histogram scalar)")
	}
}
