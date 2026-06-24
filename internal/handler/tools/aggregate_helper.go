package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

var validAggregations = map[string]bool{
	"count":          true,
	"count_distinct": true,
	"avg":            true,
	"sum":            true,
	"min":            true,
	"max":            true,
	"p50":            true,
	"p75":            true,
	"p90":            true,
	"p95":            true,
	"p99":            true,
	"rate":           true,
}

var aggregationsWithoutField = map[string]bool{
	"count": true,
	"rate":  true,
}

const allowedAggregations = "avg, count, count_distinct, max, min, p50, p75, p90, p95, p99, rate, sum"

const conflictingFilterAliasError = "both 'filter' and 'query' were provided with different values; use only 'filter' (the 'query' alias is legacy)"

// readFilterExpr returns the QB filter expression, accepting the canonical
// "filter" key and the legacy "query" alias. TrimSpace is used only to decide
// presence/equality; the returned expression preserves the caller's original
// text. Never log filter expressions here because they can contain user data.
func readFilterExpr(args map[string]any) (string, error) {
	filterRaw := stringValue(args["filter"])
	queryRaw := stringValue(args["query"])
	filterTrimmed := strings.TrimSpace(filterRaw)
	queryTrimmed := strings.TrimSpace(queryRaw)

	if filterTrimmed != "" && queryTrimmed != "" && filterTrimmed != queryTrimmed {
		return "", errors.New(conflictingFilterAliasError)
	}
	if filterTrimmed != "" {
		return filterRaw, nil
	}
	if queryTrimmed != "" {
		return queryRaw, nil
	}
	return "", nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

// parseBoolArg parses a boolean tool argument. It accepts a real JSON bool, or
// the case-insensitive strings "true"/"false" (so legacy string-typed callers
// keep working after the schema is declared as a real boolean). Any other
// non-empty value is a typed error so the LLM gets a correctable message rather
// than the value being silently dropped.
//
// Returns (value, present, error):
//   - present is false when the key is absent or an empty string (treat as "not set")
//   - error is non-nil only when a present value cannot be interpreted as a bool
func parseBoolArg(args map[string]any, key string) (bool, bool, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return false, false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, true, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return false, false, nil
		}
		switch strings.ToLower(s) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		default:
			return false, false, fmt.Errorf(`invalid %q value %q: must be a boolean (true or false)`, key, v)
		}
	default:
		return false, false, fmt.Errorf(`invalid %q value: must be a boolean (true or false)`, key)
	}
}

// AggregateRequest keeps parameters for any aggregation query.
type AggregateRequest struct {
	AggregationExpr  string
	FilterExpression string
	GroupBy          []types.SelectField
	OrderExpr        string
	OrderDir         string
	Limit            int
	LimitClamped     bool
	StartTime        int64
	EndTime          int64
	RequestType      string // "scalar" (default) or "time_series"
	StepInterval     *int64 // nil = let backend auto-select
	// StepIntervalWarning is set when a stepInterval value was provided but could
	// not be parsed as a positive integer. The handler logs it (WARN) so a
	// silently-dropped value is detectable rather than vanishing.
	StepIntervalWarning string
}

