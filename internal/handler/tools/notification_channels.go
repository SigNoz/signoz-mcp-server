package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

var validChannelTypes = map[string]bool{
	"slack":    true,
	"webhook":  true,
	"pagerduty": true,
	"email":    true,
	"opsgenie": true,
	"msteams":  true,
}

func (h *Handler) RegisterNotificationChannelHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering notification channel handlers")

	listChannelsTool := mcp.NewTool("signoz_list_notification_channels",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"List all notification channels configured in SigNoz.\n\n"+
				"Returns channel id, name, type (slack, webhook, pagerduty, email, opsgenie, msteams), "+
				"configuration details, and timestamps.\n\n"+
				"Use this tool to discover existing channels before creating new ones or to verify channel configurations.\n\n"+
				"Results are paginated. Use 'limit' and 'offset' to page through large result sets. "+
				"The response includes pagination metadata: total count, hasMore flag, and nextOffset for the next page.",
		),
		mcp.WithString("limit", mcp.Description("Maximum number of channels to return per page. Default: 50.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50). Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0.")),
	)

	s.AddTool(listChannelsTool, h.handleListNotificationChannels)

	createChannelTool := mcp.NewTool("signoz_create_notification_channel",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Create a notification channel in SigNoz and send a test notification to verify it works.\n\n"+
				"SUPPORTED TYPES: slack, webhook, pagerduty, email, opsgenie, msteams\n\n"+
				"REQUIRED FIELDS BY TYPE:\n"+
				"- slack: name, slack_api_url (Slack incoming webhook URL)\n"+
				"- webhook: name, webhook_url (endpoint URL)\n"+
				"- pagerduty: name, pagerduty_routing_key (integration/routing key)\n"+
				"- email: name, email_to (comma-separated email addresses)\n"+
				"- opsgenie: name, opsgenie_api_key (OpsGenie API key)\n"+
				"- msteams: name, msteams_webhook_url (MS Teams incoming webhook URL)\n\n"+
				"After creating the channel, a test notification is always sent to verify the channel is working. "+
				"The response includes both the channel creation result and the test notification outcome.",
		),
		// Common fields
		mcp.WithString("type", mcp.Required(), mcp.Description("Channel type. One of: slack, webhook, pagerduty, email, opsgenie, msteams")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique name for the notification channel")),
		mcp.WithString("send_resolved", mcp.Description("Whether to send notifications when alerts resolve. Values: 'true' or 'false'. Default: 'true'")),

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

	s.AddTool(createChannelTool, h.handleCreateNotificationChannel)
}

