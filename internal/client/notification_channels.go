package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// notificationChannelWrite is the single-attempt boundary for every channel
// mutation. PUT and DELETE are replay-safe by method, but these endpoints have
// no idempotency key and a lost response can follow a commit.
func (s *SigNoz) notificationChannelWrite(ctx context.Context, call apiCall) (json.RawMessage, error) {
	return s.callAPI(withReplaySafe(ctx, false), ChannelWriteTimeout, call)
}

type notificationChannelResponse struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	ID          string `json:"id"`
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
	query := &apiclient.ListNotificationChannelsParams{
		Query:  optString(params.Query),
		Kind:   optEnum[apitypes.AlertmanagertypesChannelKind](params.Kind),
		Sort:   ptr(apitypes.AlertmanagertypesChannelListSort(params.Sort)),
		Order:  ptr(apitypes.AlertmanagertypesChannelListOrder(params.Order)),
		Limit:  ptr(params.Limit),
		Offset: ptr(params.Offset),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing notification channels")
	response, err := s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListNotificationChannels(ctx, query)
	})
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
	for index := range wire.Channels {
		if err := wire.Channels[index].Validate(); err != nil {
			return types.NotificationChannelList{}, fmt.Errorf("parse notification channel at index %d: %w", index, err)
		}
	}
	return types.NotificationChannelList{Channels: wire.Channels, Total: *wire.Total}, nil
}

func (s *SigNoz) getNotificationChannelV2(ctx context.Context, id string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching notification channel", slog.String("id", id))
	response, err := s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetNotificationChannel(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	data, err := successResponseData(response, "notification channel")
	if err != nil {
		return nil, err
	}
	if err := validateNotificationChannelData(data); err != nil {
		return nil, err
	}
	return data, nil
}

// validateNotificationChannelData checks only what this server consumes from a
// channel response: an object carrying the id that the tool result, the
// post-write read-back, and the test flow rely on. The provider config is
// SigNoz's contract and passes through verbatim.
func validateNotificationChannelData(data []byte) error {
	var wire notificationChannelResponse
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("parse notification channel: %w", err)
	}
	if wire.ID == "" {
		return fmt.Errorf("notification channel response is missing id")
	}
	return nil
}

func (s *SigNoz) decodeNotificationChannelResponse(operation string, response []byte, fallbackID string) (json.RawMessage, error) {
	data, err := successResponseData(response, "notification channel")
	if err != nil {
		return nil, committedNotificationChannelError(operation, response, err, fallbackID)
	}
	if err := validateNotificationChannelData(data); err != nil {
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
