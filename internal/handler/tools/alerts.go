package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/alert"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Lists currently firing/silenced/inhibited alert *instances* from Alertmanager — not rule definitions. Use signoz_get_alert with a ruleId to fetch the rule definition itself, or signoz_get_alert_history for the state timeline.\n\nReturns alert name, rule ID, severity, start time, end time, and state.\n\nFILTERING: Use server-side filters to narrow results BEFORE paginating.\n- To find a specific alert by name: filter='alertname=\"HighCPU\"'\n- To find alerts by severity: filter='severity=\"critical\"'\n- Combine matchers: filter='alertname=\"HighCPU\",severity=\"critical\"'\n- To see only firing alerts: active='true', silenced='false', inhibited='false'\n- To see only silenced alerts: silenced='true', active='false'\n- To filter by notification receiver: receiver='slack-.*'\nBy default all alert states (active, silenced, inhibited) are included.\n\nPAGINATION: Supports 'limit' and 'offset'. Response includes 'pagination' with 'total', 'hasMore', and 'nextOffset'. Prefer 'filter' to find specific alerts instead of paginating all pages. Default: limit=50, offset=0."),
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
		mcp.WithDescription("Get the rule definition for a specific alert rule by ruleId (GET /api/v2/rules/{ruleId}).\n\nResponse shape depends on the SigNoz server version. Post-#10997 servers return the canonical Rule type with audit fields createdAt/updatedAt/createdBy/updatedBy; older servers return GettableRule with createAt/updateAt/createBy/updateBy (no 'd')."),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID (UUIDv7 on v2 servers).")),
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
			"Creates a new alert rule in SigNoz (POST /api/v2/rules).\n\n"+
				"SCHEMA — pick based on ruleType:\n"+
				"- threshold_rule / promql_rule → v2alpha1 with structured condition.thresholds (per-tier channel routing), evaluation block, notificationSettings.\n"+
				"- anomaly_rule → **v1 schema**: top-level evalWindow and frequency; condition.op, condition.matchType, condition.target, condition.algorithm, condition.seasonality; compositeQuery.queries[].spec.functions carries the anomaly function. Omit thresholds, evaluation, schemaVersion.\n\n"+
				"CRITICAL: You MUST read these resources BEFORE generating any alert payload:\n"+
				"1. signoz://alert/instructions — REQUIRED: Alert structure, field descriptions, valid values\n"+
				"2. signoz://alert/examples — REQUIRED: Ten canonical payloads (mirrored from SigNoz PR #11023) covering metric/logs/traces threshold, PromQL, anomaly (v1), tiered thresholds, formula, and full notificationSettings.\n\n"+
				"RECOMMENDED: Use signoz_get_alert on an existing alert to study the exact structure.\n\n"+
				"NOTIFICATION CHANNELS: At least one notification channel is required. "+
				"If the user explicitly names a channel, use it directly. "+
				"Otherwise, do NOT guess or assume channel names — call this tool WITHOUT channels to get the list of available channels, "+
				"present that list to the user, let them choose, then call again with their selection. "+
				"If no suitable channel exists, use signoz_create_notification_channel first.\n\n"+
				"Supports all alert types (metrics, logs, traces, exceptions) and rule types (threshold, promql, anomaly).\n"+
				"Labels enable routing policies — always set labels.severity (critical, error, warning, or info) to match your highest threshold tier, and add team/service labels for routing.",
		),
		mcp.WithInputSchema[types.AlertRule](),
	)
	s.AddTool(createAlertTool, h.handleCreateAlert)

	updateAlertTool := mcp.NewTool(
		"signoz_update_alert",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("UUIDv7 of the alert rule to update. Obtain it from signoz_get_alert or signoz_list_alerts.")),
		mcp.WithDescription(
			"Updates an existing alert rule in SigNoz (PUT /api/v2/rules/{ruleId}). Replaces the full rule configuration.\n\n"+
				"CRITICAL: Read signoz://alert/instructions and signoz://alert/examples before generating the payload. "+
				"Always fetch the current rule with signoz_get_alert first and merge changes on top of it — PUT replaces the full rule.\n\n"+
				"The rule payload is the same shape as signoz_create_alert. All the same validation rules apply, including "+
				"the notification-channel presence check.",
		),
		mcp.WithInputSchema[types.AlertRule](),
	)
	s.AddTool(updateAlertTool, h.handleUpdateAlert)

	deleteAlertTool := mcp.NewTool(
		"signoz_delete_alert",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("UUIDv7 of the alert rule to delete. The server validates the UUID format and returns invalid_input on bad values.")),
		mcp.WithDescription("Deletes an alert rule by ID (DELETE /api/v2/rules/{ruleId}). Irreversible. Confirm with the user before calling."),
	)
	s.AddTool(deleteAlertTool, h.handleDeleteAlert)

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
	h.logger.DebugContext(ctx, "Tool called: signoz_list_alerts")
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
		h.logger.ErrorContext(ctx, "Failed to list alerts", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var apiResponse types.APIAlertsResponse
	if err := json.Unmarshal(alerts, &apiResponse); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse alerts response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(alerts)))
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
		h.logger.ErrorContext(ctx, "Failed to wrap alerts with pagination", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ruleID, ok := req.Params.Arguments.(map[string]any)["ruleId"].(string)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid ruleId parameter type", slog.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a"}`), nil
	}
	if ruleID == "" {
		h.logger.WarnContext(ctx, "Empty ruleId parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" cannot be empty. Provide a valid alert rule ID (UUID format)`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_alert", slog.String("ruleId", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get alert", slog.String("ruleId", ruleID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(respJSON)), nil
}

