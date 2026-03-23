package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("List active alerts from SigNoz. Returns list of alert with: alert name, rule ID, severity, start time, end time, and state. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific alert, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of alerts to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)
	s.AddTool(alertsTool, h.handleListAlerts)

	getAlertTool := mcp.NewTool("signoz_get_alert",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Get details of a specific alert rule by ruleId"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert ruleId")),
	)
	s.AddTool(getAlertTool, h.handleGetAlert)

	alertHistoryTool := mcp.NewTool("signoz_get_alert_history",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Get alert history timeline for a specific rule. Defaults to last 6 hours if no time specified."),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start timestamp in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End timestamp in milliseconds (optional, defaults to now)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("limit", mcp.Description("Limit number of results (default: 20)")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	s.AddTool(alertHistoryTool, h.handleGetAlertHistory)
}

func (h *Handler) handleListAlerts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_list_alerts")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	alerts, err := client.ListAlerts(ctx)
	if err != nil {
		log.Error("Failed to list alerts", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var apiResponse types.APIAlertsResponse
	if err := json.Unmarshal(alerts, &apiResponse); err != nil {
		log.Error("Failed to parse alerts response", zap.Error(err), zap.String("response", string(alerts)))
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
		log.Error("Failed to wrap alerts with pagination", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	ruleID, ok := req.Params.Arguments.(map[string]any)["ruleId"].(string)
	if !ok {
		log.Warn("Invalid ruleId parameter type", zap.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a"}`), nil
	}
	if ruleID == "" {
		log.Warn("Empty ruleId parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" cannot be empty. Provide a valid alert rule ID (UUID format)`), nil
	}

	log.Debug("Tool called: signoz_get_alert", zap.String("ruleId", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
	if err != nil {
		log.Error("Failed to get alert", zap.String("ruleId", ruleID), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(respJSON)), nil
}

func (h *Handler) handleGetAlertHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args := req.Params.Arguments.(map[string]any)

	ruleID, ok := args["ruleId"].(string)
	if !ok || ruleID == "" {
		log.Warn("Invalid or empty ruleId parameter", zap.Any("ruleId", args["ruleId"]))
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a non-empty string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "24h"}`), nil
	}

	startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")

	var start, end int64
	if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
		log.Warn("Invalid start timestamp format", zap.String("start", startStr), zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697385600000") or use "timeRange" parameter instead (e.g., "24h")`, startStr)), nil
	}
	if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
		log.Warn("Invalid end timestamp format", zap.String("end", endStr), zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697472000000") or use "timeRange" parameter instead (e.g., "24h")`, endStr)), nil
	}

	_, offset := paginate.ParseParams(args)

	limit := 20
	if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
		if limitInt, err := strconv.Atoi(limitStr); err != nil {
			log.Warn("Invalid limit format", zap.String("limit", limitStr), zap.Error(err))
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
			log.Warn("Invalid order value", zap.String("order", orderStr))
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

	log.Debug("Tool called: signoz_get_alert_history",
		zap.String("ruleId", ruleID),
		zap.Int64("start", start),
		zap.Int64("end", end),
		zap.Int("offset", offset),
		zap.Int("limit", limit),
		zap.String("order", order))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	respJSON, err := client.GetAlertHistory(ctx, ruleID, historyReq)
	if err != nil {
		log.Error("Failed to get alert history",
			zap.String("ruleId", ruleID),
			zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(respJSON)), nil
}
