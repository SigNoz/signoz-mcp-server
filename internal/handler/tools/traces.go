package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const tracesFilterParamDescription = "Filter expression using SigNoz search syntax (see signoz://traces/query-builder-guide). Combine conditions with AND, OR, and parentheses for precedence. Unknown keys hard-error; keys present in multiple contexts default to resource context. Disambiguate with attribute.<key>, resource.<key>, or span.<key>. Discover valid keys with signoz_get_field_keys, then confirm values with signoz_get_field_values, before filtering. Examples: \"service.name = 'payment-svc' AND has_error = true\", \"http_method = 'POST' AND (has_error = true OR duration_nano > 1000000000)\"."

func (h *Handler) RegisterTracesHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering traces handlers")

	// aggregate_traces: compute statistics over traces with GROUP BY
	aggregateTracesTool := mcp.NewTool("signoz_aggregate_traces",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants custom aggregate statistics over spans—counts, rates, latency percentiles, grouped/top-N breakdowns, or time series—not individual span rows or a full trace hierarchy. For the built-in operation table for one traced service, ranked by p99, use signoz_get_service_top_operations. Use signoz_search_traces for raw spans or trace-ID discovery, and signoz_get_trace_details for one known trace ID. Before calling, read signoz://traces/query-builder-guide; discover unfamiliar workspace fields with signoz_get_field_keys. Defaults to the last 1 hour."),
		mcp.WithString("aggregation", mcp.Required(), mcp.Description("Aggregation function to apply. One of: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate")),
		mcp.WithString("aggregateOn", mcp.Description("Field name to aggregate on (e.g., 'duration_nano'). Required for all aggregations except count and rate.")),
		mcp.WithString("groupBy", mcp.Description("Comma-separated list of field names to group results by (e.g., 'service.name' or 'service.name, name'). Leave empty for a single aggregate value.")),
		mcp.WithString("filter", mcp.Description(tracesFilterParamDescription+" Combined with service/operation/error/duration params using AND.")),
		mcp.WithString("service", mcp.Description("Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter.")),
		mcp.WithString("operation", mcp.Description("Shortcut filter for span/operation name. Equivalent to adding name = '<value>' to filter.")),
		mcp.WithBoolean("error", boolOrStringType(), mcp.Description("Shortcut filter for error spans (true or false). Equivalent to adding has_error = true/false to filter.")),
		mcp.WithString("minDuration", mcp.Description("Minimum span duration in nanoseconds. Example: '500000000' for 500ms.")),
		mcp.WithString("maxDuration", mcp.Description("Maximum span duration in nanoseconds. Example: '2000000000' for 2s.")),
		mcp.WithString("orderBy", mcp.Description("How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration_nano) asc'. Defaults to the aggregation expression descending.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(types.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description("Maximum number of groups to return (default: 100, max: 10000; higher values are clamped). For time_series queries, groups are ranked across the entire time range, so a short-lived spike can fall outside the selected top groups.")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("requestType", mcp.DefaultString("scalar"), mcp.Enum("scalar", "time_series"), mcp.Description(aggregateRequestTypeDescription)),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description(stepIntervalDesc)),
	)

	h.addTool(s, aggregateTracesTool, h.handleAggregateTraces)

	searchTracesTool := mcp.NewTool("signoz_search_traces",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants individual raw span rows matching service, operation, error, duration, or field filters, or needs to discover trace IDs. It returns paginated spans, not aggregate trends/groups or a full trace hierarchy; use signoz_aggregate_traces for statistics and signoz_get_trace_details for one known trace ID. Read signoz://traces/query-builder-guide before using unfamiliar workspace fields. Defaults to the last 1 hour."),
		mcp.WithString("filter", mcp.Description(tracesFilterParamDescription+" Combined with shortcut params using AND.")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by.")),
		mcp.WithString("operation", mcp.Description("Operation/span name to filter by.")),
		mcp.WithBoolean("error", boolOrStringType(), mcp.Description("Filter by error status (true or false).")),
		mcp.WithString("minDuration", mcp.Description("Minimum span duration in nanoseconds. Example: '500000000' for 500ms.")),
		mcp.WithString("maxDuration", mcp.Description("Maximum span duration in nanoseconds. Example: '2000000000' for 2s.")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(types.DefaultRawQueryLimit)), intOrStringType(), mcp.Description("Maximum number of span rows to return (default: 100, max: 10000; higher values are clamped — paginate with offset).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of span rows to skip for pagination (default: 0).")),
	)

	h.addTool(s, searchTracesTool, h.handleSearchTraces)

	getTraceDetailsTool := mcp.NewTool("signoz_get_trace_details",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user already has a known trace ID and wants that trace's spans, metadata, and hierarchy. If the ID is unknown, discover it with signoz_search_traces first. Supply a time window containing the trace; the default last 6 hours can miss an older trace. Do not use this for filtering many spans or aggregate analysis."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Known trace ID to retrieve. Discover it with signoz_search_traces when the user has not supplied one.")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithBoolean("includeSpans", boolOrStringType(), mcp.Description("Include detailed span information (default: true).")),
	)

	h.addTool(s, getTraceDetailsTool, h.handleGetTraceDetails)
}

func (h *Handler) handleAggregateTraces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	reqData, err := parseAggregateTracesArgs(args)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	if reqData.StepIntervalWarning != "" {
		h.logger.WarnContext(ctx, "aggregate_traces stepInterval dropped", slog.String("reason", reqData.StepIntervalWarning))
	}

	queryPayload := types.BuildAggregateQueryPayload("traces",
		reqData.StartTime, reqData.EndTime, reqData.AggregationExpr,
		reqData.FilterExpression, reqData.GroupBy,
		reqData.OrderExpr, reqData.OrderDir, reqData.Limit,
		reqData.RequestType, reqData.StepInterval,
	)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal aggregate traces query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_aggregate_traces",
		slog.String("aggregation", reqData.AggregationExpr),
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to aggregate traces", err)
		return upstreamQueryError(err, "traces"), nil
	}

	return aggregateResult(ctx, h.logger, "signoz_aggregate_traces", result, reqData.LimitClamped), nil
}

