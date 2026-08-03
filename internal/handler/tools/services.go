package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants APM services with trace activity and their call or latency summaries in a time window. It returns paginated traced-service records; absence means no trace activity in that window, not that a matching service.name never appears in logs. For log values use signoz_get_field_values with signal=\"logs\" and name=\"service.name\"; for one service's operations use signoz_get_service_top_operations. Follow pagination.nextOffset until hasMore=false before concluding a traced service is absent."),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum services per page. Default: 50; max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of services to skip. Default: 0; use pagination.nextOffset for the next page.")),
	)

	h.addTool(s, listTool, h.handleListServices)

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants the built-in operation table for one traced service in a time window. It ranks operation names by p99 latency and returns p50, p95, p99, call count, and error count. Use signoz_list_services to discover active traced service names. For custom aggregation, grouping, time series, cross-service comparison, or arbitrary trace filters, use signoz_aggregate_traces instead. The optional tags parameter is a JSON-encoded TagQueryParam array."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Exact traced service name, typically from signoz_list_services.")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("tags", mcp.Description("JSON-encoded TagQueryParam array; omit for no tag filter. Example: [{\"key\":\"http.method\",\"tagType\":\"SpanAttribute\",\"operator\":\"In\",\"stringValues\":[\"GET\"]}]. Pass the array as a string, not as a JSON array value.")),
	)

	h.addTool(s, getOpsTool, h.handleGetServiceTopOperations)

	serviceMapTool := mcp.NewTool("signoz_get_service_map",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants the service dependency graph — which services call which — behind SigNoz's Service Map. It returns one record per caller/callee edge with call count, call rate, error rate, and latency quantiles, so you can trace a failure to a downstream service. Filter to one service with \"service\" plus \"direction\" to get just its callers or callees. Use signoz_list_services for the flat service list and signoz_get_service_top_operations for one service's operations. Edges come from a per-minute rollup table, so very narrow windows can legitimately return nothing; widen timeRange before concluding a dependency does not exist."),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("service", mcp.Description("Optional exact service name to filter edges to, typically from signoz_list_services. Omit for the full graph.")),
		mcp.WithString("direction", mcp.DefaultString("both"), mcp.Description("Which edges to keep relative to \"service\": \"downstream\" (services it calls), \"upstream\" (services that call it), or \"both\" (default). Requires \"service\"; ignored otherwise.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum edges per page. Default: 50; max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of edges to skip. Default: 0; use pagination.nextOffset for the next page.")),
		mcp.WithString("tags", mcp.Description("JSON-encoded TagQueryParam array applied server-side before the graph is built; omit for no tag filter. Example: [{\"key\":\"deployment.environment\",\"tagType\":\"ResourceAttribute\",\"operator\":\"In\",\"stringValues\":[\"prod\"]}]. Pass the array as a string, not as a JSON array value.")),
	)

	h.addTool(s, serviceMapTool, h.handleGetServiceMap)
}

// serviceMapDirections enumerates the accepted "direction" values so an invalid
// one fails validation instead of silently degrading to an unfiltered graph.
var serviceMapDirections = map[string]bool{"both": true, "upstream": true, "downstream": true}

