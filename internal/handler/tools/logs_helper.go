package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

var validLogAggregations = map[string]bool{
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

type AggregateLogsRequest struct {
	AggregationExpr  string
	FilterExpression string
	GroupBy          []types.SelectField
	OrderExpr        string
	OrderDir         string
	Limit            int
	StartTime        int64
	EndTime          int64
}

// parseAggregateLogsArgs validates and parses arguments for the aggregate_logs tool.
// this is crucial as the input is provided by llm and if there is an error it must be suggested how to correct
func parseAggregateLogsArgs(args map[string]any) (*AggregateLogsRequest, error) {
	aggregation, _ := args["aggregation"].(string)
	if aggregation == "" {
		return nil, fmt.Errorf(
			"\"aggregation\" is required. Supported values: %s. "+
				"Tip: for simple totals use {\"aggregation\": \"count\", \"groupBy\": \"service.name\"}",
			allowedAggregations)
	}
	if !validLogAggregations[aggregation] {
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

	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	filter, _ := args["filter"].(string)
	filterExpr := buildFilterExpr(filter, service, severity, "")

	var groupByFields []types.SelectField
	if groupByStr, _ := args["groupBy"].(string); groupByStr != "" {
		for _, field := range strings.Split(groupByStr, ",") {
			field = strings.TrimSpace(field)
			if field != "" {
				groupByFields = append(groupByFields, types.SelectField{Name: field, Signal: "logs"})
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

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	return &AggregateLogsRequest{
		AggregationExpr:  aggregationExpr,
		FilterExpression: filterExpr,
		GroupBy:          groupByFields,
		OrderExpr:        orderExpr,
		OrderDir:         orderDir,
		Limit:            limit,
		StartTime:        startTime,
		EndTime:          endTime,
	}, nil
}

type SearchLogsRequest struct {
	FilterExpression string
	Limit            int
	Offset           int
	StartTime        int64
	EndTime          int64
}

func parseSearchLogsArgs(args map[string]any) (*SearchLogsRequest, error) {
	query, _ := args["query"].(string)
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	searchText, _ := args["searchText"].(string)
	filterExpr := buildFilterExpr(query, service, severity, searchText)

	limit, err := intArg(args, "limit", 100)
	if err != nil {
		return nil, err
	}

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
		Offset:           offset,
		StartTime:        startTime,
		EndTime:          endTime,
	}, nil
}

func buildFilterExpr(query, service, severity, searchText string) string {
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

func resolveTimestamps(args map[string]any, defaultRange string) (int64, int64, error) {
	if _, ok := args["timeRange"]; !ok {
		if _, ok := args["start"]; !ok {
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
