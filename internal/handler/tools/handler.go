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

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type Handler struct {
	client    *client.SigNoz
	logger    *zap.Logger
	signozURL string
}

func NewHandler(log *zap.Logger, client *client.SigNoz, signozURL string) *Handler {
	return &Handler{client: client, logger: log, signozURL: signozURL}
}

// getClient returns the appropriate client based on the context
// If an API key is found in the context, it creates a new client with that key
// Otherwise, it returns the default client
func (h *Handler) getClient(ctx context.Context) *client.SigNoz {
	if apiKey, ok := util.GetAPIKey(ctx); ok && apiKey != "" && h.signozURL != "" {
		h.logger.Debug("Creating client with API key from context")
		return client.NewClient(h.logger, h.signozURL, apiKey)
	}
	return h.client
}

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listKeysTool := mcp.NewTool("list_metric_keys",
		mcp.WithDescription("List available metric keys from SigNoz"),
	)

	s.AddTool(listKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: list_metric_keys")
		client := h.getClient(ctx)
		resp, err := client.ListMetricKeys(ctx)
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
		client := h.getClient(ctx)
		resp, err := client.SearchMetricKeys(ctx, searchText)
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
		client := h.getClient(ctx)
		alerts, err := client.ListAlerts(ctx)
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
		client := h.getClient(ctx)
		respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
		if err != nil {
			h.logger.Error("Failed to get alert", zap.String("ruleId", ruleID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(respJSON)), nil
	})

	alertHistoryTool := mcp.NewTool("get_alert_history",
		mcp.WithDescription("Get alert history timeline for a specific rule"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start timestamp in milliseconds")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End timestamp in milliseconds")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("limit", mcp.Description("Limit number of results (default: 20)")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	s.AddTool(alertHistoryTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		ruleID, ok := args["ruleId"].(string)
		if !ok || ruleID == "" {
			h.logger.Warn("Invalid or empty ruleId parameter", zap.Any("ruleId", args["ruleId"]))
			return mcp.NewToolResultText("ruleId parameter must be a non-empty string"), nil
		}

		startStr, ok := args["start"].(string)
		if !ok || startStr == "" {
			h.logger.Warn("Invalid or empty start parameter", zap.Any("start", args["start"]))
			return mcp.NewToolResultText("start parameter must be a non-empty string (timestamp in milliseconds)"), nil
		}

		endStr, ok := args["end"].(string)
		if !ok || endStr == "" {
			h.logger.Warn("Invalid or empty end parameter", zap.Any("end", args["end"]))
			return mcp.NewToolResultText("end parameter must be a non-empty string (timestamp in milliseconds)"), nil
		}

		var start, end int64
		if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
			h.logger.Warn("Invalid start timestamp format", zap.String("start", startStr), zap.Error(err))
			return mcp.NewToolResultText("start must be a valid timestamp in milliseconds"), nil
		}
		if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
			h.logger.Warn("Invalid end timestamp format", zap.String("end", endStr), zap.Error(err))
			return mcp.NewToolResultText("end must be a valid timestamp in milliseconds"), nil
		}

		offset := 0
		if offsetStr, ok := args["offset"].(string); ok && offsetStr != "" {
			if _, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil {
				h.logger.Warn("Invalid offset format", zap.String("offset", offsetStr), zap.Error(err))
				return mcp.NewToolResultText("offset must be a valid integer"), nil
			}
		}

		limit := 20
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if _, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil {
				h.logger.Warn("Invalid limit format", zap.String("limit", limitStr), zap.Error(err))
				return mcp.NewToolResultText("limit must be a valid integer"), nil
			}
		}

		order := "asc"
		if orderStr, ok := args["order"].(string); ok && orderStr != "" {
			if orderStr == "asc" || orderStr == "desc" {
				order = orderStr
			} else {
				h.logger.Warn("Invalid order value", zap.String("order", orderStr))
				return mcp.NewToolResultText("order must be 'asc' or 'desc'"), nil
			}
		}

		historyReq := types.AlertHistoryRequest{
			Start:  start,
			End:    end,
			Offset: offset,
			Limit:  limit,
			Order:  order,
			Filters: types.AlertHistoryFilters{
				Items: []interface{}{},
				Op:    "AND",
			},
		}

		h.logger.Debug("Tool called: get_alert_history",
			zap.String("ruleId", ruleID),
			zap.Int64("start", start),
			zap.Int64("end", end),
			zap.Int("offset", offset),
			zap.Int("limit", limit),
			zap.String("order", order))

		client := h.getClient(ctx)
		respJSON, err := client.GetAlertHistory(ctx, ruleID, historyReq)
		if err != nil {
			h.logger.Error("Failed to get alert history",
				zap.String("ruleId", ruleID),
				zap.Error(err))
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
		client := h.getClient(ctx)
		result, err := client.ListDashboards(ctx)
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
		client := h.getClient(ctx)
		data, err := client.GetDashboard(ctx, uuid)
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
		client := h.getClient(ctx)
		result, err := client.ListServices(ctx, start, end)
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

		client := h.getClient(ctx)
		result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
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
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		mcp.WithDescription("Execute a SigNoz Query Builder v5 query. The LLM should build the complete structured query JSON matching SigNoz's Query Builder v5 format. Example structure: {\"schemaVersion\":\"v1\",\"start\":1756386047000,\"end\":1756387847000,\"requestType\":\"raw\",\"compositeQuery\":{\"queries\":[{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"traces\",\"disabled\":false,\"limit\":10,\"offset\":0,\"order\":[{\"key\":{\"name\":\"timestamp\"},\"direction\":\"desc\"}],\"having\":{\"expression\":\"\"},\"selectFields\":[{\"name\":\"service.name\",\"fieldDataType\":\"string\",\"signal\":\"traces\",\"fieldContext\":\"resource\"},{\"name\":\"duration_nano\",\"fieldDataType\":\"\",\"signal\":\"traces\",\"fieldContext\":\"span\"}]}}]},\"formatOptions\":{\"formatTableResultForUI\":false,\"fillGaps\":false},\"variables\":{}}. See docs: https://signoz.io/docs/userguide/query-builder-v5/"),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	s.AddTool(executeQuery, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_execute_builder_query")

		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			h.logger.Warn("Invalid arguments payload type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError("invalid arguments payload"), nil
		}

		queryObj, ok := args["query"].(map[string]any)
		if !ok {
			h.logger.Warn("Invalid query parameter type", zap.Any("type", args["query"]))
			return mcp.NewToolResultError("query parameter must be a JSON object"), nil
		}

		queryJSON, err := json.Marshal(queryObj)
		if err != nil {
			h.logger.Error("Failed to marshal query object", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
		}

		var queryPayload types.QueryPayload
		if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
			h.logger.Error("Failed to unmarshal query payload", zap.Error(err))
			return mcp.NewToolResultError("invalid query payload structure: " + err.Error()), nil
		}

		if err := queryPayload.Validate(); err != nil {
			h.logger.Error("Query validation failed", zap.Error(err))
			return mcp.NewToolResultError("query validation error: " + err.Error()), nil
		}

		finalQueryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal validated query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
		}

		client := h.getClient(ctx)
		data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
		if err != nil {
			h.logger.Error("Failed to execute query builder v5", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		h.logger.Debug("Successfully executed query builder v5")
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

		h.logger.Debug("Tool called: signoz_query_helper",
			zap.String("signal", signal),
			zap.String("query_type", queryType))

		helpText := types.BuildQueryHelpText(signal, queryType)
		return mcp.NewToolResultText(helpText), nil
	})
}

func (h *Handler) RegisterLogsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering logs handlers")

	listLogViewsTool := mcp.NewTool("list_log_views",
		mcp.WithDescription("List all saved log views from SigNoz (returns summary with name, ID, description, and query details)"),
	)

	s.AddTool(listLogViewsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: list_log_views")
		client := h.getClient(ctx)
		result, err := client.ListLogViews(ctx)
		if err != nil {
			h.logger.Error("Failed to list log views", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getLogViewTool := mcp.NewTool("get_log_view",
		mcp.WithDescription("Get full details of a specific log view by ID (returns complete log view configuration with query structure)"),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("Log view ID")),
	)

	s.AddTool(getLogViewTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		viewID, ok := req.Params.Arguments.(map[string]any)["viewId"].(string)
		if !ok {
			h.logger.Warn("Invalid viewId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`"viewId" parameter must be a string`), nil
		}
		if viewID == "" {
			h.logger.Warn("Empty viewId parameter")
			return mcp.NewToolResultError(`"viewId" parameter cannot be empty`), nil
		}

		h.logger.Debug("Tool called: get_log_view", zap.String("viewId", viewID))
		client := h.getClient(ctx)
		data, err := client.GetLogView(ctx, viewID)
		if err != nil {
			h.logger.Error("Failed to get log view", zap.String("viewId", viewID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	getLogsForAlertTool := mcp.NewTool("get_logs_for_alert",
		mcp.WithDescription("Get logs related to a specific alert (automatically determines time range and service from alert details)"),
		mcp.WithString("alertId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range around alert (e.g., '1h', '30m', '2h') - default: '1h'")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
	)

	s.AddTool(getLogsForAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		alertID, ok := args["alertId"].(string)
		if !ok || alertID == "" {
			return mcp.NewToolResultError("alertId parameter is required and must be a string"), nil
		}

		timeRange := "1h"
		if tr, ok := args["timeRange"].(string); ok && tr != "" {
			timeRange = tr
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}

		h.logger.Debug("Tool called: get_logs_for_alert", zap.String("alertId", alertID))
		client := h.getClient(ctx)
		alertData, err := client.GetAlertByRuleID(ctx, alertID)
		if err != nil {
			h.logger.Error("Failed to get alert details", zap.String("alertId", alertID), zap.Error(err))
			return mcp.NewToolResultError("failed to get alert details: " + err.Error()), nil
		}

		var alertResponse map[string]interface{}
		if err := json.Unmarshal(alertData, &alertResponse); err != nil {
			h.logger.Error("Failed to parse alert data", zap.Error(err))
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

		if duration, err := time.ParseDuration(timeRange); err == nil {
			startTime = now.Add(-duration).UnixMilli()
		}

		filterExpression := "severity_text IN ('ERROR', 'WARN', 'FATAL')"
		if serviceName != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", serviceName)
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to get logs for alert", zap.String("alertId", alertID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	getErrorLogsTool := mcp.NewTool("get_error_logs",
		mcp.WithDescription("Get logs with ERROR or FATAL severity within a time range"),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time in milliseconds")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time in milliseconds")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
	)

	s.AddTool(getErrorLogsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, ok := args["start"].(string)
		if !ok || start == "" {
			return mcp.NewToolResultError("start parameter is required and must be a string"), nil
		}

		end, ok := args["end"].(string)
		if !ok || end == "" {
			return mcp.NewToolResultError("end parameter is required and must be a string"), nil
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}

		filterExpression := "severity_text IN ('ERROR', 'FATAL')"

		if service, ok := args["service"].(string); ok && service != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", service)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError("invalid start timestamp format"), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError("invalid end timestamp format"), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: get_error_logs", zap.String("start", start), zap.String("end", end))
		client := h.getClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to get error logs", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	searchLogsByServiceTool := mcp.NewTool("search_logs_by_service",
		mcp.WithDescription("Search logs for a specific service within a time range"),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name to search logs for")),
		mcp.WithString("start", mcp.Required(), mcp.Description("Start time in milliseconds")),
		mcp.WithString("end", mcp.Required(), mcp.Description("End time in milliseconds")),
		mcp.WithString("severity", mcp.Description("Log severity filter (DEBUG, INFO, WARN, ERROR, FATAL)")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
	)

	s.AddTool(searchLogsByServiceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError("service parameter is required and must be a string"), nil
		}

		start, ok := args["start"].(string)
		if !ok || start == "" {
			return mcp.NewToolResultError("start parameter is required and must be a string"), nil
		}

		end, ok := args["end"].(string)
		if !ok || end == "" {
			return mcp.NewToolResultError("end parameter is required and must be a string"), nil
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}

		filterExpression := fmt.Sprintf("service.name in ['%s']", service)

		if severity, ok := args["severity"].(string); ok && severity != "" {
			filterExpression += fmt.Sprintf(" AND severity_text = '%s'", severity)
		}

		if searchText, ok := args["searchText"].(string); ok && searchText != "" {
			filterExpression += fmt.Sprintf(" AND body CONTAINS '%s'", searchText)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError("invalid start timestamp format"), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError("invalid end timestamp format"), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: search_logs_by_service", zap.String("service", service), zap.String("start", start), zap.String("end", end))
		client := h.getClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to search logs by service", zap.String("service", service), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

}
