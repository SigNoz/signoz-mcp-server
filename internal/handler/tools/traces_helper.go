package tools

import (
	"fmt"
	"strings"
)

// parseAggregateTracesArgs validates and parses arguments for the aggregate_traces tool.
func parseAggregateTracesArgs(args map[string]any) (*AggregateRequest, error) {
	service, _ := args["service"].(string)
	operation, _ := args["operation"].(string)
	errorFilter, _ := args["error"].(string)
	filter, _ := args["filter"].(string)
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter)

	return parseAggregateArgs(args, "traces", filterExpr)
}

// buildTraceFilterExpr combines free-form filter with trace-specific shortcut filters.
func buildTraceFilterExpr(query, service, operation, errorFilter string) string {
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
	return strings.Join(parts, " AND ")
}
