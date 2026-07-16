package tools

import (
	"fmt"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
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
	errorFilter, errorPresent, err := parseBoolArg(args, "error")
	if err != nil {
		return nil, err
	}
	minDuration, _ := args["minDuration"].(string)
	maxDuration, _ := args["maxDuration"].(string)
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter, errorPresent, minDuration, maxDuration)

	limit, err := intArg(args, "limit", types.DefaultRawQueryLimit)
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
	errorFilter, errorPresent, err := parseBoolArg(args, "error")
	if err != nil {
		return nil, err
	}
	minDuration, _ := args["minDuration"].(string)
	maxDuration, _ := args["maxDuration"].(string)
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter, errorPresent, minDuration, maxDuration)

	return parseAggregateArgs(args, "traces", filterExpr)
}

// buildTraceFilterExpr combines free-form filter with trace-specific shortcut
// filters. The error shortcut is applied only when errorPresent is true; an
// invalid value is rejected upstream by parseBoolArg rather than silently
// dropped here (which previously WIDENED results by omitting the filter).
func buildTraceFilterExpr(query, service, operation string, errorFilter, errorPresent bool, minDuration, maxDuration string) string {
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
	if errorPresent {
		if errorFilter {
			parts = append(parts, "has_error = true")
		} else {
			parts = append(parts, "has_error = false")
		}
	}
	if minDuration != "" {
		parts = append(parts, fmt.Sprintf("duration_nano >= %s", minDuration))
	}
	if maxDuration != "" {
		parts = append(parts, fmt.Sprintf("duration_nano <= %s", maxDuration))
	}
	return strings.Join(parts, " AND ")
}
