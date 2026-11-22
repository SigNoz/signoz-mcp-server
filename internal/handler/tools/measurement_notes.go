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
	spanMetricsLatencyBucket                 = "signoz_latency.bucket"
	spanMetricsLatencyCount                  = "signoz_latency.count"
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

var requestShapedAggregation = regexp.MustCompile(`(?i)^\s*(?:count\(\s*\)|rate\(\s*\)|(?:p\d{2}|avg|median)\(\s*duration_nano\s*\))\s*$`)

// filterToken is one token of a filter expression: a quoted literal, holding
// its unquoted value; a run of comparison characters such as = or !=; one of
// ( ) and ,; or any other run of non-space characters, which is a key or a
// keyword such as AND or IN. Matching keys on tokens keeps a longer key such
// as k8s.service.name, and text inside a literal, from reading as a key.
type filterToken struct {
	text    string
	literal bool
}

// is reports whether the token is a key or keyword equal to one of words,
// ignoring case. A literal is never a key.
func (t filterToken) is(words ...string) bool {
	if t.literal {
		return false
	}
	for _, word := range words {
		if strings.EqualFold(t.text, word) {
			return true
		}
	}
	return false
}

const (
	filterSpace      = " \t\r\n"
	filterComparison = "=!<>"
	filterBreaks     = filterSpace + filterComparison + `'"(),`
)

func tokenizeFilter(filter string) []filterToken {
	var tokens []filterToken
	for i := 0; i < len(filter); {
		switch c := filter[i]; {
		case strings.IndexByte(filterSpace, c) >= 0:
			i++
		case c == '\'' || c == '"':
			var value strings.Builder
			i++
			for ; i < len(filter) && filter[i] != c; i++ {
				if filter[i] == '\\' && i+1 < len(filter) {
					i++
				}
				value.WriteByte(filter[i])
			}
			tokens = append(tokens, filterToken{text: value.String(), literal: true})
			i++ // the closing quote
		case c == '(' || c == ')' || c == ',':
			tokens = append(tokens, filterToken{text: string(c)})
			i++
		case strings.IndexByte(filterComparison, c) >= 0:
			j := i + 1
			for j < len(filter) && strings.IndexByte(filterComparison, filter[j]) >= 0 {
				j++
			}
			tokens = append(tokens, filterToken{text: filter[i:j]})
			i = j
		default:
			j := i + 1
			for j < len(filter) && strings.IndexByte(filterBreaks, filter[j]) < 0 {
				j++
			}
			tokens = append(tokens, filterToken{text: filter[i:j]})
			i = j
		}
	}
	return tokens
}

// comparedValue reads the comparison that starts the tokens after a key: = with
// a literal, or IN with a one-literal list. It returns the literal and the number
// of tokens the comparison spans, or ok false for any other comparison.
func comparedValue(tokens []filterToken) (value string, length int, ok bool) {
	switch {
	case len(tokens) >= 2 && tokens[0].is("=") && tokens[1].literal:
		return tokens[1].text, 2, true
	case len(tokens) >= 4 && tokens[0].is("IN") && tokens[1].is("(") && tokens[2].literal && tokens[3].is(")"):
		return tokens[2].text, 4, true
	}
	return "", 0, false
}

func isServiceKey(t filterToken) bool { return t.is("service.name", "resource.service.name") }

// filterService returns the one service a filter is scoped to: the value its
// only service.name or resource.service.name key is compared with, by = or a
// one-value IN. It returns "" when the filter names no service or more than
// one, or has an OR or NOT that can widen the scope.
func filterService(filter string) string {
	tokens := tokenizeFilter(filter)
	service, keys := "", 0
	for i, token := range tokens {
		if token.is("OR", "NOT") {
			return ""
		}
		if isServiceKey(token) {
			keys++
			service, _, _ = comparedValue(tokens[i+1:])
		}
	}
	if keys != 1 {
		return ""
	}
	return service
}

// filterIsOnlyService reports whether a filter is nothing but one service
// predicate, possibly in parentheses.
func filterIsOnlyService(filter string) bool {
	tokens := tokenizeFilter(filter)
	for len(tokens) >= 2 && tokens[0].is("(") && tokens[len(tokens)-1].is(")") {
		tokens = tokens[1 : len(tokens)-1]
	}
	if len(tokens) == 0 || !isServiceKey(tokens[0]) {
		return false
	}
	_, length, ok := comparedValue(tokens[1:])
	return ok && 1+length == len(tokens)
}

