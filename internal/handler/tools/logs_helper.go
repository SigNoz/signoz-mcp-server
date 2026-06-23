package tools

import (
	"fmt"
	"strings"
)

// parseAggregateLogsArgs validates and parses arguments for the aggregate_logs tool.
func parseAggregateLogsArgs(args map[string]any) (*AggregateRequest, error) {
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildLogFilterExpr(filter, service, severity, "")

	return parseAggregateArgs(args, "logs", filterExpr)
}

// SearchLogsRequest holds the parsed parameters for a log search query.
type SearchLogsRequest struct {
	FilterExpression string
	Limit            int
	LimitClamped     bool
	Offset           int
	StartTime        int64
	EndTime          int64
}

func parseSearchLogsArgs(args map[string]any) (*SearchLogsRequest, error) {
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	searchText, _ := args["searchText"].(string)
	filterExpr := buildLogFilterExpr(filter, service, severity, searchText)

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

	return &SearchLogsRequest{
		FilterExpression: filterExpr,
		Limit:            limit,
		LimitClamped:     clamped,
		Offset:           offset,
		StartTime:        startTime,
		EndTime:          endTime,
	}, nil
}

// buildLogFilterExpr combines with log specific  filters.
func buildLogFilterExpr(query, service, severity, searchText string) string {
	var parts []string
	if query != "" {
		parts = append(parts, query)
	}
	if service != "" {
		parts = append(parts, fmt.Sprintf("service.name = '%s'", service))
	}
	if severity != "" {
		parts = append(parts, fmt.Sprintf("severity_text = '%s'", severity))
	}
	if searchText != "" {
		parts = append(parts, fmt.Sprintf("body CONTAINS '%s'", searchText))
	}
	return strings.Join(parts, " AND ")
}
