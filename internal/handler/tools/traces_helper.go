package tools

import (
	"fmt"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

const tracesLegacyQueryAliasError = `parameter validation failed: "query" is not accepted by trace tools; use "filter" with a Query Builder filter expression (see signoz://traces/query-builder-guide)`

// readTraceFilterExpr reads the canonical filter parameter for trace tools.
func readTraceFilterExpr(args map[string]any) (string, error) {
	return readFilterRejectingQueryAlias(args, tracesLegacyQueryAliasError)
}

// SearchTracesRequest holds the parsed parameters for a trace search query.
type SearchTracesRequest struct {
	FilterExpression string
	SelectFields     []querybuilder.FieldKey
	Limit            int
	LimitClamped     bool
	Offset           int
	StartTime        int64
	EndTime          int64
	QueryType        string // envelope type, forwarded as given; "" means builder_query
	RequestType      string // "trace" lists one row per trace; anything else lists spans
	OrderExpr        string // "" keeps the query type's default order
	OrderDir         string
}

// maxTraceSelectFields bounds how many extra columns one search can request.
const maxTraceSelectFields = maxSelectFields

func parseSearchTracesArgs(args map[string]any) (*SearchTracesRequest, error) {
	filter, err := readTraceFilterExpr(args)
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

	limit, err := intArg(args, "limit", querybuilder.DefaultRawQueryLimit)
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
	var orderExpr, orderDir string
	if raw := strings.TrimSpace(stringValue(args["orderBy"])); raw != "" {
		orderExpr, orderDir = parseOrderBy(raw)
	}

	queryType, _ := args["queryType"].(string)
	requestType, _ := args["requestType"].(string)

	return &SearchTracesRequest{
		QueryType:        queryType,
		RequestType:      requestType,
		OrderExpr:        orderExpr,
		OrderDir:         orderDir,
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
	filter, err := readTraceFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildTraceFilterExpr(filter, service, operation, errorFilter, errorPresent, minDuration, maxDuration)

	request, err := parseAggregateArgs(args, "traces", filterExpr)
	if err != nil {
		return nil, err
	}
	request.QueryType, _ = args["queryType"].(string)
	return request, nil
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
func parseTraceSelectFields(raw any) ([]querybuilder.FieldKey, error) {
	names, err := parseFieldNameList("selectFields", raw)
	if err != nil {
		return nil, err
	}

	fields := append([]querybuilder.FieldKey(nil), querybuilder.TraceSearchCoreFields...)
	seen := make(map[string]string, len(fields)+len(names))
	for _, field := range fields {
		seen[field.Name] = querybuilder.FieldContextOf(field)
	}
	extra := 0
	for _, name := range names {
		field, ok := traceSelectField(name)
		if !ok {
			continue
		}
		context := querybuilder.FieldContextOf(field)
		if held, ok := seen[field.Name]; ok {
			if err := selectFieldCollision(name, field.Name, held, context); err != nil {
				return nil, err
			}
			continue
		}
		seen[field.Name] = context
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

func traceSelectField(name string) (querybuilder.FieldKey, bool) {
	if name == "" {
		return querybuilder.FieldKey{}, false
	}
	for _, c := range traceSelectFieldContexts {
		if bare, found := strings.CutPrefix(name, c.prefix); found && bare != "" {
			if known, ok := traceGroupByFieldMetadata[bare]; ok && querybuilder.FieldContextOf(known) == c.context {
				return known, true
			}
			return querybuilder.NewFieldKey(bare, "traces", c.context, ""), true
		}
	}
	return aggregateGroupByField("traces", name), true
}
