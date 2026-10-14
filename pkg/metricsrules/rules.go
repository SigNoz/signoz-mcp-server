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

// ResolvedAggregation is the validated, defaults-applied result.
type ResolvedAggregation struct {
	TimeAggregation  string
	SpaceAggregation string
	ReduceTo         string   // populated when requestType=scalar
	Decisions        []string // human-readable explanation of each default applied
	Warnings         []string // non-fatal issues
}

// --- valid aggregation sets per metric type ---

var gaugeTimeAggs = newSet("latest", "sum", "avg", "min", "max", "count", "count_distinct")
var gaugeSpaceAggs = newSet("sum", "avg", "min", "max", "count")

var counterTimeAggs = newSet("rate", "increase")
var counterSpaceAggs = newSet("sum", "avg", "min", "max", "count")

var nonMonotonicSumTimeAggs = newSet("avg", "sum", "min", "max", "count", "count_distinct")
var nonMonotonicSumSpaceAggs = newSet("sum", "avg", "min", "max", "count")

var histogramSpaceAggs = newSet("p50", "p75", "p90", "p95", "p99")

// ApplyDefaults resolves timeAggregation, spaceAggregation, and (for scalar)
// reduceTo based on metric type metadata. It returns a ResolvedAggregation
// with all chosen values and a Decisions slice explaining every default.
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
	default:
		return r, fmt.Errorf("unknown metricType %q — valid values: gauge, sum, histogram, exponential_histogram", p.MetricType)
	}

	// Validate the resolved aggregations against the allowed sets.
	if err := validateResolved(metricType, p.IsMonotonic, &r); err != nil {
		return r, err
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
	// Histograms do not use timeAggregation — it is handled automatically.
	if p.TimeAggregation != "" {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"timeAggregation %q ignored for %s — histogram time aggregation is automatic", p.TimeAggregation, metricType))
	}
	r.TimeAggregation = ""
	r.Decisions = append(r.Decisions, fmt.Sprintf("timeAggregation: (empty) — automatic for %s", metricType))

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

// validateResolved checks the final resolved aggregations against the valid sets.
func validateResolved(metricType string, isMonotonic bool, r *ResolvedAggregation) error {
	switch metricType {
	case "gauge":
		if r.TimeAggregation != "" && !gaugeTimeAggs.has(r.TimeAggregation) {
			return invalidTimeAggError(r.TimeAggregation, metricType, gaugeTimeAggs)
		}
		if !gaugeSpaceAggs.has(r.SpaceAggregation) {
			return invalidSpaceAggError(r.SpaceAggregation, metricType, gaugeSpaceAggs)
		}
	case "sum":
		if isMonotonic {
			if r.TimeAggregation != "" && !counterTimeAggs.has(r.TimeAggregation) {
				return invalidTimeAggError(r.TimeAggregation, "monotonic counter", counterTimeAggs)
			}
			if !counterSpaceAggs.has(r.SpaceAggregation) {
				return invalidSpaceAggError(r.SpaceAggregation, "monotonic counter", counterSpaceAggs)
			}
		} else {
			if r.TimeAggregation != "" && !nonMonotonicSumTimeAggs.has(r.TimeAggregation) {
				return invalidTimeAggError(r.TimeAggregation, "non-monotonic sum", nonMonotonicSumTimeAggs)
			}
			if !nonMonotonicSumSpaceAggs.has(r.SpaceAggregation) {
				return invalidSpaceAggError(r.SpaceAggregation, "non-monotonic sum", nonMonotonicSumSpaceAggs)
			}
		}
	case "histogram", "exponential_histogram":
		if !histogramSpaceAggs.has(r.SpaceAggregation) {
			return fmt.Errorf(
				"spaceAggregation %q is not valid for %s.\nHistograms only support: %s",
				r.SpaceAggregation, metricType, histogramSpaceAggs.String())
		}
	}
	return nil
}

// --- error helpers ---

func invalidTimeAggError(value, metricType string, valid set) error {
	return fmt.Errorf(
		"timeAggregation %q is not valid for metric type %q.\nValid values: %s",
		value, metricType, valid.String())
}

func invalidSpaceAggError(value, metricType string, valid set) error {
	return fmt.Errorf(
		"spaceAggregation %q is not valid for metric type %q.\nValid values: %s",
		value, metricType, valid.String())
}

// --- simple string set ---

type set map[string]struct{}

func newSet(vals ...string) set {
	s := make(set, len(vals))
	for _, v := range vals {
		s[v] = struct{}{}
	}
	return s
}

func (s set) has(v string) bool {
	_, ok := s[v]
	return ok
}

func (s set) String() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}
