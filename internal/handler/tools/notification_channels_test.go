package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleListNotificationChannels_Success(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"id":"1","name":"my-slack","type":"slack","data":"{\"name\":\"my-slack\",\"slack_configs\":[{\"api_url\":\"https://hooks.slack.com/x\",\"channel\":\"#alerts\",\"send_resolved\":true}]}","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},{"id":"2","name":"my-email","type":"email","data":"{\"name\":\"my-email\",\"email_configs\":[{\"to\":\"oncall@example.com\",\"send_resolved\":true}]}","created_at":"2024-02-01T00:00:00Z","updated_at":"2024-02-01T00:00:00Z"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_notification_channels", map[string]any{})

	result, err := h.handleListNotificationChannels(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatalf("expected data array, got %T", resp["data"])
	}
	if len(data) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(data))
	}

	// Verify summarized fields only (id, name, type, timestamps — no full config)
	ch := data[0].(map[string]any)
	if ch["name"] != "my-slack" {
		t.Errorf("expected name=my-slack, got %v", ch["name"])
	}
	if ch["type"] != "slack" {
		t.Errorf("expected type=slack, got %v", ch["type"])
	}
	if ch["id"] != "1" {
		t.Errorf("expected id=1, got %v", ch["id"])
	}
	if _, exists := ch["data"]; exists {
		t.Error("expected summarized response to omit the full data/config field")
	}

	// Verify pagination metadata
	pagination, ok := resp["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination metadata")
	}
	if pagination["total"].(float64) != 2 {
		t.Errorf("expected total=2, got %v", pagination["total"])
	}
}

func TestHandleListNotificationChannels_Empty(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_notification_channels", map[string]any{})

	result, err := h.handleListNotificationChannels(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Errorf("expected 0 channels, got %d", len(data))
	}

	pagination := resp["pagination"].(map[string]any)
	if pagination["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", pagination["total"])
	}
}

func TestHandleListNotificationChannels_APIError(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_notification_channels", map[string]any{})

	result, err := h.handleListNotificationChannels(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for API failure")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "connection refused") {
		t.Errorf("expected error message in result, got: %s", text)
	}
}

func TestHandleListNotificationChannels_Pagination(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"id":"1","name":"ch-1","type":"slack","data":"{}"},{"id":"2","name":"ch-2","type":"email","data":"{}"},{"id":"3","name":"ch-3","type":"webhook","data":"{}"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_notification_channels", map[string]any{
		"limit":  "2",
		"offset": "0",
	})

	result, err := h.handleListNotificationChannels(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 channels (limit=2), got %d", len(data))
	}

	pagination := resp["pagination"].(map[string]any)
	if pagination["total"].(float64) != 3 {
		t.Errorf("expected total=3, got %v", pagination["total"])
	}
	if pagination["hasMore"] != true {
		t.Error("expected hasMore=true")
	}
	if pagination["nextOffset"].(float64) != 2 {
		t.Errorf("expected nextOffset=2, got %v", pagination["nextOffset"])
	}
}

