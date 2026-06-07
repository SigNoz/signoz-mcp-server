package tools

import (
	"encoding/json"
	"fmt"
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
	// Bound the group limit to keep a high-cardinality groupBy from buffering an
	// unbounded scalar response. Surfaced via aggregateResult's note so the
	// assistant knows the group set was truncated (there is no offset pagination
	// for aggregation groups — the note advises narrowing instead).
	limit, limitClamped := clampLimit(limit)

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	requestType, _ := args["requestType"].(string)
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

// clampedResult wraps a raw backend JSON payload as a tool result. The JSON
// payload is always the first content block so clients can parse it intact;
// when the requested limit was clamped, `note` is appended as a separate second
// content block (rather than prepended into the JSON) so the caller is told the
// response is truncated without breaking JSON parsing.
func clampedResult(payload []byte, limitClamped bool, note string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(string(payload))
	if limitClamped {
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// rawSearchResult is the result wrapper for raw row tools (search_logs /
// search_traces), which support offset pagination.
func rawSearchResult(payload []byte, limitClamped bool) *mcp.CallToolResult {
	return clampedResult(payload, limitClamped, fmt.Sprintf(
		"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
		MaxRawResultLimit))
}

// aggregateResult is the result wrapper for aggregation tools. Aggregations
// have no offset pagination, so the note advises narrowing the query instead.
func aggregateResult(payload []byte, limitClamped bool) *mcp.CallToolResult {
	return clampedResult(payload, limitClamped, fmt.Sprintf(
		"note: result limited to %d groups to bound server memory; narrow the time range, filters, or groupBy cardinality for fewer, more-specific groups.",
		MaxRawResultLimit))
}

// builderQueryResult is the result wrapper for execute_builder_query, whose
// payload may mix raw and aggregation builder queries, so the note covers both.
func builderQueryResult(payload []byte, limitClamped bool) *mcp.CallToolResult {
	return clampedResult(payload, limitClamped, fmt.Sprintf(
		"note: a query \"limit\" was reduced to %d to bound server memory; narrow the time range/filters (or, for raw queries, paginate with \"offset\") to retrieve more.",
		MaxRawResultLimit))
}
