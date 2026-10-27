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

func (h *Handler) RegisterLogsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering logs handlers")

	// aggregate_logs: compute statistics over logs with GROUP BY
	aggregateLogsTool := mcp.NewTool("signoz_aggregate_logs",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants aggregate statistics over logs (counts, rates, averages, percentiles, or grouped/top-N breakdowns) rather than individual log records. Use signoz_search_logs for log rows and message inspection; use signoz_execute_builder_query only for queries this tool cannot express. Log fields are workspace-specific, so read signoz://logs/query-builder-guide and discover unfamiliar keys with signoz_get_field_keys. Defaults to the last 1 hour."),
		mcp.WithString("aggregations", mcp.Required(), stringOrStringArrayType(), mcp.Description(logsAggregationsParamDescription)),
		mcp.WithString("groupBy", stringOrStringArrayType(), mcp.Description(logsGroupByParamDescription)),
		mcp.WithString("having", mcp.Description(havingParamDescription)),
		mcp.WithString("filter", mcp.Description(aggregateLogsFilterParamDescription)),
		mcp.WithString("service", mcp.Description(aggregateLogsServiceParamDescription)),
		mcp.WithString("severity", mcp.Description(aggregateLogsSeverityParamDescription)),
		mcp.WithString("orderBy", mcp.Description(aggregateLogsOrderByParamDescription)),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description(aggregateLimitParamDescription)),
		functionsProperty(),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("requestType", mcp.DefaultString("scalar"), mcp.Enum("scalar", "time_series"), mcp.Description(aggregateRequestTypeParamDescription)),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description(stepIntervalParamDescription)),
	)

	h.addTool(s, aggregateLogsTool, h.handleAggregateLogs)

	// search_logs: log search with optional filters
	// ToDo: use this function for error logs or logs by service
	searchLogsTool := mcp.NewTool("signoz_search_logs",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants individual log records or messages matching text, service, severity, or field filters. It returns paginated rows, not counts, trends, or grouped breakdowns; use signoz_aggregate_logs for those, and signoz_execute_builder_query only for queries this tool cannot express. Rows carry a default set of log columns; to include other attributes, such as service.name, name them in selectFields, or set allFields for each row's complete record. A field missing from a row was not selected. You do not need the guide when using only searchText, service, severity, time, or pagination. Read signoz://logs/query-builder-guide before filtering on unfamiliar fields. Defaults to the last 1 hour."),
		mcp.WithString("filter", mcp.Description(logsFilterParamDescription)),
		mcp.WithString("service", mcp.Description(searchLogsServiceParamDescription)),
		mcp.WithString("severity", mcp.Description(searchLogsSeverityParamDescription)),
		mcp.WithString("searchText", mcp.Description(logsSearchTextParamDescription)),
		mcp.WithString("searchScope", mcp.Enum("body", "attribute", "resource", "all"), mcp.Description(logsSearchScopeParamDescription)),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultRawQueryLimit)), intOrStringType(), mcp.Description(searchLogsLimitParamDescription)),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description(searchLogsOffsetParamDescription)),
		mcp.WithString("orderBy", mcp.Description(searchLogsOrderByParamDescription)),
		mcp.WithString("selectFields", stringOrStringArrayType(), mcp.Description(logsSelectFieldsParamDescription)),
		mcp.WithBoolean("allFields", boolOrStringType(), mcp.Description(searchLogsAllFieldsParamDescription)),
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
		QueryType:    reqData.QueryType,
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

	var notes []string
	if reqData.SearchScopeIgnored {
		notes = append(notes, searchScopeIgnoredNote)
	}
	if reqData.SelectFieldsIgnored {
		notes = append(notes, selectFieldsIgnoredNote)
	}
	return rawSearchResult(ctx, h.logger, "signoz_search_logs", result, reqData.Limit, reqData.Offset, reqData.LimitClamped, notes...), nil
}