// narrowsSpans reports a predicate that picks spans on purpose: one on the span
// name, which picks operations, or on the span kind other than kind = 'Server',
// such as outgoing client calls. Server spans still include nested ones.
func narrowsSpans(tokens []filterToken) bool {
	for i, token := range tokens {
		if token.is("name", "span.name") {
			return true
		}
		if token.is("kind", "kind_string", "span.kind", "span.kind_string") {
			rest := tokens[i+1:]
			if len(rest) < 2 || !rest[0].is("=") || !rest[1].literal || !strings.EqualFold(rest[1].text, "server") {
				return true
			}
		}
	}
	return false
}

// traceAllSpansNote fires on an ungrouped count, rate, or latency over every
// span of one service. SigNoz counts each matching span, so a request that
// records several spans counts several times; signoz_list_services scopes the
// same question to entry spans. callerFilter is the filter argument as sent,
// without the service shortcut, which decides how the fix reads.
func traceAllSpansNote(filter, callerFilter string, groupBy int, aggregations []string, queryType string) (measurementNote, bool) {
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
	if narrowsSpans(tokenizeFilter(filter)) {
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
	fix := "add AND (" + querybuilder.EntrySpanFilter + ") to the filter"
	if strings.TrimSpace(callerFilter) == "" {
		fix = "set filter to " + querybuilder.EntrySpanFilter + " and keep the service argument"
	}
	return measurementNote{rule: ruleTraceAllSpans, text: fmt.Sprintf(
		"note: this aggregates every span of service %q that matches the filter, not its requests; one request can record several spans, including a server span nested inside another. "+
			"For the service's requests (request count, rate, or latency), %s, as signoz_list_services does.",
		service, fix)}, true
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
	HasFunctions     bool // post-query functions change what each step holds
	HasHaving        bool // a having filter drops buckets the percentile needs
	StartMs, EndMs   int64
}

var percentilePattern = regexp.MustCompile(`^p(\d{2})$`)

// metricMeasurementNotes runs every metric rule over one query that is not a
// formula input. Formula results combine their inputs, so a ratio of two
// summed rates is still a ratio; callers skip queries a formula references.
// Post-query functions change what each step holds, so no rule runs then.
func metricMeasurementNotes(m metricMeasurement) []measurementNote {
	if m.HasFunctions {
		return nil
	}
	// The aggregation defaults accept any letter case for the type.
	m.MetricType = strings.ToLower(m.MetricType)
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
// of the window. For an explicit-bucket histogram, each bucket's count, read
// with increase and summed by le, gives a single percentile by interpolation;
// the limit must return every bucket of every group, because SigNoz applies it
// to the reduced series. Like SigNoz's own percentile, a rank in the +Inf
// bucket takes the highest finite bound. A having filter can drop buckets, so
// it gets no recipe.
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
		"note: reduceTo %s combined the %s of each %s, so this is not the %s over the whole window.",
		reduce, percentile, stepPhrase(m.StepSeconds), percentile)
	if explicitBucketHistogram(m) && !m.HasHaving {
		keys := append(append([]string(nil), m.GroupBy...), "le")
		perGroup := ""
		if len(m.GroupBy) > 0 {
			perGroup = " within each group"
		}
		text += fmt.Sprintf(
			" For a single %s, query %s again with timeAggregation increase, spaceAggregation sum, reduceTo sum, groupBy %s, and a limit that returns every bucket of every group, then interpolate%s within the first bucket whose count reaches %s%% of the total; if that is the +Inf bucket, take the highest finite bound, as SigNoz does.",
			percentile, m.MetricName, strings.Join(keys, ", "), perGroup, match[1])
	}
	return measurementNote{rule: rulePercentileOfSteps, text: text}, true
}

// explicitBucketHistogram reports a histogram with le buckets: its type when
// known, or else a bucket series name, as SigNoz (X.bucket) and Prometheus
// (X_bucket) store them.
func explicitBucketHistogram(m metricMeasurement) bool {
	if m.MetricType != "" {
		return m.MetricType == "histogram"
	}
	return strings.HasSuffix(m.MetricName, ".bucket") || strings.HasSuffix(m.MetricName, "_bucket")
}

