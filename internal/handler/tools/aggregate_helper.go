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
	if err := validateRequestType(requestType); err != nil {
		return nil, err
	}
	if requestType == "" {
		requestType = "scalar"
	}

	var stepInterval *int64
	if v, ok := args["stepInterval"].(string); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			stepInterval = &n
		}
	}

	return &AggregateRequest{
		AggregationExpr:  aggregationExpr,
		FilterExpression: filterExpr,
		GroupBy:          groupByFields,
		OrderExpr:        orderExpr,
		OrderDir:         orderDir,
		Limit:            limit,
		LimitClamped:     limitClamped,
		StartTime:        startTime,
		EndTime:          endTime,
		RequestType:      requestType,
		StepInterval:     stepInterval,
	}, nil
}

func resolveTimestamps(args map[string]any, defaultRange string) (int64, int64, error) {
	// Reject a present-but-malformed start/end LOUDLY before falling through to
	// the default window. GetTimestampsWithDefaults silently defaults on a bad
	// value, which would hand back the wrong time range with no error.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		return 0, 0, err
	}
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

// rawSearchResult is the result wrapper for raw row tools (search_logs /
// search_traces), which support offset pagination.
func rawSearchResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limitClamped bool) *mcp.CallToolResult {
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
			MaxRawResultLimit))
	}
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
