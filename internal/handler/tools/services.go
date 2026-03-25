package tools

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
)

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List all services in SigNoz. Defaults to last 6 hours if no time specified. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific service, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithString("limit", mcp.Description("Maximum number of services to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listTool, h.handleListServices)

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get top operations for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithString("tags", mcp.Description("Optional tags JSON array")),
	)

	s.AddTool(getOpsTool, h.handleGetServiceTopOperations)
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args := req.Params.Arguments.(map[string]any)

	start, end := timeutil.GetTimestampsWithDefaults(args, "ns")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	log.Debug("Tool called: signoz_list_services", zap.String("start", start), zap.String("end", end), zap.Int("limit", limit), zap.Int("offset", offset))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListServices(ctx, start, end)
	if err != nil {
		log.Error("Failed to list services", zap.String("start", start), zap.String("end", end), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var services []any
	if err := json.Unmarshal(result, &services); err != nil {
		log.Error("Failed to parse services response", zap.Error(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}

	total := len(services)
	pagedServices := paginate.Array(services, offset, limit)

	resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
	if err != nil {
		log.Error("Failed to wrap services with pagination", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetServiceTopOperations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args := req.Params.Arguments.(map[string]any)

	service, ok := args["service"].(string)
	if !ok {
		log.Warn("Invalid service parameter type", zap.Any("type", args["service"]))
		return mcp.NewToolResultError(`Parameter validation failed: "service" must be a string. Example: {"service": "frontend-api", "timeRange": "1h"}`), nil
	}
	if service == "" {
		log.Warn("Empty service parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "service" cannot be empty. Provide a valid service name. Use signoz_list_services tool to see available services.`), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, "ns")

	var tags json.RawMessage
	if t, ok := args["tags"].(string); ok && t != "" {
		tags = json.RawMessage(t)
	} else {
		tags = json.RawMessage("[]")
	}

	log.Debug("Tool called: signoz_get_service_top_operations",
		zap.String("start", start),
		zap.String("end", end),
		zap.String("service", service))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
	if err != nil {
		log.Error("Failed to get service top operations",
			zap.String("start", start),
			zap.String("end", end),
			zap.String("service", service),
			zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
