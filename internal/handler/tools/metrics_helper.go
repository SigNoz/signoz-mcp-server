package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
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
	// StepIntervalInvalid holds the raw stepInterval value when it was present
	// but not a valid positive integer count of seconds. The handler surfaces it
	// as a note and falls back to backend auto-select rather than coercing it.
	StepIntervalInvalid string
	RequestType         string
	Formula             string
	FormulaQueries      []formulaSubQuery
	Source              string
	// Result shaping. Limit 0 means the server default; an empty OrderKey keeps
	// __result desc.
	Limit        int
	LimitClamped bool
	OrderKey     string
	OrderDir     string
	Having       string
	Functions    []querybuilder.Function
}

// formulaSubQuery represents one sub-query within a formula request. Its
// jsonschema tags are the advertised item schema of formulaQueries; groupBy and
// isMonotonic also accept their string forms, which the schema adds separately.
type formulaSubQuery struct {
	Name             string   `json:"name" jsonschema:"Query name the formula references, for example B."`
	MetricName       string   `json:"metricName" jsonschema:"Metric to query, for example http_requests_total."`
	MetricType       string   `json:"metricType,omitempty" jsonschema:"gauge, sum, histogram, exponential_histogram, or summary. Omit to auto-fetch it with temporality and monotonicity."`
	IsMonotonic      bool     `json:"isMonotonic,omitempty" jsonschema:"Whether a sum metric only increases. Auto-fetched when metricType is omitted."`
	Temporality      string   `json:"temporality,omitempty" jsonschema:"cumulative, delta, or unspecified. Auto-fetched when metricType is omitted."`
	TimeAggregation  string   `json:"timeAggregation,omitempty" jsonschema:"Aggregation over time buckets: latest, sum, avg, min, max, count, count_distinct, rate, or increase. Defaulted by metric type."`
	SpaceAggregation string   `json:"spaceAggregation,omitempty" jsonschema:"Aggregation across series: sum, avg, min, max, count, or p50 to p99 for histograms. Defaulted by metric type."`
	GroupBy          []string `json:"groupBy,omitempty" jsonschema:"Field names to group by, as an array or a comma-separated string. Use the same groupBy as the primary query so the formula can match series."`
	Filter           string   `json:"filter,omitempty" jsonschema:"Filter expression, for example \"service.name = 'checkout'\"."`
}

// formulaSubQuerySchema is the formulaQueries item schema, derived from
// formulaSubQuery with the string forms the parser also accepts.
func formulaSubQuerySchema() map[string]any {
	schema := mcp.SchemaFor[formulaSubQuery]()
	properties, _ := schema["properties"].(map[string]any)
	if groupBy, ok := properties["groupBy"].(map[string]any); ok {
		groupBy["type"] = []string{"array", "string"}
	}
	if isMonotonic, ok := properties["isMonotonic"].(map[string]any); ok {
		isMonotonic["type"] = []string{"boolean", "string"}
	}
	return schema
}