// stepAlignmentNote fires when the caller sets a scalar step at least as long
// as the window and the window does not start on a multiple of it. SigNoz
// starts steps at multiples of the step since the Unix epoch, so the window
// spans two steps: the first reaches back before the window, except that a
// cumulative histogram percentile with no timeAggregation has no earlier
// sample to take a rate from and leaves that first step out, which leaves
// nothing when the next step starts at or after the window's end. An unset
// step is shorter, but SigNoz still aligns the start down to a step and the
// end down to a minute, so the note promises no exact window.
func stepAlignmentNote(m metricMeasurement) (measurementNote, bool) {
	stepMs, windowMs := m.StepSeconds*1000, m.EndMs-m.StartMs
	if m.RequestType != "scalar" || !m.CallerStep || stepMs <= 0 || windowMs <= 0 || stepMs < windowMs || m.StartMs%stepMs == 0 {
		return measurementNote{}, false
	}
	first := m.StartMs - m.StartMs%stepMs
	next := first + stepMs
	firstText := time.UnixMilli(first).UTC().Format(time.RFC3339)
	nextText := time.UnixMilli(next).UTC().Format(time.RFC3339)
	cumulative := fmt.Sprintf("the result starts at %s and leaves out the window before it", nextText)
	if next >= m.EndMs {
		cumulative = fmt.Sprintf("the first step is left out and the next starts at %s, at or after the window's end, so the result can be empty", nextText)
	}
	percentile := percentilePattern.MatchString(strings.ToLower(m.SpaceAggregation))
	var effect string
	switch {
	case percentile && strings.EqualFold(m.Temporality, "cumulative") && m.TimeAggregation == "":
		effect = "for this cumulative histogram " + cumulative
	case strings.EqualFold(m.Temporality, "delta") || (!percentile && m.MetricType != ""):
		effect = fmt.Sprintf("the first step starts at %s, before the window, and counts data from before it", firstText)
	default:
		effect = fmt.Sprintf("the first step starts at %s, before the window, and counts data from before it, or, for a cumulative histogram percentile, %s", firstText, cumulative)
	}
	return measurementNote{rule: ruleStepAlignment, text: fmt.Sprintf(
		"note: SigNoz starts steps at multiples of %ds since the Unix epoch, so this %ds step does not cover the window as one step: %s. "+
			"Leave stepInterval unset so SigNoz picks a shorter step whose steps stay close to the window, though it still aligns the start down to a step and the end down to a minute, and choose reduceTo for the question.",
		m.StepSeconds, m.StepSeconds, effect)}, true
}

// rateSummedNote fires when a scalar request sums per-step rates. Each step
// holds a per-second rate summed across series, so their sum depends on the
// step count and is neither a count nor a rate. Another spaceAggregation, such
// as count, makes each step something other than a rate, and SigNoz reads a
// histogram's buckets with increase whatever timeAggregation says.
func rateSummedNote(m metricMeasurement) (measurementNote, bool) {
	space := strings.ToLower(m.SpaceAggregation)
	if m.RequestType != "scalar" || !strings.EqualFold(m.TimeAggregation, "rate") || !strings.EqualFold(m.ReduceTo, "sum") || space != "sum" && space != "" {
		return measurementNote{}, false
	}
	if m.MetricType == "exponential_histogram" || explicitBucketHistogram(m) {
		return measurementNote{}, false
	}
	return measurementNote{rule: ruleRateSummed, text: fmt.Sprintf(
		"note: reduceTo sum added up the per-second rate of each %s, which is neither a count nor a rate. "+
			"For the total count in the window, use timeAggregation increase with reduceTo sum; for the average per-second rate, use reduceTo avg.",
		stepPhrase(m.StepSeconds))}, true
}

// counterRunningTotalNote fires when a cumulative counter is read with a time
// aggregation other than rate or increase. A cumulative counter's value is its
// running total since it started, and count counts its samples. A scalar keeps
// the caller's reduceTo, so the count in the window also needs reduceTo sum.
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
	case "count":
		reads = "counts the counter's samples, not the events it counts"
	default:
		return measurementNote{}, false
	}
	increase := "timeAggregation increase"
	if m.RequestType == "scalar" {
		increase += " with reduceTo sum"
	}
	return measurementNote{rule: ruleCounterRunningTotal, text: fmt.Sprintf(
		"note: %s is a cumulative counter, so timeAggregation %s %s. For the count in the window, use %s; for per second, use rate.",
		m.MetricName, timeAggregation, reads, increase)}, true
}