// parseAggregateArgs validates and parses  aggregate arguments.
// this is crucial as the input is provided by llm and if there is an error it must be suggested how to correct
func parseAggregateArgs(args map[string]any, signal string, filterExpr string) (*AggregateRequest, error) {
	aggregation, _ := args["aggregation"].(string)
	if aggregation == "" {
		return nil, fmt.Errorf(
			"\"aggregation\" is required. Supported values: %s. "+
				"Tip: for simple totals use {\"aggregation\": \"count\", \"groupBy\": \"service.name\"}",
			allowedAggregations)
	}
	if !validAggregations[aggregation] {
		return nil, fmt.Errorf(
			"invalid aggregation %q. Supported values: %s. "+
				"Tip: for counting use \"count\", for averages use \"avg\"",
			aggregation, allowedAggregations)
	}

	aggregateOn, _ := args["aggregateOn"].(string)
	if !aggregationsWithoutField[aggregation] && aggregateOn == "" {
		return nil, fmt.Errorf(
			"\"aggregateOn\" is required for %q aggregation. Specify the field to aggregate, "+
				"e.g. {\"aggregation\": \"%s\", \"aggregateOn\": \"duration\"}",
			aggregation, aggregation)
	}

	var aggregationExpr string
	if aggregateOn != "" {
		aggregationExpr = fmt.Sprintf("%s(%s)", aggregation, aggregateOn)
	} else {
		aggregationExpr = fmt.Sprintf("%s()", aggregation)
	}

	var groupByFields []types.SelectField
	if groupByStr, _ := args["groupBy"].(string); groupByStr != "" {
		for _, field := range strings.Split(groupByStr, ",") {
			field = strings.TrimSpace(field)
			if field != "" {
				groupByFields = append(groupByFields, types.SelectField{Name: field, Signal: signal})
			}
		}
	}

	orderByRaw, _ := args["orderBy"].(string)
	orderByStr := strings.TrimSpace(orderByRaw)
	orderExpr, orderDir := aggregationExpr, "desc"
	if orderByStr != "" {
		lower := strings.ToLower(orderByStr)
		switch {
		case strings.HasSuffix(lower, " asc"):
			orderExpr = strings.TrimSpace(orderByStr[:len(orderByStr)-4])
			orderDir = "asc"
		case strings.HasSuffix(lower, " desc"):
			orderExpr = strings.TrimSpace(orderByStr[:len(orderByStr)-5])
		default:
			orderExpr = orderByStr
		}
	}

	limit, err := intArg(args, "limit", 10)
	if err != nil {
		return nil, err
	}
	// Bound the group limit (high-cardinality groupBy). Surfaced via
	// aggregateResult's note since aggregations have no offset pagination.
	limit, limitClamped := clampLimit(limit)

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	requestType, _ := args["requestType"].(string)
	if requestType == "" {
		requestType = "scalar"
	}

	// stepInterval: accept either a JSON number or a numeric string via
	// parseIntLoose (matching query_metrics). A string-only assertion silently
	// dropped a real JSON number. A present-but-unparseable value (<= 0 or
	// garbage) is surfaced as a warning rather than silently ignored.
	var stepInterval *int64
	var stepIntervalWarning string
	if raw, present := args["stepInterval"]; present && raw != nil {
		if s, isStr := raw.(string); !isStr || strings.TrimSpace(s) != "" {
			if n := parseIntLoose(raw); n > 0 {
				stepInterval = &n
			} else {
				stepIntervalWarning = fmt.Sprintf(
					"stepInterval %v could not be parsed as a positive integer (seconds); letting the backend auto-select the bucket size",
					raw)
			}
		}
	}

	return &AggregateRequest{
		AggregationExpr:     aggregationExpr,
		FilterExpression:    filterExpr,
		GroupBy:             groupByFields,
		OrderExpr:           orderExpr,
		OrderDir:            orderDir,
		Limit:               limit,
		LimitClamped:        limitClamped,
		StartTime:           startTime,
		EndTime:             endTime,
		RequestType:         requestType,
		StepInterval:        stepInterval,
		StepIntervalWarning: stepIntervalWarning,
	}, nil
}

