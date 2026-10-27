package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/notificationchannel"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterNotificationChannelHandlers(server *mcp.Server) {
	h.logger.Debug("Registering notification channel handlers")
	h.addTool(server, mcp.NewTool("signoz_list_notification_channels",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("List notification channels as configuration-free summaries. Use query and kind to filter before paging; total counts every matching channel. Use signoz_get_notification_channel with an ID to read the complete provider configuration."),
		notificationSchemaOption(notificationListSchema()),
	), h.handleListNotificationChannels)
	h.addTool(server, mcp.NewTool("signoz_create_notification_channel",
		withCreateToolAnnotations(),
		mcp.WithDescription("Use this when the user wants a new notification channel; use signoz_update_notification_channel to replace an existing one. The payload is config: {kind, spec} plus name, or generateName=true with displayName. Read signoz://notification-channel/examples for every kind SigNoz accepts, each kind's spec fields, and a worked payload per kind, unless already read in the same prepared operation. SigNoz rejects an unknown kind, an unknown or missing spec field, and a duplicate name, each with the exact requirement. Minimal example: {\"name\":\"ops-hook\",\"config\":{\"kind\":\"webhook\",\"spec\":{\"url\":\"https://example.com/hook\"}}}. name is an immutable DNS-1123 label; alert routing references displayName (SigNoz defaults it from name). test defaults to false: ask whether the user wants one live test notification and set test=true only if they agree; the channel remains created if the test fails."),
		mcp.WithInputSchema[types.CreateNotificationChannelInput](),
		boolOrStringProperties("test"),
	), h.handleCreateNotificationChannel)
	h.addTool(server, mcp.NewTool("signoz_update_notification_channel",
		withNonIdempotentUpdateToolAnnotations(),
		mcp.WithDescription("Use this when the user wants to replace one notification channel's complete config: {kind, spec}; it is a full replacement and may change the provider kind. First call signoz_get_notification_channel and preserve every setting the user did not ask to change; send only id and config, because name and displayName are immutable. A fetched config is writable as-is; to unset an optional field, omit it, because SigNoz rejects an empty string for template-backed fields such as titles and messages. SigNoz also rejects an unknown kind and an unknown or missing spec field with the exact requirement; signoz://notification-channel/examples lists every kind's spec fields. Set test=true only when the user wants one live test notification after the update; the default is false and the update remains committed if the test fails."),
		mcp.WithInputSchema[types.UpdateNotificationChannelInput](),
		boolOrStringProperties("test"),
	), h.handleUpdateNotificationChannel)
	h.addTool(server, mcp.NewTool("signoz_get_notification_channel",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("Get one notification channel by ID, including its complete provider config. Use signoz_list_notification_channels to discover IDs. The response preserves backend-owned fields so an update can retain the full current configuration."),
		notificationSchemaOption(notificationIDSchema()),
	), h.handleGetNotificationChannel)
	h.addTool(server, mcp.NewTool("signoz_delete_notification_channel",
		withDeleteToolAnnotations(),
		mcp.WithDescription("Permanently delete one notification channel by ID after the user has confirmed the exact channel. This tool does not check alert-rule references."),
		notificationSchemaOption(notificationIDSchema()),
	), h.handleDeleteNotificationChannel)

	channelExamples := mcp.NewResource(
		"signoz://notification-channel/examples",
		"Notification Channel Reference and Examples",
		mcp.WithResourceDescription("Read this before composing config for signoz_create_notification_channel or signoz_update_notification_channel unless already read in the same prepared operation. It lists every provider kind SigNoz accepts, each kind's config.spec fields with the required ones marked, and a worked create payload per kind plus two replacement configs."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(notificationchannel.Examples))),
	)
	h.addResource(server, channelExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     notificationchannel.Examples,
			},
		}, nil
	})
}

