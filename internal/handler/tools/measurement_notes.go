package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

// Measurement notes say what a successful query measured when the request has
// a shape that commonly answers a different question than the caller asked,
// such as counting spans for a request count. Each one stays true when the
// caller meant the query as written, and names a fix only where the fix was
// checked against SigNoz. Every note is counted by tool and rule.
type measurementRule string

const (
	ruleTraceAllSpans        measurementRule = "trace_all_spans"
	rulePercentileOfSteps    measurementRule = "percentile_of_steps"
	ruleStepAlignment        measurementRule = "step_alignment"
	ruleRateSummed           measurementRule = "rate_summed"
	ruleCounterRunningTotal  measurementRule = "counter_running_total"
	ruleSpanMetricsAllSpans  measurementRule = "span_metrics_all_spans"
	ruleFormulaInputLimit    measurementRule = "formula_input_limit"
	spanMetricsCallsMetric                   = "signoz_calls_total"
	spanMetricsLatencyPrefix                 = "signoz_latency"
)

type measurementNote struct {
	rule measurementRule
	text string
}

// recordMeasurementNotes counts each note and returns their texts in order.
func (h *Handler) recordMeasurementNotes(ctx context.Context, toolName string, notes []measurementNote) []string {
	texts := make([]string, 0, len(notes))
	for _, note := range notes {
		texts = append(texts, note.text)
		h.logger.DebugContext(ctx, "measurement note added",
			slog.String("gen_ai.tool.name", toolName),
			slog.String("mcp.result_note.rule", string(note.rule)))
		if h.meters != nil {
			attrs := []attribute.KeyValue{
				attribute.String("gen_ai.tool.name", toolName),
				otelpkg.MCPResultNoteRuleKey.String(string(note.rule)),
			}
			attrs = otelpkg.AppendClientSource(ctx, attrs)
			h.meters.ToolResultMeasurementNotes.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
	}
	return texts
}

// --- traces ---

var (
	// One service.name (optionally resource.-prefixed) with = or a one-value IN.
	singleServicePattern = regexp.MustCompile(`(?i)(?:resource\.)?service\.name\s*(?:=\s*'((?:[^'\\]|\\.)*)'|IN\s*\(\s*'((?:[^'\\]|\\.)*)'\s*\))`)
	serviceKeyPattern    = regexp.MustCompile(`(?i)\bservice\.name\b`)
	// A predicate on the span name narrows to chosen operations; service.name
	// and other dotted keys ending in name are excluded by the leading boundary.
	spanNamePredicatePattern = regexp.MustCompile(`(?i)(?:^|[\s(])(?:span\.)?name\s*(?:=|!=|<>|\bIN\b|\bNOT\b|\bLIKE\b|\bILIKE\b|\bCONTAINS\b|\bREGEXP\b|\bEXISTS\b)`)
	spanKindPattern          = regexp.MustCompile(`(?i)(?:^|[\s(])(?:span\.)?kind(?:_string)?\s*(?:=|!=|<>|\bIN\b|\bNOT\b)`)
	requestShapedAggregation = regexp.MustCompile(`(?i)^\s*(?:count\(\s*\)|rate\(\s*\)|(?:p\d{2}|avg|median)\(\s*duration_nano\s*\))\s*$`)
)

// filterService returns the one service a filter is scoped to, or "" when it
// names none or more than one.
func filterService(filter string) string {
	if len(serviceKeyPattern.FindAllStringIndex(filter, -1)) != 1 {
		return ""
	}
	match := singleServicePattern.FindStringSubmatch(filter)
	if match == nil {
		return ""
	}
	value := match[1]
	if value == "" {
		value = match[2]
	}
	return strings.ReplaceAll(value, `\'`, `'`)
}

// traceAllSpansNote fires on an ungrouped count, rate, or latency over every
// span of one service. SigNoz counts each matching span, so a request that
// records several spans counts several times; signoz_list_services scopes the
// same question to entry spans.
func traceAllSpansNote(filter string, groupBy int, aggregations []string, queryType string) (measurementNote, bool) {
	if groupBy > 0 || queryType == querybuilder.QueryTypeBuilderAI {
		return measurementNote{}, false
	}
	service := filterService(filter)
	if service == "" {
		return measurementNote{}, false
	}
	lower := strings.ToLower(filter)
	for _, scoped := range []string{"isroot", "isentrypoint", "parent_span_id", "trace_id", "span_id"} {
		if strings.Contains(lower, scoped) {
			return measurementNote{}, false
		}
	}
	if spanNamePredicatePattern.MatchString(filter) {
		return measurementNote{}, false
	}
	// A kind other than server is a deliberate choice of spans, such as
	// outgoing client calls; server spans still include nested ones.
	if spanKindPattern.MatchString(filter) && !strings.Contains(lower, "server") {
		return measurementNote{}, false
	}
	matched := false
	for _, expression := range aggregations {
		if requestShapedAggregation.MatchString(expression) {
			matched = true
			break
		}
	}
	if !matched {
		return measurementNote{}, false
	}
	return measurementNote{rule: ruleTraceAllSpans, text: fmt.Sprintf(
		"note: this aggregates every span of service %q that matches the filter, not its requests; one request can record several spans, including a server span nested inside another. "+
			"For the service's requests (request count, rate, or latency), add isEntryPoint = true OR isRoot = true to the filter, as signoz_list_services does.",
		service)}, true
}

// --- metrics ---

// metricMeasurement is what the metric rules read from one resolved query.
type metricMeasurement struct {
	MetricName       string
	MetricType       string // "" when unknown, as in signoz_execute_builder_query
	IsMonotonic      bool
	Temporality      string
	TimeAggregation  string
	SpaceAggregation string
	ReduceTo         string
	Filter           string
	GroupBy          []string
	RequestType      string
	StepSeconds      int64
	CallerStep       bool // the caller set stepInterval
	StartMs, EndMs   int64
}

var percentilePattern = regexp.MustCompile(`^p(\d{2})$`)

// metricMeasurementNotes runs every metric rule over one query that is not a
// formula input. Formula results combine their inputs, so a ratio of two
// summed rates is still a ratio; callers skip queries a formula references.
func metricMeasurementNotes(m metricMeasurement) []measurementNote {
	var notes []measurementNote
	if note, ok := percentileOfStepsNote(m); ok {
		notes = append(notes, note)
	}
	if note, ok := stepAlignmentNote(m); ok {
		notes = append(notes, note)
	}
	if note, ok := rateSummedNote(m); ok {
		notes = append(notes, note)
	}
	if note, ok := counterRunningTotalNote(m); ok {
		notes = append(notes, note)
	}
	if note, ok := spanMetricsAllSpansNote(m); ok {
		notes = append(notes, note)
	}
	return notes
}

func stepPhrase(step int64) string {
	if step > 0 {
		return fmt.Sprintf("%ds step", step)
	}
	return "step"
}

// percentileOfStepsNote fires when a scalar request reduces per-step
// percentiles with avg, median, or sum. SigNoz computes the percentile within
// each step and then reduces the series, so the result is not the percentile
// of the window. Each bucket's count over the window, read with increase and
// summed by le, gives it by interpolation; for a cumulative histogram, PromQL
// over increase() for the whole window returns it directly.
func percentileOfStepsNote(m metricMeasurement) (measurementNote, bool) {
	match := percentilePattern.FindStringSubmatch(strings.ToLower(m.SpaceAggregation))
	if m.RequestType != "scalar" || match == nil {
		return measurementNote{}, false
	}
	reduce := strings.ToLower(m.ReduceTo)
	if reduce != "avg" && reduce != "median" && reduce != "sum" {
		return measurementNote{}, false
	}
	windowMs := m.EndMs - m.StartMs
	if stepMs := m.StepSeconds * 1000; stepMs > 0 && windowMs > 0 && m.StartMs%stepMs == 0 && windowMs <= stepMs {
		// One step covers exactly the window, so its percentile is the window's.
		return measurementNote{}, false
	}
	percentile := "p" + match[1]
	text := fmt.Sprintf(
		"note: reduceTo %s combined the %s of each %s, so this is not the %s over the whole window. "+
			"For the window's %s, query %s again with timeAggregation increase, spaceAggregation sum, reduceTo sum, and groupBy le, which returns each bucket's count over the window, and interpolate within the first bucket whose count reaches %s%% of the total.",
		reduce, percentile, stepPhrase(m.StepSeconds), percentile, percentile, m.MetricName, match[1])
	if windowMs > 0 && !strings.EqualFold(m.Temporality, "delta") {
		promql := windowPercentilePromQL(match[1], m.MetricName, m.Filter, (windowMs+999)/1000)
		if strings.EqualFold(m.Temporality, "cumulative") {
			text += fmt.Sprintf(" For this cumulative histogram, this PromQL through signoz_execute_builder_query with requestType scalar also returns it: %s", promql)
		} else {
			text += fmt.Sprintf(" If this histogram is cumulative, this PromQL through signoz_execute_builder_query with requestType scalar also returns it (SigNoz's PromQL returns no data for a delta histogram): %s", promql)
		}
	}
	return measurementNote{rule: rulePercentileOfSteps, text: text}, true
}

// stepAlignmentNote fires when the caller sets a scalar step at least as long
// as the window and the window does not start on a multiple of it. SigNoz
// starts steps at multiples of the step since the Unix epoch, so the window
// spans two steps: the first reaches back before the window, except that a
// cumulative histogram percentile has no earlier sample to take a rate from
// and leaves that first step out.
func stepAlignmentNote(m metricMeasurement) (measurementNote, bool) {
	stepMs, windowMs := m.StepSeconds*1000, m.EndMs-m.StartMs
	if m.RequestType != "scalar" || !m.CallerStep || stepMs <= 0 || windowMs <= 0 || stepMs < windowMs || m.StartMs%stepMs == 0 {
		return measurementNote{}, false
	}
	first := m.StartMs - m.StartMs%stepMs
	firstText := time.UnixMilli(first).UTC().Format(time.RFC3339)
	boundary := time.UnixMilli(first + stepMs).UTC().Format(time.RFC3339)
	percentile := percentilePattern.MatchString(strings.ToLower(m.SpaceAggregation))
	var effect string
	switch {
	case percentile && strings.EqualFold(m.Temporality, "cumulative"):
		effect = fmt.Sprintf("for this cumulative histogram the result starts at %s and leaves out the window before it", boundary)
	case strings.EqualFold(m.Temporality, "delta") || (!percentile && m.MetricType != ""):
		effect = fmt.Sprintf("the first step starts at %s, before the window, and counts data from before it", firstText)
	default:
		effect = fmt.Sprintf("the first step starts at %s, before the window, and counts data from before it, or, for a cumulative histogram percentile, the result starts at %s and leaves out the window before it", firstText, boundary)
	}
	return measurementNote{rule: ruleStepAlignment, text: fmt.Sprintf(
		"note: SigNoz starts steps at multiples of %ds since the Unix epoch, so this %ds step does not cover the window as one step: %s. For one value over the window, leave stepInterval unset and choose reduceTo for the question.",
		m.StepSeconds, m.StepSeconds, effect)}, true
}

var simpleEquality = regexp.MustCompile(`^\s*([A-Za-z_][\w.]*)\s*=\s*'((?:[^'\\]|\\.)*)'\s*$`)
var andSeparator = regexp.MustCompile(`(?i)\s+AND\s+`)

// windowPercentilePromQL renders the whole-window percentile query. A filter
// made only of key = 'value' conditions becomes label matchers; any other
// filter is left for the caller to translate.
func windowPercentilePromQL(digits, metricName, filter string, windowSeconds int64) string {
	quantile := "0." + digits
	selector := []string{strconv.Quote(metricName)}
	translated := true
	if trimmed := strings.TrimSpace(filter); trimmed != "" {
		if strings.ContainsAny(trimmed, "()") {
			translated = false
		} else {
			for _, part := range andSeparator.Split(trimmed, -1) {
				match := simpleEquality.FindStringSubmatch(part)
				if match == nil {
					translated = false
					break
				}
				selector = append(selector, strconv.Quote(match[1])+"="+strconv.Quote(strings.ReplaceAll(match[2], `\'`, `'`)))
			}
		}
	}
	if !translated {
		selector = []string{strconv.Quote(metricName)}
	}
	query := fmt.Sprintf("histogram_quantile(%s, sum by (le) (increase({%s}[%ds])))", quantile, strings.Join(selector, ", "), windowSeconds)
	if !translated {
		query += ", adding your filter's conditions as label matchers"
	}
	return query
}

// rateSummedNote fires when a scalar request sums per-step rates. Each step
// holds a per-second rate, so their sum depends on the step count and is
// neither a count nor a rate.
func rateSummedNote(m metricMeasurement) (measurementNote, bool) {
	if m.RequestType != "scalar" || !strings.EqualFold(m.TimeAggregation, "rate") || !strings.EqualFold(m.ReduceTo, "sum") {
		return measurementNote{}, false
	}
	return measurementNote{rule: ruleRateSummed, text: fmt.Sprintf(
		"note: reduceTo sum added up the per-second rate of each %s, which is neither a count nor a rate. "+
			"For the total count in the window, use timeAggregation increase with reduceTo sum; for the average per-second rate, use reduceTo avg.",
		stepPhrase(m.StepSeconds))}, true
}

// counterRunningTotalNote fires when a cumulative counter is read with a time
// aggregation other than rate or increase. A cumulative counter's value is its
// running total since it started, and count counts its samples.
func counterRunningTotalNote(m metricMeasurement) (measurementNote, bool) {
	if m.MetricType != "sum" || !m.IsMonotonic || !strings.EqualFold(m.Temporality, "cumulative") {
		return measurementNote{}, false
	}
	timeAggregation := strings.ToLower(m.TimeAggregation)
	var reads string
	switch timeAggregation {
	case "latest", "max", "min", "avg":
		reads = "reads its running total since the counter started, not what happened in this window"
	case "sum":
		reads = "adds up samples of its running total, not what happened in this window"
	case "count", "count_distinct":
		reads = "counts the counter's samples, not the events it counts"
	default:
		return measurementNote{}, false
	}
	return measurementNote{rule: ruleCounterRunningTotal, text: fmt.Sprintf(
		"note: %s is a cumulative counter, so timeAggregation %s %s. For the count in the window, use timeAggregation increase; for per second, use rate.",
		m.MetricName, timeAggregation, reads)}, true
}

// spanMetricsAllSpansNote fires on SigNoz's span metrics for one service with
// no span-kind or operation narrowing. The collector records one call per
// span, so a request with several spans counts several times.
func spanMetricsAllSpansNote(m metricMeasurement) (measurementNote, bool) {
	calls := m.MetricName == spanMetricsCallsMetric
	latency := strings.HasPrefix(m.MetricName, spanMetricsLatencyPrefix)
	if !calls && !latency {
		return measurementNote{}, false
	}
	service := filterService(m.Filter)
	if service == "" {
		return measurementNote{}, false
	}
	lower := strings.ToLower(m.Filter)
	if strings.Contains(lower, "span.kind") || strings.Contains(lower, "operation") {
		return measurementNote{}, false
	}
	for _, key := range m.GroupBy {
		if key = strings.ToLower(strings.TrimSpace(key)); key == "operation" || key == "span.kind" {
			return measurementNote{}, false
		}
	}
	if calls {
		return measurementNote{rule: ruleSpanMetricsAllSpans, text: fmt.Sprintf(
			"note: %s counts every span of service %q, one per span, so one request that records several spans counts several times. "+
				"For the service's requests, use signoz_list_services (numCalls, callRate), or signoz_aggregate_traces with isEntryPoint = true OR isRoot = true.",
			m.MetricName, service)}, true
	}
	return measurementNote{rule: ruleSpanMetricsAllSpans, text: fmt.Sprintf(
		"note: %s records the duration of every span of service %q, including spans inside each request. "+
			"For the service's request latency, use signoz_aggregate_traces with p95(duration_nano) and isEntryPoint = true OR isRoot = true.",
		m.MetricName, service)}, true
}

// --- signoz_execute_builder_query ---

// builderQueryMeasurementNotes runs the trace and metric rules over each
// enabled builder query of a query_range request, plus the formula-input
// limit rule. Metric types are unknown here, so the counter rule does not run.
func builderQueryMeasurementNotes(request map[string]any, response json.RawMessage) []measurementNote {
	var notes []measurementNote
	for _, input := range querybuilder.LimitedFormulaInputs(request) {
		notes = append(notes, measurementNote{rule: ruleFormulaInputLimit, text: fmt.Sprintf(
			"note: query %q is a formula input with limit %d, which SigNoz applies before the formula runs, so the formula combined only its top %d series. Remove limit from formula inputs to combine every series.",
			input.QueryName, input.Limit, input.Limit)})
	}

	requestType, _ := request["requestType"].(string)
	startMs, endMs := int64(numberValue(request["start"])), int64(numberValue(request["end"]))
	steps := responseStepIntervals(response)
	composite, _ := request["compositeQuery"].(map[string]any)
	queries, _ := composite["queries"].([]any)
	formulaInputs := querybuilder.FormulaInputNames(request)
	for _, raw := range queries {
		envelope, _ := raw.(map[string]any)
		if envelope["type"] != "builder_query" {
			continue
		}
		spec, _ := envelope["spec"].(map[string]any)
		if spec == nil || spec["disabled"] == true {
			continue
		}
		name, _ := spec["name"].(string)
		if formulaInputs[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		filter := ""
		if filterObject, ok := spec["filter"].(map[string]any); ok {
			filter, _ = filterObject["expression"].(string)
		}
		groupBy := specGroupByNames(spec["groupBy"])
		aggregations, _ := spec["aggregations"].([]any)
		switch spec["signal"] {
		case "traces":
			var expressions []string
			for _, item := range aggregations {
				if object, ok := item.(map[string]any); ok {
					if expression, ok := object["expression"].(string); ok {
						expressions = append(expressions, expression)
					}
				}
			}
			if note, ok := traceAllSpansNote(filter, len(groupBy), expressions, ""); ok {
				notes = append(notes, note)
			}
		case "metrics":
			if len(aggregations) == 0 {
				continue
			}
			aggregation, _ := aggregations[0].(map[string]any)
			metricName, _ := aggregation["metricName"].(string)
			timeAggregation, _ := aggregation["timeAggregation"].(string)
			spaceAggregation, _ := aggregation["spaceAggregation"].(string)
			reduceTo, _ := aggregation["reduceTo"].(string)
			temporality, _ := aggregation["temporality"].(string)
			notes = append(notes, metricMeasurementNotes(metricMeasurement{
				MetricName:       metricName,
				Temporality:      temporality,
				TimeAggregation:  timeAggregation,
				SpaceAggregation: spaceAggregation,
				ReduceTo:         reduceTo,
				Filter:           filter,
				GroupBy:          groupBy,
				RequestType:      requestType,
				StepSeconds:      steps[name],
				CallerStep:       spec["stepInterval"] != nil,
				StartMs:          startMs,
				EndMs:            endMs,
			})...)
		}
	}
	return notes
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func specGroupByNames(raw any) []string {
	items, _ := raw.([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			if name, ok := object["name"].(string); ok && name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// responseStepIntervals reads data.meta.stepIntervals, keyed by query name.
func responseStepIntervals(response json.RawMessage) map[string]int64 {
	var body struct {
		Data struct {
			Meta struct {
				StepIntervals map[string]int64 `json:"stepIntervals"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &body); err != nil {
		return nil
	}
	return body.Data.Meta.StepIntervals
}
