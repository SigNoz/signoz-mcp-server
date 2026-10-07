package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/alert"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type alertListOutput struct {
	Data       []types.Alert     `json:"data"`
	Pagination paginate.Metadata `json:"pagination"`
}

type alertRuleListOutput struct {
	Data       []types.AlertRuleSummary `json:"data"`
	Pagination paginate.Metadata        `json:"pagination"`
}

var serverPopulatedAlertFields = []string{
	"createdAt", "updatedAt", "createdBy", "updatedBy",
	"createAt", "updateAt", "createBy", "updateBy",
}

var alertHistoryStateValues = []string{
	"inactive", "pending", "recovering", "firing", "nodata", "disabled",
}

func (h *Handler) RegisterAlertsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithOutputSchema[alertListOutput](),
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants current firing, silenced, or inhibited Alertmanager alert instances and their state, severity, timing, and rule IDs. Do not use it for configured rules or history: use signoz_list_alert_rules for rule summaries, signoz_get_alert for one definition, or signoz_get_alert_history for one rule's timeline. Filter by alert labels, state, or receiver before paginating."),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of alerts to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of results to skip for pagination. Default: 0.")),
		mcp.WithBoolean("active", boolOrStringType(), mcp.Description("Include active (firing) alerts. Default: true (server-side).")),
		mcp.WithBoolean("silenced", boolOrStringType(), mcp.Description("Include silenced alerts. Default: true (server-side).")),
		mcp.WithBoolean("inhibited", boolOrStringType(), mcp.Description("Include inhibited alerts. Default: true (server-side).")),
		mcp.WithString("filter", mcp.Description("Comma-separated alert-label comparisons; each is a label followed by =, !=, =~ (regex), or !~ (negative regex) and a quoted value. Examples: 'alertname=\"HighCPU\"' or 'alertname=\"HighCPU\",severity=\"critical\"'. All comparisons must match.")),
		mcp.WithString("receiver", mcp.Description("Regex to filter alerts by receiver name. Example: 'slack-.*' to match all Slack receivers.")),
	)
	h.addTool(s, alertsTool, h.handleListAlerts)

	alertRulesTool := mcp.NewTool("signoz_list_alert_rules",
		mcp.WithOutputSchema[alertRuleListOutput](),
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants configured alert-rule summaries, including inactive/OK and disabled rules. It returns rule IDs, names, types, state, severity, labels, and timestamps; use signoz_get_alert with an ID for the full definition. Do not use it for current firing/silenced/inhibited instances: use signoz_list_alerts. Paginate with limit and offset."),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of alert rules to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of results to skip for pagination. Default: 0.")),
	)
	h.addTool(s, alertRulesTool, h.handleListAlertRules)

	getAlertTool := mcp.NewTool("signoz_get_alert",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants one configured alert rule's full definition, or before signoz_update_alert when a complete current definition is not already available for the prepared operation. Reuse a still-current definition fetched for that operation instead of repeating this preflight. It requires a known rule ID; use signoz_list_alert_rules to discover IDs. Do not use it for current alert instances or firing history: use signoz_list_alerts or signoz_get_alert_history."),
		// Not declared mcp.Required(): the legacy alias "ruleId" must remain a
		// valid call for schema-aware clients that validate args against the
		// advertised inputSchema. The handler validates that one of id/ruleId is
		// present. See readResourceID.
		mcp.WithString("id", mcp.Description("Alert rule UUID. Required; obtain it from signoz_list_alert_rules.")),
	)
	h.addTool(s, getAlertTool, h.handleGetAlert)

	alertHistoryTool := mcp.NewTool("signoz_get_alert_history",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants alert firing history or the state-transition timeline of one configured rule; use signoz_list_alerts for current instances and signoz_get_alert for the rule definition. It requires a rule ID from signoz_list_alert_rules, defaults to the last 6 hours, and supports state/filter narrowing. For the next page, pass data.nextCursor as cursor and repeat the original filters, time range, and order."),
		mcp.WithString("id", mcp.Description("Alert rule ID. Required; obtain it from signoz_list_alert_rules.")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start timestamp in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End timestamp in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("state", mcp.Enum(alertHistoryStateValues...), mcp.Description("Filter by alert state: inactive, pending, recovering, firing, nodata, or disabled. Omit to return all transitions.")),
		mcp.WithString("filter", mcp.Description("Filter timeline labels using SigNoz query-builder syntax. Combine conditions with AND, OR, and parentheses; quote string values with single quotes and use operators such as =, !=, IN, and NOT IN. Example: \"severity = 'critical' AND (team = 'payments' OR service.name = 'checkout')\". To discover label keys, first call without a filter and inspect data.items[].labels[].key.name. If a filter returns no matches, retry unfiltered and verify the key spelling; malformed expressions return validation errors.")),
		mcp.WithString("cursor", mcp.Description("Opaque continuation cursor. Repeat the original time range, state, filter, and order when fetching the next page. Omit cursor for the first page.")),
		mcp.WithString("limit", mcp.DefaultString("20"), intOrStringType(), mcp.Description("Rows per page. Default: 20; max: 10000 (higher values are clamped).")),
		mcp.WithString("order", mcp.DefaultString("asc"), mcp.Enum("asc", "desc"), mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	h.addTool(s, alertHistoryTool, h.handleGetAlertHistory)

	createAlertTool := mcp.NewTool(
		"signoz_create_alert",
		withCreateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants a new SigNoz alert rule; use signoz_update_alert to change an existing rule. "+
				"Supports v2alpha1 threshold/PromQL alerts and metric-only v1 anomaly alerts. Reuse signoz://alert/instructions and signoz://alert/examples from the same prepared operation; for PromQL read signoz://promql/instructions when needed. "+
				"For direct routing, reuse a fully paginated signoz_list_notification_channels result only from the same still-current prepared operation; otherwise call it, refreshing only if state may have changed. Use exact returned immutable routing displayName values, not machine name. If none fits, ask the user or offer signoz_create_notification_channel with settings the user provides; never guess or create automatically. V2 direct routing needs a channel on every threshold tier; SigNoz ignores a v2 top-level preferredChannels list (those channels are never notified). Confirmed v2 policy routing may omit tier channels; v1 anomaly uses preferredChannels.",
		),
		mcp.WithInputSchema[types.CreateAlertInput](),
	)
	h.addTool(s, createAlertTool, h.handleCreateAlert)

	updateAlertTool := mcp.NewTool(
		"signoz_update_alert",
		withUpdateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to change an existing SigNoz alert rule; use signoz_create_alert for a new rule. This is a full replacement: call signoz_get_alert unless its complete result is available from the same still-current prepared operation, then preserve every unchanged field. Likewise reuse signoz://alert/instructions, signoz://alert/examples, and a fully paginated signoz_list_notification_channels result only from that operation; otherwise read/call them, refreshing only if state may have changed. Use exact returned immutable routing displayName values, not machine name. If no direct channel fits, ask the user or offer signoz_create_notification_channel with settings the user provides; never create automatically. V2 direct routing needs a channel on every threshold tier; SigNoz ignores a v2 top-level preferredChannels list (those channels are never notified). Confirmed v2 policy routing may omit tier channels; v1 anomaly uses preferredChannels.",
		),
		mcp.WithInputSchema[types.UpdateAlertInput](),
	)
	h.addTool(s, updateAlertTool, h.handleUpdateAlert)

	deleteAlertTool := mcp.NewTool(
		"signoz_delete_alert",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithString("id", mcp.Description("Alert rule UUID. Required; obtain it from signoz_list_alert_rules.")),
		mcp.WithDescription("Use this when the user explicitly wants to permanently delete a configured alert rule. Resolve its ID with signoz_list_alert_rules and confirm the exact rule first. If both steps are already complete, call this tool directly without repeating list/get preflight. Do not use it to disable a rule or clear a firing instance."),
	)
	h.addTool(s, deleteAlertTool, h.handleDeleteAlert)

	// Register alert resources for create alert
	h.registerAlertResources(s)
}

