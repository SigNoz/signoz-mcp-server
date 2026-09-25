package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

// The SigNoz v4 waterfall returns every span of a trace (up to its select-all
// limit) in pre-order; these bound what one signoz_get_trace_details page holds.
const (
	traceDetailPageBudgetBytes  = 100_000
	traceDetailMaxEventsPerSpan = 3
	traceDetailMaxValueChars    = 2000
	traceDetailMaxErrorSpans    = 10
	traceDetailMaxSlowestSpans  = 5
	traceDetailMaxAncestors     = 5
)

var errWaterfallShape = errors.New("waterfall response has no data.spans array")

type waterfallTrace struct {
	StartTimestampMillis  json.Number      `json:"startTimestampMillis"`
	EndTimestampMillis    json.Number      `json:"endTimestampMillis"`
	RootServiceName       string           `json:"rootServiceName"`
	RootServiceEntryPoint string           `json:"rootServiceEntryPoint"`
	TotalSpansCount       json.Number      `json:"totalSpansCount"`
	TotalErrorSpansCount  json.Number      `json:"totalErrorSpansCount"`
	Spans                 []map[string]any `json:"spans"`
	HasMissingSpans       bool             `json:"hasMissingSpans"`
	HasMore               bool             `json:"hasMore"`
}

// parseWaterfall decodes the {status, data} envelope with json.Number so span
// durations and timestamps keep their exact integer values.
func parseWaterfall(body []byte) (*waterfallTrace, error) {
	var envelope struct {
		Data *waterfallTrace `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Data == nil || envelope.Data.Spans == nil {
		return nil, errWaterfallShape
	}
	return envelope.Data, nil
}

type traceSpanRef struct {
	SpanID        string      `json:"span_id"`
	Name          string      `json:"name"`
	Service       string      `json:"service,omitempty"`
	DurationNano  json.Number `json:"duration_nano,omitempty"`
	HasError      bool        `json:"has_error,omitempty"`
	StatusMessage string      `json:"status_message,omitempty"`
}

type traceServiceSummary struct {
	Name       string `json:"name"`
	Spans      int    `json:"spans"`
	ErrorSpans int    `json:"errorSpans"`
}

type traceDetailsSummary struct {
	TotalSpans           json.Number           `json:"totalSpans"`
	TotalErrorSpans      json.Number           `json:"totalErrorSpans"`
	StartTimestampMillis json.Number           `json:"startTimestampMillis"`
	EndTimestampMillis   json.Number           `json:"endTimestampMillis"`
	RootService          string                `json:"rootService"`
	RootOperation        string                `json:"rootOperation"`
	HasMissingSpans      bool                  `json:"hasMissingSpans"`
	Complete             bool                  `json:"complete"`
	Services             []traceServiceSummary `json:"services"`
	ErrorSpans           []traceSpanRef        `json:"errorSpans"`
	SlowestSpans         []traceSpanRef        `json:"slowestSpans"`
}

type traceDetailsFocus struct {
	SpanID           string         `json:"span_id"`
	Ancestors        []traceSpanRef `json:"ancestors"`
	AncestorsOmitted int            `json:"ancestorsOmitted,omitempty"`
}

type traceDetailsResource struct {
	ResourceID int            `json:"resourceId"`
	Attributes map[string]any `json:"attributes"`
}

type traceDetailsPagination struct {
	Returned   int    `json:"returned"`
	HasMore    bool   `json:"hasMore"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type traceDetailsResult struct {
	TraceID    string                  `json:"traceId"`
	WebURL     string                  `json:"webUrl,omitempty"`
	Summary    traceDetailsSummary     `json:"summary"`
	Focus      *traceDetailsFocus      `json:"focus,omitempty"`
	Resources  []traceDetailsResource  `json:"resources,omitempty"`
	Spans      []map[string]any        `json:"spans,omitempty"`
	Pagination *traceDetailsPagination `json:"pagination,omitempty"`
}

func buildTraceDetails(trace *waterfallTrace, traceID, focusSpanID, cursor, webURL string, includeSpans bool) (*traceDetailsResult, []string, *mcp.CallToolResult) {
	result := &traceDetailsResult{TraceID: traceID, WebURL: webURL, Summary: summarizeTrace(trace)}
	var notes []string
	if trace.HasMore {
		notes = append(notes, fmt.Sprintf(
			"note: this trace has %s spans, more than SigNoz returns at once; the summary and pages cover a window of %d spans. Pass spanId to move the window.",
			trace.TotalSpansCount, len(trace.Spans)))
	}
	if trace.HasMissingSpans {
		notes = append(notes, `note: some parent spans were never received; SigNoz inserts a placeholder span named "Missing Span" for each.`)
	}

	spans := trace.Spans
	if focusSpanID != "" {
		index := spanIndex(spans, focusSpanID)
		if index < 0 {
			return nil, nil, validationErrorf("spanId", "%q is not a span in trace %q; use a span_id from summary.errorSpans, summary.slowestSpans, or spans", focusSpanID, traceID)
		}
		result.Focus = focusOn(spans, index)
		spans = subtree(spans, index)
	}
	if !includeSpans {
		return result, notes, nil
	}

	start := 0
	if cursor != "" {
		index := spanIndex(spans, cursor)
		if index < 0 {
			return nil, nil, validationErrorf("cursor", "%q does not match a span in this trace view; call again without cursor, keeping the same spanId", cursor)
		}
		start = index + 1
	}

	resourceIDs, resourceMaps := indexResources(trace.Spans)
	page, resources, trimmed := pageSpans(spans[start:], resourceIDs, resourceMaps)
	result.Spans = page
	result.Resources = resources
	result.Pagination = &traceDetailsPagination{Returned: len(page)}
	if end := start + len(page); end < len(spans) {
		result.Pagination.HasMore = true
		result.Pagination.NextCursor = spanID(spans[end-1])
		notes = append(notes, fmt.Sprintf(
			"note: returned spans %d-%d of %d in this view; call signoz_get_trace_details again with cursor=%q for the next page, or with spanId to focus on one span and its subtree.",
			start+1, end, len(spans), result.Pagination.NextCursor))
	}
	if trimmed {
		notes = append(notes, "note: some spans had events or long values trimmed (see eventsOmitted and the truncation markers); read a full value with signoz_search_traces, filtering on span_id and naming the field in selectFields.")
	}
	return result, notes, nil
}

func summarizeTrace(trace *waterfallTrace) traceDetailsSummary {
	summary := traceDetailsSummary{
		TotalSpans:           trace.TotalSpansCount,
		TotalErrorSpans:      trace.TotalErrorSpansCount,
		StartTimestampMillis: trace.StartTimestampMillis,
		EndTimestampMillis:   trace.EndTimestampMillis,
		RootService:          trace.RootServiceName,
		RootOperation:        trace.RootServiceEntryPoint,
		HasMissingSpans:      trace.HasMissingSpans,
		Complete:             !trace.HasMore,
		Services:             []traceServiceSummary{},
		ErrorSpans:           []traceSpanRef{},
		SlowestSpans:         []traceSpanRef{},
	}

	byService := map[string]*traceServiceSummary{}
	for _, span := range trace.Spans {
		hasError := spanHasError(span)
		if hasError && len(summary.ErrorSpans) < traceDetailMaxErrorSpans {
			summary.ErrorSpans = append(summary.ErrorSpans, spanRef(span))
		}
		name := spanService(span)
		if name == "" {
			continue
		}
		entry, ok := byService[name]
		if !ok {
			entry = &traceServiceSummary{Name: name}
			byService[name] = entry
		}
		entry.Spans++
		if hasError {
			entry.ErrorSpans++
		}
	}
	for _, entry := range byService {
		summary.Services = append(summary.Services, *entry)
	}
	sort.Slice(summary.Services, func(i, j int) bool {
		if summary.Services[i].Spans != summary.Services[j].Spans {
			return summary.Services[i].Spans > summary.Services[j].Spans
		}
		return summary.Services[i].Name < summary.Services[j].Name
	})

	slowest := append([]map[string]any(nil), trace.Spans...)
	sort.SliceStable(slowest, func(i, j int) bool {
		return spanInt(slowest[i], "duration_nano") > spanInt(slowest[j], "duration_nano")
	})
	for _, span := range slowest[:min(len(slowest), traceDetailMaxSlowestSpans)] {
		summary.SlowestSpans = append(summary.SlowestSpans, spanRef(span))
	}
	return summary
}

// focusOn returns the breadcrumb for spans[index]: the root first, then its
// nearest ancestors top-down. Deep traces keep only the nearest few.
func focusOn(spans []map[string]any, index int) *traceDetailsFocus {
	byID := make(map[string]map[string]any, len(spans))
	for _, span := range spans {
		byID[spanID(span)] = span
	}
	var chain []map[string]any
	seen := map[string]bool{spanID(spans[index]): true}
	for parent := spanString(spans[index], "parent_span_id"); parent != "" && !seen[parent]; {
		span, ok := byID[parent]
		if !ok {
			break
		}
		seen[parent] = true
		chain = append(chain, span)
		parent = spanString(span, "parent_span_id")
	}

	focus := &traceDetailsFocus{SpanID: spanID(spans[index]), Ancestors: []traceSpanRef{}}
	if len(chain) > traceDetailMaxAncestors+1 {
		focus.AncestorsOmitted = len(chain) - traceDetailMaxAncestors - 1
		chain = append(chain[:traceDetailMaxAncestors:traceDetailMaxAncestors], chain[len(chain)-1])
	}
	for i := len(chain) - 1; i >= 0; i-- {
		focus.Ancestors = append(focus.Ancestors, spanRef(chain[i]))
	}
	return focus
}

// subtree relies on pre-order: a span's descendants follow it contiguously
// until the next span at the same or a shallower level.
func subtree(spans []map[string]any, index int) []map[string]any {
	level := spanInt(spans[index], "level")
	end := index + 1
	for end < len(spans) && spanInt(spans[end], "level") > level {
		end++
	}
	return spans[index:end]
}

// indexResources numbers each distinct resource map in trace order, so ids
// stay stable across pages of the same trace.
func indexResources(spans []map[string]any) (map[string]int, []map[string]any) {
	ids := map[string]int{}
	var maps []map[string]any
	for _, span := range spans {
		resource, _ := span["resource"].(map[string]any)
		key := resourceKey(resource)
		if _, ok := ids[key]; !ok {
			ids[key] = len(maps)
			maps = append(maps, resource)
		}
	}
	return ids, maps
}

func resourceKey(resource map[string]any) string {
	b, _ := json.Marshal(resource)
	return string(b)
}

func pageSpans(spans []map[string]any, resourceIDs map[string]int, resourceMaps []map[string]any) ([]map[string]any, []traceDetailsResource, bool) {
	page := []map[string]any{}
	var resources []traceDetailsResource
	included := map[int]bool{}
	used := 0
	trimmed := false
	for _, span := range spans {
		resource, _ := span["resource"].(map[string]any)
		id := resourceIDs[resourceKey(resource)]
		compacted, spanTrimmed := compactSpan(span, id)
		cost := jsonSize(compacted)
		var newResource *traceDetailsResource
		if !included[id] {
			capped, _ := truncateValues(resourceMaps[id])
			attributes, _ := capped.(map[string]any)
			newResource = &traceDetailsResource{ResourceID: id, Attributes: attributes}
			cost += jsonSize(newResource)
		}
		if len(page) > 0 && used+cost > traceDetailPageBudgetBytes {
			break
		}
		used += cost
		page = append(page, compacted)
		trimmed = trimmed || spanTrimmed
		if newResource != nil {
			included[id] = true
			resources = append(resources, *newResource)
		}
	}
	return page, resources, trimmed
}

// compactSpan drops what the page already states once (trace_id, the resource
// map, a reference that repeats parent_span_id) and empty values, then caps
// events and long strings.
func compactSpan(span map[string]any, resourceID int) (map[string]any, bool) {
	out := make(map[string]any, len(span))
	trimmed := false
	for key, value := range span {
		switch key {
		case "trace_id", "resource":
			continue
		case "references":
			if referencesOnlyParent(value, spanString(span, "parent_span_id")) {
				continue
			}
		case "events":
			events, _ := value.([]any)
			if len(events) > traceDetailMaxEventsPerSpan {
				out["eventsOmitted"] = len(events) - traceDetailMaxEventsPerSpan
				value = keepErrorEventsFirst(events)[:traceDetailMaxEventsPerSpan]
				trimmed = true
			}
		}
		if isEmptyValue(value) {
			continue
		}
		capped, cut := truncateValues(value)
		trimmed = trimmed || cut
		out[key] = capped
	}
	out["resourceId"] = resourceID
	return out, trimmed
}

func referencesOnlyParent(value any, parentID string) bool {
	refs, _ := value.([]any)
	if len(refs) != 1 {
		return len(refs) == 0
	}
	ref, _ := refs[0].(map[string]any)
	refType, _ := ref["refType"].(string)
	refSpan, _ := ref["spanId"].(string)
	return refSpan == parentID && (refType == "" || refType == "CHILD_OF")
}

func keepErrorEventsFirst(events []any) []any {
	ordered := append([]any(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return isErrorEvent(ordered[i]) && !isErrorEvent(ordered[j]) })
	return ordered
}

func isErrorEvent(event any) bool {
	m, _ := event.(map[string]any)
	if isError, _ := m["isError"].(bool); isError {
		return true
	}
	name, _ := m["name"].(string)
	return name == "exception"
}

// truncateValues appends "…[+N chars]" to each string it cuts.
func truncateValues(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		if len(runes) <= traceDetailMaxValueChars {
			return v, false
		}
		return fmt.Sprintf("%s…[+%d chars]", string(runes[:traceDetailMaxValueChars]), len(runes)-traceDetailMaxValueChars), true
	case map[string]any:
		out := make(map[string]any, len(v))
		cutAny := false
		for key, item := range v {
			capped, cut := truncateValues(item)
			out[key] = capped
			cutAny = cutAny || cut
		}
		return out, cutAny
	case []any:
		out := make([]any, len(v))
		cutAny := false
		for i, item := range v {
			capped, cut := truncateValues(item)
			out[i] = capped
			cutAny = cutAny || cut
		}
		return out, cutAny
	default:
		return value, false
	}
}

func isEmptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func spanRef(span map[string]any) traceSpanRef {
	duration, _ := span["duration_nano"].(json.Number)
	status, _ := truncateValues(spanString(span, "status_message"))
	return traceSpanRef{
		SpanID:        spanID(span),
		Name:          spanString(span, "name"),
		Service:       spanService(span),
		DurationNano:  duration,
		HasError:      spanHasError(span),
		StatusMessage: status.(string),
	}
}

func spanIndex(spans []map[string]any, id string) int {
	for i, span := range spans {
		if spanID(span) == id {
			return i
		}
	}
	return -1
}

func spanID(span map[string]any) string { return spanString(span, "span_id") }

func spanString(span map[string]any, key string) string {
	value, _ := span[key].(string)
	return value
}

func spanService(span map[string]any) string {
	resource, _ := span["resource"].(map[string]any)
	name, _ := resource["service.name"].(string)
	return strings.TrimSpace(name)
}

func spanHasError(span map[string]any) bool {
	hasError, _ := span["has_error"].(bool)
	return hasError
}

func spanInt(span map[string]any, key string) int64 {
	number, _ := span[key].(json.Number)
	value, _ := number.Int64()
	return value
}

func jsonSize(value any) int {
	b, _ := json.Marshal(value)
	return len(b)
}