func TestHandleCreateNotificationChannel_Slack(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-1","name":"my-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "my-slack",
		"slack_api_url": "https://hooks.slack.com/services/T123/B456/xxx",
		"slack_channel": "#alerts",
		"slack_title":   "{{ .CommonLabels.alertname }}",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	// Verify receiver JSON structure
	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	if receiver["name"] != "my-slack" {
		t.Errorf("expected name=my-slack, got %v", receiver["name"])
	}
	slackConfigs, ok := receiver["slack_configs"].([]any)
	if !ok || len(slackConfigs) != 1 {
		t.Fatalf("expected 1 slack_configs entry, got %v", receiver["slack_configs"])
	}
	cfg := slackConfigs[0].(map[string]any)
	if cfg["api_url"] != "https://hooks.slack.com/services/T123/B456/xxx" {
		t.Errorf("expected api_url to match, got %v", cfg["api_url"])
	}
	if cfg["channel"] != "#alerts" {
		t.Errorf("expected channel=#alerts, got %v", cfg["channel"])
	}

	// Verify response contains test_notification success
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"success":true`) {
		t.Errorf("expected test_notification success in response, got: %s", text)
	}
}

func TestHandleCreateNotificationChannel_Webhook(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-2","name":"my-webhook","type":"webhook"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":             "webhook",
		"name":             "my-webhook",
		"webhook_url":      "https://example.com/hook",
		"webhook_username": "user1",
		"webhook_password": "pass1",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	webhookConfigs, ok := receiver["webhook_configs"].([]any)
	if !ok || len(webhookConfigs) != 1 {
		t.Fatalf("expected 1 webhook_configs entry, got %v", receiver["webhook_configs"])
	}
	cfg := webhookConfigs[0].(map[string]any)
	if cfg["url"] != "https://example.com/hook" {
		t.Errorf("expected url to match, got %v", cfg["url"])
	}
	httpConfig, ok := cfg["http_config"].(map[string]any)
	if !ok {
		t.Fatal("expected http_config in webhook config")
	}
	basicAuth, ok := httpConfig["basic_auth"].(map[string]any)
	if !ok {
		t.Fatal("expected basic_auth in http_config")
	}
	if basicAuth["username"] != "user1" {
		t.Errorf("expected username=user1, got %v", basicAuth["username"])
	}
}

func TestHandleCreateNotificationChannel_PagerDuty(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-3","name":"my-pd","type":"pagerduty"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":                  "pagerduty",
		"name":                  "my-pd",
		"pagerduty_routing_key": "key-123",
		"pagerduty_severity":    "critical",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	pdConfigs, ok := receiver["pagerduty_configs"].([]any)
	if !ok || len(pdConfigs) != 1 {
		t.Fatalf("expected 1 pagerduty_configs entry, got %v", receiver["pagerduty_configs"])
	}
	cfg := pdConfigs[0].(map[string]any)
	if cfg["routing_key"] != "key-123" {
		t.Errorf("expected routing_key=key-123, got %v", cfg["routing_key"])
	}
	if cfg["severity"] != "critical" {
		t.Errorf("expected severity=critical, got %v", cfg["severity"])
	}
}

func TestHandleCreateNotificationChannel_Email(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-4","name":"my-email","type":"email"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":     "email",
		"name":     "my-email",
		"email_to": "oncall@example.com,backup@example.com",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	emailConfigs, ok := receiver["email_configs"].([]any)
	if !ok || len(emailConfigs) != 1 {
		t.Fatalf("expected 1 email_configs entry, got %v", receiver["email_configs"])
	}
	cfg := emailConfigs[0].(map[string]any)
	if cfg["to"] != "oncall@example.com,backup@example.com" {
		t.Errorf("expected to field to match, got %v", cfg["to"])
	}
}

func TestHandleCreateNotificationChannel_OpsGenie(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-5","name":"my-og","type":"opsgenie"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":                 "opsgenie",
		"name":                 "my-og",
		"opsgenie_api_key":     "og-key-abc",
		"opsgenie_priority":    "P1",
		"opsgenie_message":     "{{ .CommonLabels.alertname }}",
		"opsgenie_description": "Alert fired",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	ogConfigs, ok := receiver["opsgenie_configs"].([]any)
	if !ok || len(ogConfigs) != 1 {
		t.Fatalf("expected 1 opsgenie_configs entry, got %v", receiver["opsgenie_configs"])
	}
	cfg := ogConfigs[0].(map[string]any)
	if cfg["api_key"] != "og-key-abc" {
		t.Errorf("expected api_key=og-key-abc, got %v", cfg["api_key"])
	}
	if cfg["priority"] != "P1" {
		t.Errorf("expected priority=P1, got %v", cfg["priority"])
	}
}

func TestHandleCreateNotificationChannel_MSTeams(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-6","name":"my-teams","type":"msteams"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":                "msteams",
		"name":                "my-teams",
		"msteams_webhook_url": "https://outlook.webhook.office.com/webhookb2/xxx",
		"msteams_title":       "Alert",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	teamsConfigs, ok := receiver["msteamsv2_configs"].([]any)
	if !ok || len(teamsConfigs) != 1 {
		t.Fatalf("expected 1 msteamsv2_configs entry, got %v", receiver["msteamsv2_configs"])
	}
	cfg := teamsConfigs[0].(map[string]any)
	if cfg["webhook_url"] != "https://outlook.webhook.office.com/webhookb2/xxx" {
		t.Errorf("expected webhook_url to match, got %v", cfg["webhook_url"])
	}
}

func TestHandleCreateNotificationChannel_MissingType(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"name": "test",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing type")
	}
}

func TestHandleCreateNotificationChannel_InvalidType(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type": "invalid",
		"name": "test",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid type")
	}
}

func TestHandleCreateNotificationChannel_MissingName(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type": "slack",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing name")
	}
}

func TestHandleCreateNotificationChannel_MissingRequiredField(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "slack missing api_url",
			args: map[string]any{"type": "slack", "name": "test"},
		},
		{
			name: "webhook missing url",
			args: map[string]any{"type": "webhook", "name": "test"},
		},
		{
			name: "pagerduty missing routing_key",
			args: map[string]any{"type": "pagerduty", "name": "test"},
		},
		{
			name: "email missing to",
			args: map[string]any{"type": "email", "name": "test"},
		},
		{
			name: "opsgenie missing api_key",
			args: map[string]any{"type": "opsgenie", "name": "test"},
		},
		{
			name: "msteams missing webhook_url",
			args: map[string]any{"type": "msteams", "name": "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &client.MockClient{}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_create_notification_channel", tt.args)

			result, err := h.handleCreateNotificationChannel(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Error("expected error result for missing required field")
			}
		})
	}
}

func TestHandleCreateNotificationChannel_CreateError(t *testing.T) {
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			return nil, fmt.Errorf("channel name already exists")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "duplicate",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when create fails")
	}
}

func TestHandleCreateNotificationChannel_TestFails(t *testing.T) {
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"ch-1","name":"bad-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return fmt.Errorf("webhook returned 403 forbidden")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "bad-slack",
		"slack_api_url": "https://hooks.slack.com/services/invalid",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The tool should NOT return an error result — channel was created, only test failed
	if result.IsError {
		t.Fatal("expected success result (channel was created, test failure is in the response body)")
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"success":false`) {
		t.Errorf("expected test_notification failure in response, got: %s", text)
	}
	if !strings.Contains(text, "webhook returned 403 forbidden") {
		t.Errorf("expected test error message in response, got: %s", text)
	}
}

