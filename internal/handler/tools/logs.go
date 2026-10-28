package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

const logsAggregationsParamDescription = "Aggregation expressions to compute, written as the Query Builder writes them: count(), count_distinct(user_id), avg(duration), sum(bytes), min(x), max(x), p50(x) to p99(x), rate(), or a conditional such as countIf(severity_text = 'ERROR'). " +
	"One expression or an array; results come back as __result_0, __result_1, ... in that order. Example: [\"count()\", \"p99(duration)\"]. SigNoz validates the expressions."

const logsOrderByParamDescription = "Row order as '<field> <direction>', e.g. 'timestamp asc' or 'severity_number desc'. Defaults to timestamp desc (newest first); when ordering by timestamp, id breaks ties in the same direction."

const logSelectFieldsDescription = "Extra fields to return on each row, added to the default set: timestamp, id, body, severity_text, trace_id, span_id. Pass an array of names or a comma-separated string, at most 50. " +
	"Names can be resource attributes (service.name, k8s.pod.name), log attributes (http.route), JSON body fields, or other log columns (severity_number); a resource., attribute., log., or body. prefix picks the context. Each field comes back as its own row key. " +
	"Discover names with signoz_get_field_keys (signal=\"logs\"). Example: [\"service.name\", \"http.route\"]."

const logsFilterParamDescription = "Filter expression using SigNoz search syntax (see signoz://logs/query-builder-guide). Combine conditions with AND, OR, and parentheses. Unknown keys hard-error; keys present in multiple contexts default to resource context. Disambiguate with attribute.<key> or resource.<key>. Log keys are workspace-specific; logs have no spec-mandated resource attributes, so even service.name is only present when the log pipeline sets it. Discover keys with signoz_get_field_keys and values with signoz_get_field_values before filtering. For message text use body CONTAINS 'term'. Only when you don't know which field holds a term, use search('term') for every field or search('term', attribute); scopes (body, attribute, resource, log) are ORed. search() is slow and may be rejected over wide time ranges. Surrounding filters keep AND/NOT semantics. Examples: \"service.name = 'payment-svc' AND severity_text = 'ERROR'\", \"body CONTAINS 'timeout' AND severity_text = 'ERROR'\"."

func (h *Handler) RegisterLogsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering logs handlers")

	// aggregate_logs: compute statistics over logs with GROUP BY
	aggregateLogsTool := mcp.NewTool("signoz_aggregate_logs",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants aggregate statistics over logs (counts, rates, averages, percentiles, or grouped/top-N breakdowns) rather than individual log records. Use signoz_search_logs for log rows and message inspection; use signoz_execute_builder_query only for queries this tool cannot express. Log fields are workspace-specific, so read signoz://logs/query-builder-guide and discover unfamiliar keys with signoz_get_field_keys. Defaults to the last 1 hour."),
		mcp.WithString("aggregations", mcp.Required(), stringOrStringArrayType(), mcp.Description(logsAggregationsParamDescription)),
		mcp.WithString("groupBy", stringOrStringArrayType(), mcp.Description("Field names to group results by, as an array or a comma-separated string (e.g. [\"service.name\", \"severity_text\"] or 'service.name, severity_text'). Leave empty for a single aggregate value.")),
		mcp.WithString("having", mcp.Description(havingParamDescription)),
		mcp.WithString("filter", mcp.Description(logsFilterParamDescription+" Combined with service/severity params using AND.")),
		mcp.WithString("service", mcp.Description("Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter. Fails with `key service.name not found` when this workspace's logs lack that attribute; discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead.")),
		mcp.WithString("severity", mcp.Description("Shortcut filter for severity_text. Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\").")),
		mcp.WithString("orderBy", mcp.Description("How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration) asc'. Defaults to the first aggregation expression descending.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description("Maximum number of groups to return (default: 100, max: 10000; higher values are clamped). For time_series queries, groups are ranked across the entire time range, so a short-lived spike can fall outside the selected top groups.")),
		functionsProperty(),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("requestType", mcp.DefaultString("scalar"), mcp.Enum("scalar", "time_series"), mcp.Description(aggregateRequestTypeDescription)),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description(stepIntervalDesc)),
	)

	h.addTool(s, aggregateLogsTool, h.handleAggregateLogs)

	// search_logs: log search with optional filters
	// ToDo: use this function for error logs or logs by service
	searchLogsTool := mcp.NewTool("signoz_search_logs",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants individual log records or messages matching text, service, severity, or field filters. It returns paginated rows, not counts, trends, or grouped breakdowns; use signoz_aggregate_logs for those, and signoz_execute_builder_query only for queries this tool cannot express. Rows carry a default set of log columns; to include other attributes, such as service.name, name them in selectFields. A field missing from a row was not selected. You do not need the guide when using only searchText, service, severity, time, or pagination. Read signoz://logs/query-builder-guide before filtering on unfamiliar fields. Defaults to the last 1 hour."),
		mcp.WithString("filter", mcp.Description(logsFilterParamDescription)),
		mcp.WithString("service", mcp.Description("Optional service name to filter by (adds service.name = '<value>'). Fails with `key service.name not found` when this workspace's logs lack that attribute; discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead.")),
		mcp.WithString("severity", mcp.Description("Filter on severity_text. Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\").")),
		mcp.WithString("searchText", mcp.Description("Literal text to find, escaped automatically. searchScope chooses where it matches; the default is the log body. Combines with filter using AND.")),
		mcp.WithString("searchScope", mcp.Enum("body", "attribute", "resource", "all"), mcp.Description("Where searchText matches: body (default) for message text; attribute or resource for their keys and values; all for every field. Scopes other than body are slower, and SigNoz may reject them over wide time ranges. Use them only when you don't know which key holds the text; when you do, put a predicate such as attribute.<key> CONTAINS 'text' in filter instead.")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultRawQueryLimit)), intOrStringType(), mcp.Description("Maximum number of logs to return (default: 100, max: 10000; higher values are clamped; paginate with offset)")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("orderBy", mcp.Description(logsOrderByParamDescription)),
		mcp.WithString("selectFields", stringOrStringArrayType(), mcp.Description(logSelectFieldsDescription)),
	)

	h.addTool(s, searchLogsTool, h.handleSearchLogs)
}

