package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

var validChannelTypes = map[string]bool{
	"slack":     true,
	"webhook":   true,
	"pagerduty": true,
	"email":     true,
	"opsgenie":  true,
	"msteams":   true,
}

func (h *Handler) RegisterNotificationChannelHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering notification channel handlers")

	listChannelsTool := mcp.NewTool("signoz_list_notification_channels",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants to discover configured notification channels, verify exact channel names before creating or updating an alert, avoid a duplicate name before channel creation, or find a channel ID. It returns paginated summaries only: id, name, type, and timestamps; it does not return provider-specific settings. Use signoz_get_notification_channel with an ID for all settings.",
		),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of channels to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50). Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0.")),
	)

	h.addTool(s, listChannelsTool, h.handleListNotificationChannels)

	createChannelTool := mcp.NewTool("signoz_create_notification_channel",
		withCreateToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants a new SigNoz notification channel. First call signoz_list_notification_channels and confirm the requested name is not already used. Supply the provider-specific required field documented in the input schema. A test notification is sent after creation; if the test fails, the channel still exists and the response reports the failure.\n"+
				"SUPPORTED TYPES: slack, webhook, pagerduty, email, opsgenie, msteams\n\n"+
				"Use signoz_update_notification_channel to change an existing channel.",
		),
		// Common fields
		mcp.WithString("type", mcp.Required(), mcp.Description("Channel type: slack, webhook, pagerduty, email, opsgenie, or msteams.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique channel name. Before creating, verify it is unused with signoz_list_notification_channels.")),
		mcp.WithBoolean("send_resolved", boolOrStringType(), mcp.Description("Whether to send notifications when alerts resolve. Default: true.")),

		// Slack fields
		mcp.WithString("slack_api_url", mcp.Description("Slack incoming webhook URL. Required when type=slack. Example: https://hooks.slack.com/services/T.../B.../xxx")),
		mcp.WithString("slack_channel", mcp.Description("Slack channel or username to post to. Example: '#alerts' or '@oncall'")),
		mcp.WithString("slack_title", mcp.Description("Message title template (Go template syntax supported)")),
		mcp.WithString("slack_text", mcp.Description("Message body template (Go template syntax supported)")),

		// Webhook fields
		mcp.WithString("webhook_url", mcp.Description("Webhook endpoint URL. Required when type=webhook")),
		mcp.WithString("webhook_username", mcp.Description("Username for basic authentication (optional)")),
		mcp.WithString("webhook_password", mcp.Description("Password for basic authentication (optional)")),

		// PagerDuty fields
		mcp.WithString("pagerduty_routing_key", mcp.Description("PagerDuty integration/routing key. Required when type=pagerduty")),
		mcp.WithString("pagerduty_description", mcp.Description("Incident description (Go template syntax supported)")),
		mcp.WithString("pagerduty_severity", mcp.Description("Incident severity: critical, error, warning, or info")),

		// Email fields
		mcp.WithString("email_to", mcp.Description("Comma-separated list of email addresses. Required when type=email")),
		mcp.WithString("email_html", mcp.Description("Custom HTML email template (Go template syntax supported)")),

		// OpsGenie fields
		mcp.WithString("opsgenie_api_key", mcp.Description("OpsGenie API key. Required when type=opsgenie")),
		mcp.WithString("opsgenie_message", mcp.Description("Alert message (Go template syntax supported)")),
		mcp.WithString("opsgenie_description", mcp.Description("Alert description (Go template syntax supported)")),
		mcp.WithString("opsgenie_priority", mcp.Description("Alert priority: P1, P2, P3, P4, or P5")),

		// MS Teams fields
		mcp.WithString("msteams_webhook_url", mcp.Description("MS Teams incoming webhook URL. Required when type=msteams")),
		mcp.WithString("msteams_title", mcp.Description("Message title template (Go template syntax supported)")),
		mcp.WithString("msteams_text", mcp.Description("Message body template (Go template syntax supported)")),
	)

	h.addTool(s, createChannelTool, h.handleCreateNotificationChannel)

	updateChannelTool := mcp.NewTool("signoz_update_notification_channel",
		withNonIdempotentUpdateToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants to change an existing SigNoz notification channel. This is a full replacement: first find the ID with signoz_list_notification_channels, then call signoz_get_notification_channel and merge the requested change while preserving the complete provider configuration and send_resolved value. Omitting send_resolved resets it to true. A test notification is sent after update; an update can succeed even when that test fails, which is reported in the response.\n"+
				"SUPPORTED TYPES: slack, webhook, pagerduty, email, opsgenie, msteams\n\n"+
				"Do not use this for partial updates.",
		),
		// ID field (required for update)
		mcp.WithString("id", mcp.Required(), mcp.Description("Notification channel UUID. Obtain it from signoz_list_notification_channels.")),
		// Common fields
		mcp.WithString("type", mcp.Required(), mcp.Description("Complete replacement channel type: slack, webhook, pagerduty, email, opsgenie, or msteams. Preserve the current type unless the user requested a change.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Complete replacement channel name. Preserve the current name unless the user requested a change.")),
		mcp.WithBoolean("send_resolved", boolOrStringType(), mcp.Description("Complete replacement resolved-notification setting. Copy the current value from signoz_get_notification_channel unless changing it; omission resets to true.")),

		// Slack fields
		mcp.WithString("slack_api_url", mcp.Description("Slack incoming webhook URL. Required when type=slack. Example: https://hooks.slack.com/services/T.../B.../xxx")),
		mcp.WithString("slack_channel", mcp.Description("Slack channel or username to post to. Example: '#alerts' or '@oncall'")),
		mcp.WithString("slack_title", mcp.Description("Message title template (Go template syntax supported)")),
		mcp.WithString("slack_text", mcp.Description("Message body template (Go template syntax supported)")),

		// Webhook fields
		mcp.WithString("webhook_url", mcp.Description("Webhook endpoint URL. Required when type=webhook")),
		mcp.WithString("webhook_username", mcp.Description("Username for basic authentication (optional)")),
		mcp.WithString("webhook_password", mcp.Description("Password for basic authentication (optional)")),

		// PagerDuty fields
		mcp.WithString("pagerduty_routing_key", mcp.Description("PagerDuty integration/routing key. Required when type=pagerduty")),
		mcp.WithString("pagerduty_description", mcp.Description("Incident description (Go template syntax supported)")),
		mcp.WithString("pagerduty_severity", mcp.Description("Incident severity: critical, error, warning, or info")),

		// Email fields
		mcp.WithString("email_to", mcp.Description("Comma-separated list of email addresses. Required when type=email")),
		mcp.WithString("email_html", mcp.Description("Custom HTML email template (Go template syntax supported)")),

		// OpsGenie fields
		mcp.WithString("opsgenie_api_key", mcp.Description("OpsGenie API key. Required when type=opsgenie")),
		mcp.WithString("opsgenie_message", mcp.Description("Alert message (Go template syntax supported)")),
		mcp.WithString("opsgenie_description", mcp.Description("Alert description (Go template syntax supported)")),
		mcp.WithString("opsgenie_priority", mcp.Description("Alert priority: P1, P2, P3, P4, or P5")),

		// MS Teams fields
		mcp.WithString("msteams_webhook_url", mcp.Description("MS Teams incoming webhook URL. Required when type=msteams")),
		mcp.WithString("msteams_title", mcp.Description("Message title template (Go template syntax supported)")),
		mcp.WithString("msteams_text", mcp.Description("Message body template (Go template syntax supported)")),
	)

	h.addTool(s, updateChannelTool, h.handleUpdateNotificationChannel)

	getChannelTool := mcp.NewTool("signoz_get_notification_channel",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants all provider-specific settings for one notification channel, especially before replacing it with signoz_update_notification_channel. It requires a known channel ID; use signoz_list_notification_channels to discover IDs. Do not use it to list channel names."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Notification channel UUID. Obtain it from signoz_list_notification_channels.")),
	)
	h.addTool(s, getChannelTool, h.handleGetNotificationChannel)

	deleteChannelTool := mcp.NewTool("signoz_delete_notification_channel",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user explicitly wants to permanently delete a notification channel. Resolve its ID with signoz_list_notification_channels and confirm the exact channel first. If both steps are already complete, call this tool directly without repeating list/get preflight. This tool does not check whether alert rules reference the channel; inspect configured rules first when dependency safety is required."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Notification channel UUID. Obtain it from signoz_list_notification_channels.")),
	)
	h.addTool(s, deleteChannelTool, h.handleDeleteNotificationChannel)
}