func (h *Handler) handleGetAlertHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.Params.Arguments.(map[string]any)

	ruleID, ok := args["ruleId"].(string)
	if !ok || ruleID == "" {
		h.logger.WarnContext(ctx, "Invalid or empty ruleId parameter", slog.Any("ruleId", args["ruleId"]))
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a non-empty string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "24h"}`), nil
	}

	startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")

	var start, end int64
	if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
		h.logger.WarnContext(ctx, "Invalid start timestamp format", slog.String("start", startStr), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697385600000") or use "timeRange" parameter instead (e.g., "24h")`, startStr)), nil
	}
	if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
		h.logger.WarnContext(ctx, "Invalid end timestamp format", slog.String("end", endStr), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697472000000") or use "timeRange" parameter instead (e.g., "24h")`, endStr)), nil
	}

	_, offset := paginate.ParseParams(args)

	limit := 20
	if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
		if limitInt, err := strconv.Atoi(limitStr); err != nil {
			h.logger.WarnContext(ctx, "Invalid limit format", slog.String("limit", limitStr), logpkg.ErrAttr(err))
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
			h.logger.WarnContext(ctx, "Invalid order value", slog.String("order", orderStr))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "order" value: "%s". Must be either "asc" or "desc"`, orderStr)), nil
		}
	}

	var state string
	if stateStr, ok := args["state"].(string); ok && stateStr != "" {
		if stateStr != "firing" && stateStr != "inactive" {
			h.logger.WarnContext(ctx, "Invalid state value", slog.String("state", stateStr))
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

	h.logger.DebugContext(ctx, "Tool called: signoz_get_alert_history",
		slog.String("ruleId", ruleID),
		slog.Int64("start", start),
		slog.Int64("end", end),
		slog.Int("offset", offset),
		slog.Int("limit", limit),
		slog.String("order", order))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	respJSON, err := client.GetAlertHistory(ctx, ruleID, historyReq)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get alert history",
			slog.String("ruleId", ruleID),
			logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(respJSON)), nil
}

func (h *Handler) handleCreateAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map for create alert.")
		return mcp.NewToolResultError(`Parameter validation failed: The alert configuration object is empty or improperly formatted.`), nil
	}

	cleanJSON, errResult := h.validateAlertPayload(ctx, rawConfig)
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_alert")
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := client.CreateAlertRule(ctx, cleanJSON)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create alert rule in SigNoz", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleUpdateAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map for update alert.")
		return mcp.NewToolResultError(`Parameter validation failed: The alert configuration object is empty or improperly formatted.`), nil
	}

	ruleID, _ := rawConfig["ruleId"].(string)
	if ruleID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" is required. Provide the UUIDv7 of the rule to update.`), nil
	}
	if !util.IsUUIDv7(ruleID) {
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "ruleId": %q is not a UUIDv7. Obtain the rule ID from signoz_list_alerts or signoz_get_alert.`, ruleID)), nil
	}
	delete(rawConfig, "ruleId")

	cleanJSON, errResult := h.validateAlertPayload(ctx, rawConfig)
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_alert", slog.String("ruleId", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := client.UpdateAlertRule(ctx, ruleID, cleanJSON); err != nil {
		h.logger.ErrorContext(ctx, "Failed to update alert rule in SigNoz", slog.String("ruleId", ruleID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(`{"status":"success","ruleId":%q}`, ruleID)), nil
}

func (h *Handler) handleDeleteAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError(`Parameter validation failed: expected an arguments object with "ruleId".`), nil
	}
	ruleID, _ := args["ruleId"].(string)
	if ruleID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "ruleId" is required.`), nil
	}
	if !util.IsUUIDv7(ruleID) {
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid "ruleId": %q is not a UUIDv7. The SigNoz API will reject this with invalid_input.`, ruleID)), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_alert", slog.String("ruleId", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := client.DeleteAlertRule(ctx, ruleID); err != nil {
		h.logger.ErrorContext(ctx, "Failed to delete alert rule in SigNoz", slog.String("ruleId", ruleID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(`{"status":"success","ruleId":%q}`, ruleID)), nil
}

// validateAlertPayload runs the alert validation pipeline and the
// notification-channel reference check shared by create and update. It returns
// the cleaned JSON body, or a non-nil tool-result describing the validation
// error to surface to the caller.
func (h *Handler) validateAlertPayload(ctx context.Context, rawConfig map[string]any) ([]byte, *mcp.CallToolResult) {
	delete(rawConfig, "searchContext")

	cleanJSON, err := alert.ValidateFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Alert validation failed", logpkg.ErrAttr(err))
		return nil, mcp.NewToolResultError(fmt.Sprintf("Alert validation error: %s", err.Error()))
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error())
	}

	availableChannels, err := fetchChannelNames(ctx, client)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to fetch notification channels for validation", logpkg.ErrAttr(err))
		return nil, mcp.NewToolResultError(fmt.Sprintf("Failed to fetch notification channels: %s", err.Error()))
	}

	referencedChannels := extractReferencedChannels(rawConfig)

	if len(referencedChannels) == 0 {
		return nil, mcp.NewToolResultError(formatNoChannelsError(availableChannels))
	}

	if invalid := findInvalidChannels(referencedChannels, availableChannels); len(invalid) > 0 {
		return nil, mcp.NewToolResultError(formatInvalidChannelsError(invalid, availableChannels))
	}

	return cleanJSON, nil
}

// fetchChannelNames retrieves all notification channel names from the SigNoz API.
func fetchChannelNames(ctx context.Context, c signozclient.Client) ([]string, error) {
	resp, err := c.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse notification channels response: %w", err)
	}

	names := make([]string, 0, len(parsed.Data))
	for _, ch := range parsed.Data {
		if ch.Name != "" {
			names = append(names, ch.Name)
		}
	}
	return names, nil
}

// extractReferencedChannels collects all channel names referenced in the alert
// payload from condition.thresholds.spec[].channels and preferredChannels.
func extractReferencedChannels(rawConfig map[string]any) []string {
	seen := map[string]bool{}

	// Check preferredChannels
	if pc, ok := rawConfig["preferredChannels"].([]any); ok {
		for _, v := range pc {
			if name, ok := v.(string); ok && name != "" {
				seen[name] = true
			}
		}
	}

	// Check condition.thresholds.spec[].channels
	cond, _ := rawConfig["condition"].(map[string]any)
	if cond == nil {
		return mapKeys(seen)
	}
	thresholds, _ := cond["thresholds"].(map[string]any)
	if thresholds == nil {
		return mapKeys(seen)
	}
	specs, _ := thresholds["spec"].([]any)
	for _, s := range specs {
		spec, ok := s.(map[string]any)
		if !ok {
			continue
		}
		channels, ok := spec["channels"].([]any)
		if !ok {
			continue
		}
		for _, ch := range channels {
			if name, ok := ch.(string); ok && name != "" {
				seen[name] = true
			}
		}
	}

	return mapKeys(seen)
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// findInvalidChannels returns channel names that are not in the available list.
func findInvalidChannels(referenced, available []string) []string {
	avail := map[string]bool{}
	for _, name := range available {
		avail[name] = true
	}
	var invalid []string
	for _, name := range referenced {
		if !avail[name] {
			invalid = append(invalid, name)
		}
	}
	return invalid
}

func formatNoChannelsError(available []string) string {
	var sb strings.Builder
	sb.WriteString("No notification channels specified in the alert. At least one channel is required.\n\n")

	if len(available) > 0 {
		sb.WriteString("Available notification channels:\n")
		for _, name := range available {
			sb.WriteString(fmt.Sprintf("  - %s\n", name))
		}
		sb.WriteString("\nPlease choose one or more channels and set them in condition.thresholds.spec[].channels.\n")
	} else {
		sb.WriteString("No notification channels exist yet.\n")
	}
	sb.WriteString("To create a new channel, use the signoz_create_notification_channel tool first.")
	return sb.String()
}

func formatInvalidChannelsError(invalid, available []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("The following notification channels do not exist: %s\n\n", strings.Join(invalid, ", ")))

	if len(available) > 0 {
		sb.WriteString("Available notification channels:\n")
		for _, name := range available {
			sb.WriteString(fmt.Sprintf("  - %s\n", name))
		}
		sb.WriteString("\nPlease use one of the available channels, or create a new one with signoz_create_notification_channel.")
	} else {
		sb.WriteString("No notification channels exist yet. Create one with signoz_create_notification_channel first.")
	}
	return sb.String()
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
