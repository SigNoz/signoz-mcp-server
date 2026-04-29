package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterLogsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering logs handlers")

	// aggregate_logs: compute statistics over logs with GROUP BY
	aggregateLogsTool := mcp.NewTool("signoz_aggregate_logs",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Aggregate logs to compute statistics like count, average, sum, min, max, or percentiles, optionally grouped by fields. "+
			"Use this for questions like 'how many errors per service?', 'average response time by endpoint', 'top error messages by count'. "+
			"Defaults to last 1 hour if no time specified."),
		mcp.WithString("aggregation", mcp.Required(), mcp.Description("Aggregation function to apply. One of: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate")),
		mcp.WithString("aggregateOn", mcp.Description("Field name to aggregate on (e.g., 'response_time', 'duration'). Required for all aggregations except count and rate.")),
		mcp.WithString("groupBy", mcp.Description("Comma-separated list of field names to group results by (e.g., 'service.name' or 'service.name, severity_text'). Leave empty for a single aggregate value.")),
		mcp.WithString("filter", mcp.Description("Filter expression using SigNoz search syntax (e.g., \"status_code >= 400 AND http.method = 'POST'\"). Combined with service/severity params using AND.")),
		mcp.WithString("service", mcp.Description("Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter.")),
		mcp.WithString("severity", mcp.Description("Shortcut filter for log severity (DEBUG, INFO, WARN, ERROR, FATAL). Equivalent to adding severity_text = '<value>' to filter.")),
		mcp.WithString("orderBy", mcp.Description("How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration) asc'. Defaults to the aggregation expression descending.")),
		mcp.WithString("limit", mcp.Description("Maximum number of groups to return (default: 10)")),
		mcp.WithString("timeRange", mcp.Description("Time range string. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h', '7d'. Defaults to '1h'.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("requestType", mcp.Description("Controls whether to return a single aggregate or a time-series. Choose based on the user's question — do NOT ask the user to set this.\n\n\"scalar\" (default) — Returns one aggregate value computed over the entire time range. Use when the answer is a single number or a ranked/grouped table: \"how many errors today?\", \"what is the p99 latency of checkout?\", \"which service has the most errors?\", \"top 10 slowest endpoints\".\n\n\"time_series\" — Returns one value per time bucket so you can see changes over time. Use ONLY when the user's question is about WHEN something happened, HOW a metric changed, or to find SPIKES/TRENDS across time: \"when did errors spike?\", \"how did p99 change hour by hour?\", \"show error count per hour\", \"at what time is traffic highest?\".\n\nIf the intent is ambiguous (e.g. \"show latency over 24h\" could mean either), ask the user to clarify before calling this tool.\n\nIMPORTANT: If the question has ANY temporal component (spike, trend, change over time, \"when did X happen\"), always use \"time_series\" — it answers both the count AND the timing in one call. Never call this tool twice for the same question.\nExample: \"get error count and find when it spiked\" → \"time_series\".")),
		mcp.WithString("stepInterval", mcp.Description("Time bucket size in seconds for time_series mode (optional). When omitted, the backend auto-selects an appropriate interval. Only set this if the user explicitly requests a specific granularity. Examples: \"60\" (1 min), \"3600\" (1 hour), \"86400\" (1 day).")),
	)

	addTool(s, aggregateLogsTool, h.handleAggregateLogs)

	// search_logs: log search with optional filters
	// ToDo: use this function for error logs or logs by service
	searchLogsTool := mcp.NewTool("signoz_search_logs",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Search logs with flexible filtering. Supports free-form query expressions, optional service/severity filters, and body text search. "+
			"Use service param to scope to a single service, severity param for error-only queries (e.g., severity='ERROR'), or query param for any filter expression. "+
			"Defaults to last 1 hour if no time specified."),
		mcp.WithString("query", mcp.Description("Free-form filter expression using SigNoz search syntax. Examples: \"service.name = 'payment-svc' AND http.status_code >= 400\", \"workflow_run_id = 'wr_123'\", \"body CONTAINS 'timeout'\". Supports any log field/attribute.")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by.")),
		mcp.WithString("severity", mcp.Description("Optional severity filter (DEBUG, INFO, WARN, ERROR, FATAL).")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body (uses CONTAINS matching).")),
		mcp.WithString("timeRange", mcp.Description("Time range string. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h', '7d'. Defaults to '1h'.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	addTool(s, searchLogsTool, h.handleSearchLogs)
}

func (h *Handler) handleAggregateLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}

	reqData, err := parseAggregateLogsArgs(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		h.logger.ErrorContext(ctx, "Failed to aggregate logs", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleSearchLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}

	reqData, err := parseSearchLogsArgs(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		h.logger.ErrorContext(ctx, "Failed to search logs", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}