// parseTriStateBool reads an optional boolean filter that must stay nil when
// absent (so the backend applies its own default) but hard-errors on a garbage
// value rather than silently dropping it (which previously widened results).
func parseTriStateBool(args map[string]any, key string) (*bool, error) {
	v, present, err := parseBoolArg(args, key)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return &v, nil
}

func (h *Handler) handleListAlerts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_alerts")
	args := req.GetArguments()
	limit, offset, limitClamped := paginate.ParseParamsClamped(args)

	active, err := parseTriStateBool(args, "active")
	if err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	}
	inhibited, err := parseTriStateBool(args, "inhibited")
	if err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	}
	silenced, err := parseTriStateBool(args, "silenced")
	if err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	}
	params := types.ListAlertsParams{
		Active:    active,
		Inhibited: inhibited,
		Silenced:  silenced,
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
		return clientError(err), nil
	}
	alerts, err := client.ListAlerts(ctx, params)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list alerts", err)
		return upstreamError(err), nil
	}

	var apiResponse types.APIAlertsResponse
	if err := json.Unmarshal(alerts, &apiResponse); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse alerts response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(alerts)))
		return upstreamResponseError("failed to parse alerts response: " + err.Error()), nil
	}

	// takes only meaningful data
	base := h.resourceWebURLBase(ctx)
	alertsList := make([]types.Alert, 0, len(apiResponse.Data))
	for _, apiAlert := range apiResponse.Data {
		webURL, _ := util.ResourceWebURL(base, "alert", apiAlert.Labels.RuleID)
		alertsList = append(alertsList, types.Alert{
			Alertname: apiAlert.Labels.Alertname,
			RuleID:    apiAlert.Labels.RuleID,
			Severity:  apiAlert.Labels.Severity,
			StartsAt:  apiAlert.StartsAt,
			EndsAt:    apiAlert.EndsAt,
			State:     apiAlert.Status.State,
			WebURL:    webURL,
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
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleListAlertRules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_alert_rules")
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	rules, err := client.ListAlertRules(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list alert rules", err)
		return upstreamError(err), nil
	}

	var apiResponse types.APIAlertRulesResponse
	if err := json.Unmarshal(rules, &apiResponse); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse alert rules response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(rules)))
		return upstreamResponseError("failed to parse alert rules response: " + err.Error()), nil
	}

	base := h.resourceWebURLBase(ctx)
	ruleSummaries := make([]types.AlertRuleSummary, 0, len(apiResponse.Data))
	for _, apiRule := range apiResponse.Data {
		createdAt := apiRule.CreatedAt
		if createdAt == "" {
			createdAt = apiRule.CreateAt
		}
		updatedAt := apiRule.UpdatedAt
		if updatedAt == "" {
			updatedAt = apiRule.UpdateAt
		}

		webURL, _ := util.ResourceWebURL(base, "alert", apiRule.ID)
		ruleSummaries = append(ruleSummaries, types.AlertRuleSummary{
			RuleID:      apiRule.ID,
			Alert:       apiRule.Alert,
			AlertType:   apiRule.AlertType,
			RuleType:    apiRule.RuleType,
			State:       apiRule.State,
			Disabled:    apiRule.Disabled,
			Severity:    apiRule.Labels["severity"],
			Description: apiRule.Description,
			Labels:      apiRule.Labels,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			WebURL:      webURL,
		})
	}

	total := len(ruleSummaries)
	rulesArray := make([]any, len(ruleSummaries))
	for i, v := range ruleSummaries {
		rulesArray[i] = v
	}
	pagedRules := paginate.Array(rulesArray, offset, limit)

	resultJSON, err := paginate.Wrap(pagedRules, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap alert rules with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleGetAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	ruleID := readResourceID(args, "ruleId")
	if ruleID == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid alert rule ID (UUID format). Example: {"id": "0196634d-5d66-75c4-b778-e317f49dab7a"}`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_alert", slog.String("id", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get alert", err, slog.String("ruleId", ruleID))
		return upstreamError(err), nil
	}

	respJSON = h.enrichAlertWebURL(ctx, respJSON, ruleID)
	return structuredResult(respJSON), nil
}

// enrichAlertWebURL injects a webUrl deep link into a single-alert passthrough
// body. Delegates to util.InjectWebURL, which preserves large int64 fields and
// fails open on unparseable input.
func (h *Handler) enrichAlertWebURL(ctx context.Context, data []byte, ruleID string) []byte {
	base := h.resourceWebURLBase(ctx)
	return util.InjectWebURL(data, base, "alert", ruleID)
}

func (h *Handler) handleGetAlertHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	ruleID := readResourceID(args, "ruleId")
	if ruleID == "" {
		h.logger.WarnContext(ctx, "Invalid or empty id parameter", slog.Any("id", args["id"]), slog.Any("ruleId", args["ruleId"]))
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Example: {"id": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "24h"}`), nil
	}

	if _, present := args["offset"]; present {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "offset" is no longer supported; use data.nextCursor as "cursor" for subsequent pages.`), nil
	}

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")
	var start, end int64
	if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
		h.logger.WarnContext(ctx, "Invalid start timestamp format", slog.String("start", startStr), logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697385600000") or use "timeRange" parameter instead (e.g., "24h")`, startStr)), nil
	}
	if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
		h.logger.WarnContext(ctx, "Invalid end timestamp format", slog.String("end", endStr), logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected milliseconds since epoch (e.g., "1697472000000") or use "timeRange" parameter instead (e.g., "24h")`, endStr)), nil
	}
	if start >= end {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "start" must be earlier than "end".`), nil
	}

	cursor := strings.TrimSpace(stringArg(args, "cursor"))
	defaultLimit := 20
	if cursor != "" {
		defaultLimit = 0 // let the upstream cursor retain its encoded page size
	}
	limit, err := intArg(args, "limit", defaultLimit)
	if err != nil {
		h.logger.WarnContext(ctx, "Invalid limit format", slog.Any("limit", args["limit"]), logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	limit, limitClamped := clampLimit(limit)

	order := "asc"
	if orderArg := strings.TrimSpace(stringArg(args, "order")); orderArg != "" {
		if orderArg != "asc" && orderArg != "desc" {
			h.logger.WarnContext(ctx, "Invalid order value", slog.String("order", orderArg))
			return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "order" value: "%s". Must be either "asc" or "desc"`, orderArg)), nil
		}
		order = orderArg
	}

	state := strings.TrimSpace(stringArg(args, "state"))
	if state != "" {
		valid := false
		for _, candidate := range alertHistoryStateValues {
			if state == candidate {
				valid = true
				break
			}
		}
		if !valid {
			h.logger.WarnContext(ctx, "Invalid state value", slog.String("state", state))
			return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "state" value: "%s". Must be one of: %s`, state, strings.Join(alertHistoryStateValues, ", "))), nil
		}
	}

	filterExpression := strings.TrimSpace(stringArg(args, "filter"))
	if filterExpression == "" {
		filterExpression = strings.TrimSpace(stringArg(args, "filterExpression"))
	}

	historyReq := types.AlertHistoryRequest{
		Start:            start,
		End:              end,
		State:            state,
		FilterExpression: filterExpression,
		Limit:            limit,
		Order:            order,
		Cursor:           cursor,
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_alert_history",
		slog.String("ruleId", ruleID),
		slog.Int64("start", historyReq.Start),
		slog.Int64("end", historyReq.End),
		slog.Bool("hasCursor", historyReq.Cursor != ""),
		slog.Int("limit", historyReq.Limit),
		slog.String("order", historyReq.Order))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	respJSON, err := client.GetAlertHistory(ctx, ruleID, historyReq)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get alert history", err, slog.String("ruleId", ruleID))
		var statusErr *signozclient.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			result := upstreamError(err)
			result.Content = append(result.Content, mcp.NewTextContent(
				`recovery: Verify "id" in the SigNoz UI or, on SigNoz v0.120.0+, with signoz_list_alert_rules. If the rule exists, upgrade SigNoz to v0.118.0 or later; older versions do not support this tool.`))
			return result, nil
		}
		return upstreamError(err), nil
	}

	returnedRows, rowsKnown := countAlertHistoryRows(respJSON)
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"cursor\" (or narrow the time range) for more.",
			MaxRawResultLimit))
	}
	notes = append(notes, alertHistoryCompletenessNote(
		respJSON, returnedRows, historyReq.Limit, rowsKnown,
		historyReq.Start, historyReq.End, historyReq.Order,
	))
	return resultWithNotes(respJSON, notes...), nil
}

