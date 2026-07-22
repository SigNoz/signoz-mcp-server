package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithOutputSchema[alertListOutput](),
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants current firing, silenced, or inhibited Alertmanager alert instances and their state, severity, timing, and rule IDs. Do not use it for configured rules or history: use signoz_list_alert_rules for rule summaries, signoz_get_alert for one definition, or signoz_get_alert_history for its timeline. Filter by alert labels, state, or receiver before paginating."),
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
		mcp.WithDescription("Use this when the user wants one configured alert rule's full definition, or before signoz_update_alert so unchanged fields can be preserved. It requires a known rule ID; use signoz_list_alert_rules to discover IDs. Do not use it for current alert instances or firing history: use signoz_list_alerts or signoz_get_alert_history."),
		// Not declared mcp.Required(): the legacy alias "ruleId" must remain a
		// valid call for schema-aware clients that validate args against the
		// advertised inputSchema. The handler validates that one of id/ruleId is
		// present. See readResourceID.
		mcp.WithString("id", mcp.Description("Alert rule ID (UUIDv7 on v2 servers). Required; obtain it from signoz_list_alert_rules.")),
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
				"Supported cases are v2alpha1 threshold alerts over metrics, logs, traces, or exceptions; v2alpha1 PromQL alerts; and metric-only v1 anomaly alerts, which use top-level evalWindow/frequency and no thresholds, evaluation, or schemaVersion. "+
				"Before composing the payload, read signoz://alert/instructions and signoz://alert/examples; for PromQL also read signoz://promql/instructions. "+
				"At least one valid notification channel is required, even when notificationSettings.usePolicy=true. Before creating, call signoz_list_notification_channels to verify user-provided names or show available names and ask the user to choose; never guess. If validation still rejects a channel name, show the current names and retry.",
		),
		mcp.WithInputSchema[types.CreateAlertInput](),
	)
	h.addTool(s, createAlertTool, h.handleCreateAlert)

	updateAlertTool := mcp.NewTool(
		"signoz_update_alert",
		withUpdateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to change an existing SigNoz alert rule; use signoz_create_alert for a new rule. This is a full replacement: first call signoz_get_alert and merge the requested change while preserving every other field. Before composing, read signoz://alert/instructions and signoz://alert/examples; for PromQL also read signoz://promql/instructions. Before updating, call signoz_list_notification_channels to verify every selected channel name or show available names and ask the user to choose; never guess. At least one valid channel is required even when notificationSettings.usePolicy=true. If validation still rejects a channel name, show the current names and retry.",
		),
		mcp.WithInputSchema[types.UpdateAlertInput](),
	)
	h.addTool(s, updateAlertTool, h.handleUpdateAlert)

	deleteAlertTool := mcp.NewTool(
		"signoz_delete_alert",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithString("id", mcp.Description("Alert rule UUIDv7. Required; obtain it from signoz_list_alert_rules.")),
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
	base, _ := util.GetSigNozURL(ctx)
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

	base, _ := util.GetSigNozURL(ctx)
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

	respJSON = enrichAlertWebURL(ctx, respJSON, ruleID)
	return structuredResult(respJSON), nil
}

// enrichAlertWebURL injects a webUrl deep link into a single-alert passthrough
// body. Delegates to util.InjectWebURL, which preserves large int64 fields and
// fails open on unparseable input.
func enrichAlertWebURL(ctx context.Context, data []byte, ruleID string) []byte {
	base, _ := util.GetSigNozURL(ctx)
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

	cleanJSON, errResult := h.validateAlertPayload(ctx, rawConfig)
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
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide the UUIDv7 of the rule to update.`), nil
	}
	if !util.IsUUIDv7(ruleID) {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "id": %q is not a UUIDv7. Obtain the rule ID from signoz_list_alert_rules or signoz_get_alert.`, ruleID)), nil
	}
	delete(rawConfig, "id")
	delete(rawConfig, "ruleId")

	cleanJSON, errResult := h.validateAlertPayload(ctx, rawConfig)
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
	if !util.IsUUIDv7(ruleID) {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "id": %q is not a UUIDv7. The SigNoz API will reject this with invalid_input.`, ruleID)), nil
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

// validateAlertPayload runs the alert validation pipeline and the
// notification-channel reference check shared by create and update. It returns
// the cleaned JSON body, or a non-nil tool-result describing the validation
// error to surface to the caller.
func (h *Handler) validateAlertPayload(ctx context.Context, rawConfig map[string]any) ([]byte, *mcp.CallToolResult) {
	delete(rawConfig, "searchContext")
	for _, field := range serverPopulatedAlertFields {
		delete(rawConfig, field)
	}

	cleanJSON, err := alert.ValidateFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Alert validation failed", logpkg.ErrAttr(err))
		return nil, validationResult(fmt.Sprintf("Alert validation error: %s", err.Error()))
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, clientError(err)
	}

	availableChannels, err := fetchChannelNames(ctx, client)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to fetch notification channels for validation", logpkg.ErrAttr(err))
		return nil, upstreamError(fmt.Errorf("could not fetch notification channels for alert validation: %w", err))
	}

	referencedChannels := extractReferencedChannels(rawConfig)

	if len(referencedChannels) == 0 {
		return nil, validationResult(formatNoChannelsError(availableChannels))
	}

	if invalid := findInvalidChannels(referencedChannels, availableChannels); len(invalid) > 0 {
		return nil, validationResult(formatInvalidChannelsError(invalid, availableChannels))
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
		mcp.WithResourceDescription("Read this before creating or updating an alert. It explains fields, rule types, queries, thresholds, evaluation, and notification setup. For payload examples, read signoz://alert/examples."),
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
		mcp.WithResourceDescription("Read this after signoz://alert/instructions when you need alert payload examples for metrics, logs, traces, exceptions, PromQL, anomalies, formulas, tiered thresholds, cumulative alerts, or notification settings. Verify every example channel name with signoz_list_notification_channels."),
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
