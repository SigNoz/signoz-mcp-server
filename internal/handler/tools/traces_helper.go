package tools

import (
	"fmt"
	"strings"
)

// SearchTracesRequest holds the parsed parameters for a trace search query.
type SearchTracesRequest struct {
	FilterExpression string
	Limit            int
	LimitClamped     bool
	Offset           int
	StartTime        int64
	EndTime          int64
}

func parseSearchTracesArgs(args map[string]any) (*SearchTracesRequest, error) {
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	service, _ := args["service"].(string)
	operation, _ := args["operation"].(string)
	errorFilter, _ := args["error"].(string)
	minDuration, _ := args["minDuration"].(string)
	maxDuration, _ := args["maxDuration"].(string)
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter, minDuration, maxDuration)

	limit, err := intArg(args, "limit", 100)
	if err != nil {
		return nil, err
	}
	limit, clamped := clampLimit(limit)

	offset, err := intArg(args, "offset", 0)
	if err != nil {
		return nil, err
	}

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	return &SearchTracesRequest{
		FilterExpression: filterExpr,
		Limit:            limit,
		LimitClamped:     clamped,
		Offset:           offset,
		StartTime:        startTime,
		EndTime:          endTime,
	}, nil
}

// parseAggregateTracesArgs validates and parses arguments for the aggregate_traces tool.
func parseAggregateTracesArgs(args map[string]any) (*AggregateRequest, error) {
	service, _ := args["service"].(string)
	operation, _ := args["operation"].(string)
	errorFilter, _ := args["error"].(string)
	minDuration, _ := args["minDuration"].(string)
	maxDuration, _ := args["maxDuration"].(string)
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter, minDuration, maxDuration)

	return parseAggregateArgs(args, "traces", filterExpr)
}

// buildTraceFilterExpr combines free-form filter with trace-specific shortcut filters.
func buildTraceFilterExpr(query, service, operation, errorFilter, minDuration, maxDuration string) string {
	var parts []string
	if query != "" {
		parts = append(parts, query)
	}
	if service != "" {
		parts = append(parts, fmt.Sprintf("service.name = '%s'", service))
	}
	if operation != "" {
		parts = append(parts, fmt.Sprintf("name = '%s'", operation))
	}
	if errorFilter != "" {
		switch errorFilter {
		case "true":
			parts = append(parts, "hasError = true")
		case "false":
			parts = append(parts, "hasError = false")
		}
	}
	if minDuration != "" {
		parts = append(parts, fmt.Sprintf("durationNano >= %s", minDuration))
	}
	if maxDuration != "" {
		parts = append(parts, fmt.Sprintf("durationNano <= %s", maxDuration))
	}
	return strings.Join(parts, " AND ")
}
