package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// doNotificationChannelWrite is the single-attempt boundary for every channel
// mutation. PUT and DELETE are replay-safe in the generic client, but these
// endpoints have no idempotency key and a lost response can follow a commit.
func (s *SigNoz) doNotificationChannelWrite(ctx context.Context, method, reqURL string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	return s.doRequestWithReplayPolicy(ctx, method, reqURL, body, timeout, false)
}

type notificationChannelResponse struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName"`
	Config      json.RawMessage `json:"config"`
	ID          string          `json:"id"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

type CommittedNotificationChannelError struct {
	Operation   string
	ID          string
	Name        string
	DisplayName string
	Err         error
}

func (e *CommittedNotificationChannelError) Error() string {
	return fmt.Sprintf("SigNoz committed the %s but could not parse its response: %v", e.Operation, e.Err)
}

func (e *CommittedNotificationChannelError) Unwrap() error { return e.Err }

func (s *SigNoz) ListNotificationChannelsV2(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
	if err := params.Normalize(); err != nil {
		return types.NotificationChannelList{}, err
	}
	query := url.Values{}
	if params.Query != "" {
		query.Set("query", params.Query)
	}
	if params.Kind != "" {
		query.Set("kind", params.Kind)
	}
	query.Set("sort", params.Sort)
	query.Set("order", params.Order)
	query.Set("limit", strconv.Itoa(params.Limit))
	query.Set("offset", strconv.Itoa(params.Offset))

	reqURL := fmt.Sprintf("%s/api/v2/notification_channels?%s", s.baseURL, query.Encode())
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing notification channels")
	response, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		return types.NotificationChannelList{}, err
	}
	data, err := successResponseData(response, "notification channel list")
	if err != nil {
		return types.NotificationChannelList{}, err
	}
	var wire struct {
		Channels []types.ListedNotificationChannel `json:"channels"`
		Total    *int                              `json:"total"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: %w", err)
	}
	if wire.Channels == nil {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: channels must be an array")
	}
	if wire.Total == nil {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: total is required")
	}
	if *wire.Total < 0 {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: invalid total %d", *wire.Total)
	}
	if len(wire.Channels) == 0 && params.Offset < *wire.Total {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: non-progressing page returned 0 channels at offset %d before filtered total %d", params.Offset, *wire.Total)
	}
	if len(wire.Channels) > 0 && (len(wire.Channels) > *wire.Total || params.Offset > *wire.Total-len(wire.Channels)) {
		return types.NotificationChannelList{}, fmt.Errorf("parse notification channel list: page at offset %d with %d channels exceeds filtered total %d", params.Offset, len(wire.Channels), *wire.Total)
	}
	drift := notificationListResponseHasUnknownFields(data)
	for index := range wire.Channels {
		if err := wire.Channels[index].Validate(); err != nil {
			return types.NotificationChannelList{}, fmt.Errorf("parse notification channel at index %d: %w", index, err)
		}
		if !types.IsValidNotificationChannelKind(wire.Channels[index].Kind) {
			drift = true
		}
	}
	if drift {
		s.logger.Warn("Notification channel list contains fields or kinds outside the pinned v0.142.0 contract; preserving the summaries")
	}
	return types.NotificationChannelList{Channels: wire.Channels, Total: *wire.Total}, nil
}

func (s *SigNoz) getNotificationChannelV2(ctx context.Context, id string) (json.RawMessage, error) {
	if err := types.ValidateUUID(id); err != nil {
		return nil, err
	}
	reqURL := fmt.Sprintf("%s/api/v2/notification_channels/%s", s.baseURL, url.PathEscape(id))
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching notification channel", slog.String("id", id))
	response, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		return nil, err
	}
	data, err := successResponseData(response, "notification channel")
	if err != nil {
		return nil, err
	}
	if err := s.validateNotificationChannelData(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *SigNoz) validateNotificationChannelData(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return fmt.Errorf("parse notification channel: expected an object")
	}
	drift := hasUnknownJSONKeys(root, "name", "displayName", "config", "id", "createdAt", "updatedAt")
	var wire notificationChannelResponse
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("parse notification channel: %w", err)
	}
	if err := types.ValidateUUID(wire.ID); err != nil {
		return err
	}
	if wire.Name == "" || wire.DisplayName == "" || wire.CreatedAt == "" || wire.UpdatedAt == "" {
		return fmt.Errorf("notification channel response is missing required identity or timestamp fields")
	}
	var config struct {
		Kind string          `json:"kind"`
		Spec json.RawMessage `json:"spec"`
	}
	var configObject map[string]json.RawMessage
	if err := json.Unmarshal(wire.Config, &configObject); err != nil || configObject == nil {
		return fmt.Errorf("parse notification channel config: expected an object")
	}
	drift = drift || hasUnknownJSONKeys(configObject, "kind", "spec")
	if err := json.Unmarshal(wire.Config, &config); err != nil {
		return fmt.Errorf("parse notification channel config: %w", err)
	}
	if config.Kind == "" {
		return fmt.Errorf("notification channel response is missing config.kind")
	}
	if len(config.Spec) == 0 || bytes.Equal(bytes.TrimSpace(config.Spec), []byte("null")) {
		return fmt.Errorf("notification channel response is missing config.spec")
	}
	var specObject map[string]json.RawMessage
	if err := json.Unmarshal(config.Spec, &specObject); err != nil || specObject == nil {
		return fmt.Errorf("parse notification channel config.spec: expected an object")
	}
	target, allowed, required := notificationResponseSpec(config.Kind)
	if target == nil {
		drift = true
	} else {
		if err := json.Unmarshal(config.Spec, target); err != nil {
			return fmt.Errorf("parse notification channel config.spec: %w", err)
		}
		drift = drift || hasUnknownJSONKeys(specObject, allowed...)
		for _, field := range required {
			raw, present := specObject[field]
			var value string
			if !present || json.Unmarshal(raw, &value) != nil || value == "" {
				return fmt.Errorf("notification channel response is missing required config.spec.%s", field)
			}
		}
		if err := validateNotificationResponseSemantics(config.Kind, specObject); err != nil {
			s.logger.Warn("Notification channel response violates its provider config rules; an unchanged update will be rejected",
				slog.String("kind", config.Kind), slog.String("violation", err.Error()))
		}
	}
	if drift {
		s.logger.Warn("Notification channel response contains fields outside the pinned v0.142.0 typed contract; preserving the upstream object")
	}
	return nil
}