// aggregateSpec maps parsed aggregate arguments onto the request builder.
func aggregateSpec(signal string, reqData *AggregateRequest) querybuilder.AggregateSpec {
	return querybuilder.AggregateSpec{
		Signal:       signal,
		Start:        reqData.StartTime,
		End:          reqData.EndTime,
		Aggregations: reqData.Aggregations,
		Filter:       reqData.FilterExpression,
		GroupBy:      reqData.GroupBy,
		OrderExpr:    reqData.OrderExpr,
		OrderDir:     reqData.OrderDir,
		Limit:        reqData.Limit,
		RequestType:  reqData.RequestType,
		StepInterval: reqData.StepInterval,
		Having:       reqData.Having,
		Functions:    reqData.Functions,
	}
}

func (h *Handler) handleAggregateLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	reqData, err := parseAggregateLogsArgs(args)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	if reqData.StepIntervalWarning != "" {
		h.logger.WarnContext(ctx, "aggregate_logs stepInterval dropped", slog.String("reason", reqData.StepIntervalWarning))
	}

	queryPayload, err := querybuilder.Aggregate(aggregateSpec("logs", reqData))
	if err != nil {
		return InternalErrorResult("failed to build query payload: " + err.Error()), nil
	}

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal aggregate query payload", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_aggregate_logs",
		slog.String("aggregation", reqData.AggregationExpr),
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to aggregate logs", err)
		return upstreamQueryError(err, "logs"), nil
	}

	return aggregateResult(ctx, h.logger, "signoz_aggregate_logs", result, reqData.LimitClamped, aggregateNotes(reqData)...), nil
}

func (h *Handler) handleSearchLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	reqData, err := parseSearchLogsArgs(args)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	queryPayload, err := querybuilder.LogsSearch(
		reqData.StartTime, reqData.EndTime, reqData.FilterExpression,
		reqData.Limit, reqData.Offset,
		querybuilder.RawSearchOptions{
			SelectFields: reqData.SelectFields,
			OrderKey:     reqData.OrderKey,
			OrderDir:     reqData.OrderDir,
		},
	)
	if err != nil {
		return InternalErrorResult("failed to build query payload: " + err.Error()), nil
	}

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal search query payload", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_search_logs",
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to search logs", err)
		return upstreamQueryError(err, "logs"), nil
	}

	return rawSearchResult(ctx, h.logger, "signoz_search_logs", result, reqData.Limit, reqData.Offset, reqData.LimitClamped), nil
}