func (h *Handler) handleGetNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	id, errResult := requireStringArg(args, "id")
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_notification_channel", slog.String("id", id))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	resp, err := client.GetNotificationChannel(ctx, id)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get notification channel", err, slog.String("id", id))
		return upstreamError(err), nil
	}
	return structuredResult(resp), nil
}

func (h *Handler) handleDeleteNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	id, errResult := requireStringArg(args, "id")
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_notification_channel", slog.String("id", id))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	if err := client.DeleteNotificationChannel(ctx, id); err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete notification channel", err, slog.String("id", id))
		return upstreamError(err), nil
	}
	return structuredResult([]byte(fmt.Sprintf(`{"status":"success","id":%q}`, id))), nil
}

func (h *Handler) handleListNotificationChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_notification_channels")
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	result, err := client.ListNotificationChannels(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list notification channels", err)
		return upstreamError(err), nil
	}

	var response map[string]any
	if err := json.Unmarshal(result, &response); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse notification channels response", logpkg.ErrAttr(err))
		return upstreamResponseError("failed to parse response: " + err.Error()), nil
	}

	// Upstream returns `data: null`, omits `data`, or returns an empty
	// object/scalar when there are no channels. Treat any non-array shape as zero
	// rows rather than surfacing a format error (mirrors the list_views
	// coerce-to-empty-page pattern).
	var data []any
	if raw, present := response["data"]; present && raw != nil {
		if arr, ok := raw.([]any); ok {
			data = arr
		} else {
			h.logger.DebugContext(ctx, "notification channels response data was not an array; treating as empty",
				slog.String("data", logpkg.TruncAny(raw)))
		}
	}

	// Summarize each channel to essential fields only (id, name, type, timestamps).
	// The raw "data" field contains the full config (webhook URLs, API keys, templates)
	// which bloats the response beyond token limits. Name lives on the top-level
	// Channel field; if absent (older SigNoz), fall back to the nested data.name.
	summarized := make([]any, 0, len(data))
	for _, item := range data {
		ch, ok := item.(map[string]any)
		if !ok {
			continue
		}
		summary := map[string]any{
			"id":        ch["id"],
			"type":      ch["type"],
			"createdAt": ch["createdAt"],
			"updatedAt": ch["updatedAt"],
		}
		if name, ok := ch["name"].(string); ok && name != "" {
			summary["name"] = name
		} else if dataStr, ok := ch["data"].(string); ok && dataStr != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(dataStr), &parsed); err == nil {
				summary["name"] = parsed["name"]
			}
		}
		summarized = append(summarized, summary)
	}

	total := len(summarized)
	pagedData := paginate.Array(summarized, offset, limit)

	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap notification channels with pagination", logpkg.ErrAttr(err))
		return internalError("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleCreateNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_create_notification_channel")

	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	// Validate required fields
	channelType, err := requireStringField(args, "type", ". Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	if !validChannelTypes[channelType] {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid channel type: "%s". Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams`, channelType)), nil
	}

	name, err := requireStringField(args, "name", ". Provide a unique name for the notification channel")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	sendResolved := true
	if v, present, err := parseBoolArg(args, "send_resolved"); err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	} else if present {
		sendResolved = v
	}

	// Errors here are per-type required-field validation failures, coded for retry.
	receiverJSON, err := buildReceiverJSON(channelType, name, sendResolved, args)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to build receiver JSON", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	// Step 1: Create the channel
	createResp, err := client.CreateNotificationChannel(ctx, receiverJSON)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create notification channel", err, slog.String("type", channelType), slog.String("name", name))
		return upstreamError(err), nil
	}

	h.logger.InfoContext(ctx, "Notification channel created", slog.String("type", channelType), slog.String("name", name))

	// Step 2: Test the channel
	testErr := client.TestNotificationChannel(ctx, receiverJSON)

	// Build combined response
	result := map[string]any{
		"channel": json.RawMessage(createResp),
	}

	var testFailureNote string
	if testErr != nil {
		h.logger.WarnContext(ctx, "Test notification failed", slog.String("name", name), logpkg.ErrAttr(testErr))
		result["test_notification"] = map[string]any{
			"success": false,
			"error":   testErr.Error(),
			"message": fmt.Sprintf("Channel '%s' was created but the test notification failed: %s. Please verify the channel configuration.", name, testErr.Error()),
		}
		testFailureNote = testNotificationWarningNote(name, "created", testErr)
	} else {
		h.logger.InfoContext(ctx, "Test notification sent successfully", slog.String("name", name))
		result["test_notification"] = map[string]any{
			"success": true,
			"message": fmt.Sprintf("Test notification sent successfully to channel '%s'. The channel is working correctly.", name),
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return internalError("failed to marshal response: " + err.Error()), nil
	}

	// Fail OPEN: the channel WAS created, so we do not flip IsError (avoids a
	// misleading error and a duplicate-create retry). The test-send failure is
	// surfaced as a prominent advisory note alongside the structured body.
	return structuredResultWithNotes(resultJSON, testFailureNote), nil
}

