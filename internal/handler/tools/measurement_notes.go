package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
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
// ( ) [ ] and ,; or any other run of non-space characters, which is a key, a
// keyword such as AND or IN, or an unquoted value. Matching keys on tokens
// keeps a longer key such as k8s.service.name, and text inside a literal, from
// reading as a key.
type filterToken struct {
	text    string
	literal bool
}

// word reports a key, keyword, or unquoted value: neither a literal nor a
// comparison or punctuation mark.
func (t filterToken) word() bool {
	return !t.literal && t.text != "" && strings.IndexByte(filterComparison+filterPunctuation, t.text[0]) < 0
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
	filterSpace       = " \t\r\n"
	filterComparison  = "=!<>"
	filterPunctuation = "()[],"
	filterBreaks      = filterSpace + filterComparison + filterPunctuation + `'"`
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
		case strings.IndexByte(filterPunctuation, c) >= 0:
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

// filterValue returns a token's value when it can be one: a quoted literal,
// or an unquoted number, boolean, or bare word, as SigNoz's filter grammar
// allows.
func filterValue(t filterToken) (string, bool) {
	return t.text, t.literal || t.word()
}

// comparedValues reads the comparison that starts the tokens after a key, in
// the spellings SigNoz's filter grammar (FilterQuery.g4) allows for equality
// and membership: = or == with a value, or IN with a list in parentheses or
// brackets, or with a single value. It returns the values and the number of
// tokens the comparison spans, or ok false for any other comparison.
func comparedValues(tokens []filterToken) (values []string, length int, ok bool) {
	if len(tokens) < 2 {
		return nil, 0, false
	}
	switch {
	case tokens[0].is("=", "=="):
		if value, ok := filterValue(tokens[1]); ok {
			return []string{value}, 2, true
		}
	case tokens[0].is("IN"):
		if value, ok := filterValue(tokens[1]); ok {
			return []string{value}, 2, true
		}
		var closing string
		switch {
		case tokens[1].is("("):
			closing = ")"
		case tokens[1].is("["):
			closing = "]"
		default:
			return nil, 0, false
		}
		for i := 2; i < len(tokens); i++ {
			switch {
			case tokens[i].is(closing):
				return values, i + 1, len(values) > 0
			case tokens[i].is(","):
			default:
				value, ok := filterValue(tokens[i])
				if !ok {
					return nil, 0, false
				}
				values = append(values, value)
			}
		}
	}
	return nil, 0, false
}

var (
	// filterOperators are the keywords that, like a comparison, follow a key.
	// SigNoz's grammar also accepts the singular CONTAIN and EXIST.
	filterOperators = []string{"IN", "NOT", "LIKE", "ILIKE", "BETWEEN", "EXISTS", "EXIST", "REGEXP", "CONTAINS", "CONTAIN"}
	filterKeywords  = append([]string{"AND", "OR"}, filterOperators...)
)

// filterKey is one predicate key of a filter: where it sits, the key as
// written in lower case, and the tokens that follow it.
type filterKey struct {
	index int
	name  string
	rest  []filterToken
}

// is reports whether the key is one of names, bare or under the field context
// SigNoz accepts for it, as in span.isRoot or resource.service.name. Another
// context names another field: scope.name is not the span's name.
func (k filterKey) is(context string, names ...string) bool {
	for _, name := range names {
		if k.name == name || k.name == context+"."+name {
			return true
		}
	}
	return false
}

// filterKeys returns a filter's predicate keys. A key is a word followed by a
// comparison or an operator keyword, so an unquoted value, such as the service
// in service.name == operation, is not one.
func filterKeys(tokens []filterToken) []filterKey {
	var keys []filterKey
	for i, token := range tokens {
		if !token.word() || token.is(filterKeywords...) || i+1 == len(tokens) {
			continue
		}
		next := tokens[i+1]
		if next.literal || strings.IndexByte(filterComparison, next.text[0]) < 0 && !next.is(filterOperators...) {
			continue
		}
		keys = append(keys, filterKey{index: i, name: strings.ToLower(token.text), rest: tokens[i+1:]})
	}
	return keys
}

// hasFilterKey reports whether any predicate key is one of names, bare or
// under context.
func hasFilterKey(keys []filterKey, context string, names ...string) bool {
	return slices.ContainsFunc(keys, func(key filterKey) bool { return key.is(context, names...) })
}

// filterService returns the one service a filter is scoped to: the value its
// only service.name key is compared with, by equality or a one-value IN. It
// returns "" when the filter names no service or more than one, or has an OR or
// NOT that can widen the scope.
func filterService(filter string) string {
	tokens := tokenizeFilter(filter)
	for _, token := range tokens {
		if token.is("OR", "NOT") {
			return ""
		}
	}
	service, found := "", 0
	for _, key := range filterKeys(tokens) {
		if !key.is("resource", "service.name") {
			continue
		}
		found++
		service = ""
		if values, _, ok := comparedValues(key.rest); ok && len(values) == 1 {
			service = values[0]
		}
	}
	if found != 1 {
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
	keys := filterKeys(tokens)
	if len(keys) != 1 || keys[0].index != 0 || !keys[0].is("resource", "service.name") {
		return false
	}
	values, length, ok := comparedValues(keys[0].rest)
	return ok && len(values) == 1 && 1+length == len(tokens)
}

// narrowsSpans reports a predicate that picks spans on purpose: one on the span
// name, which picks operations, or on the span kind other than equality to
// Server, such as outgoing client calls. Server spans still include nested ones.
func narrowsSpans(keys []filterKey) bool {
	for _, key := range keys {
		switch {
		case key.is("span", "name"):
			return true
		case key.is("span", "kind", "kind_string"):
			values, _, ok := comparedValues(key.rest)
			if !ok {
				return true
			}
			for _, value := range values {
				// kind holds the OTLP number, where 2 is the server kind.
				if !strings.EqualFold(value, "server") && (!key.is("span", "kind") || value != "2") {
					return true
				}
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
	// Entry, root, parent, and ID keys already scope the spans.
	keys := filterKeys(tokenizeFilter(filter))
	if hasFilterKey(keys, "span", "isroot", "isentrypoint", "parent_span_id", "trace_id", "span_id") || narrowsSpans(keys) {
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
// nothing when the next step starts at or after the end SigNoz uses, the
// requested end floored to the minute. SigNoz picks a step of at least 60s
// when none is set and still aligns the start down to a step and the end down
// to a minute, so the note promises no exact window.
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
	// SigNoz floors the end to the step, or to the minute for a step of a minute or more.
	endMs := m.EndMs - m.EndMs%(min(m.StepSeconds, 60)*1000)
	if next >= endMs {
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
			"Leave stepInterval unset so SigNoz picks the step, at least 60s, and choose reduceTo for the question; SigNoz still aligns the start down to a step and the end down to a minute.",
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
// running total since it started, and count counts its samples. The retry it
// names sets every aggregation, because a kept spaceAggregation or reduceTo
// changes the number: a scalar count sums each series' increase across series
// and steps, and a scalar per-second rate averages the summed rate over steps.
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
	retry := "For the count per step, use timeAggregation increase, and for the per-second rate, rate, each with spaceAggregation sum."
	if m.RequestType == "scalar" {
		retry = "For the count in the window, use timeAggregation increase, spaceAggregation sum, and reduceTo sum; for the average per-second rate, use timeAggregation rate, spaceAggregation sum, and reduceTo avg."
	}
	return measurementNote{rule: ruleCounterRunningTotal, text: fmt.Sprintf(
		"note: %s is a cumulative counter, so timeAggregation %s %s. %s",
		m.MetricName, timeAggregation, reads, retry)}, true
}

// spanMetricsAllSpansNote fires on SigNoz's span metrics for one service with
// no span-kind or operation narrowing. The collector records one call per
// span, so a request with several spans counts several times. The trace query
// it suggests matches the metric query's request type and window: a metric
// time series needs requestType time_series, because signoz_aggregate_traces
// defaults to scalar, and signoz_list_services returns whole-window totals, so
// it is offered only for a scalar over the whole service. The collector names
// its labels apart from span fields (operation, span.kind, status.code, with
// values such as STATUS_CODE_ERROR), so other conditions and groupings are to
// be translated, not copied; le is the histogram's own bucket label.
func spanMetricsAllSpansNote(m metricMeasurement) (measurementNote, bool) {
	calls := m.MetricName == spanMetricsCallsMetric || m.MetricName == spanMetricsLatencyCount
	latency := m.MetricName == spanMetricsLatencyBucket
	if !calls && !latency {
		return measurementNote{}, false
	}
	service := filterService(m.Filter)
	if service == "" || hasFilterKey(filterKeys(tokenizeFilter(m.Filter)), "attribute", "span.kind", "operation") {
		return measurementNote{}, false
	}
	var groups []string
	for _, key := range m.GroupBy {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "operation", "span.kind":
			return measurementNote{}, false
		case "le":
		default:
			groups = append(groups, key)
		}
	}
	onlyService := filterIsOnlyService(m.Filter)
	aggregation := "count() or rate()"
	if latency {
		aggregation = "p95(duration_nano)"
		if match := percentilePattern.FindStringSubmatch(strings.ToLower(m.SpaceAggregation)); match != nil {
			aggregation = "p" + match[1] + "(duration_nano)"
		}
	}
	call := fmt.Sprintf("signoz_aggregate_traces with %s, service %q, and filter %s", aggregation, service, querybuilder.EntrySpanFilter)
	if !onlyService {
		call = fmt.Sprintf("signoz_aggregate_traces with %s, service %q, and filter (%s) AND this query's other conditions", aggregation, service, querybuilder.EntrySpanFilter)
	}
	if len(groups) > 0 {
		call += ", grouped by " + strings.Join(groups, ", ")
	}
	switch {
	case m.RequestType == "scalar":
		call += ", over the same window"
	case m.StepSeconds > 0:
		call += fmt.Sprintf(", with requestType time_series, the same window, and stepInterval %d", m.StepSeconds)
	default:
		call += ", with requestType time_series, the same window, and the same step"
	}
	if calls && m.RequestType == "scalar" && onlyService && len(groups) == 0 {
		call = "signoz_list_services over the same window (numCalls, callRate), or " + call
	}
	var carried []string
	if !onlyService {
		carried = append(carried, "conditions")
	}
	if len(groups) > 0 {
		carried = append(carried, "keys")
	}
	translate := ""
	if len(carried) > 0 {
		translate = " Write those " + strings.Join(carried, " and ") + " as span fields, not span-metric labels: operation is name, span.kind SPAN_KIND_SERVER is kind_string 'Server', and status.code STATUS_CODE_ERROR is status_code_string 'Error'."
	}
	measured := fmt.Sprintf("note: %s counts every span of service %q, one per span, so one request that records several spans counts several times. For the service's requests, use ", m.MetricName, service)
	if latency {
		measured = fmt.Sprintf("note: %s records the duration of every span of service %q, including spans inside each request. For the service's request latency, use ", m.MetricName, service)
	}
	return measurementNote{rule: ruleSpanMetricsAllSpans, text: measured + call + "." + translate}, true
}

// --- signoz_execute_builder_query ---

// builderQueryMeasurementNotes runs the trace and metric rules over each
// enabled builder query of a query_range request, plus the formula-input
// limit rule. steps is the step SigNoz reported per query name. Metric types are
// unknown here, so the counter rule does not run. Each per-query note names
// its query, because two queries can measure the same way.
func builderQueryMeasurementNotes(request map[string]any, steps map[string]int64) []measurementNote {
	composite, _ := request["compositeQuery"].(map[string]any)
	queries, _ := composite["queries"].([]any)
	// SigNoz skips a metrics query on a missing metric and reports no step for
	// it, so it has no measurement to describe, as a formula input or alone.
	skipped := make(map[string]bool)
	for _, raw := range queries {
		envelope, _ := raw.(map[string]any)
		spec, _ := envelope["spec"].(map[string]any)
		if name, _ := spec["name"].(string); envelope["type"] == "builder_query" && spec["signal"] == "metrics" && steps[name] <= 0 {
			skipped[name] = true
		}
	}

	var notes []measurementNote
	for _, input := range querybuilder.LimitedFormulaInputs(request) {
		if skipped[input.QueryName] {
			continue
		}
		notes = append(notes, measurementNote{rule: ruleFormulaInputLimit, text: fmt.Sprintf(
			"note: query %q is a formula input with limit %d, which SigNoz applies before the formula runs, so the formula combined only its top %d series. Remove limit from formula inputs to combine every series.",
			input.QueryName, input.Limit, input.Limit)})
	}

	requestType, _ := request["requestType"].(string)
	startMs, endMs := epochMillis(int64(numberValue(request["start"]))), epochMillis(int64(numberValue(request["end"])))
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
			if len(aggregations) == 0 || skipped[name] {
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
