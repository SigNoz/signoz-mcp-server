package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterLogsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering logs handlers")

	listLogViewsTool := mcp.NewTool("signoz_list_log_views",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("List all saved log views from SigNoz (returns summary with name, ID, description, and query details). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific log view, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of views to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listLogViewsTool, h.handleListLogViews)

	getLogViewTool := mcp.NewTool("signoz_get_log_view",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("Get full details of a specific log view by ID (returns complete log view configuration with query structure)"),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("Log view ID")),
	)

	s.AddTool(getLogViewTool, h.handleGetLogView)

	getLogsForAlertTool := mcp.NewTool("signoz_get_logs_for_alert",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("Get logs related to a specific alert (automatically determines time range and service from alert details)"),
		mcp.WithString("alertId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range around alert (optional). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '15m', '30m', '1h', '2h', '6h'. Defaults to '1h' if not provided.")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(getLogsForAlertTool, h.handleGetLogsForAlert)

	// aggregate_logs: compute statistics over logs with GROUP BY
	aggregateLogsTool := mcp.NewTool("signoz_aggregate_logs",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
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
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("requestType", mcp.Description("Controls whether to return a single aggregate or a time-series. Choose based on the user's question — do NOT ask the user to set this.\n\n\"scalar\" (default) — Returns one aggregate value computed over the entire time range. Use when the answer is a single number or a ranked/grouped table: \"how many errors today?\", \"what is the p99 latency of checkout?\", \"which service has the most errors?\", \"top 10 slowest endpoints\".\n\n\"time_series\" — Returns one value per time bucket so you can see changes over time. Use ONLY when the user's question is about WHEN something happened, HOW a metric changed, or to find SPIKES/TRENDS across time: \"when did errors spike?\", \"how did p99 change hour by hour?\", \"show error count per hour\", \"at what time is traffic highest?\".\n\nIf the intent is ambiguous (e.g. \"show latency over 24h\" could mean either), ask the user to clarify before calling this tool.\n\nIMPORTANT: If the question has ANY temporal component (spike, trend, change over time, \"when did X happen\"), always use \"time_series\" — it answers both the count AND the timing in one call. Never call this tool twice for the same question.\nExample: \"get error count and find when it spiked\" → \"time_series\".")),
		mcp.WithString("stepInterval", mcp.Description("Time bucket size in seconds for time_series mode (optional). When omitted, the backend auto-selects an appropriate interval. Only set this if the user explicitly requests a specific granularity. Examples: \"60\" (1 min), \"3600\" (1 hour), \"86400\" (1 day).")),
	)

	s.AddTool(aggregateLogsTool, h.handleAggregateLogs)

	// search_logs: log search with optional filters
	// ToDo: use this function for error logs or logs by service
	searchLogsTool := mcp.NewTool("signoz_search_logs",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("Search logs with flexible filtering. Supports free-form query expressions, optional service/severity filters, and body text search. "+
			"Use service param to scope to a single service, severity param for error-only queries (e.g., severity='ERROR'), or query param for any filter expression. "+
			"Defaults to last 1 hour if no time specified."),
		mcp.WithString("query", mcp.Description("Free-form filter expression using SigNoz search syntax. Examples: \"service.name = 'payment-svc' AND http.status_code >= 400\", \"workflow_run_id = 'wr_123'\", \"body CONTAINS 'timeout'\". Supports any log field/attribute.")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by.")),
		mcp.WithString("severity", mcp.Description("Optional severity filter (DEBUG, INFO, WARN, ERROR, FATAL).")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body (uses CONTAINS matching).")),
		mcp.WithString("timeRange", mcp.Description("Time range string. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h', '7d'. Defaults to '1h'.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(searchLogsTool, h.handleSearchLogs)
}

func (h *Handler) handleListLogViews(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_list_log_views")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListLogViews(ctx)
	if err != nil {
		log.Error("Failed to list log views", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var logViews map[string]any
	if err := json.Unmarshal(result, &logViews); err != nil {
		log.Error("Failed to parse log views response", zap.Error(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}

	data, ok := logViews["data"].([]any)
	if !ok {
		log.Error("Invalid log views response format", zap.Any("data", logViews["data"]))
		return mcp.NewToolResultError("invalid response format: expected data array"), nil
	}

	total := len(data)
	pagedData := paginate.Array(data, offset, limit)

	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		log.Error("Failed to wrap log views with pagination", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetLogView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	viewID, ok := req.Params.Arguments.(map[string]any)["viewId"].(string)
	if !ok {
		log.Warn("Invalid viewId parameter type", zap.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" must be a string. Example: {"viewId": "error-logs-view-123"}`), nil
	}
	if viewID == "" {
		log.Warn("Empty viewId parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Provide a valid log view ID. Use signoz_list_log_views tool to see available log views.`), nil
	}

	log.Debug("Tool called: signoz_get_log_view", zap.String("viewId", viewID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.GetLogView(ctx, viewID)
	if err != nil {
		log.Error("Failed to get log view", zap.String("viewId", viewID), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleGetLogsForAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args := req.Params.Arguments.(map[string]any)

	alertID, ok := args["alertId"].(string)
	if !ok || alertID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "alertId" must be a non-empty string. Example: {"alertId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "1h", "limit": "50"}`), nil
	}

	timeRange := "1h"
	if tr, ok := args["timeRange"].(string); ok && tr != "" {
		timeRange = tr
	}

	limit := 100
	if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
		if limitInt, err := strconv.Atoi(limitStr); err == nil {
			limit = limitInt
		}
	}

	_, offset := paginate.ParseParams(req.Params.Arguments)

	log.Debug("Tool called: signoz_get_logs_for_alert", zap.String("alertId", alertID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	alertData, err := client.GetAlertByRuleID(ctx, alertID)
	if err != nil {
		log.Error("Failed to get alert details", zap.String("alertId", alertID), zap.Error(err))
		return mcp.NewToolResultError("failed to get alert details: " + err.Error()), nil
	}

	var alertResponse map[string]interface{}
	if err := json.Unmarshal(alertData, &alertResponse); err != nil {
		log.Error("Failed to parse alert data", zap.Error(err))
		return mcp.NewToolResultError("failed to parse alert data: " + err.Error()), nil
	}

	serviceName := ""
	if data, ok := alertResponse["data"].(map[string]interface{}); ok {
		if labels, ok := data["labels"].(map[string]interface{}); ok {
			if service, ok := labels["service_name"].(string); ok {
				serviceName = service
			} else if service, ok := labels["service"].(string); ok {
				serviceName = service
			}
		}
	}

	now := time.Now()
	startTime := now.Add(-1 * time.Hour).UnixMilli()
	endTime := now.UnixMilli()

	if duration, err := timeutil.ParseTimeRange(timeRange); err == nil {
		startTime = now.Add(-duration).UnixMilli()
	}

	filterExpression := "severity_text IN ('ERROR', 'WARN', 'FATAL')"
	if serviceName != "" {
		filterExpression += fmt.Sprintf(" AND service.name in ['%s']", serviceName)
	}

	queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		log.Error("Failed to marshal query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		log.Error("Failed to get logs for alert", zap.String("alertId", alertID), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleAggregateLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
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
		log.Error("Failed to marshal aggregate query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	log.Debug("Tool called: signoz_aggregate_logs",
		zap.String("aggregation", reqData.AggregationExpr),
		zap.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		log.Error("Failed to aggregate logs", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleSearchLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
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
		log.Error("Failed to marshal search query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	log.Debug("Tool called: signoz_search_logs",
		zap.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		log.Error("Failed to search logs", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}