func (h *Handler) handleCreateAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map for create alert.")
		return notAConfigObjectError(), nil
	}

	cleanJSON, errResult := h.prepareAlertPayload(ctx, rawConfig)
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_alert")
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	data, err := client.CreateAlertRule(ctx, cleanJSON)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create alert rule in SigNoz", err)
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleUpdateAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map for update alert.")
		return notAConfigObjectError(), nil
	}

	ruleID := readResourceID(rawConfig, "ruleId")
	if ruleID == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Obtain the rule UUID from signoz_list_alert_rules.`), nil
	}
	delete(rawConfig, "id")
	delete(rawConfig, "ruleId")

	cleanJSON, errResult := h.prepareAlertPayload(ctx, rawConfig)
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_alert", slog.String("ruleId", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	if err := client.UpdateAlertRule(ctx, ruleID, cleanJSON); err != nil {
		h.logUpstreamFailure(ctx, "Failed to update alert rule in SigNoz", err, slog.String("ruleId", ruleID))
		return upstreamError(err), nil
	}

	return structuredResult([]byte(fmt.Sprintf(`{"status":"success","ruleId":%q}`, ruleID))), nil
}

func (h *Handler) handleDeleteAlert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	ruleID := readResourceID(args, "ruleId")
	if ruleID == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required.`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_alert", slog.String("id", ruleID))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	if err := client.DeleteAlertRule(ctx, ruleID); err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete alert rule in SigNoz", err, slog.String("ruleId", ruleID))
		return upstreamError(err), nil
	}

	return structuredResult([]byte(fmt.Sprintf(`{"status":"success","ruleId":%q}`, ruleID))), nil
}

