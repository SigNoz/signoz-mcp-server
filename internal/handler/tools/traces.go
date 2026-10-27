package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// Row shapes of signoz_search_traces, sent as the query_range requestType.
const (
	requestTypeRaw   = string(apitypes.Querybuildertypesv5RequestTypeRaw)
	requestTypeTrace = string(apitypes.Querybuildertypesv5RequestTypeTrace)
)

func (h *Handler) RegisterTracesHandlers(s *mcp.Server) {
	h.logger.Debug("Registering traces handlers")

	// aggregate_traces: compute statistics over traces with GROUP BY
	aggregateTracesTool := mcp.NewTool("signoz_aggregate_traces",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants custom aggregate statistics over spans (counts, rates, latency percentiles, grouped/top-N breakdowns, or time series) rather than individual span rows or a full trace hierarchy. For the built-in operation table for one traced service, ranked by p99, use signoz_get_service_top_operations. Use signoz_search_traces for raw spans, trace-ID discovery, or one row per trace (such as the most expensive AI traces), and signoz_get_trace_details for one known trace ID. Before calling, read signoz://traces/query-builder-guide; discover unfamiliar workspace fields with signoz_get_field_keys. Defaults to the last 1 hour."),
		mcp.WithString("aggregations", mcp.Required(), stringOrStringArrayType(), mcp.Description(tracesAggregationsParamDescription)),
		mcp.WithString("groupBy", stringOrStringArrayType(), mcp.Description(tracesGroupByParamDescription)),
		mcp.WithString("having", mcp.Description(havingParamDescription)),
		mcp.WithString("filter", mcp.Description(aggregateTracesFilterParamDescription)),
		mcp.WithString("service", mcp.Description(aggregateTracesServiceParamDescription)),
		mcp.WithString("operation", mcp.Description(aggregateTracesOperationParamDescription)),
		mcp.WithBoolean("error", boolOrStringType(), mcp.Description(aggregateTracesErrorParamDescription)),
		mcp.WithString("minDuration", mcp.Description(minDurationParamDescription)),
		mcp.WithString("maxDuration", mcp.Description(maxDurationParamDescription)),
		mcp.WithString("orderBy", mcp.Description(aggregateTracesOrderByParamDescription)),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description(aggregateLimitParamDescription)),
		functionsProperty(),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("requestType", mcp.DefaultString("scalar"), mcp.Enum("scalar", "time_series"), mcp.Description(aggregateRequestTypeParamDescription)),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description(stepIntervalParamDescription)),
	)

	h.addTool(s, aggregateTracesTool, h.handleAggregateTraces)

	searchTracesTool := mcp.NewTool("signoz_search_traces",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants individual span rows matching service, operation, error, duration, or field filters, needs to discover trace IDs, or wants one row per trace (requestType=trace), such as the slowest traces or the most expensive AI traces. It returns paginated rows, not aggregate trends/groups or a full trace hierarchy; use signoz_aggregate_traces for statistics and signoz_get_trace_details for one known trace ID. Span rows carry a default set of span fields; name other fields in selectFields. A field missing from a row was not selected, so do not read it as absent from the span. Read signoz://traces/query-builder-guide before using unfamiliar workspace fields. To search AI traces, including by per-trace cost, token, or call counts, set queryType to builder_ai_query. Defaults to the last 1 hour."),
		mcp.WithString("filter", mcp.Description(searchTracesFilterParamDescription)),
		mcp.WithString("service", mcp.Description(searchTracesServiceParamDescription)),
		mcp.WithString("operation", mcp.Description(searchTracesOperationParamDescription)),
		mcp.WithBoolean("error", boolOrStringType(), mcp.Description(searchTracesErrorParamDescription)),
		mcp.WithString("minDuration", mcp.Description(minDurationParamDescription)),
		mcp.WithString("maxDuration", mcp.Description(maxDurationParamDescription)),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultRawQueryLimit)), intOrStringType(), mcp.Description(searchTracesLimitParamDescription)),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description(searchTracesOffsetParamDescription)),
		mcp.WithString("selectFields", stringOrStringArrayType(), mcp.Description(tracesSelectFieldsParamDescription)),
		mcp.WithString("orderBy", mcp.Description(searchTracesOrderByParamDescription)),
		mcp.WithString("queryType", mcp.DefaultString(querybuilder.QueryTypeBuilder), mcp.Enum(querybuilder.QueryTypeBuilder, querybuilder.QueryTypeBuilderAI), mcp.Description(searchTracesQueryTypeParamDescription)),
		mcp.WithString("requestType", mcp.DefaultString(requestTypeRaw), mcp.Enum(requestTypeRaw, requestTypeTrace), mcp.Description(searchTracesRequestTypeParamDescription)),
	)

	h.addTool(s, searchTracesTool, h.handleSearchTraces)

	getTraceDetailsTool := mcp.NewTool("signoz_get_trace_details",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user already has a known trace ID and wants that trace's spans, metadata, and hierarchy. If the ID is unknown, discover it with signoz_search_traces first. Supply a time window containing the trace; the default last 6 hours can miss an older trace. Do not use this for filtering many spans or aggregate analysis."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description(traceIDParamDescription)),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeParamDescription("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(traceDetailsStartParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(traceDetailsEndParamDescription)),
		mcp.WithBoolean("includeSpans", boolOrStringType(), mcp.Description(includeSpansParamDescription)),
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

	queryPayload, err := querybuilder.Aggregate(aggregateSpec("traces", reqData))
	if err != nil {
		return InternalErrorResult("failed to build query payload: " + err.Error()), nil
	}

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal aggregate traces query payload", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_aggregate_traces",
		slog.String("aggregation", reqData.AggregationExpr),
		slog.String("filter", reqData.FilterExpression))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to aggregate traces", err)
		return upstreamQueryError(err, "traces"), nil
	}

	return aggregateResult(ctx, h.logger, "signoz_aggregate_traces", result, reqData.LimitClamped, aggregateNotes(reqData)...), nil
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

	// Out-of-enum requestType and queryType values run best-effort (span rows,
	// builder_query) and the schema validation notice flags them.
	build := querybuilder.SpanSearch
	if reqData.RequestType == requestTypeTrace {
		build = querybuilder.TraceList
	}
	queryPayload, err := build(reqData.StartTime, reqData.EndTime, querybuilder.TraceSearchOptions{
		Filter:       reqData.FilterExpression,
		Limit:        reqData.Limit,
		Offset:       reqData.Offset,
		SelectFields: reqData.SelectFields,
		QueryType:    reqData.QueryType,
		OrderExpr:    reqData.OrderExpr,
		OrderDir:     reqData.OrderDir,
	})
	if err != nil {
		return InternalErrorResult("failed to build query payload: " + err.Error()), nil
	}

	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal query payload", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal query payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_search_traces",
		slog.String("requestType", reqData.RequestType),
		slog.String("queryType", reqData.QueryType),
		slog.String("filter", reqData.FilterExpression))

	client, base, clientErr := h.getResourceClient(ctx)
	if clientErr != nil {
		return clientErr, nil
	}
	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to search traces", err)
		return upstreamQueryError(err, "traces"), nil
	}

	result = h.enrichSearchTracesWebURL(ctx, base, result)
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
	client, base, clientErr := h.getResourceClient(ctx)
	if clientErr != nil {
		return clientErr, nil
	}
	result, err := client.GetTraceDetails(ctx, traceID, includeSpans, startTime, endTime)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get trace details", err, slog.String("traceId", traceID))
		return upstreamError(err), nil
	}
	result = h.enrichTraceWebURL(ctx, base, result, traceID)
	return structuredResult(result), nil
}

// enrichTraceWebURL injects a webUrl deep link into a single-trace passthrough
// body. Delegates to util.InjectWebURL, which preserves large int64 fields
// (e.g. duration_nano) and fails open on unparseable input.
func (h *Handler) enrichTraceWebURL(ctx context.Context, base string, data []byte, traceID string) []byte {
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
func (h *Handler) enrichSearchTracesWebURL(ctx context.Context, base string, data []byte) []byte {
	if base == "" {
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
