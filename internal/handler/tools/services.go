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
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List all services in SigNoz. Defaults to last 6 hours if no time specified. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific service, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago). Other magnitudes (seconds/micros/nanos) are auto-detected, so legacy nanosecond values still work.")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds (optional, defaults to now). Other magnitudes (seconds/micros/nanos) are auto-detected.")),
		mcp.WithString("limit", mcp.DefaultString("50"), mcp.Description("Maximum number of services to return per page. Use this to paginate through large result sets. Default: 50, max: 1000 (higher values are clamped). Must be greater than 0.")),
		mcp.WithString("offset", mcp.DefaultString("0"), mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	addTool(s, listTool, h.handleListServices)

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get top operations for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago). Other magnitudes (seconds/micros/nanos) are auto-detected, so legacy nanosecond values still work.")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds (optional, defaults to now). Other magnitudes (seconds/micros/nanos) are auto-detected.")),
		mcp.WithString("tags", mcp.Description("Optional tag filters as a raw JSON array string, passed through to the SigNoz API as-is (advanced).")),
	)

	addTool(s, getOpsTool, h.handleGetServiceTopOperations)
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("Parameter validation failed: " + err.Error()), nil
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
		h.logger.ErrorContext(ctx, "Failed to list services", slog.String("start", start), slog.String("end", end), logpkg.ErrAttr(err))
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
		return mcp.NewToolResultError("Parameter validation failed: " + err.Error()), nil
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
		h.logger.ErrorContext(ctx, "Failed to get service top operations",
			slog.String("start", start),
			slog.String("end", end),
			slog.String("service", service),
			logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
