package tools

import (
	"fmt"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// SearchTracesRequest holds the parsed parameters for a trace search query.
type SearchTracesRequest struct {
	FilterExpression string
	SelectFields     []types.SelectField
	Limit            int
	LimitClamped     bool
	Offset           int
	StartTime        int64
	EndTime          int64
}

// maxTraceSelectFields bounds how many extra columns one search can request.
const maxTraceSelectFields = 50

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

	selectFields, err := parseTraceSelectFields(args["selectFields"])
	if err != nil {
		return nil, err
	}

	return &SearchTracesRequest{
		FilterExpression: filterExpr,
		SelectFields:     selectFields,
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

// parseTraceSelectFields returns the core search columns plus any extra fields
// the caller named. It accepts an array of names or a comma-separated string;
// a resource., attribute., tag., or span. prefix sets the field context.
func parseTraceSelectFields(raw any) ([]types.SelectField, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
	case string:
		names = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf(`invalid "selectFields" item %v: each item must be a field name string, e.g. ["http.route", "db.statement"]`, item)
			}
			names = append(names, name)
		}
	case []string:
		names = v
	default:
		return nil, fmt.Errorf(`invalid "selectFields" value %v: pass an array of field names or a comma-separated string, e.g. "http.route, db.statement"`, raw)
	}

	fields := append([]types.SelectField(nil), types.TraceSearchCoreFields...)
	seen := make(map[string]bool, len(fields)+len(names))
	for _, field := range fields {
		seen[field.Name] = true
	}
	extra := 0
	for _, name := range names {
		field, ok := traceSelectField(strings.TrimSpace(name))
		if !ok || seen[field.Name] {
			continue
		}
		seen[field.Name] = true
		fields = append(fields, field)
		extra++
	}
	if extra > maxTraceSelectFields {
		return nil, fmt.Errorf(`"selectFields" names %d extra fields; request at most %d per search`, extra, maxTraceSelectFields)
	}
	return fields, nil
}

// traceSelectFieldContexts maps accepted name prefixes to the Query Builder
// field context; selectFields uses "tag" for span attributes.
var traceSelectFieldContexts = []struct{ prefix, context string }{
	{"resource.", "resource"},
	{"attribute.", "tag"},
	{"tag.", "tag"},
	{"span.", "span"},
}

func traceSelectField(name string) (types.SelectField, bool) {
	if name == "" {
		return types.SelectField{}, false
	}
	for _, c := range traceSelectFieldContexts {
		if bare, found := strings.CutPrefix(name, c.prefix); found && bare != "" {
			if known, ok := traceGroupByFieldMetadata[bare]; ok && known.FieldContext == c.context {
				return known, true
			}
			return types.SelectField{Name: bare, Signal: "traces", FieldContext: c.context}, true
		}
	}
	return aggregateGroupByField("traces", name), true
}
