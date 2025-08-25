package tools

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

type Handler struct {
	client *client.SigNoz
	logger *zap.Logger
}

func NewHandler(log *zap.Logger, client *client.SigNoz) *Handler {
	return &Handler{client: client, logger: log}
}

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Info("Registering Metrics Handlers")

	listKeysTool := mcp.NewTool("list_metric_keys",
		mcp.WithDescription("List available metric keys from SigNoz"),
	)

	s.AddTool(listKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := h.client.ListMetricKeys(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(resp)), nil
	})

	searchKeysTool := mcp.NewTool("search_metric_keys",
		mcp.WithDescription("Search available metric keys from SigNoz by text"),
		mcp.WithString("searchText", mcp.Required(), mcp.Description("Search text for metric keys")),
	)

	s.AddTool(searchKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		searchText, ok := req.Params.Arguments.(map[string]any)["searchText"].(string)
		if !ok {
			return mcp.NewToolResultText("searchText parameter must be a string"), nil
		}
		if searchText == "" {
			return mcp.NewToolResultText("searchText parameter can not be empty"), nil
		}

		resp, err := h.client.SearchMetricKeys(ctx, searchText)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(resp)), nil
	})
}

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Info("Registering Alerts Handler")

	alertsTool := mcp.NewTool("list_alerts",
		mcp.WithDescription("List active alerts from SigNoz"),
	)
	s.AddTool(alertsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		alerts, err := h.client.ListAlerts(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(alerts)), nil
	})

	getAlertTool := mcp.NewTool("get_alert",
		mcp.WithDescription("Get details of a specific alert rule by ruleId"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert ruleId")),
	)
	s.AddTool(getAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ruleID, ok := req.Params.Arguments.(map[string]any)["ruleId"].(string)
		if !ok {
			return mcp.NewToolResultText("ruleId parameter must be a string"), nil
		}
		if ruleID == "" {
			return mcp.NewToolResultText("ruleId must not be empty"), nil
		}

		respJSON, err := h.client.GetAlertByRuleID(ctx, ruleID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(respJSON)), nil
	})

}

func (h *Handler) RegisterDashboardHandlers(s *server.MCPServer) {
	tool := mcp.NewTool("list_dashboards",
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps)"),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := h.client.ListDashboards(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getDashboardTool := mcp.NewTool("get_dashboard",
		mcp.WithDescription("Get full details of a specific dashboard by UUID (returns complete dashboard configuration with all panels and queries)"),
		mcp.WithString("uuid", mcp.Required(), mcp.Description("Dashboard UUID")),
	)

	s.AddTool(getDashboardTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uuid, ok := req.Params.Arguments.(map[string]any)["uuid"].(string)
		if !ok {
			return mcp.NewToolResultError(`"uuid" parameter must be a string`), nil
		}
		if uuid == "" {
			return mcp.NewToolResultError(`"uuid" parameter cannot be empty`), nil
		}
		data, err := h.client.GetDashboard(ctx, uuid)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	listTool := mcp.NewTool("list_services",
		mcp.WithDescription("List all services in SigNoz within a given time range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time (nanoseconds)")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time (nanoseconds)")),
	)

	s.AddTool(listTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)
		start, ok := args["start"].(string)
		if !ok {
			return mcp.NewToolResultError("start parameter must be a string"), nil
		}
		if start == "" {
			return mcp.NewToolResultError("start parameter cannot be empty"), nil
		}

		end, ok := args["end"].(string)
		if !ok {
			return mcp.NewToolResultError("end parameter must be a string"), nil
		}
		if end == "" {
			return mcp.NewToolResultError("end parameter cannot be empty"), nil
		}

		result, err := h.client.ListServices(ctx, start, end)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getOpsTool := mcp.NewTool("get_service_top_operations",
		mcp.WithDescription("Get top operations for a specific service in SigNoz within a given time range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time (nanoseconds)")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time (nanoseconds)")),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("tags", mcp.Description("Optional tags JSON array")),
	)

	s.AddTool(getOpsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)
		start, ok := args["start"].(string)
		if !ok {
			return mcp.NewToolResultError("start parameter must be a string"), nil
		}
		if start == "" {
			return mcp.NewToolResultError("start parameter cannot be empty"), nil
		}
		end, ok := args["end"].(string)
		if !ok {
			return mcp.NewToolResultError("end parameter must be a string"), nil
		}
		if end == "" {
			return mcp.NewToolResultError("end parameter cannot be empty"), nil
		}

		service, ok := args["service"].(string)
		if !ok {
			return mcp.NewToolResultError("service parameter must be a string"), nil
		}
		if service == "" {
			return mcp.NewToolResultError("service parameter cannot be empty"), nil
		}

		var tags json.RawMessage
		if t, ok := args["tags"].(string); ok && t != "" {
			tags = json.RawMessage(t)
		} else {
			tags = json.RawMessage("[]")
		}

		result, err := h.client.GetServiceTopOperations(ctx, start, end, service, tags)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})
}