func (h *Handler) handleSearchTraces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	reqData, err := parseSearchTracesArgs(args)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	queryPayload := types.BuildTracesQueryPayload(reqData.StartTime, reqData.EndTime, reqData.FilterExpression, reqData.Limit, reqData.Offset)

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_search_traces",
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to search traces", err)
		return upstreamQueryError(err, "traces"), nil
	}

	result = h.enrichSearchTracesWebURL(ctx, result)
	return rawSearchResult(ctx, h.logger, "signoz_search_traces", result, reqData.Limit, reqData.Offset, reqData.LimitClamped), nil
}

func (h *Handler) handleGetTraceDetails(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	traceID, errResult := requireStringArg(args, "traceId")
	if errResult != nil {
		return errResult, nil
	}

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

	includeSpans := true
	if v, present, err := parseBoolArg(args, "includeSpans"); err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	} else if present {
		includeSpans = v
	}

	var startTime, endTime int64
	if err := json.Unmarshal([]byte(start), &startTime); err != nil {
		return validationErrorf("start", `invalid timestamp format: %s. Use "timeRange" instead (e.g., "1h", "24h")`, start), nil
	}
	if err := json.Unmarshal([]byte(end), &endTime); err != nil {
		return validationErrorf("end", `invalid timestamp format: %s. Use "timeRange" instead (e.g., "1h", "24h")`, end), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_trace_details", slog.String("traceId", traceID), slog.Bool("includeSpans", includeSpans), slog.String("start", start), slog.String("end", end))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetTraceDetails(ctx, traceID, includeSpans, startTime, endTime)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get trace details", err, slog.String("traceId", traceID))
		return upstreamError(err), nil
	}
	result = enrichTraceWebURL(ctx, result, traceID)
	return structuredResult(result), nil
}

// enrichTraceWebURL injects a webUrl deep link into a single-trace passthrough
// body. Delegates to util.InjectWebURL, which preserves large int64 fields
// (e.g. duration_nano) and fails open on unparseable input.
func enrichTraceWebURL(ctx context.Context, data []byte, traceID string) []byte {
	base, _ := util.GetSigNozURL(ctx)
	return util.InjectWebURL(data, base, "trace", traceID)
}

// enrichSearchTracesWebURL injects a per-row webUrl deep link into a search
// traces passthrough body, one per result row keyed off each row's trace_id
// (with legacy traceID/traceId fallbacks during migration).
// Delegates to util.InjectRowsWebURL, which preserves large int64 fields
// (e.g. duration_nano) and fails open on unparseable input.
//
// Enrichment is fail-open, so a change to the upstream response would silently
// stop producing links. The handler only runs on a 2xx /api/v5/query_range body
// (doRequest errors on non-2xx), so anything we can't walk is a real anomaly,
// not an error response. We WARN on all three drift modes so the silent
// degradation is detectable: envelope drift (results[] not reachable), rows-key
// drift (result objects present but no readable rows[] array), and column-alias
// drift (rows present but none enrichable). An ordinary empty result stays silent.
func (h *Handler) enrichSearchTracesWebURL(ctx context.Context, data []byte) []byte {
	base, ok := util.GetSigNozURL(ctx)
	if !ok || base == "" {
		return data // no instance URL on the request — nothing to enrich, nothing to warn about
	}

	out, res := util.InjectRowsWebURL(data, base, "trace", "trace_id", "traceID", "traceId")
	switch {
	case !res.ResultsReached:
		h.logger.WarnContext(ctx,
			"search_traces webUrl enrichment could not locate results[] in the v5 response; the upstream response envelope may have changed")
	case res.ResultCount > 0 && res.RowsArraysReached == 0:
		h.logger.WarnContext(ctx,
			"search_traces webUrl enrichment found result objects but no readable rows[] array; the per-result rows key may have changed",
			slog.Int("resultCount", res.ResultCount))
	case res.RowsSeen > res.RowsEnriched:
		h.logger.WarnContext(ctx,
			"search_traces webUrl enrichment found rows without supported trace id columns trace_id/traceID/traceId",
			slog.Int("rowsSeen", res.RowsSeen),
			slog.Int("rowsEnriched", res.RowsEnriched))
	}
	return out
}