func resolveTimestamps(args map[string]any, defaultRange string) (int64, int64, error) {
	if _, ok := args["timeRange"]; !ok {
		_, hasStart := args["start"]
		if !hasStart {
			args["timeRange"] = defaultRange
		}
	}
	start, end := timeutil.GetTimestampsWithDefaults(args, "ms")
	var startTime, endTime int64
	if err := json.Unmarshal([]byte(start), &startTime); err != nil {
		return 0, 0, fmt.Errorf("invalid start timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	if err := json.Unmarshal([]byte(end), &endTime); err != nil {
		return 0, 0, fmt.Errorf("invalid end timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	return startTime, endTime, nil
}

func intArg(args map[string]any, key string, defaultVal int) (int, error) {
	str, _ := args[key].(string)
	if str == "" {
		return defaultVal, nil
	}
	num, err := strconv.Atoi(str)
	if err != nil {
		return 0, fmt.Errorf("invalid %q value %q: must be a number", key, str)
	}
	if num <= 0 {
		return defaultVal, nil
	}
	return num, nil
}

// MaxRawResultLimit caps how many raw rows search_logs / search_traces will
// request from the backend. Each row is read fully into memory, decoded, and
// re-marshaled into the tool response (~1.6 MiB per 1000 log rows measured
// against a real backend), so an uncapped limit is an unbounded single-request
// memory vector on the shared, memory-limited multi-tenant pod. Callers that
// need more rows should paginate via offset.
const MaxRawResultLimit = 10000

// clampLimit bounds a parsed limit to MaxRawResultLimit. It returns the
// effective limit and whether clamping occurred so handlers can surface it.
func clampLimit(n int) (int, bool) {
	if n > MaxRawResultLimit {
		return MaxRawResultLimit, true
	}
	return n, false
}

// extractBackendWarningMessages parses non-fatal QB v5 warning messages from a
// success response. It fails open: malformed or unexpected response shapes
// simply produce no notes and never block returning the raw backend payload.
func extractBackendWarningMessages(response json.RawMessage) []string {
	var resp struct {
		Data struct {
			Warning struct {
				Warnings []struct {
					Message string `json:"message"`
				} `json:"warnings"`
			} `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil
	}
	var messages []string
	for _, warning := range resp.Data.Warning.Warnings {
		if strings.TrimSpace(warning.Message) != "" {
			messages = append(messages, warning.Message)
		}
	}
	return messages
}

func backendWarningsNote(messages []string) string {
	var b strings.Builder
	b.WriteString("note: SigNoz backend returned non-fatal warnings:")
	for _, message := range messages {
		b.WriteString("\n- ")
		b.WriteString(message)
	}
	return b.String()
}

func warnBackendWarnings(ctx context.Context, logger *slog.Logger, toolName string, messages []string) {
	if len(messages) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"SigNoz query builder returned non-fatal warnings",
		slog.String("tool", toolName),
		slog.Int("warningCount", len(messages)),
	)
}

// resultWithNotes wraps a raw JSON payload as a tool result. The JSON is always
// the first (parseable) content block; notes are appended as separate blocks
// rather than prepended into the JSON.
func resultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(string(payload))
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// countQueryRangeRows sums the number of rows across all results in a QB v5
// query_range passthrough body. The expected nesting is
// data.data.results[].rows[] (a render.Success envelope wrapping a
// QueryRangeResponse), the same shape util.InjectRowsWebURL walks. It fails
// open: it returns (0, false) on any shape it cannot walk so a completeness
// note is simply omitted rather than wrong.
func countQueryRangeRows(payload []byte) (int, bool) {
	// Walk with json.RawMessage at each level so we can distinguish a MISSING /
	// null / non-array leaf from a genuinely-empty array. A missing or null
	// "results"/"rows" key means we could not locate the rows — we must NOT
	// claim count=0 (which would assert a misleading hasMore=false); fail open
	// to (0, false) so the caller emits the generic "more may exist" note.
	var envelope struct {
		Data struct {
			Data struct {
				Results json.RawMessage `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, false
	}
	results, ok := decodeNonNilArray(envelope.Data.Data.Results)
	if !ok {
		return 0, false
	}
	total := 0
	for _, rawResult := range results {
		var result struct {
			// Use RawMessage (not []json.RawMessage) so a missing/null "rows"
			// key on any single result is detectable rather than silently 0.
			Rows json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(rawResult, &result); err != nil {
			return 0, false
		}
		rows, ok := decodeNonNilArray(result.Rows)
		if !ok {
			// A result object without a readable rows[] array — the v5 contract
			// always emits "rows", so this is drift. Fail open rather than
			// undercount and falsely report completeness.
			return 0, false
		}
		total += len(rows)
	}
	return total, true
}

// countDataArrayRows counts the elements of a JSON array nested at data.<key>
// in a passthrough body (e.g. data.items for alert history, data.metrics for
// list_metrics, data.samples for top_metrics). It fails open: it returns
// (0, false) when the expected leaf is absent, null, or not an array, so a
// completeness note is omitted (or falls back to the generic note) rather than
// asserting a misleading hasMore=false.
func countDataArrayRows(payload []byte, key string) (int, bool) {
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, false
	}
	raw, present := resp.Data[key]
	if !present {
		return 0, false
	}
	arr, ok := decodeNonNilArray(raw)
	if !ok {
		return 0, false
	}
	return len(arr), true
}

// decodeNonNilArray reports whether raw is present and decodes as a NON-NIL JSON
// array, returning its elements. A missing field (nil/empty raw), a literal
// JSON null, or a non-array value all return (nil, false) so callers can treat
// "couldn't locate the array" distinctly from "located an empty array".
func decodeNonNilArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false // key absent
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false // not an array
	}
	if arr == nil {
		return nil, false // literal null
	}
	return arr, true
}

// completenessNote builds a pagination/completeness advisory for raw-passthrough
// tools that accept limit/offset but expose no hasMore signal of their own. When
// the returned row count is known it infers hasMore from returnedRows == limit
// and reports the nextOffset; otherwise it falls back to a generic "limit
// applied; more may exist" note so the LLM is never left assuming completeness.
func completenessNote(returnedRows, limit, offset int, rowsKnown bool) string {
	if !rowsKnown {
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Paginate with \"offset\" (or narrow the query) to be sure.",
			limit)
	}
	hasMore := limit > 0 && returnedRows >= limit
	nextOffset := offset + returnedRows
	if hasMore {
		return fmt.Sprintf(
			"note: returned %d rows (limit %d) — more results likely exist (hasMore=true). Fetch the next page with offset=%d.",
			returnedRows, limit, nextOffset)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// rawSearchResult is the result wrapper for raw row tools (search_logs /
// search_traces), which support offset pagination. It appends a completeness
// note (hasMore + nextOffset) inferred from the returned row count so callers
// never silently assume a truncated page is complete.
func rawSearchResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limit, offset int, limitClamped bool) *mcp.CallToolResult {
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
			MaxRawResultLimit))
	}
	returnedRows, rowsKnown := countQueryRangeRows(payload)
	notes = append(notes, completenessNote(returnedRows, limit, offset, rowsKnown))
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, logger, toolName, warnings)
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(payload, notes...)
}

// aggregateResult is the result wrapper for aggregation tools. Aggregations
// have no offset pagination, so the note advises narrowing the query instead.
func aggregateResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limitClamped bool) *mcp.CallToolResult {
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d groups to bound server memory; narrow the time range, filters, or groupBy cardinality for fewer, more-specific groups.",
			MaxRawResultLimit))
	}
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, logger, toolName, warnings)
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(payload, notes...)
}