// spanMetricsAllSpansNote fires on SigNoz's span metrics for one service with
// no span-kind or operation narrowing. The collector records one call per
// span, so a request with several spans counts several times. The trace query
// it suggests covers only the service; when the metric query also filters or
// groups, the note says to carry those over rather than offer a call that
// answers a wider question.
func spanMetricsAllSpansNote(m metricMeasurement) (measurementNote, bool) {
	calls := m.MetricName == spanMetricsCallsMetric || m.MetricName == spanMetricsLatencyCount
	latency := m.MetricName == spanMetricsLatencyBucket
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
	onlyService := filterIsOnlyService(m.Filter)
	filter := "filter " + querybuilder.EntrySpanFilter
	if !onlyService {
		filter = "filter (" + querybuilder.EntrySpanFilter + ") AND this query's other filter conditions"
	}
	if len(m.GroupBy) > 0 {
		filter += ", grouped by this query's groupBy"
	}
	if calls {
		services := ""
		if onlyService && len(m.GroupBy) == 0 {
			// signoz_list_services answers for the whole service only.
			services = "signoz_list_services (numCalls, callRate), or "
		}
		return measurementNote{rule: ruleSpanMetricsAllSpans, text: fmt.Sprintf(
			"note: %s counts every span of service %q, one per span, so one request that records several spans counts several times. "+
				"For the service's requests, use %ssignoz_aggregate_traces with count() or rate(), service %q, and %s.",
			m.MetricName, service, services, service, filter)}, true
	}
	percentile := "p95"
	if match := percentilePattern.FindStringSubmatch(strings.ToLower(m.SpaceAggregation)); match != nil {
		percentile = "p" + match[1]
	}
	return measurementNote{rule: ruleSpanMetricsAllSpans, text: fmt.Sprintf(
		"note: %s records the duration of every span of service %q, including spans inside each request. "+
			"For the service's request latency, use signoz_aggregate_traces with %s(duration_nano), service %q, and %s.",
		m.MetricName, service, percentile, service, filter)}, true
}

// --- signoz_execute_builder_query ---

// builderQueryMeasurementNotes runs the trace and metric rules over each
// enabled builder query of a query_range request, plus the formula-input
// limit rule. steps is the step SigNoz used per query name. Metric types are
// unknown here, so the counter rule does not run. Each per-query note names
// its query, because two queries can measure the same way.
func builderQueryMeasurementNotes(request map[string]any, steps map[string]int64) []measurementNote {
	var notes []measurementNote
	for _, input := range querybuilder.LimitedFormulaInputs(request) {
		notes = append(notes, measurementNote{rule: ruleFormulaInputLimit, text: fmt.Sprintf(
			"note: query %q is a formula input with limit %d, which SigNoz applies before the formula runs, so the formula combined only its top %d series. Remove limit from formula inputs to combine every series.",
			input.QueryName, input.Limit, input.Limit)})
	}

	requestType, _ := request["requestType"].(string)
	startMs, endMs := epochMillis(int64(numberValue(request["start"]))), epochMillis(int64(numberValue(request["end"])))
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
		var queryNotes []measurementNote
		switch spec["signal"] {
		case "traces":
			// A raw or trace request lists spans; it aggregates nothing.
			if requestType != "scalar" && requestType != "time_series" {
				continue
			}
			var expressions []string
			for _, item := range aggregations {
				if object, ok := item.(map[string]any); ok {
					if expression, ok := object["expression"].(string); ok {
						expressions = append(expressions, expression)
					}
				}
			}
			if note, ok := traceAllSpansNote(filter, filter, len(groupBy), expressions, ""); ok {
				queryNotes = append(queryNotes, note)
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
			queryNotes = metricMeasurementNotes(metricMeasurement{
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
				HasFunctions:     hasItems(spec["functions"]),
				HasHaving:        havingExpression(spec["having"]) != "",
				StartMs:          startMs,
				EndMs:            endMs,
			})
		}
		for _, note := range queryNotes {
			note.text = strings.Replace(note.text, "note: ", fmt.Sprintf("note: in query %q, ", name), 1)
			notes = append(notes, note)
		}
	}
	return notes
}

// epochMillis reads an epoch of any resolution as milliseconds, the way
// SigNoz's ToMilliSecs coerces a query_range request's start and end: it
// scales the value to 13 digits, the width of a millisecond epoch today.
func epochMillis(epoch int64) int64 {
	if epoch <= 0 {
		return epoch
	}
	digits := len(strconv.FormatInt(epoch, 10))
	for ; digits < 13; digits++ {
		epoch *= 10
	}
	for ; digits > 13; digits-- {
		epoch /= 10
	}
	return epoch
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

func havingExpression(raw any) string {
	switch having := raw.(type) {
	case string:
		return strings.TrimSpace(having)
	case map[string]any:
		expression, _ := having["expression"].(string)
		return strings.TrimSpace(expression)
	}
	return ""
}

func hasItems(raw any) bool {
	items, _ := raw.([]any)
	return len(items) > 0
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
