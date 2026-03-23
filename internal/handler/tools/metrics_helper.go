package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// metricsQueryRequest holds parsed arguments for signoz_query_metrics.
type metricsQueryRequest struct {
	MetricName       string
	MetricType       string
	IsMonotonic      bool
	Temporality      string
	TimeAggregation  string
	SpaceAggregation string
	ReduceTo         string
	GroupBy          []string
	Filter           string
	TimeRange        string
	Start            int64
	End              int64
	StepInterval     int64
	RequestType      string
	Formula          string
	FormulaQueries   []formulaSubQuery
}

// formulaSubQuery represents one sub-query within a formula request.
type formulaSubQuery struct {
	Name             string   `json:"name"`
	MetricName       string   `json:"metricName"`
	MetricType       string   `json:"metricType"`
	IsMonotonic      bool     `json:"isMonotonic"`
	Temporality      string   `json:"temporality"`
	TimeAggregation  string   `json:"timeAggregation"`
	SpaceAggregation string   `json:"spaceAggregation"`
	GroupBy          []string `json:"groupBy"`
	Filter           string   `json:"filter"`
}

func parseMetricsQueryArgs(args map[string]any) (*metricsQueryRequest, error) {
	metricName, _ := args["metricName"].(string)
	if metricName == "" {
		return nil, fmt.Errorf("\"metricName\" is required. Use signoz_list_metrics to find available metrics")
	}

	req := &metricsQueryRequest{
		MetricName:       metricName,
		MetricType:       stringArg(args, "metricType"),
		Temporality:      stringArg(args, "temporality"),
		TimeAggregation:  stringArg(args, "timeAggregation"),
		SpaceAggregation: stringArg(args, "spaceAggregation"),
		ReduceTo:         stringArg(args, "reduceTo"),
		Filter:           stringArg(args, "filter"),
		TimeRange:        stringArg(args, "timeRange"),
		RequestType:      stringArg(args, "requestType"),
		Formula:          stringArg(args, "formula"),
	}

	// isMonotonic — accept bool or string
	switch v := args["isMonotonic"].(type) {
	case bool:
		req.IsMonotonic = v
	case string:
		req.IsMonotonic = strings.EqualFold(v, "true")
	}

	// groupBy — accept []string, []any, or comma-separated string
	switch v := args["groupBy"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				req.GroupBy = append(req.GroupBy, strings.TrimSpace(s))
			}
		}
	case string:
		if v != "" {
			for _, field := range strings.Split(v, ",") {
				field = strings.TrimSpace(field)
				if field != "" {
					req.GroupBy = append(req.GroupBy, field)
				}
			}
		}
	}

	// start / end
	if s, ok := args["start"]; ok {
		req.Start = parseIntLoose(s)
	}
	if e, ok := args["end"]; ok {
		req.End = parseIntLoose(e)
	}

	// stepInterval
	if si, ok := args["stepInterval"]; ok {
		req.StepInterval = parseIntLoose(si)
	}

	// formulaQueries — JSON array of sub-query objects
	if fq, ok := args["formulaQueries"]; ok {
		switch v := fq.(type) {
		case []any:
			data, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("invalid formulaQueries: %w", err)
			}
			if err := json.Unmarshal(data, &req.FormulaQueries); err != nil {
				return nil, fmt.Errorf("invalid formulaQueries schema: %w", err)
			}
		case string:
			if v != "" {
				if err := json.Unmarshal([]byte(v), &req.FormulaQueries); err != nil {
					return nil, fmt.Errorf("invalid formulaQueries JSON string: %w", err)
				}
			}
		}
	}

	// Defaults
	if req.RequestType == "" {
		req.RequestType = "time_series"
	}
	if req.TimeRange == "" && req.Start == 0 && req.End == 0 {
		req.TimeRange = "1h"
	}

	return req, nil
}

// buildGroupByFields converts field name strings to SelectField with auto-detected fieldContext.
func buildGroupByFields(names []string) []types.SelectField {
	fields := make([]types.SelectField, 0, len(names))
	for _, name := range names {
		fields = append(fields, types.SelectField{
			Name:         name,
			Signal:       "metrics",
			FieldContext: detectFieldContext(name),
		})
	}
	return fields
}

// resourcePrefixes are attribute name prefixes that indicate a resource attribute.
var resourcePrefixes = []string{
	"k8s.", "container.", "host.", "cloud.", "deployment.", "process.",
	"service.", "telemetry.", "os.",
}

func detectFieldContext(name string) string {
	for _, prefix := range resourcePrefixes {
		if strings.HasPrefix(name, prefix) {
			return "resource"
		}
	}
	return "attribute"
}

// autoStepInterval calculates a step interval targeting ~300 data points, minimum 60s.
func autoStepInterval(startMs, endMs int64) int64 {
	rangeMs := endMs - startMs
	if rangeMs <= 0 {
		return 60
	}
	step := rangeMs / 300 / 1000 // convert ms range to seconds, divide by 300 points
	if step < 60 {
		return 60
	}
	return step
}

// --- helpers ---

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func parseIntLoose(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		_, _ = fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}
