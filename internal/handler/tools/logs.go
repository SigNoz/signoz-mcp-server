package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

const logsFilterParamDescription = "Filter expression using SigNoz search syntax (see signoz://logs/query-builder-guide). Combine conditions with AND, OR, and parentheses for precedence. Unknown keys hard-error; keys present in multiple contexts default to resource context. Disambiguate with attribute.<key> or resource.<key>. Log keys are workspace-specific — logs have no spec-mandated resource attributes, so even service.name is only present when the log pipeline sets it. Discover valid keys with signoz_get_field_keys, then confirm values with signoz_get_field_values, before filtering. Examples: \"service.name = 'payment-svc' AND severity_text = 'ERROR'\", \"(severity_text = 'ERROR' OR body CONTAINS 'panic') AND k8s.namespace.name = 'prod'\", \"body.user.id = '123'\"."

func (h *Handler) RegisterLogsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering logs handlers")

	// aggregate_logs: compute statistics over logs with GROUP BY
	aggregateLogsTool := mcp.NewTool("signoz_aggregate_logs",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants aggregate statistics over logs—counts, rates, averages, percentiles, or grouped/top-N breakdowns—not individual log records. Use signoz_search_logs for log rows and message inspection; use signoz_execute_builder_query only for queries this tool cannot express. Log fields are workspace-specific, so read signoz://logs/query-builder-guide and discover unfamiliar keys with signoz_get_field_keys. Defaults to the last 1 hour."),
		mcp.WithString("aggregation", mcp.Required(), mcp.Description("Aggregation function to apply. One of: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate")),
		mcp.WithString("aggregateOn", mcp.Description("Field name to aggregate on (e.g., 'response_time', 'duration'). Required for all aggregations except count and rate.")),
		mcp.WithString("groupBy", mcp.Description("Comma-separated list of field names to group results by (e.g., 'service.name' or 'service.name, severity_text'). Leave empty for a single aggregate value.")),
		mcp.WithString("filter", mcp.Description(logsFilterParamDescription+" Combined with service/severity params using AND.")),
		mcp.WithString("service", mcp.Description("Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter. Fails with `key service.name not found` when this workspace's logs lack that attribute — then discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead.")),
		mcp.WithString("severity", mcp.Description("Shortcut filter for severity_text. Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\").")),
		mcp.WithString("orderBy", mcp.Description("How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration) asc'. Defaults to the aggregation expression descending.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(types.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description("Maximum number of groups to return (default: 100, max: 10000; higher values are clamped). For time_series queries, groups are ranked across the entire time range, so a short-lived spike can fall outside the selected top groups.")),
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
		mcp.WithDescription("Use this when the user wants individual log records or messages matching text, service, severity, or field filters. It returns paginated rows, not counts, trends, or grouped breakdowns; use signoz_aggregate_logs for those, and signoz_execute_builder_query only for queries this tool cannot express. You do not need the guide when using only searchText, service, severity, time, or pagination. Read signoz://logs/query-builder-guide before filtering on unfamiliar fields. Defaults to the last 1 hour."),
		mcp.WithString("filter", mcp.Description(logsFilterParamDescription)),
		mcp.WithString("service", mcp.Description("Optional service name to filter by (adds service.name = '<value>'). Fails with `key service.name not found` when this workspace's logs lack that attribute — then discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead.")),
		mcp.WithString("severity", mcp.Description("Filter on severity_text. Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\").")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body (uses CONTAINS matching).")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(types.DefaultRawQueryLimit)), intOrStringType(), mcp.Description("Maximum number of logs to return (default: 100, max: 10000; higher values are clamped — paginate with offset)")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Offset for pagination (default: 0)")),
	)

	h.addTool(s, searchLogsTool, h.handleSearchLogs)
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

	queryPayload := types.BuildAggregateQueryPayload("logs",
		reqData.StartTime, reqData.EndTime, reqData.AggregationExpr,
		reqData.FilterExpression, reqData.GroupBy,
		reqData.OrderExpr, reqData.OrderDir, reqData.Limit,
		reqData.RequestType, reqData.StepInterval,
	)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal aggregate query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_aggregate_logs",
		slog.String("aggregation", reqData.AggregationExpr),
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to aggregate logs", err)
		return upstreamQueryError(err, "logs"), nil
	}

	return aggregateResult(ctx, h.logger, "signoz_aggregate_logs", result, reqData.LimitClamped), nil
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

	queryPayload := types.BuildLogsQueryPayload(
		reqData.StartTime, reqData.EndTime, reqData.FilterExpression,
		reqData.Limit, reqData.Offset,
	)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal search query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_search_logs",
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to search logs", err)
		return upstreamQueryError(err, "logs"), nil
	}

	return rawSearchResult(ctx, h.logger, "signoz_search_logs", result, reqData.Limit, reqData.Offset, reqData.LimitClamped), nil
}
