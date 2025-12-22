package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type Handler struct {
	client      *signozclient.SigNoz
	logger      *zap.Logger
	signozURL   string
	clientCache map[string]*signozclient.SigNoz
	cacheMutex  sync.RWMutex
}

func NewHandler(log *zap.Logger, client *signozclient.SigNoz, signozURL string) *Handler {
	return &Handler{
		client:      client,
		logger:      log,
		signozURL:   signozURL,
		clientCache: make(map[string]*signozclient.SigNoz),
	}
}

// getClient returns the appropriate client based on the context
// If an API key is found in the context, it returns a cached client with that key
// Otherwise, it returns the default client
func (h *Handler) GetClient(ctx context.Context) *signozclient.SigNoz {
	if apiKey, ok := util.GetAPIKey(ctx); ok && apiKey != "" && h.signozURL != "" {
		// Check cache first
		h.cacheMutex.RLock()
		if cachedClient, exists := h.clientCache[apiKey]; exists {
			h.cacheMutex.RUnlock()
			return cachedClient
		}
		h.cacheMutex.RUnlock()

		h.cacheMutex.Lock()
		defer h.cacheMutex.Unlock()

		// just to check if other goroutine created client
		if cachedClient, exists := h.clientCache[apiKey]; exists {
			return cachedClient
		}

		h.logger.Debug("Creating client with API key from context")
		newClient := signozclient.NewClient(h.logger, h.signozURL, apiKey)
		h.clientCache[apiKey] = newClient
		return newClient
	}
	return h.client
}

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listKeysTool := mcp.NewTool("signoz_list_metric_keys",
		mcp.WithDescription("List available metric keys from SigNoz. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. Use limit to control the number of results returned (default: 50). Use offset to skip results for pagination (default: 0). For large result sets, paginate by incrementing offset: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc."),
		mcp.WithString("limit", mcp.Description("Maximum number of keys to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_metric_keys")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		resp, err := client.ListMetricKeys(ctx)
		if err != nil {
			h.logger.Error("Failed to list metric keys", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// received api data - {"data": {"attributeKeys": [...]}}
		var response map[string]any
		if err := json.Unmarshal(resp, &response); err != nil {
			h.logger.Error("Failed to parse metric keys response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		dataObj, ok := response["data"].(map[string]any)
		if !ok {
			h.logger.Error("Invalid metric keys response format", zap.Any("data", response["data"]))
			return mcp.NewToolResultError("invalid response format: expected data object"), nil
		}

		attributeKeys, ok := dataObj["attributeKeys"].([]any)
		if !ok {
			h.logger.Error("Invalid attributeKeys format", zap.Any("attributeKeys", dataObj["attributeKeys"]))
			return mcp.NewToolResultError("invalid response format: expected attributeKeys array"), nil
		}

		total := len(attributeKeys)
		pagedKeys := paginate.Array(attributeKeys, offset, limit)

		// response wrapped in paged structured format
		resultJSON, err := paginate.Wrap(pagedKeys, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap metric keys with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	searchKeysTool := mcp.NewTool("signoz_search_metric_by_text",
		mcp.WithDescription("Search metrics by text (substring autocomplete)"),
		mcp.WithString("searchText", mcp.Required(), mcp.Description("Search text for metric keys")),
	)

	s.AddTool(searchKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		searchText, ok := req.Params.Arguments.(map[string]any)["searchText"].(string)
		if !ok {
			h.logger.Warn("Invalid searchText parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "searchText" must be a string. Example: {"searchText": "cpu_usage"}`), nil
		}
		if searchText == "" {
			h.logger.Warn("Empty searchText parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "searchText" cannot be empty. Provide a search term like "cpu", "memory", or "request"`), nil
		}

		h.logger.Debug("Tool called: signoz_search_metric_by_text", zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		resp, err := client.SearchMetricByText(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to search metric by text", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(resp)), nil
	})
}

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithDescription("List active alerts from SigNoz. Returns list of alert with: alert name, rule ID, severity, start time, end time, and state. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific alert, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of alerts to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)
	s.AddTool(alertsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_alerts")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		alerts, err := client.ListAlerts(ctx)
		if err != nil {
			h.logger.Error("Failed to list alerts", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var apiResponse types.APIAlertsResponse
		if err := json.Unmarshal(alerts, &apiResponse); err != nil {
			h.logger.Error("Failed to parse alerts response", zap.Error(err), zap.String("response", string(alerts)))
			return mcp.NewToolResultError("failed to parse alerts response: " + err.Error()), nil
		}

		// takes only meaningful data
		alertsList := make([]types.Alert, 0, len(apiResponse.Data))
		for _, apiAlert := range apiResponse.Data {
			alertsList = append(alertsList, types.Alert{
				Alertname: apiAlert.Labels.Alertname,
				RuleID:    apiAlert.Labels.RuleID,
				Severity:  apiAlert.Labels.Severity,
				StartsAt:  apiAlert.StartsAt,
				EndsAt:    apiAlert.EndsAt,
				State:     apiAlert.Status.State,
			})
		}

		total := len(alertsList)
		alertsArray := make([]any, len(alertsList))
		for i, v := range alertsList {
			alertsArray[i] = v
		}
		pagedAlerts := paginate.Array(alertsArray, offset, limit)

		resultJSON, err := paginate.Wrap(pagedAlerts, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap alerts with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getAlertTool := mcp.NewTool("signoz_get_alert",
		mcp.WithDescription("Get details of a specific alert rule by ruleId"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert ruleId")),
	)
	s.AddTool(getAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ruleID, ok := req.Params.Arguments.(map[string]any)["ruleId"].(string)
		if !ok {
			h.logger.Warn("Invalid ruleId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a"}`), nil
		}
		if ruleID == "" {
			h.logger.Warn("Empty ruleId parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" cannot be empty. Provide a valid alert rule ID (UUID format)`), nil
		}

		h.logger.Debug("Tool called: signoz_get_alert", zap.String("ruleId", ruleID))
		client := h.GetClient(ctx)
		respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
		if err != nil {
			h.logger.Error("Failed to get alert", zap.String("ruleId", ruleID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(respJSON)), nil
	})

	alertHistoryTool := mcp.NewTool("signoz_get_alert_history",
		mcp.WithDescription("Get alert history timeline for a specific rule. Defaults to last 6 hours if no time specified."),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start timestamp in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End timestamp in milliseconds (optional, defaults to now)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("limit", mcp.Description("Limit number of results (default: 20)")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	s.AddTool(alertHistoryTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		ruleID, ok := args["ruleId"].(string)
		if !ok || ruleID == "" {
			h.logger.Warn("Invalid or empty ruleId parameter", zap.Any("ruleId", args["ruleId"]))
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a non-empty string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "24h"}`), nil
		}

		startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")

		var start, end int64
		if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
			h.logger.Warn("Invalid start timestamp format", zap.String("start", startStr), zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697385600000") or use "timeRange" parameter instead (e.g., "24h")`, startStr)), nil
		}
		if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
			h.logger.Warn("Invalid end timestamp format", zap.String("end", endStr), zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697472000000") or use "timeRange" parameter instead (e.g., "24h")`, endStr)), nil
		}

		_, offset := paginate.ParseParams(args)

		limit := 20
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err != nil {
				h.logger.Warn("Invalid limit format", zap.String("limit", limitStr), zap.Error(err))
				return mcp.NewToolResultError(fmt.Sprintf(`Invalid "limit" value: "%s". Expected integer between 1-1000 (e.g., "20", "50", "100")`, limitStr)), nil
			} else if limitInt > 0 {
				limit = limitInt
			}
		}

		order := "asc"
		if orderStr, ok := args["order"].(string); ok && orderStr != "" {
			if orderStr == "asc" || orderStr == "desc" {
				order = orderStr
			} else {
				h.logger.Warn("Invalid order value", zap.String("order", orderStr))
				return mcp.NewToolResultError(fmt.Sprintf(`Invalid "order" value: "%s". Must be either "asc" or "desc"`, orderStr)), nil
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

		h.logger.Debug("Tool called: signoz_get_alert_history",
			zap.String("ruleId", ruleID),
			zap.Int64("start", start),
			zap.Int64("end", end),
			zap.Int("offset", offset),
			zap.Int("limit", limit),
			zap.String("order", order))

		client := h.GetClient(ctx)
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

	tool := mcp.NewTool("signoz_list_dashboards",
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific dashboard, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of dashboards to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_dashboards")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		result, err := client.ListDashboards(ctx)
		if err != nil {
			h.logger.Error("Failed to list dashboards", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var dashboards map[string]any
		if err := json.Unmarshal(result, &dashboards); err != nil {
			h.logger.Error("Failed to parse dashboards response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		data, ok := dashboards["data"].([]any)
		if !ok {
			h.logger.Error("Invalid dashboards response format", zap.Any("data", dashboards["data"]))
			return mcp.NewToolResultError("invalid response format: expected data array"), nil
		}

		total := len(data)
		pagedData := paginate.Array(data, offset, limit)

		resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap dashboards with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		mcp.WithDescription("Get full details of a specific dashboard by UUID (returns complete dashboard configuration with all panels and queries)"),
		mcp.WithString("uuid", mcp.Required(), mcp.Description("Dashboard UUID")),
	)

	s.AddTool(getDashboardTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uuid, ok := req.Params.Arguments.(map[string]any)["uuid"].(string)
		if !ok {
			h.logger.Warn("Invalid uuid parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "uuid" must be a string. Example: {"uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
		}
		if uuid == "" {
			h.logger.Warn("Empty uuid parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
		}

		h.logger.Debug("Tool called: signoz_get_dashboard", zap.String("uuid", uuid))
		client := h.GetClient(ctx)
		data, err := client.GetDashboard(ctx, uuid)
		if err != nil {
			h.logger.Error("Failed to get dashboard", zap.String("uuid", uuid), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	createDashboardTool := mcp.NewTool(
		"signoz_create_dashboard",
		mcp.WithDescription(
			"Creates a new monitoring dashboard based on the provided title, layout, and widget configuration. "+
				"CRITICAL: You MUST read these resources BEFORE generating any dashboard output:\n"+
				"1. signoz://dashboard/instructions - REQUIRED: Dashboard structure and basics\n"+
				"2. signoz://dashboard/widgets-instructions - REQUIRED: Widget configuration rules\n"+
				"3. signoz://dashboard/widgets-examples - REQUIRED: Complete widget examples with all required fields\n\n"+
				"QUERY-SPECIFIC RESOURCES (read based on query type used):\n"+
				"- For PromQL queries: signoz://dashboard/promql-example\n"+
				"- For Query Builder queries: signoz://dashboard/query-builder-example\n"+
				"- For ClickHouse SQL on logs: signoz://dashboard/clickhouse-schema-for-logs + signoz://dashboard/clickhouse-logs-example\n"+
				"- For ClickHouse SQL on metrics: signoz://dashboard/clickhouse-schema-for-metrics + signoz://dashboard/clickhouse-metrics-example\n"+
				"- For ClickHouse SQL on traces: signoz://dashboard/clickhouse-schema-for-traces + signoz://dashboard/clickhouse-traces-example\n\n"+
				"IMPORTANT: The widgets-examples resource contains complete, working widget configurations. "+
				"You must consult it to ensure all required fields (id, panelTypes, title, query, selectedLogFields, selectedTracesFields, thresholds, contextLinks) are properly populated.",
		),
		mcp.WithInputSchema[types.Dashboard](),
	)

	s.AddTool(createDashboardTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rawConfig, ok := req.Params.Arguments.(map[string]any)

		if !ok || len(rawConfig) == 0 {
			h.logger.Warn("Received empty or invalid arguments map.")
			return mcp.NewToolResultError(`Parameter validation failed: The dashboard configuration object is empty or improperly formatted.`), nil
		}

		configJSON, err := json.Marshal(rawConfig)
		if err != nil {
			h.logger.Error("Failed to unmarshal raw configuration", zap.Error(err))
			return mcp.NewToolResultError(
				fmt.Sprintf("Could not decode raw configuration. Error: %s", err.Error()),
			), nil
		}

		var dashboardConfig types.Dashboard
		if err := json.Unmarshal(configJSON, &dashboardConfig); err != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("Parameter decoding error: The provided JSON structure for the dashboard configuration is invalid. Error details: %s", err.Error()),
			), nil
		}

		h.logger.Debug("Tool called: signoz_create_dashboard", zap.String("title", dashboardConfig.Title))
		client := h.GetClient(ctx)
		data, err := client.CreateDashboard(ctx, dashboardConfig)

		if err != nil {
			h.logger.Error("Failed to create dashboard in SigNoz", zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})

	updateDashboardTool := mcp.NewTool(
		"signoz_update_dashboard",
		mcp.WithDescription(
			"Update an existing dashboard by supplying its UUID along with a fully assembled dashboard JSON object.\n\n"+
				"MANDATORY FIRST STEP: Read signoz://dashboard/widgets-examples before doing ANYTHING else. This is NON-NEGOTIABLE.\n\n"+
				"The provided object must represent the complete post-update state, combining the current dashboard data and the intended modifications.\n\n"+
				"REQUIRED RESOURCES (read ALL before generating output):\n"+
				"1. signoz://dashboard/instructions\n"+
				"2. signoz://dashboard/widgets-instructions\n"+
				"3. signoz://dashboard/widgets-examples ← CRITICAL: Shows complete widget field structure\n\n"+
				"CONDITIONAL RESOURCES (based on query type):\n"+
				"• PromQL → signoz://dashboard/promql-example\n"+
				"• Query Builder → signoz://dashboard/query-builder-example\n"+
				"• ClickHouse Logs → signoz://dashboard/clickhouse-schema-for-logs + clickhouse-logs-example\n"+
				"• ClickHouse Metrics → signoz://dashboard/clickhouse-schema-for-metrics + clickhouse-metrics-example\n"+
				"• ClickHouse Traces → signoz://dashboard/clickhouse-schema-for-traces + clickhouse-traces-example\n\n"+
				"WARNING: Failing to consult widgets-examples will result in incomplete widget configurations missing required fields "+
				"(id, panelTypes, title, query, selectedLogFields, selectedTracesFields, thresholds, contextLinks).",
		),
		mcp.WithInputSchema[types.UpdateDashboardInput](),
	)

	s.AddTool(updateDashboardTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rawConfig, ok := req.Params.Arguments.(map[string]any)

		if !ok || len(rawConfig) == 0 {
			h.logger.Warn("Received empty or invalid arguments map from Claude.")
			return mcp.NewToolResultError(`Parameter validation failed: The dashboard configuration object is empty or improperly formatted.`), nil
		}

		configJSON, err := json.Marshal(rawConfig)
		if err != nil {
			h.logger.Error("Failed to unmarshal raw configuration", zap.Error(err))
			return mcp.NewToolResultError(
				fmt.Sprintf("Could not decode raw configuration. Error: %s", err.Error()),
			), nil
		}

		var updateDashboardConfig types.UpdateDashboardInput
		if err := json.Unmarshal(configJSON, &updateDashboardConfig); err != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("Parameter decoding error: The provided JSON structure for the dashboard configuration is invalid. Error details: %s", err.Error()),
			), nil
		}

		if updateDashboardConfig.UUID == "" {
			h.logger.Warn("Empty uuid parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use list_dashboards tool to see available dashboards.`), nil
		}

		h.logger.Debug("Tool called: signoz_update_dashboard", zap.String("title", updateDashboardConfig.Dashboard.Title))
		client := h.GetClient(ctx)
		err = client.UpdateDashboard(ctx, updateDashboardConfig.UUID, updateDashboardConfig.Dashboard)

		if err != nil {
			h.logger.Error("Failed to update dashboard in SigNoz", zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
		}

		return mcp.NewToolResultText("dashboard updated"), nil
	})

	// resources for create and update dashboard
	clickhouseLogsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-logs",
		"ClickHouse Logs Schema",
		mcp.WithResourceDescription("ClickHouse schema for logs_v2, logs_v2_resource, tag_attributes_v2 and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseLogsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.LogsSchema,
			},
		}, nil
	})

	clickhouseLogsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-logs-example",
		"Clickhouse Examples for logs",
		mcp.WithResourceDescription("ClickHouse SQL query examples for SigNoz logs. Includes resource filter patterns (CTE), timeseries queries, value queries, common use cases (Kubernetes clusters, error logs by service), and key patterns for timestamp filtering, attribute access (resource vs standard, indexed vs non-indexed), severity filters, variables, and performance optimization tips."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseLogsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForLogs,
			},
		}, nil
	})

	clickhouseMetricsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-metrics",
		"ClickHouse Metrics Schema",
		mcp.WithResourceDescription("ClickHouse schema for samples_v4, exp_hist, time_series_v4 (and 6hrs/1day variants) and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseMetricsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.MetricsSchema,
			},
		}, nil
	})

	clickhouseMetricsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-metrics-example",
		"Clickhouse Examples for Metrics",
		mcp.WithResourceDescription("ClickHouse SQL query examples for SigNoz metrics. Includes basic queries , rate calculation patterns for counter metrics (using lagInFrame and runningDifference), error rate calculations (ratio of two metrics), histogram quantile queries for latency percentiles (P95, P99), and key patterns for time series table selection by granularity, timestamp filtering, label filtering, time interval aggregation, variables, and performance optimization"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseMetricsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForMetrics,
			},
		}, nil
	})

	clickhouseTracesSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-traces",
		"ClickHouse Traces Schema",
		mcp.WithResourceDescription("ClickHouse schema for signoz_index_v3, signoz_spans, signoz_error_index_v2, traces_v3_resource, dependency_graph_minutes_v2, trace_summary, top_level_operations and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseTracesSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.TracesSchema,
			},
		}, nil
	})

	clickhouseTracesExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-traces-example",
		"Clickhouse Examples for Traces",
		mcp.WithResourceDescription("ClickHouse SQL examples for SigNoz traces: resource filters, timeseries/value/table queries, span event extraction, latency analysis, and performance tips."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseTracesExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForTraces,
			},
		}, nil
	})

	promqlExample := mcp.NewResource(
		"signoz://dashboard/promql-example",
		"Promql Examples",
		mcp.WithResourceDescription("PromQL guide for SigNoz: critical syntax rules for OpenTelemetry metrics with dots, formatting patterns, examples by metric type, and error prevention."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(promqlExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.PromqlQuery,
			},
		}, nil
	})

	queryBuilderExample := mcp.NewResource(
		"signoz://dashboard/query-builder-example",
		"Query Builder Examples",
		mcp.WithResourceDescription("SigNoz Query Builder reference: CRITICAL OpenTelemetry metric naming conventions (dot vs underscore suffixes), filtering, aggregation, search syntax, operators, field existence behavior, full-text search, functions, advanced examples, and best practices."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(queryBuilderExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.Querybuilder,
			},
		}, nil
	})

	dashboardInstructions := mcp.NewResource(
		"signoz://dashboard/instructions",
		"Dashboard Basic Instructions",
		mcp.WithResourceDescription("SigNoz dashboard basics: title, tags, description, and comprehensive variable configuration rules (types, properties, referencing, chaining)."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(dashboardInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.Basics,
			},
		}, nil
	})

	widgetsInstructions := mcp.NewResource(
		"signoz://dashboard/widgets-instructions",
		"Dashboard Basic Instructions",
		mcp.WithResourceDescription("SigNoz dashboard widgets: 7 panel types (Bar, Histogram, List, Pie, Table, Timeseries, Value) with use cases, configuration options, and critical layout rules (grid coordinates, dimensions, legends)."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(widgetsInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.WidgetsInstructions,
			},
		}, nil
	})

	widgetsExamplesResource := mcp.NewResource(
		"signoz://dashboard/widgets-examples",
		"Dashboard Widgets Examples",
		mcp.WithResourceDescription("Basic Example widgets"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(widgetsExamplesResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.WidgetExamples,
			},
		}, nil
	})

}

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		mcp.WithDescription("List all services in SigNoz. Defaults to last 6 hours if no time specified. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific service, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithString("limit", mcp.Description("Maximum number of services to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ns")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		h.logger.Debug("Tool called: signoz_list_services", zap.String("start", start), zap.String("end", end), zap.Int("limit", limit), zap.Int("offset", offset))
		client := h.GetClient(ctx)
		result, err := client.ListServices(ctx, start, end)
		if err != nil {
			h.logger.Error("Failed to list services", zap.String("start", start), zap.String("end", end), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var services []any
		if err := json.Unmarshal(result, &services); err != nil {
			h.logger.Error("Failed to parse services response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		total := len(services)
		pagedServices := paginate.Array(services, offset, limit)

		resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap services with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		mcp.WithDescription("Get top operations for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithString("tags", mcp.Description("Optional tags JSON array")),
	)

	s.AddTool(getOpsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok {
			h.logger.Warn("Invalid service parameter type", zap.Any("type", args["service"]))
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a string. Example: {"service": "frontend-api", "timeRange": "1h"}`), nil
		}
		if service == "" {
			h.logger.Warn("Empty service parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "service" cannot be empty. Provide a valid service name. Use signoz_list_services tool to see available services.`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ns")

		var tags json.RawMessage
		if t, ok := args["tags"].(string); ok && t != "" {
			tags = json.RawMessage(t)
		} else {
			tags = json.RawMessage("[]")
		}

		h.logger.Debug("Tool called: signoz_get_service_top_operations",
			zap.String("start", start),
			zap.String("end", end),
			zap.String("service", service))

		client := h.GetClient(ctx)
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

		client := h.GetClient(ctx)
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

	listLogViewsTool := mcp.NewTool("signoz_list_log_views",
		mcp.WithDescription("List all saved log views from SigNoz (returns summary with name, ID, description, and query details). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific log view, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of views to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listLogViewsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_log_views")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		result, err := client.ListLogViews(ctx)
		if err != nil {
			h.logger.Error("Failed to list log views", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var logViews map[string]any
		if err := json.Unmarshal(result, &logViews); err != nil {
			h.logger.Error("Failed to parse log views response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		data, ok := logViews["data"].([]any)
		if !ok {
			h.logger.Error("Invalid log views response format", zap.Any("data", logViews["data"]))
			return mcp.NewToolResultError("invalid response format: expected data array"), nil
		}

		total := len(data)
		pagedData := paginate.Array(data, offset, limit)

		resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap log views with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getLogViewTool := mcp.NewTool("signoz_get_log_view",
		mcp.WithDescription("Get full details of a specific log view by ID (returns complete log view configuration with query structure)"),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("Log view ID")),
	)

	s.AddTool(getLogViewTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		viewID, ok := req.Params.Arguments.(map[string]any)["viewId"].(string)
		if !ok {
			h.logger.Warn("Invalid viewId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "viewId" must be a string. Example: {"viewId": "error-logs-view-123"}`), nil
		}
		if viewID == "" {
			h.logger.Warn("Empty viewId parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Provide a valid log view ID. Use signoz_list_log_views tool to see available log views.`), nil
		}

		h.logger.Debug("Tool called: signoz_get_log_view", zap.String("viewId", viewID))
		client := h.GetClient(ctx)
		data, err := client.GetLogView(ctx, viewID)
		if err != nil {
			h.logger.Error("Failed to get log view", zap.String("viewId", viewID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	getLogsForAlertTool := mcp.NewTool("signoz_get_logs_for_alert",
		mcp.WithDescription("Get logs related to a specific alert (automatically determines time range and service from alert details)"),
		mcp.WithString("alertId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range around alert (optional). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '15m', '30m', '1h', '2h', '6h'. Defaults to '1h' if not provided.")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(getLogsForAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		alertID, ok := args["alertId"].(string)
		if !ok || alertID == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "alertId" must be a non-empty string. Example: {"alertId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "1h", "limit": "50"}`), nil
		}

		timeRange := "1h"
		if tr, ok := args["timeRange"].(string); ok && tr != "" {
			timeRange = tr
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		h.logger.Debug("Tool called: signoz_get_logs_for_alert", zap.String("alertId", alertID))
		client := h.GetClient(ctx)
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

		if duration, err := timeutil.ParseTimeRange(timeRange); err == nil {
			startTime = now.Add(-duration).UnixMilli()
		}

		filterExpression := "severity_text IN ('ERROR', 'WARN', 'FATAL')"
		if serviceName != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", serviceName)
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

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

	getErrorLogsTool := mcp.NewTool("signoz_get_error_logs",
		mcp.WithDescription("Get logs with ERROR or FATAL severity. Defaults to last 6 hours if no time specified."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 25, max: 200)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(getErrorLogsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 25
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				if limitInt > 200 {
					limit = 200
				} else if limitInt < 1 {
					limit = 1
				} else {
					limit = limitInt
				}
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		filterExpression := "severity_text IN ('ERROR', 'FATAL')"

		if service, ok := args["service"].(string); ok && service != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", service)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: signoz_get_error_logs", zap.String("start", start), zap.String("end", end))
		client := h.GetClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to get error logs", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	searchLogsByServiceTool := mcp.NewTool("signoz_search_logs_by_service",
		mcp.WithDescription("Search logs for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name to search logs for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("severity", mcp.Description("Log severity filter (DEBUG, INFO, WARN, ERROR, FATAL)")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(searchLogsByServiceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a non-empty string. Example: {"service": "consumer-svc-1", "searchText": "error", "timeRange": "1h", "limit": "50"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		filterExpression := fmt.Sprintf("service.name in ['%s']", service)

		if severity, ok := args["severity"].(string); ok && severity != "" {
			filterExpression += fmt.Sprintf(" AND severity_text = '%s'", severity)
		}

		if searchText, ok := args["searchText"].(string); ok && searchText != "" {
			filterExpression += fmt.Sprintf(" AND body CONTAINS '%s'", searchText)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: signoz_search_logs_by_service", zap.String("service", service), zap.String("start", start), zap.String("end", end))
		client := h.GetClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to search logs by service", zap.String("service", service), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

}

func (h *Handler) RegisterTracesHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering traces handlers")

	getTraceFieldValuesTool := mcp.NewTool("signoz_get_trace_field_values",
		mcp.WithDescription("Get available field values for trace queries"),
		mcp.WithString("fieldName", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name')")),
		mcp.WithString("searchText", mcp.Description("Search text to filter values (optional)")),
	)

	s.AddTool(getTraceFieldValuesTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		fieldName, ok := args["fieldName"].(string)
		if !ok || fieldName == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "fieldName" must be a non-empty string. Examples: {"fieldName": "service.name"}, {"fieldName": "http.status_code"}, {"fieldName": "operation"}`), nil
		}

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Debug("Tool called: signoz_get_trace_field_values", zap.String("fieldName", fieldName), zap.String("searchText", searchText))
		result, err := h.client.GetTraceFieldValues(ctx, fieldName, searchText)
		if err != nil {
			h.logger.Error("Failed to get trace field values", zap.String("fieldName", fieldName), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	searchTracesByServiceTool := mcp.NewTool("signoz_search_traces_by_service",
		mcp.WithDescription("Search traces for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name to search traces for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("operation", mcp.Description("Operation name to filter by")),
		mcp.WithString("error", mcp.Description("Filter by error status (true/false)")),
		mcp.WithString("minDuration", mcp.Description("Minimum duration in nanoseconds")),
		mcp.WithString("maxDuration", mcp.Description("Maximum duration in nanoseconds")),
		mcp.WithString("limit", mcp.Description("Maximum number of traces to return (default: 100)")),
	)

	s.AddTool(searchTracesByServiceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a non-empty string. Example: {"service": "frontend-api", "timeRange": "2h", "error": "true", "limit": "100"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		filterExpression := fmt.Sprintf("service.name in ['%s']", service)

		if operation, ok := args["operation"].(string); ok && operation != "" {
			filterExpression += fmt.Sprintf(" AND name = '%s'", operation)
		}

		if errorFilter, ok := args["error"].(string); ok && errorFilter != "" {
			switch errorFilter {
			case "true":
				filterExpression += " AND hasError = true"
			case "false":
				filterExpression += " AND hasError = false"
			}
		}

		if minDuration, ok := args["minDuration"].(string); ok && minDuration != "" {
			filterExpression += fmt.Sprintf(" AND durationNano >= %s", minDuration)
		}

		if maxDuration, ok := args["maxDuration"].(string); ok && maxDuration != "" {
			filterExpression += fmt.Sprintf(" AND durationNano <= %s", maxDuration)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: signoz_search_traces_by_service", zap.String("service", service), zap.String("start", start), zap.String("end", end))
		result, err := h.client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to search traces by service", zap.String("service", service), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceDetailsTool := mcp.NewTool("signoz_get_trace_details",
		mcp.WithDescription("Get comprehensive trace information including all spans and metadata. Defaults to last 6 hours if no time specified."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Trace ID to get details for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("includeSpans", mcp.Description("Include detailed span information (true/false, default: true)")),
	)

	s.AddTool(getTraceDetailsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		h.logger.Debug("Tool called: signoz_get_trace_details", zap.String("traceId", traceID), zap.Bool("includeSpans", includeSpans), zap.String("start", start), zap.String("end", end))
		result, err := h.client.GetTraceDetails(ctx, traceID, includeSpans, startTime, endTime)
		if err != nil {
			h.logger.Error("Failed to get trace details", zap.String("traceId", traceID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceErrorAnalysisTool := mcp.NewTool("signoz_get_trace_error_analysis",
		mcp.WithDescription("Analyze error patterns in traces. Defaults to last 6 hours if no time specified."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("service", mcp.Description("Service name to filter by (optional)")),
	)

	s.AddTool(getTraceErrorAnalysisTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		service := ""
		if s, ok := args["service"].(string); ok && s != "" {
			service = s
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
		}

		h.logger.Debug("Tool called: signoz_get_trace_error_analysis", zap.String("start", start), zap.String("end", end), zap.String("service", service))
		result, err := h.client.GetTraceErrorAnalysis(ctx, startTime, endTime, service)
		if err != nil {
			h.logger.Error("Failed to get trace error analysis", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceSpanHierarchyTool := mcp.NewTool("signoz_get_trace_span_hierarchy",
		mcp.WithDescription("Get trace span relationships and hierarchy. Defaults to last 6 hours if no time specified."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Trace ID to get span hierarchy for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
	)

	s.AddTool(getTraceSpanHierarchyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		traceID, ok := args["traceId"].(string)
		if !ok || traceID == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "traceId" must be a non-empty string. Example: {"traceId": "abc123def456", "timeRange": "1h"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: %s. Use "timeRange" parameter instead (e.g., "1h", "24h")`, end)), nil
		}

		h.logger.Debug("Tool called: signoz_get_trace_span_hierarchy", zap.String("traceId", traceID), zap.String("start", start), zap.String("end", end))
		result, err := h.client.GetTraceSpanHierarchy(ctx, traceID, startTime, endTime)
		if err != nil {
			h.logger.Error("Failed to get trace span hierarchy", zap.String("traceId", traceID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

}