func (h *Handler) handleGetServiceMap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	service, _ := args["service"].(string)

	direction := "both"
	if d, ok := args["direction"].(string); ok && d != "" {
		direction = d
	}
	if !serviceMapDirections[direction] {
		return validationError("direction", "must be one of \"both\", \"upstream\", or \"downstream\""), nil
	}
	// direction only means something relative to a named service. Silently
	// ignoring it would let an agent believe it received a filtered graph.
	if direction != "both" && service == "" {
		return validationError("direction", "requires \"service\" to be set; direction is relative to one service"), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	// tags is passed through to the SigNoz API verbatim, matching
	// signoz_get_service_top_operations: the backend expects a structured
	// []TagQueryParam array, so the caller supplies that raw JSON.
	var tags json.RawMessage
	if t, ok := args["tags"].(string); ok && t != "" {
		tags = json.RawMessage(t)
	} else {
		tags = json.RawMessage("[]")
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_service_map",
		slog.String("start", start),
		slog.String("end", end),
		slog.String("service", service),
		slog.String("direction", direction))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetServiceMap(ctx, start, end, tags)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get service map", err, slog.String("start", start), slog.String("end", end))
		return upstreamError(err), nil
	}

	edges, errResult := h.parseServiceMapEdges(ctx, result)
	if errResult != nil {
		return errResult, nil
	}

	if service != "" {
		edges = filterServiceMapEdges(edges, service, direction)
	}

	total := len(edges)
	resultJSON, err := paginate.Wrap(paginate.Array(edges, offset, limit), total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap service map with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

// parseServiceMapEdges decodes /api/v1/dependency_graph, which is an
// undocumented internal endpoint. It currently answers with a bare JSON array
// (the legacy WriteJSON path), but the newer render.Success helper used
// elsewhere in SigNoz wraps payloads as {"status":..,"data":..}. Accept both and
// WARN on the wrapped form so upstream drift produces a signal rather than an
// empty result the caller would read as "no dependencies".
func (h *Handler) parseServiceMapEdges(ctx context.Context, raw json.RawMessage) ([]any, *mcp.CallToolResult) {
	var edges []any
	if err := json.Unmarshal(raw, &edges); err == nil {
		return edges, nil
	}

	var wrapped struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse service map response", logpkg.ErrAttr(err))
		return nil, upstreamResponseError("failed to parse response: " + err.Error())
	}

	h.logger.WarnContext(ctx, "Service map response was wrapped in a status/data envelope; /api/v1/dependency_graph previously returned a bare array. Upstream response shape may have changed.",
		slog.Int("edges", len(wrapped.Data)))
	return wrapped.Data, nil
}

// filterServiceMapEdges keeps only edges touching service, in the requested
// direction. Edges whose parent/child are not strings are dropped rather than
// guessed at.
func filterServiceMapEdges(edges []any, service, direction string) []any {
	filtered := make([]any, 0, len(edges))
	for _, edge := range edges {
		m, ok := edge.(map[string]any)
		if !ok {
			continue
		}
		parent, _ := m["parent"].(string)
		child, _ := m["child"].(string)

		var keep bool
		switch direction {
		case "downstream":
			keep = parent == service
		case "upstream":
			keep = child == service
		default:
			keep = parent == service || child == service
		}
		if keep {
			filtered = append(filtered, edge)
		}
	}
	return filtered
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	h.logger.DebugContext(ctx, "Tool called: signoz_list_services", slog.String("start", start), slog.String("end", end), slog.Int("limit", limit), slog.Int("offset", offset))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.ListServices(ctx, start, end)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list services", err, slog.String("start", start), slog.String("end", end))
		return upstreamError(err), nil
	}

	var services []any
	if err := json.Unmarshal(result, &services); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse services response", logpkg.ErrAttr(err))
		return upstreamResponseError("failed to parse response: " + err.Error()), nil
	}

	if base, hasURL := util.GetSigNozURL(ctx); hasURL {
		for _, item := range services {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["serviceName"].(string)
			if webURL, ok := util.ResourceWebURL(base, "service", name); ok {
				m["webUrl"] = webURL
			}
		}
	}

	total := len(services)
	pagedServices := paginate.Array(services, offset, limit)

	resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap services with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleGetServiceTopOperations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	service, errResult := requireStringArg(args, "service")
	if errResult != nil {
		h.logger.WarnContext(ctx, "Invalid service parameter", slog.Any("type", args["service"]))
		return errResult, nil
	}

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)

	// tags is passed through to the SigNoz API verbatim. The backend's
	// /api/v1/service/top_operations expects a structured []TagQueryParam array,
	// so the caller supplies that raw JSON; an absent/non-string value defaults
	// to an empty filter. (A friendlier typed-tags schema is tracked as a follow-up.)
	var tags json.RawMessage
	if t, ok := args["tags"].(string); ok && t != "" {
		tags = json.RawMessage(t)
	} else {
		tags = json.RawMessage("[]")
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_service_top_operations",
		slog.String("start", start),
		slog.String("end", end),
		slog.String("service", service))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get service top operations", err, slog.String("start", start), slog.String("end", end), slog.String("service", service))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
