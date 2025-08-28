package tools

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

type Handler struct {
	client *client.SigNoz
	logger *zap.Logger
}

func NewHandler(log *zap.Logger, client *client.SigNoz) *Handler {
	return &Handler{client: client, logger: log}
}

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listKeysTool := mcp.NewTool("list_metric_keys",
		mcp.WithDescription("List available metric keys from SigNoz"),
	)

	s.AddTool(listKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: list_metric_keys")
		resp, err := h.client.ListMetricKeys(ctx)
		if err != nil {
			h.logger.Error("Failed to list metric keys", zap.Error(err))
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
			h.logger.Warn("Invalid searchText parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultText("searchText parameter must be a string"), nil
		}
		if searchText == "" {
			h.logger.Warn("Empty searchText parameter")
			return mcp.NewToolResultText("searchText parameter can not be empty"), nil
		}

		h.logger.Debug("Tool called: search_metric_keys", zap.String("searchText", searchText))
		resp, err := h.client.SearchMetricKeys(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to search metric keys", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(resp)), nil
	})
}

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("list_alerts",
		mcp.WithDescription("List active alerts from SigNoz"),
	)
	s.AddTool(alertsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: list_alerts")
		alerts, err := h.client.ListAlerts(ctx)
		if err != nil {
			h.logger.Error("Failed to list alerts", zap.Error(err))
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
			h.logger.Warn("Invalid ruleId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultText("ruleId parameter must be a string"), nil
		}
		if ruleID == "" {
			h.logger.Warn("Empty ruleId parameter")
			return mcp.NewToolResultText("ruleId must not be empty"), nil
		}

		h.logger.Debug("Tool called: get_alert", zap.String("ruleId", ruleID))
		respJSON, err := h.client.GetAlertByRuleID(ctx, ruleID)
		if err != nil {
			h.logger.Error("Failed to get alert", zap.String("ruleId", ruleID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(respJSON)), nil
	})

}

func (h *Handler) RegisterDashboardHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering dashboard handlers")

	tool := mcp.NewTool("list_dashboards",
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps)"),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: list_dashboards")
		result, err := h.client.ListDashboards(ctx)
		if err != nil {
			h.logger.Error("Failed to list dashboards", zap.Error(err))
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
			h.logger.Warn("Invalid uuid parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`"uuid" parameter must be a string`), nil
		}
		if uuid == "" {
			h.logger.Warn("Empty uuid parameter")
			return mcp.NewToolResultError(`"uuid" parameter cannot be empty`), nil
		}

		h.logger.Debug("Tool called: get_dashboard", zap.String("uuid", uuid))
		data, err := h.client.GetDashboard(ctx, uuid)
		if err != nil {
			h.logger.Error("Failed to get dashboard", zap.String("uuid", uuid), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("list_services",
		mcp.WithDescription("List all services in SigNoz within a given time range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time (nanoseconds)")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time (nanoseconds)")),
	)

	s.AddTool(listTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)
		start, ok := args["start"].(string)
		if !ok {
			h.logger.Warn("Invalid start parameter type", zap.Any("type", args["start"]))
			return mcp.NewToolResultError("start parameter must be a string"), nil
		}
		if start == "" {
			h.logger.Warn("Empty start parameter")
			return mcp.NewToolResultError("start parameter cannot be empty"), nil
		}

		end, ok := args["end"].(string)
		if !ok {
			h.logger.Warn("Invalid end parameter type", zap.Any("type", args["end"]))
			return mcp.NewToolResultError("end parameter must be a string"), nil
		}
		if end == "" {
			h.logger.Warn("Empty end parameter")
			return mcp.NewToolResultError("end parameter cannot be empty"), nil
		}

		h.logger.Debug("Tool called: list_services", zap.String("start", start), zap.String("end", end))
		result, err := h.client.ListServices(ctx, start, end)
		if err != nil {
			h.logger.Error("Failed to list services", zap.String("start", start), zap.String("end", end), zap.Error(err))
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
			h.logger.Warn("Invalid start parameter type", zap.Any("type", args["start"]))
			return mcp.NewToolResultError("start parameter must be a string"), nil
		}
		if start == "" {
			h.logger.Warn("Empty start parameter")
			return mcp.NewToolResultError("start parameter cannot be empty"), nil
		}
		end, ok := args["end"].(string)
		if !ok {
			h.logger.Warn("Invalid end parameter type", zap.Any("type", args["end"]))
			return mcp.NewToolResultError("end parameter must be a string"), nil
		}
		if end == "" {
			h.logger.Warn("Empty end parameter")
			return mcp.NewToolResultError("end parameter cannot be empty"), nil
		}

		service, ok := args["service"].(string)
		if !ok {
			h.logger.Warn("Invalid service parameter type", zap.Any("type", args["service"]))
			return mcp.NewToolResultError("service parameter must be a string"), nil
		}
		if service == "" {
			h.logger.Warn("Empty service parameter")
			return mcp.NewToolResultError("service parameter cannot be empty"), nil
		}

		var tags json.RawMessage
		if t, ok := args["tags"].(string); ok && t != "" {
			tags = json.RawMessage(t)
		} else {
			tags = json.RawMessage("[]")
		}

		h.logger.Debug("Tool called: get_service_top_operations",
			zap.String("start", start),
			zap.String("end", end),
			zap.String("service", service))

		result, err := h.client.GetServiceTopOperations(ctx, start, end, service, tags)
		if err != nil {
			h.logger.Error("Failed to get service top operations",
				zap.String("start", start),
				zap.String("end", end),
				zap.String("service", service),
				zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})
}

func (h *Handler) RegisterQueryBuilderV5Handlers(s *server.MCPServer) {
	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		mcp.WithDescription("Execute a SigNoz Query Builder v5 query. The LLM should build the complete structured query JSON matching SigNoz's Query Builder v5 format. Example structure: {\"schemaVersion\":\"v1\",\"start\":1756386047000,\"end\":1756387847000,\"requestType\":\"raw\",\"compositeQuery\":{\"queries\":[{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"traces\",\"disabled\":false,\"limit\":10,\"offset\":0,\"order\":[{\"key\":{\"name\":\"timestamp\"},\"direction\":\"desc\"}],\"having\":{\"expression\":\"\"},\"selectFields\":[{\"name\":\"service.name\",\"fieldDataType\":\"string\",\"signal\":\"traces\",\"fieldContext\":\"resource\"},{\"name\":\"duration_nano\",\"fieldDataType\":\"\",\"signal\":\"traces\",\"fieldContext\":\"span\"}]}}]},\"formatOptions\":{\"formatTableResultForUI\":false,\"fillGaps\":false},\"variables\":{}}. See docs: https://signoz.io/docs/userguide/query-builder-v5/"),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	s.AddTool(executeQuery, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid arguments payload"), nil
		}

		queryObj, ok := args["query"].(map[string]any)
		if !ok {
			return mcp.NewToolResultError("query parameter must be a JSON object"), nil
		}

		queryJSON, err := json.Marshal(queryObj)
		if err != nil {
			return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
		}

		var queryPayload querybuilder.QueryPayload
		if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
			return mcp.NewToolResultError("invalid query payload structure: " + err.Error()), nil
		}

		if err := queryPayload.Validate(); err != nil {
			return mcp.NewToolResultError("query validation error: " + err.Error()), nil
		}

		finalQueryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
		}

		data, err := h.client.QueryBuilderV5(ctx, finalQueryJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})

	// Helper tool for LLM to discover available fields and build better queries (this can be iterative )
	queryHelper := mcp.NewTool("signoz_query_helper",
		mcp.WithDescription("Helper tool for building SigNoz queries. Provides guidance on available fields, signal types, and query structure. Use this to understand what fields are available for traces, logs, and metrics before building queries."),
		mcp.WithString("signal", mcp.Description("Signal type to get help for: traces, logs, or metrics")),
		mcp.WithString("query_type", mcp.Description("Type of help needed: fields, structure, examples, or all")),
	)

	s.AddTool(queryHelper, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)
		signal, _ := args["signal"].(string)
		queryType, _ := args["query_type"].(string)

		if signal == "" {
			signal = "traces"
		}
		if queryType == "" {
			queryType = "all"
		}

		helpText := querybuilder.BuildQueryHelpText(signal, queryType)
		return mcp.NewToolResultText(helpText), nil
	})
}