// testNotificationWarningNote formats the prominent advisory shown when a
// channel was created/updated successfully but its verification test-send
// failed. Kept uniform with the other "note:" advisory blocks.
func testNotificationWarningNote(name, action string, testErr error) string {
	return fmt.Sprintf(
		"note: WARNING — notification channel %q was %s successfully, but the verification test notification FAILED: %s. The channel exists but may not deliver alerts; verify its configuration (URL/key/credentials) and re-test.",
		name, action, testErr.Error())
}

func (h *Handler) handleUpdateNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_update_notification_channel")

	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	// Validate id
	id, err := requireStringField(args, "id", ". Provide the UUID of the notification channel to update")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	// Validate required fields
	channelType, err := requireStringField(args, "type", ". Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	if !validChannelTypes[channelType] {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid channel type: "%s". Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams`, channelType)), nil
	}

	name, err := requireStringField(args, "name", ". Provide a unique name for the notification channel")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	sendResolved := true
	if v, present, err := parseBoolArg(args, "send_resolved"); err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	} else if present {
		sendResolved = v
	}

	// Errors here are per-type required-field validation failures, coded for retry.
	receiverJSON, err := buildReceiverJSON(channelType, name, sendResolved, args)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to build receiver JSON", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	// Step 1: Update the channel (204 No Content on success)
	if err := client.UpdateNotificationChannel(ctx, id, receiverJSON); err != nil {
		h.logUpstreamFailure(ctx, "Failed to update notification channel", err, slog.String("type", channelType), slog.String("name", name), slog.String("id", id))
		return upstreamError(err), nil
	}

	h.logger.InfoContext(ctx, "Notification channel updated", slog.String("type", channelType), slog.String("name", name), slog.String("id", id))

	// Follow up with a GET so the tool result carries the current channel state —
	// the PUT returns 204 with no body in the new API.
	channelResp, getErr := client.GetNotificationChannel(ctx, id)
	var readBackNote string
	if getErr != nil {
		h.logger.WarnContext(ctx, "Channel updated but follow-up GET failed", slog.String("id", id), logpkg.ErrAttr(getErr))
		// Fail OPEN: update succeeded; surface the unverified read-back as a note.
		readBackNote = fmt.Sprintf(
			"note: read-back after update failed: %s; the update itself succeeded but the returned channel state could not be re-fetched and may be stale.",
			getErr.Error())
	}

	// Step 2: Test the channel
	testErr := client.TestNotificationChannel(ctx, receiverJSON)

	// Build combined response
	result := map[string]any{
		"id": id,
	}
	if len(channelResp) > 0 {
		result["channel"] = json.RawMessage(channelResp)
	}

	var testFailureNote string
	if testErr != nil {
		h.logger.WarnContext(ctx, "Test notification failed", slog.String("name", name), logpkg.ErrAttr(testErr))
		result["test_notification"] = map[string]any{
			"success": false,
			"error":   testErr.Error(),
			"message": fmt.Sprintf("Channel '%s' was updated but the test notification failed: %s. Please verify the channel configuration.", name, testErr.Error()),
		}
		testFailureNote = testNotificationWarningNote(name, "updated", testErr)
	} else {
		h.logger.InfoContext(ctx, "Test notification sent successfully", slog.String("name", name))
		result["test_notification"] = map[string]any{
			"success": true,
			"message": fmt.Sprintf("Test notification sent successfully to channel '%s'. The channel is working correctly.", name),
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return internalError("failed to marshal response: " + err.Error()), nil
	}

	// Fail OPEN: channel was updated; test-send and read-back failures become notes.
	return structuredResultWithNotes(resultJSON, testFailureNote, readBackNote), nil
}