// validateNotificationResponseSemantics applies the request-side provider rules
// to a readback, skipping the empty strings SigNoz writes for unset templates.
func validateNotificationResponseSemantics(kind string, specObject map[string]json.RawMessage) error {
	spec := make(map[string]json.RawMessage, len(specObject))
	for field, raw := range specObject {
		spec[field] = raw
	}
	for _, field := range types.NotificationChannelUnsetTemplateFields(kind) {
		if bytes.Equal(bytes.TrimSpace(spec[field]), []byte(`""`)) {
			delete(spec, field)
		}
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	target, _, _ := notificationResponseSpec(kind)
	validator, ok := target.(types.NotificationChannelSpec)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(body, validator); err != nil {
		return err
	}
	return validator.Validate()
}

func notificationResponseSpec(kind string) (any, []string, []string) {
	entry := func(target any, required []string, fields ...string) (any, []string, []string) {
		return target, append([]string{"sendResolved"}, fields...), required
	}
	switch kind {
	case "slack":
		return entry(&types.NotificationChannelSlackSpec{}, []string{"apiUrl"}, "apiUrl", "channel", "title", "text")
	case "email":
		return entry(&types.NotificationChannelEmailSpec{}, []string{"to"}, "to", "html", "headers")
	case "webhook":
		return entry(&types.NotificationChannelWebhookSpec{}, []string{"url"}, "url", "username", "password", "bearerToken")
	case "pagerduty":
		return entry(&types.NotificationChannelPagerdutySpec{}, []string{"routingKey"}, "routingKey", "url", "source", "client", "clientUrl", "description", "severity", "component", "group", "class", "details")
	case "opsgenie":
		return entry(&types.NotificationChannelOpsgenieSpec{}, []string{"apiKey"}, "apiKey", "apiUrl", "message", "description", "source", "details", "priority")
	case "msteams":
		return entry(&types.NotificationChannelMSTeamsSpec{}, []string{"webhookUrl"}, "webhookUrl", "title", "text")
	case "googlechat":
		return entry(&types.NotificationChannelGoogleChatSpec{}, []string{"webhookUrl"}, "webhookUrl", "title", "text")
	case "jira":
		return entry(&types.NotificationChannelJiraSpec{}, []string{"site", "project", "issueType", "email", "apiToken"}, "site", "project", "issueType", "summary", "description", "priority", "labels", "resolveTransition", "reopenTransition", "reopenDuration", "wontFixResolution", "customFields", "email", "apiToken")
	case "jsmops":
		return entry(&types.NotificationChannelJSMOpsSpec{}, []string{"apiKey"}, "apiKey", "message", "description", "priority", "tags")
	case "incidentio":
		return entry(&types.NotificationChannelIncidentIOSpec{}, []string{"url", "token"}, "url", "token", "title", "description", "metadata")
	default:
		return nil, nil, nil
	}
}

func hasUnknownJSONKeys(object map[string]json.RawMessage, allowed ...string) bool {
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return true
		}
	}
	return false
}

func notificationListResponseHasUnknownFields(data []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return false
	}
	drift := hasUnknownJSONKeys(root, "channels", "total")
	var channels []map[string]json.RawMessage
	if json.Unmarshal(root["channels"], &channels) != nil {
		return drift
	}
	for _, channel := range channels {
		drift = drift || hasUnknownJSONKeys(channel, "id", "name", "displayName", "kind", "createdAt", "updatedAt")
	}
	return drift
}

func (s *SigNoz) decodeNotificationChannelResponse(operation string, response []byte, fallbackID string) (json.RawMessage, error) {
	data, err := successResponseData(response, "notification channel")
	if err != nil {
		return nil, committedNotificationChannelError(operation, response, err, fallbackID)
	}
	if err := s.validateNotificationChannelData(data); err != nil {
		return nil, committedNotificationChannelError(operation, response, err, fallbackID)
	}
	return data, nil
}

func committedNotificationChannelError(operation string, response []byte, parseErr error, fallbackID string) error {
	var envelope struct {
		Data notificationChannelResponse `json:"data"`
	}
	_ = json.Unmarshal(response, &envelope)
	wire := envelope.Data
	if wire.ID == "" {
		wire.ID = fallbackID
	}
	return &CommittedNotificationChannelError{
		Operation:   operation,
		ID:          wire.ID,
		Name:        wire.Name,
		DisplayName: wire.DisplayName,
		Err:         parseErr,
	}
}

func successResponseData(response []byte, label string) (json.RawMessage, error) {
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return nil, fmt.Errorf("parse %s response: %w", label, err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("parse %s response: status must be success", label)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, fmt.Errorf("parse %s response: data is required", label)
	}
	return envelope.Data, nil
}