func parseMetricsQueryArgs(args map[string]any) (*metricsQueryRequest, error) {
	metricName, _ := args["metricName"].(string)
	if metricName == "" {
		return nil, fmt.Errorf(`%s "metricName" is required. Use signoz_list_metrics to find available metrics`, validationErrorPrefix)
	}
	filter, err := readFilterExpr(args)
	if err != nil {
		return nil, err
	}
	// Read requestType once, rejecting a present-but-non-string value loudly
	// (instead of stringArg's silent drop to ""), then validate the enum and
	// reuse the value for RequestType below.
	requestType, err := readRequestType(args)
	if err != nil {
		return nil, err
	}
	if err := validateRequestType(requestType); err != nil {
		return nil, err
	}

	req := &metricsQueryRequest{
		MetricName:       metricName,
		MetricType:       stringArg(args, "metricType"),
		Temporality:      stringArg(args, "temporality"),
		TimeAggregation:  stringArg(args, "timeAggregation"),
		SpaceAggregation: stringArg(args, "spaceAggregation"),
		ReduceTo:         stringArg(args, "reduceTo"),
		Filter:           filter,
		TimeRange:        stringArg(args, "timeRange"),
		RequestType:      requestType,
		Formula:          stringArg(args, "formula"),
		Source:           stringArg(args, "source"),
	}

	// isMonotonic — accept a real bool or "true"/"false"; hard-error on garbage
	// (rather than treating any non-"true" string as false).
	if v, present, err := parseBoolArg(args, "isMonotonic"); err != nil {
		return nil, err
	} else if present {
		req.IsMonotonic = v
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

	// stepInterval — must be a positive base-10 integer count of SECONDS. A
	// JSON number is taken as-is; a STRING must be entirely numeric. A
	// non-numeric or unit-suffixed value ("1h", "60s", "abc") is NOT coerced
	// (the old fmt.Sscanf path silently turned "1h"→1s, a wrong bucket size).
	// Instead we leave StepInterval unset (0 → backend auto-select) and record
	// the rejected raw value so the handler can surface a [Decisions] note.
	if si, ok := args["stepInterval"]; ok {
		if n, raw, valid := parseStepIntervalSeconds(si); valid {
			req.StepInterval = n
		} else if raw != "" {
			req.StepIntervalInvalid = raw
		}
	}

	// formulaQueries: an array of sub-query objects, or a JSON-encoded string
	// of one. Each item's groupBy and isMonotonic accept their string forms.
	if raw, ok := args["formulaQueries"]; ok {
		var items []map[string]any
		if _, err := decodeJSONArg("formulaQueries", raw, &items); err != nil {
			return nil, fmt.Errorf(`%w; pass an array of {name, metricName, ...} objects`, err)
		}
		for i, item := range items {
			if text, ok := item["groupBy"].(string); ok {
				names, _ := parseFieldNameList("groupBy", text)
				item["groupBy"] = names
			}
			if _, ok := item["isMonotonic"].(string); ok {
				v, _, err := parseBoolArg(item, "isMonotonic")
				if err != nil {
					return nil, fmt.Errorf("%s formulaQueries[%d]: %w", validationErrorPrefix, i, err)
				}
				item["isMonotonic"] = v
			}
		}
		if len(items) > 0 {
			data, err := json.Marshal(items)
			if err != nil {
				return nil, fmt.Errorf("invalid formulaQueries: %w", err)
			}
			if err := json.Unmarshal(data, &req.FormulaQueries); err != nil {
				return nil, fmt.Errorf(`%s "formulaQueries" has the wrong shape: %v; each item is {name, metricName, metricType, isMonotonic, temporality, timeAggregation, spaceAggregation, groupBy, filter}`, validationErrorPrefix, err)
			}
		}
	}

	// Result shaping: limit, orderBy, having, functions.
	limit, err := intArg(args, "limit", 0)
	if err != nil {
		return nil, err
	}
	req.Limit, req.LimitClamped = clampLimit(limit)
	if raw := strings.TrimSpace(stringArg(args, "orderBy")); raw != "" {
		req.OrderKey, req.OrderDir = parseOrderBy(raw)
	}
	req.Having = strings.TrimSpace(stringArg(args, "having"))
	if req.Functions, err = parseFunctionsArg(args["functions"]); err != nil {
		return nil, err
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

// buildGroupByFields converts field name strings to field keys with auto-detected fieldContext.
func buildGroupByFields(names []string) []querybuilder.FieldKey {
	fields := make([]querybuilder.FieldKey, 0, len(names))
	for _, name := range names {
		fields = append(fields, querybuilder.NewFieldKey(name, "metrics", detectFieldContext(name), ""))
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

// extractStepInterval parses meta.stepIntervals from the backend response JSON
// and returns the step interval for the first query (typically "A").
// Returns 0 if not found or on parse error.
func extractStepInterval(response json.RawMessage) int64 {
	var resp struct {
		Data struct {
			Meta struct {
				StepIntervals map[string]int64 `json:"stepIntervals"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return 0
	}
	// Return the first (usually "A") step interval
	for _, v := range resp.Data.Meta.StepIntervals {
		return v
	}
	return 0
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

// parseStepIntervalSeconds parses a query_metrics stepInterval argument, which
// must be a POSITIVE integer count of seconds. Unlike parseIntLoose it is
// strict on strings: the ENTIRE trimmed string must be a base-10 integer, so
// unit-suffixed values like "1h"/"60s" and non-numeric "abc" are rejected
// rather than silently coerced to a wrong bucket size.
//
// Returns:
//   - (n, "", true)    a valid positive integer (from a JSON number or numeric string)
//   - (0, raw, false)  present but invalid — raw is its string form for surfacing
//   - (0, "", false)   absent / empty / a non-positive value (use backend auto-select)
func parseStepIntervalSeconds(v any) (n int64, raw string, valid bool) {
	switch t := v.(type) {
	case nil:
		return 0, "", false
	case float64:
		if t > 0 && float64(int64(t)) == t {
			return int64(t), "", true
		}
		return 0, strconv.FormatFloat(t, 'f', -1, 64), false
	case int:
		if t > 0 {
			return int64(t), "", true
		}
		return 0, strconv.Itoa(t), false
	case int64:
		if t > 0 {
			return t, "", true
		}
		return 0, strconv.FormatInt(t, 10), false
	case json.Number:
		i, err := t.Int64()
		if err == nil && i > 0 {
			return i, "", true
		}
		return 0, t.String(), false
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, "", false
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err == nil && i > 0 {
			return i, "", true
		}
		return 0, s, false
	}
	return 0, "", false
}
