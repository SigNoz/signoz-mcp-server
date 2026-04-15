package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/alert"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List alerts from SigNoz. Returns alert name, rule ID, severity, start time, end time, and state.\n\nFILTERING: Use server-side filters to narrow results BEFORE paginating.\n- To find a specific alert by name: filter='alertname=\"HighCPU\"'\n- To find alerts by severity: filter='severity=\"critical\"'\n- Combine matchers: filter='alertname=\"HighCPU\",severity=\"critical\"'\n- To see only firing alerts: active='true', silenced='false', inhibited='false'\n- To see only silenced alerts: silenced='true', active='false'\n- To filter by notification receiver: receiver='slack-.*'\nBy default all alert states (active, silenced, inhibited) are included.\n\nPAGINATION: Supports 'limit' and 'offset'. Response includes 'pagination' with 'total', 'hasMore', and 'nextOffset'. Prefer 'filter' to find specific alerts instead of paginating all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of alerts to return per page. Default: 50.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip for pagination. Default: 0.")),
		mcp.WithString("active", mcp.Description("Include active (firing) alerts. Values: 'true' or 'false'. Default: true.")),
		mcp.WithString("silenced", mcp.Description("Include silenced alerts. Values: 'true' or 'false'. Default: true.")),
		mcp.WithString("inhibited", mcp.Description("Include inhibited alerts. Values: 'true' or 'false'. Default: true.")),
		mcp.WithString("filter", mcp.Description("Comma-separated matcher expressions to filter alerts. Example: 'alertname=\"HighCPU\"' or 'alertname=\"HighCPU\",severity=\"critical\"'. Uses Prometheus matcher syntax.")),
		mcp.WithString("receiver", mcp.Description("Regex to filter alerts by receiver name. Example: 'slack-.*' to match all Slack receivers.")),
	)
	s.AddTool(alertsTool, h.handleListAlerts)

	getAlertTool := mcp.NewTool("signoz_get_alert",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get details of a specific alert rule by ruleId"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert ruleId")),
	)
	s.AddTool(getAlertTool, h.handleGetAlert)

	alertHistoryTool := mcp.NewTool("signoz_get_alert_history",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get alert history timeline for a specific rule. Defaults to last 6 hours if no time specified. Use 'state' to filter by alert state (e.g., only firing transitions or only resolutions)."),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start timestamp in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End timestamp in milliseconds (optional, defaults to now)")),
		mcp.WithString("state", mcp.Description("Filter history by alert state: 'firing' or 'inactive'. If omitted, returns all state transitions.")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("limit", mcp.Description("Limit number of results (default: 20)")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	s.AddTool(alertHistoryTool, h.handleGetAlertHistory)

	createAlertTool := mcp.NewTool(
		"signoz_create_alert",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Creates a new alert rule in SigNoz using v2alpha1 schema.\n\n"+
				"CRITICAL: You MUST read these resources BEFORE generating any alert payload:\n"+
				"1. signoz://alert/instructions — REQUIRED: Alert structure, field descriptions, valid values\n"+
				"2. signoz://alert/examples — REQUIRED: Complete working examples for each alert type\n\n"+
				"RECOMMENDED: Use signoz_get_alert on an existing alert to study the exact structure.\n\n"+
				"Supports all alert types (metrics, logs, traces, exceptions) and rule types (threshold, promql, anomaly).\n"+
				"Uses v2alpha1 schema with structured thresholds (multi-threshold with per-level channel routing), "+
				"evaluation block, and notificationSettings.\n"+
				"Labels enable routing policies — always include severity (info, warning, critical) and team/service labels for routing.",
		),
		mcp.WithInputSchema[types.AlertRule](),
	)
	s.AddTool(createAlertTool, h.handleCreateAlert)

	// Register alert resources for create alert
	h.registerAlertResources(s)
}

func parseBoolParam(args map[string]any, key string) *bool {
	if v, ok := args[key].(string); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return &b
		}
	}
	return nil
}

func (h *Handler) handleListAlerts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_list_alerts")
	args := req.Params.Arguments.(map[string]any)
	limit, offset := paginate.ParseParams(args)

	params := types.ListAlertsParams{
		Active:    parseBoolParam(args, "active"),
		Inhibited: parseBoolParam(args, "inhibited"),
		Silenced:  parseBoolParam(args, "silenced"),
	}
	if receiver, ok := args["receiver"].(string); ok && receiver != "" {
		params.Receiver = receiver
	}
	if filterStr, ok := args["filter"].(string); ok && filterStr != "" {
		for _, f := range strings.Split(filterStr, ",") {
			if trimmed := strings.TrimSpace(f); trimmed != "" {
				params.Filter = append(params.Filter, trimmed)
			}
		}
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	alerts, err := client.ListAlerts(ctx, params)
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

	var state string
	if stateStr, ok := args["state"].(string); ok && stateStr != "" {
		if stateStr != "firing" && stateStr != "inactive" {
			log.Warn("Invalid state value", zap.String("state", stateStr))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "state" value: "%s". Must be either "firing" or "inactive"`, stateStr)), nil
		}
		state = stateStr
	}

	historyReq := types.AlertHistoryRequest{
		Start:  start,
		End:    end,
		State:  state,
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

func (h *Handler) handleCreateAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		log.Warn("Received empty or invalid arguments map for create alert.")
		return mcp.NewToolResultError(`Parameter validation failed: The alert configuration object is empty or improperly formatted.`), nil
	}

	// Validate and normalize the alert payload.
	cleanJSON, err := alert.ValidateFromMap(rawConfig)
	if err != nil {
		log.Warn("Alert validation failed", zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("Alert validation error: %s", err.Error())), nil
	}

	log.Debug("Tool called: signoz_create_alert")
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.CreateAlertRule(ctx, cleanJSON)

	if err != nil {
		log.Error("Failed to create alert rule in SigNoz", zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// registerAlertResources registers MCP resources needed for alert creation.
func (h *Handler) registerAlertResources(s *server.MCPServer) {
	alertInstructions := mcp.NewResource(
		"signoz://alert/instructions",
		"Alert Rule Instructions",
		mcp.WithResourceDescription("SigNoz alert rule creation guide: alert types, rule types, condition structure, threshold configuration (v2alpha1 schema), composite query format, filter expressions, labels, routing, and notification settings."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(alertInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     alert.Instructions,
			},
		}, nil
	})

	alertExamples := mcp.NewResource(
		"signoz://alert/examples",
		"Alert Rule Examples",
		mcp.WithResourceDescription("Complete working alert rule examples for all types: metrics threshold, logs with v2 multi-threshold routing, traces latency, PromQL, ClickHouse SQL exceptions, anomaly detection, and formula-based alerts."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(alertExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     alert.Examples,
			},
		}, nil
	})
}