func (h *Handler) handleListNotificationChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args notificationListArgs
	if err := decodeNotificationArgs(req.Params.Arguments, &args); err != nil {
		return notificationValidationError(err), nil
	}
	params := types.NotificationChannelListParams{
		Query: args.Query, Kind: args.Kind, Sort: args.Sort, Order: args.Order,
		Limit: args.Limit, Offset: args.Offset,
	}
	if err := params.Normalize(); err != nil {
		return notificationValidationError(err), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_list_notification_channels")
	upstream, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	channels, err := upstream.ListNotificationChannelsV2(ctx, params)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list notification channels", err)
		return upstreamError(err), nil
	}
	hasMore := params.Offset+len(channels.Channels) < channels.Total
	var nextOffset any
	if hasMore {
		nextOffset = params.Offset + len(channels.Channels)
	}
	payload, err := json.Marshal(map[string]any{
		"channels": channels.Channels,
		"total":    channels.Total,
		"pagination": map[string]any{
			"limit": params.Limit, "offset": params.Offset, "count": len(channels.Channels),
			"hasMore": hasMore, "nextOffset": nextOffset,
		},
	})
	if err != nil {
		return InternalErrorResult("failed to marshal notification channel list"), nil
	}
	return structuredResult(payload), nil
}

func (h *Handler) handleGetNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, result := decodeNotificationIDArgs(req)
	if result != nil {
		return result, nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_get_notification_channel", slog.String("id", args.ID))
	upstream, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	channel, err := upstream.GetNotificationChannel(ctx, args.ID)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get notification channel", err, slog.String("id", args.ID))
		return upstreamError(err), nil
	}
	return structuredResult(channel), nil
}

func (h *Handler) handleDeleteNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, result := decodeNotificationIDArgs(req)
	if result != nil {
		return result, nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_delete_notification_channel", slog.String("id", args.ID))
	upstream, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	if err := upstream.DeleteNotificationChannel(ctx, args.ID); err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete notification channel", err, slog.String("id", args.ID))
		return upstreamError(err), nil
	}
	payload, _ := json.Marshal(map[string]any{"status": "success", "id": args.ID})
	return structuredResult(payload), nil
}

