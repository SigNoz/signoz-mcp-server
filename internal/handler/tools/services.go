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
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListServices(ctx, start, end)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list services", err, slog.String("start", start), slog.String("end", end))
		return upstreamError(err), nil
	}

	var services []any
	if err := json.Unmarshal(result, &services); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse services response", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
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
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get service top operations", err, slog.String("start", start), slog.String("end", end), slog.String("service", service))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