func TestHandleCreateNotificationChannel_SendResolvedFalse(t *testing.T) {
	var capturedBody []byte
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"ch-1"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "test-sr",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
		"send_resolved": "false",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	slackConfigs := receiver["slack_configs"].([]any)
	cfg := slackConfigs[0].(map[string]any)
	if cfg["send_resolved"] != false {
		t.Errorf("expected send_resolved=false, got %v", cfg["send_resolved"])
	}
}

func TestHandleCreateNotificationChannel_InvalidSendResolved(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "test",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
		"send_resolved": "maybe",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid send_resolved value")
	}
}

// --- Update notification channel tests ---

func TestHandleUpdateNotificationChannel_Slack(t *testing.T) {
	var capturedID string
	var capturedBody []byte
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(ctx context.Context, id string, receiverJSON []byte) (json.RawMessage, error) {
			capturedID = id
			capturedBody = receiverJSON
			return json.RawMessage(`{"data":{"id":"1","name":"my-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "1",
		"type":          "slack",
		"name":          "my-slack",
		"slack_api_url": "https://hooks.slack.com/services/T123/B456/new",
		"slack_channel": "#updated-alerts",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	if capturedID != "1" {
		t.Errorf("expected id=1, got %v", capturedID)
	}

	var receiver map[string]any
	if err := json.Unmarshal(capturedBody, &receiver); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	if receiver["name"] != "my-slack" {
		t.Errorf("expected name=my-slack, got %v", receiver["name"])
	}
	slackConfigs, ok := receiver["slack_configs"].([]any)
	if !ok || len(slackConfigs) != 1 {
		t.Fatalf("expected 1 slack_configs entry, got %v", receiver["slack_configs"])
	}
	cfg := slackConfigs[0].(map[string]any)
	if cfg["api_url"] != "https://hooks.slack.com/services/T123/B456/new" {
		t.Errorf("expected api_url to match, got %v", cfg["api_url"])
	}
	if cfg["channel"] != "#updated-alerts" {
		t.Errorf("expected channel=#updated-alerts, got %v", cfg["channel"])
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"success":true`) {
		t.Errorf("expected test_notification success in response, got: %s", text)
	}
}

func TestHandleUpdateNotificationChannel_MissingID(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "test",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing id")
	}
}

func TestHandleUpdateNotificationChannel_UpdateError(t *testing.T) {
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(ctx context.Context, id string, receiverJSON []byte) (json.RawMessage, error) {
			return nil, fmt.Errorf("channel not found")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "999",
		"type":          "slack",
		"name":          "missing",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when update fails")
	}
}

func TestHandleUpdateNotificationChannel_TestFails(t *testing.T) {
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(ctx context.Context, id string, receiverJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"1","name":"bad-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return fmt.Errorf("webhook returned 403 forbidden")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "1",
		"type":          "slack",
		"name":          "bad-slack",
		"slack_api_url": "https://hooks.slack.com/services/invalid",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The tool should NOT return an error result — channel was updated, only test failed
	if result.IsError {
		t.Fatal("expected success result (channel was updated, test failure is in the response body)")
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"success":false`) {
		t.Errorf("expected test_notification failure in response, got: %s", text)
	}
	if !strings.Contains(text, "webhook returned 403 forbidden") {
		t.Errorf("expected test error message in response, got: %s", text)
	}
}