// prepareAlertPayload strips MCP metadata and server-populated fields and
// normalizes the body (shared query bounds, MCP defaults, schema split).
// SigNoz is the only validator of rule bodies, including notification-channel
// references, which it checks against channel display names.
func (h *Handler) prepareAlertPayload(ctx context.Context, rawConfig map[string]any) ([]byte, *mcp.CallToolResult) {
	delete(rawConfig, "searchContext")
	for _, field := range serverPopulatedAlertFields {
		delete(rawConfig, field)
	}

	cleanJSON, err := alert.NormalizeFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Alert payload normalization failed", logpkg.ErrAttr(err))
		return nil, validationResult(fmt.Sprintf("Alert payload error: %s", err.Error()))
	}
	return cleanJSON, nil
}

func (h *Handler) registerAlertResources(s *mcp.Server) {
	alertInstructions := mcp.NewResource(
		"signoz://alert/instructions",
		"Alert Rule Instructions",
		mcp.WithResourceDescription("Read this before creating or updating an alert unless its current content was already read for the same prepared operation. It explains fields, rule types, queries, thresholds, evaluation, and when to reuse or call signoz_list_notification_channels. Read signoz://alert/examples only when examples are still needed."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(alert.Instructions))),
	)

	h.addResource(s, alertInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     alert.Instructions,
			},
		}, nil
	})

	alertExamples := mcp.NewResource(
		"signoz://alert/examples",
		"Alert Rule Examples",
		mcp.WithResourceDescription("Read this after signoz://alert/instructions only when examples are still needed. Resolve illustrative direct/anomaly names with signoz_list_notification_channels; if none fits, offer signoz_create_notification_channel. Confirmed v2 policy routing may omit direct channels; anomaly cannot use policy routing."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(alert.Examples))),
	)

	h.addResource(s, alertExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     alert.Examples,
			},
		}, nil
	})
}