func (h *Handler) handleCreateNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := rejectLegacyNotificationArgs(req.Params.Arguments, false); result != nil {
		return result, nil
	}
	var args notificationCreateArgs
	if err := decodeNotificationArgs(req.Params.Arguments, &args); err != nil {
		return notificationValidationError(err), nil
	}
	if len(args.Config) == 0 {
		return notificationValidationError(errors.New(`"config" is required: {kind, spec}`)), nil
	}
	createBody := map[string]any{"config": args.Config}
	if args.Name != "" {
		createBody["name"] = args.Name
	}
	if args.GenerateName {
		createBody["generateName"] = true
	}
	if args.DisplayName != "" {
		createBody["displayName"] = args.DisplayName
	}
	body, err := json.Marshal(createBody)
	if err != nil {
		return InternalErrorResult("failed to marshal notification channel create request"), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_create_notification_channel", slog.String("kind", notificationConfigKind(args.Config)))
	upstream, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	channel, err := upstream.CreateNotificationChannel(ctx, body)
	if err != nil {
		h.logNotificationChannelFailure(ctx, "Failed to create notification channel", err, slog.String("kind", notificationConfigKind(args.Config)))
		var committed *client.CommittedNotificationChannelError
		if errors.As(err, &committed) {
			return committedNotificationChannelResult(committed, args.Test), nil
		}
		return upstreamError(err), nil
	}
	id := notificationChannelID(channel)
	result := map[string]any{
		"channel":          json.RawMessage(channel),
		"testNotification": skippedNotificationTestResult(),
	}
	notes := []string{}
	if args.Test {
		testResult, note, authResult := h.testCommittedNotificationChannel(ctx, upstream, args.Config, id)
		if authResult != nil {
			return authResult, nil
		}
		result["testNotification"] = testResult
		if note != "" {
			notes = append(notes, note)
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return InternalErrorResult("failed to marshal notification channel result"), nil
	}
	return structuredResultWithNotes(payload, notes...), nil
}

func (h *Handler) handleUpdateNotificationChannel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result := rejectLegacyNotificationArgs(req.Params.Arguments, true); result != nil {
		return result, nil
	}
	var args notificationUpdateArgs
	if err := decodeNotificationArgs(req.Params.Arguments, &args); err != nil {
		return notificationValidationError(err), nil
	}
	if args.ID == "" {
		return notificationValidationError(errors.New(`"id" is required; obtain it from signoz_list_notification_channels`)), nil
	}
	if len(args.Config) == 0 {
		return notificationValidationError(errors.New(`"config" is required: {kind, spec}`)), nil
	}
	body, err := json.Marshal(map[string]any{"config": args.Config})
	if err != nil {
		return InternalErrorResult("failed to marshal notification channel update request"), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_update_notification_channel", slog.String("id", args.ID), slog.String("kind", notificationConfigKind(args.Config)))
	upstream, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	if err := upstream.UpdateNotificationChannel(ctx, args.ID, body); err != nil {
		h.logNotificationChannelFailure(ctx, "Failed to update notification channel", err, slog.String("id", args.ID), slog.String("kind", notificationConfigKind(args.Config)))
		var committed *client.CommittedNotificationChannelError
		if errors.As(err, &committed) {
			if committed.ID == "" {
				committed.ID = args.ID
			}
			return committedNotificationChannelResult(committed, args.Test), nil
		}
		return upstreamError(err), nil
	}
	result := map[string]any{
		"id":                args.ID,
		"mutationCommitted": true,
		"testNotification":  skippedNotificationTestResult(),
	}
	notes := []string{}
	channel, readErr := upstream.GetNotificationChannel(ctx, args.ID)
	if readErr != nil {
		if isNotificationAuthError(readErr) {
			testResult := unattemptedNotificationTestResult(args.Test, "test was not attempted because post-write verification could not complete")
			return committedNotificationAuthResult(readErr, args.ID, testResult), nil
		}
		h.logger.WarnContext(ctx, "Notification channel update read-back failed", slog.String("id", args.ID))
		notes = append(notes, "note: the update was committed, but SigNoz did not return the read-back state. Fetch the channel again before another update.")
	} else {
		result["channel"] = json.RawMessage(channel)
	}
	if args.Test {
		testResult, note, authResult := h.testCommittedNotificationChannel(ctx, upstream, args.Config, args.ID)
		if authResult != nil {
			return authResult, nil
		}
		result["testNotification"] = testResult
		if note != "" {
			notes = append(notes, note)
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return InternalErrorResult("failed to marshal notification channel result"), nil
	}
	return structuredResultWithNotes(payload, notes...), nil
}

func decodeNotificationIDArgs(req mcp.CallToolRequest) (notificationIDArgs, *mcp.CallToolResult) {
	var args notificationIDArgs
	if err := decodeNotificationArgs(req.Params.Arguments, &args); err != nil {
		return args, notificationValidationError(err)
	}
	if args.ID == "" {
		return args, notificationValidationError(errors.New(`"id" is required; obtain it from signoz_list_notification_channels`))
	}
	return args, nil
}

func notificationValidationError(err error) *mcp.CallToolResult {
	return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error())
}

func notificationChannelID(channel json.RawMessage) string {
	var identity struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(channel, &identity)
	return identity.ID
}

// notificationConfigKind peeks config.kind for logging; the config itself is
// forwarded to SigNoz verbatim, which is the only validator of its shape.
func notificationConfigKind(config json.RawMessage) string {
	var envelope struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(config, &envelope)
	return envelope.Kind
}

func (h *Handler) testCommittedNotificationChannel(ctx context.Context, upstream client.Client, config json.RawMessage, id string) (map[string]any, string, *mcp.CallToolResult) {
	body, err := json.Marshal(map[string]any{"config": config})
	if err != nil {
		return unattemptedNotificationTestResult(true, "test was not attempted because its request could not be prepared"), "note: the channel was committed, but the server could not prepare the requested test notification.", nil
	}
	if err := upstream.TestNotificationChannel(ctx, body); err != nil {
		h.logNotificationChannelTestFailure(ctx, err, id)
		if isNotificationAuthError(err) {
			return nil, "", committedNotificationAuthResult(err, id, failedNotificationTestResult())
		}
		if isNotificationHTTPError(err) {
			return failedNotificationTestResult(), "note: the channel was committed, but the requested test notification failed. Review the provider configuration before relying on delivery.", nil
		}
		return unknownNotificationTestResult(), "note: the channel was committed, but the test request returned no definitive response and may have been processed. Check provider delivery before retrying.", nil
	}
	return map[string]any{"requested": true, "success": true, "status": "succeeded"}, "", nil
}

func skippedNotificationTestResult() map[string]any {
	return map[string]any{"requested": false, "status": "skipped"}
}

func unattemptedNotificationTestResult(requested bool, reason string) map[string]any {
	if !requested {
		return skippedNotificationTestResult()
	}
	return map[string]any{"requested": true, "status": "skipped", "reason": reason}
}

func failedNotificationTestResult() map[string]any {
	return map[string]any{"requested": true, "success": false, "status": "failed"}
}

func unknownNotificationTestResult() map[string]any {
	return map[string]any{"requested": true, "status": "unknown"}
}

func (h *Handler) logNotificationChannelFailure(ctx context.Context, msg string, err error, attrs ...slog.Attr) {
	level := upstreamFailureLevel(err)
	if level == slog.LevelDebug {
		msg += " (request cancelled by client)"
	}
	args := make([]any, 0, len(attrs)+2)
	for _, attr := range attrs {
		args = append(args, attr)
	}
	args = appendNotificationChannelErrorMetadata(args, err)
	h.logger.Log(ctx, level, msg, args...)
}

func (h *Handler) logNotificationChannelTestFailure(ctx context.Context, err error, id string) {
	args := appendNotificationChannelErrorMetadata([]any{slog.String("id", id)}, err)
	h.logger.WarnContext(ctx, "Notification channel test failed after write", args...)
}

func appendNotificationChannelErrorMetadata(args []any, err error) []any {
	var statusErr *client.HTTPStatusError
	if errors.As(err, &statusErr) {
		args = append(args, slog.Int("status", statusErr.StatusCode))
	}
	summary := "notification channel upstream request failed"
	switch {
	case errors.Is(err, context.Canceled):
		summary = context.Canceled.Error()
	case errors.Is(err, context.DeadlineExceeded):
		summary = context.DeadlineExceeded.Error()
	}
	return append(args, slog.String("error", summary))
}

func committedNotificationChannelResult(err *client.CommittedNotificationChannelError, testRequested bool) *mcp.CallToolResult {
	fields := map[string]any{
		"mutationCommitted": true,
		"testNotification":  unattemptedNotificationTestResult(testRequested, "test was not attempted because the committed mutation response could not be validated"),
	}
	if err.ID != "" {
		fields["id"] = err.ID
	}
	if err.Name != "" {
		fields["name"] = err.Name
	}
	if err.DisplayName != "" {
		fields["displayName"] = err.DisplayName
	}
	return errorWithStructuredContent(CodeUpstreamError, "SigNoz committed the notification channel mutation, but its response could not be validated. Do not repeat the mutation; fetch the known ID instead.", fields)
}

func isNotificationHTTPError(err error) bool {
	var statusErr *client.HTTPStatusError
	return errors.As(err, &statusErr)
}

func isNotificationAuthError(err error) bool {
	var statusErr *client.HTTPStatusError
	return errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden)
}

func committedNotificationAuthResult(err error, id string, testResult map[string]any) *mcp.CallToolResult {
	result := upstreamError(err)
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		structured["mutationCommitted"] = true
		structured["testNotification"] = testResult
		if id != "" {
			structured["id"] = id
		}
	}
	return result
}