func getStringParam(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func buildReceiverJSON(channelType, name string, sendResolved bool, args map[string]any) ([]byte, error) {
	receiver := types.Receiver{Name: name}

	switch channelType {
	case "slack":
		apiURL, err := requireStringField(args, "slack_api_url", " when type=slack. Provide the Slack incoming webhook URL")
		if err != nil {
			return nil, err
		}
		cfg := types.SlackConfig{
			SendResolved: sendResolved,
			APIURL:       apiURL,
			Channel:      getStringParam(args, "slack_channel"),
			Title:        getStringParam(args, "slack_title"),
			Text:         getStringParam(args, "slack_text"),
		}
		receiver.SlackConfigs = []types.SlackConfig{cfg}

	case "webhook":
		webhookURL, err := requireStringField(args, "webhook_url", " when type=webhook. Provide the webhook endpoint URL")
		if err != nil {
			return nil, err
		}
		cfg := types.WebhookConfig{
			SendResolved: sendResolved,
			URL:          webhookURL,
		}
		username := getStringParam(args, "webhook_username")
		password := getStringParam(args, "webhook_password")
		if username != "" || password != "" {
			cfg.HTTPConfig = &types.WebhookHTTPConfig{
				BasicAuth: &types.WebhookBasicAuth{
					Username: username,
					Password: password,
				},
			}
		}
		receiver.WebhookConfigs = []types.WebhookConfig{cfg}

	case "pagerduty":
		routingKey, err := requireStringField(args, "pagerduty_routing_key", " when type=pagerduty. Provide the PagerDuty integration/routing key")
		if err != nil {
			return nil, err
		}
		cfg := types.PagerdutyConfig{
			SendResolved: sendResolved,
			RoutingKey:   routingKey,
			Description:  getStringParam(args, "pagerduty_description"),
			Severity:     getStringParam(args, "pagerduty_severity"),
		}
		receiver.PagerdutyConfigs = []types.PagerdutyConfig{cfg}

	case "email":
		to, err := requireStringField(args, "email_to", " when type=email. Provide comma-separated email addresses")
		if err != nil {
			return nil, err
		}
		cfg := types.EmailConfig{
			SendResolved: sendResolved,
			To:           to,
			HTML:         getStringParam(args, "email_html"),
		}
		receiver.EmailConfigs = []types.EmailConfig{cfg}

	case "opsgenie":
		apiKey, err := requireStringField(args, "opsgenie_api_key", " when type=opsgenie. Provide the OpsGenie API key")
		if err != nil {
			return nil, err
		}
		cfg := types.OpsgenieConfig{
			SendResolved: sendResolved,
			APIKey:       apiKey,
			Message:      getStringParam(args, "opsgenie_message"),
			Description:  getStringParam(args, "opsgenie_description"),
			Priority:     getStringParam(args, "opsgenie_priority"),
		}
		receiver.OpsgenieConfigs = []types.OpsgenieConfig{cfg}

	case "msteams":
		webhookURL, err := requireStringField(args, "msteams_webhook_url", " when type=msteams. Provide the MS Teams incoming webhook URL")
		if err != nil {
			return nil, err
		}
		cfg := types.MSTeamsV2Config{
			SendResolved: sendResolved,
			WebhookURL:   webhookURL,
			Title:        getStringParam(args, "msteams_title"),
			Text:         getStringParam(args, "msteams_text"),
		}
		receiver.MSTeamsV2Configs = []types.MSTeamsV2Config{cfg}
	}

	return types.MarshalReceiver(receiver)
}