func (h *Handler) handleListNotificationChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_list_notification_channels")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := client.ListNotificationChannels(ctx)
	if err != nil {
		log.Error("Failed to list notification channels", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var response map[string]any
	if err := json.Unmarshal(result, &response); err != nil {
		log.Error("Failed to parse notification channels response", zap.Error(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}

	data, ok := response["data"].([]any)
	if !ok {
		log.Error("Invalid notification channels response format", zap.Any("data", response["data"]))
		return mcp.NewToolResultError("invalid response format: expected data array"), nil
	}

	// Parse the "data" string field in each channel into a JSON object for readability.
	for _, item := range data {
		ch, ok := item.(map[string]any)
		if !ok {
			continue
		}
		dataStr, ok := ch["data"].(string)
		if !ok || dataStr == "" {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(dataStr), &parsed); err == nil {
			ch["data"] = parsed
		}
	}

	total := len(data)
	pagedData := paginate.Array(data, offset, limit)

	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		log.Error("Failed to wrap notification channels with pagination", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleCreateNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_create_notification_channel")

	args := req.Params.Arguments.(map[string]any)

	// Validate required fields
	channelType, _ := args["type"].(string)
	if channelType == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "type" is required. Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams`), nil
	}
	if !validChannelTypes[channelType] {
		return mcp.NewToolResultError(fmt.Sprintf(`Invalid channel type: "%s". Must be one of: slack, webhook, pagerduty, email, opsgenie, msteams`, channelType)), nil
	}

	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "name" is required. Provide a unique name for the notification channel.`), nil
	}

	sendResolved := true
	if sr, ok := args["send_resolved"].(string); ok && sr != "" {
		if sr == "false" {
			sendResolved = false
		} else if sr != "true" {
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "send_resolved" value: "%s". Must be "true" or "false"`, sr)), nil
		}
	}

	// Build receiver JSON based on type
	receiverJSON, err := buildReceiverJSON(channelType, name, sendResolved, args)
	if err != nil {
		log.Warn("Failed to build receiver JSON", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Step 1: Create the channel
	createResp, err := client.CreateNotificationChannel(ctx, receiverJSON)
	if err != nil {
		log.Error("Failed to create notification channel", zap.String("type", channelType), zap.String("name", name), zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create notification channel: %s", err.Error())), nil
	}

	log.Info("Notification channel created", zap.String("type", channelType), zap.String("name", name))

	// Step 2: Test the channel
	testErr := client.TestNotificationChannel(ctx, receiverJSON)

	// Build combined response
	result := map[string]any{
		"channel": json.RawMessage(createResp),
	}

	if testErr != nil {
		log.Warn("Test notification failed", zap.String("name", name), zap.Error(testErr))
		result["test_notification"] = map[string]any{
			"success": false,
			"error":   testErr.Error(),
			"message": fmt.Sprintf("Channel '%s' was created but the test notification failed: %s. Please verify the channel configuration.", name, testErr.Error()),
		}
	} else {
		log.Info("Test notification sent successfully", zap.String("name", name))
		result["test_notification"] = map[string]any{
			"success": true,
			"message": fmt.Sprintf("Test notification sent successfully to channel '%s'. The channel is working correctly.", name),
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func getStringParam(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func buildReceiverJSON(channelType, name string, sendResolved bool, args map[string]any) ([]byte, error) {
	receiver := types.Receiver{Name: name}

	switch channelType {
	case "slack":
		apiURL := getStringParam(args, "slack_api_url")
		if apiURL == "" {
			return nil, fmt.Errorf(`parameter validation failed: "slack_api_url" is required when type=slack. Provide the Slack incoming webhook URL`)
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
		webhookURL := getStringParam(args, "webhook_url")
		if webhookURL == "" {
			return nil, fmt.Errorf(`parameter validation failed: "webhook_url" is required when type=webhook. Provide the webhook endpoint URL`)
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
		routingKey := getStringParam(args, "pagerduty_routing_key")
		if routingKey == "" {
			return nil, fmt.Errorf(`parameter validation failed: "pagerduty_routing_key" is required when type=pagerduty. Provide the PagerDuty integration/routing key`)
		}
		cfg := types.PagerdutyConfig{
			SendResolved: sendResolved,
			RoutingKey:   routingKey,
			Description:  getStringParam(args, "pagerduty_description"),
			Severity:     getStringParam(args, "pagerduty_severity"),
		}
		receiver.PagerdutyConfigs = []types.PagerdutyConfig{cfg}

	case "email":
		to := getStringParam(args, "email_to")
		if to == "" {
			return nil, fmt.Errorf(`parameter validation failed: "email_to" is required when type=email. Provide comma-separated email addresses`)
		}
		cfg := types.EmailConfig{
			SendResolved: sendResolved,
			To:           to,
			HTML:         getStringParam(args, "email_html"),
		}
		receiver.EmailConfigs = []types.EmailConfig{cfg}

	case "opsgenie":
		apiKey := getStringParam(args, "opsgenie_api_key")
		if apiKey == "" {
			return nil, fmt.Errorf(`parameter validation failed: "opsgenie_api_key" is required when type=opsgenie. Provide the OpsGenie API key`)
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
		webhookURL := getStringParam(args, "msteams_webhook_url")
		if webhookURL == "" {
			return nil, fmt.Errorf(`parameter validation failed: "msteams_webhook_url" is required when type=msteams. Provide the MS Teams incoming webhook URL`)
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
