package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterTracesHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering traces handlers")

	// aggregate_traces: compute statistics over traces with GROUP BY
	aggregateTracesTool := mcp.NewTool("signoz_aggregate_traces",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Aggregate traces to compute statistics like count, average, sum, min, max, or percentiles over spans, optionally grouped by fields. "+
			"Use this for questions like 'p99 latency by service', 'error count per operation', 'request rate by endpoint', 'average duration by span kind'. "+
			"Also use this for error analysis — set error='true' and groupBy='service.name' to analyze error patterns across services. "+
			"Defaults to last 1 hour if no time specified."),
		mcp.WithString("aggregation", mcp.Required(), mcp.Description("Aggregation function to apply. One of: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate")),
		mcp.WithString("aggregateOn", mcp.Description("Field name to aggregate on (e.g., 'durationNano'). Required for all aggregations except count and rate.")),
		mcp.WithString("groupBy", mcp.Description("Comma-separated list of field names to group results by (e.g., 'service.name' or 'service.name, name'). Leave empty for a single aggregate value.")),
		mcp.WithString("filter", mcp.Description("Filter expression using SigNoz search syntax (e.g., \"hasError = true AND httpMethod = 'GET'\"). Combined with service/operation/error/duration params using AND.")),
		mcp.WithString("service", mcp.Description("Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter.")),
		mcp.WithString("operation", mcp.Description("Shortcut filter for span/operation name. Equivalent to adding name = '<value>' to filter.")),
		mcp.WithString("error", mcp.Description("Shortcut filter for error spans ('true' or 'false'). Equivalent to adding hasError = true/false to filter.")),
		mcp.WithString("minDuration", mcp.Description("Minimum span duration in nanoseconds. Example: '500000000' for 500ms.")),
		mcp.WithString("maxDuration", mcp.Description("Maximum span duration in nanoseconds. Example: '2000000000' for 2s.")),
		mcp.WithString("orderBy", mcp.Description("How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(durationNano) asc'. Defaults to the aggregation expression descending.")),
		mcp.WithString("limit", mcp.Description("Maximum number of groups to return (default: 10)")),
		mcp.WithString("timeRange", mcp.Description("Time range string. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h', '7d'. Defaults to '1h'.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("requestType", mcp.Description("Controls whether to return a single aggregate or a time-series. Choose based on the user's question — do NOT ask the user to set this.\n\n\"scalar\" (default) — Returns one aggregate value computed over the entire time range. Use when the answer is a single number or a ranked/grouped table: \"how many errors today?\", \"what is the p99 latency of checkout?\", \"which service has the most errors?\", \"top 10 slowest endpoints\".\n\n\"time_series\" — Returns one value per time bucket so you can see changes over time. Use ONLY when the user's question is about WHEN something happened, HOW a metric changed, or to find SPIKES/TRENDS across time: \"when did errors spike?\", \"how did p99 change hour by hour?\", \"show error count per hour\", \"at what time is traffic highest?\".\n\nIf the intent is ambiguous (e.g. \"show latency over 24h\" could mean either), ask the user to clarify before calling this tool.\n\nIMPORTANT: If the question has ANY temporal component (spike, trend, change over time, \"when did X happen\"), always use \"time_series\" — it answers both the count AND the timing in one call. Never call this tool twice for the same question.\nExample: \"get error count and find when it spiked\" → \"time_series\".")),
		mcp.WithString("stepInterval", mcp.Description("Time bucket size in seconds for time_series mode (optional). When omitted, the backend auto-selects an appropriate interval. Only set this if the user explicitly requests a specific granularity. Examples: \"60\" (1 min), \"3600\" (1 hour), \"86400\" (1 day).")),
	)

	s.AddTool(aggregateTracesTool, h.handleAggregateTraces)

	searchTracesTool := mcp.NewTool("signoz_search_traces",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Search traces with flexible filtering. Supports free-form query expressions, optional service/operation/error filters, and duration filtering. "+
			"Use service param to scope to a single service, or query param for any filter expression. "+
			"Defaults to last 1 hour if no time specified."),
		mcp.WithString("query", mcp.Description("Free-form filter expression using SigNoz search syntax. Examples: \"service.name = 'payment-svc' AND hasError = true\", \"httpMethod = 'POST' AND responseStatusCode >= 500\". Combined with shortcut params using AND.")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by.")),
		mcp.WithString("operation", mcp.Description("Operation/span name to filter by.")),
		mcp.WithString("error", mcp.Description("Filter by error status ('true' or 'false').")),
		mcp.WithString("minDuration", mcp.Description("Minimum span duration in nanoseconds. Example: '500000000' for 500ms.")),
		mcp.WithString("maxDuration", mcp.Description("Maximum span duration in nanoseconds. Example: '2000000000' for 2s.")),
		mcp.WithString("timeRange", mcp.Description("Time range string. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h', '7d'. Defaults to '1h'.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, overridden by timeRange)")),
		mcp.WithString("limit", mcp.Description("Maximum number of traces to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(searchTracesTool, h.handleSearchTraces)

	getTraceDetailsTool := mcp.NewTool("signoz_get_trace_details",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Get comprehensive trace information including all spans, metadata, and span hierarchy/relationships. Defaults to last 6 hours if no time specified."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Trace ID to get details for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("includeSpans", mcp.Description("Include detailed span information (true/false, default: true)")),
	)

	s.AddTool(getTraceDetailsTool, h.handleGetTraceDetails)
}

func (h *Handler) handleAggregateTraces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}

	reqData, err := parseAggregateTracesArgs(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	queryPayload := types.BuildAggregateQueryPayload("traces",
		reqData.StartTime, reqData.EndTime, reqData.AggregationExpr,
		reqData.FilterExpression, reqData.GroupBy,
		reqData.OrderExpr, reqData.OrderDir, reqData.Limit,
		reqData.RequestType, reqData.StepInterval,
	)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		log.Error("Failed to marshal aggregate traces query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	log.Debug("Tool called: signoz_aggregate_traces",
		zap.String("aggregation", reqData.AggregationExpr),
		zap.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		log.Error("Failed to aggregate traces", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleSearchTraces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}

	reqData, err := parseSearchTracesArgs(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	queryPayload := types.BuildTracesQueryPayload(reqData.StartTime, reqData.EndTime, reqData.FilterExpression, reqData.Limit)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		log.Error("Failed to marshal query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	log.Debug("Tool called: signoz_search_traces",
		zap.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		log.Error("Failed to search traces", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleGetTraceDetails(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args := req.Params.Arguments.(map[string]any)

	traceID, ok := args["traceId"].(string)
	if !ok || traceID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "traceId" must be a non-empty string. Example: {"traceId": "abc123def456", "includeSpans": "true", "timeRange": "1h"}`), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

	includeSpans := true
	if includeStr, ok := args["includeSpans"].(string); ok && includeStr != "" {
		includeSpans = includeStr == "true"
	}

	var startTime, endTime int64
	if err := json.Unmarshal([]byte(start), &startTime); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
	}
	if err := json.Unmarshal([]byte(end), &endTime); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
	}

	log.Debug("Tool called: signoz_get_trace_details", zap.String("traceId", traceID), zap.Bool("includeSpans", includeSpans), zap.String("start", start), zap.String("end", end))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetTraceDetails(ctx, traceID, includeSpans, startTime, endTime)
	if err != nil {
		log.Error("Failed to get trace details", zap.String("traceId", traceID), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
