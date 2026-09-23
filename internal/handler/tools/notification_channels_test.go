package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

const testNotificationChannelID = "019947a7-f200-7000-8000-000000000001"

func notificationConfig(kind string, spec map[string]any) map[string]any {
	return map[string]any{"kind": kind, "spec": spec}
}

func notificationChannelJSON(kind string) json.RawMessage {
	return json.RawMessage(`{"name":"channel","displayName":"Channel","config":{"kind":"` + kind + `","spec":{"url":"https://example.test/hook"}},"id":"` + testNotificationChannelID + `","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`)
}

func TestHandleListNotificationChannels_ForwardsNormalizedFilters(t *testing.T) {
	var got types.NotificationChannelListParams
	mock := &client.MockClient{ListNotificationChannelsV2Fn: func(_ context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
		got = params
		return types.NotificationChannelList{Channels: []types.ListedNotificationChannel{{
			ID: testNotificationChannelID, Name: "channel", DisplayName: "Channel", Kind: "webhook",
			CreatedAt: "2026-09-18T00:00:00Z", UpdatedAt: "2026-09-18T00:00:00Z",
		}}, Total: 7}, nil
	}}
	h := newTestHandler(mock)
	result, err := h.handleListNotificationChannels(testCtx(), makeToolRequest("signoz_list_notification_channels", map[string]any{
		"query": "chan & ops", "kind": "webhook", "sort": "name", "order": "asc", "limit": float64(500), "offset": float64(4),
	}))
	if err != nil || result.IsError {
		t.Fatalf("list failed: err=%v result=%v", err, result)
	}
	if got.Query != "chan & ops" || got.Kind != "webhook" || got.Sort != "name" || got.Order != "asc" || got.Limit != 200 || got.Offset != 4 {
		t.Fatalf("normalized params = %#v", got)
	}
	var body struct {
		Channels   []types.ListedNotificationChannel `json:"channels"`
		Total      int                               `json:"total"`
		Pagination struct {
			Limit      int  `json:"limit"`
			Offset     int  `json:"offset"`
			Count      int  `json:"count"`
			HasMore    bool `json:"hasMore"`
			NextOffset int  `json:"nextOffset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal([]byte(textContent(t, result)), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Channels) != 1 || body.Total != 7 {
		t.Fatalf("list body = %#v", body)
	}
	if body.Pagination.Limit != 200 || body.Pagination.Offset != 4 || body.Pagination.Count != 1 || !body.Pagination.HasMore || body.Pagination.NextOffset != 5 {
		t.Fatalf("pagination = %#v", body.Pagination)
	}
	if strings.Contains(textContent(t, result), "config") {
		t.Fatal("list leaked configuration")
	}
}

func TestHandleCreateNotificationChannel_TestIsExplicitOptIn(t *testing.T) {
	for _, tc := range []struct {
		name          string
		test          any
		wantTests     int
		wantStatus    string
		wantRequested bool
	}{
		{name: "default false", wantTests: 0, wantStatus: "skipped"},
		{name: "explicit false", test: false, wantTests: 0, wantStatus: "skipped"},
		{name: "explicit true", test: true, wantTests: 1, wantStatus: "succeeded", wantRequested: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testCalls := 0
			mock := &client.MockClient{
				CreateNotificationChannelFn: func(_ context.Context, body []byte) (json.RawMessage, error) {
					if strings.Contains(string(body), `"test"`) {
						t.Fatalf("write body contains handler-only test flag: %s", body)
					}
					return notificationChannelJSON("webhook"), nil
				},
				TestNotificationChannelFn: func(_ context.Context, body []byte) error {
					testCalls++
					var got map[string]any
					_ = json.Unmarshal(body, &got)
					if len(got) != 1 || got["config"] == nil {
						t.Fatalf("test body must contain config only: %s", body)
					}
					return nil
				},
			}
			args := map[string]any{"name": "channel", "displayName": "Channel", "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"})}
			if tc.test != nil {
				args["test"] = tc.test
			}
			result, err := newTestHandler(mock).handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", args))
			if err != nil || result.IsError {
				t.Fatalf("create failed: err=%v result=%v", err, result)
			}
			if testCalls != tc.wantTests {
				t.Fatalf("test calls = %d, want %d", testCalls, tc.wantTests)
			}
			testResult := result.StructuredContent.(map[string]any)["testNotification"].(map[string]any)
			if testResult["status"] != tc.wantStatus || testResult["requested"] != tc.wantRequested {
				t.Fatalf("testNotification = %#v", testResult)
			}
			if tc.wantRequested && testResult["success"] != true {
				t.Fatalf("testNotification = %#v, want successful test", testResult)
			}
			if !tc.wantRequested {
				if _, present := testResult["success"]; present {
					t.Fatalf("skipped testNotification has success: %#v", testResult)
				}
			}
		})
	}
}

func TestHandleListNotificationChannels_AcceptsStringNumbers(t *testing.T) {
	var got types.NotificationChannelListParams
	mock := &client.MockClient{ListNotificationChannelsV2Fn: func(_ context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
		got = params
		return types.NotificationChannelList{Channels: []types.ListedNotificationChannel{}, Total: 0}, nil
	}}
	h := newTestHandler(mock)
	result, err := h.handleListNotificationChannels(testCtx(), makeToolRequest("signoz_list_notification_channels", map[string]any{"limit": "50", "offset": " 10 "}))
	if err != nil || result.IsError {
		t.Fatalf("string limit/offset rejected: err=%v result=%v", err, result)
	}
	if got.Limit != 50 || got.Offset != 10 {
		t.Fatalf("params = %#v, want limit 50 offset 10", got)
	}
	result, _ = h.handleListNotificationChannels(testCtx(), makeToolRequest("signoz_list_notification_channels", map[string]any{"limit": "fifty"}))
	if !result.IsError || resultCode(t, result) != CodeValidationFailed || !strings.Contains(textContent(t, result), `"limit" must be an integer`) {
		t.Fatalf("non-numeric limit error = %v", result)
	}
	result, _ = h.handleListNotificationChannels(testCtx(), makeToolRequest("signoz_list_notification_channels", map[string]any{"bogus": 1}))
	if !result.IsError || !strings.Contains(textContent(t, result), `"bogus" is not a parameter of this tool`) {
		t.Fatalf("unknown parameter error = %v", result)
	}
}

func TestHandleUpdateNotificationChannel_TestStatusIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		test          any
		wantTests     int
		wantStatus    string
		wantRequested bool
	}{
		{name: "default false", wantStatus: "skipped"},
		{name: "explicit false", test: false, wantStatus: "skipped"},
		{name: "explicit true", test: true, wantTests: 1, wantStatus: "succeeded", wantRequested: true},
		{name: "string true", test: "true", wantTests: 1, wantStatus: "succeeded", wantRequested: true},
		{name: "string false", test: "false", wantStatus: "skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testCalls := 0
			mock := &client.MockClient{
				UpdateNotificationChannelFn: func(context.Context, string, []byte) error { return nil },
				GetNotificationChannelFn: func(context.Context, string) (json.RawMessage, error) {
					return notificationChannelJSON("webhook"), nil
				},
				TestNotificationChannelFn: func(context.Context, []byte) error {
					testCalls++
					return nil
				},
			}
			args := map[string]any{
				"id": testNotificationChannelID, "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}),
			}
			if tc.test != nil {
				args["test"] = tc.test
			}
			result, err := newTestHandler(mock).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", args))
			if err != nil || result.IsError {
				t.Fatalf("update failed: err=%v result=%v", err, result)
			}
			if testCalls != tc.wantTests {
				t.Fatalf("test calls = %d, want %d", testCalls, tc.wantTests)
			}
			testResult := result.StructuredContent.(map[string]any)["testNotification"].(map[string]any)
			if testResult["status"] != tc.wantStatus || testResult["requested"] != tc.wantRequested {
				t.Fatalf("testNotification = %#v", testResult)
			}
			if tc.wantRequested && testResult["success"] != true {
				t.Fatalf("testNotification = %#v, want successful test", testResult)
			}
		})
	}
}

func TestHandleUpdateNotificationChannel_IdentityFieldsRejected(t *testing.T) {
	for field, wantHint := range map[string]string{
		"name":          "channel names are immutable",
		"displayName":   "channel names are immutable",
		"generateName":  "",
		"type":          "config: {kind, spec}",
		"send_resolved": "config: {kind, spec}",
	} {
		t.Run(field, func(t *testing.T) {
			args := map[string]any{
				"id":     testNotificationChannelID,
				"config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}),
				field:    "legacy",
			}
			result, err := newTestHandler(&client.MockClient{}).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", args))
			if err != nil || !result.IsError || resultCode(t, result) != CodeValidationFailed {
				t.Fatalf("field %s was not rejected: err=%v result=%v", field, err, result)
			}
			if !strings.Contains(textContent(t, result), wantHint) {
				t.Fatalf("field %s error %q lacks hint %q", field, textContent(t, result), wantHint)
			}
		})
	}
}

func TestHandleCreateNotificationChannel_LegacyFlatParametersGuideToConfig(t *testing.T) {
	result, err := newTestHandler(&client.MockClient{}).handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type": "slack", "name": "oncall", "slack_api_url": "https://hooks.slack.test/x",
	}))
	if err != nil || !result.IsError || resultCode(t, result) != CodeValidationFailed {
		t.Fatalf("legacy create was not rejected: err=%v result=%v", err, result)
	}
	if text := textContent(t, result); !strings.Contains(text, "retired flat parameter") || !strings.Contains(text, `"kind":"slack"`) {
		t.Fatalf("legacy create error lacks migration guidance: %q", text)
	}
}

func TestHandleUpdateNotificationChannel_ReadBackFailureIsAdvisory(t *testing.T) {
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(context.Context, string, []byte) error { return nil },
		GetNotificationChannelFn:    func(context.Context, string) (json.RawMessage, error) { return nil, errors.New("read failed") },
	}
	result, err := newTestHandler(mock).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id": testNotificationChannelID, "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}),
	}))
	if err != nil || result.IsError {
		t.Fatalf("update should remain successful: err=%v result=%v", err, result)
	}
	if !strings.Contains(result.Content[1].(*mcp.TextContent).Text, "update was committed") {
		t.Fatalf("missing advisory: %#v", result.Content)
	}
}

func TestHandleUpdateNotificationChannel_NormalizesOnlyUpstreamUnsetStrings(t *testing.T) {
	var updateBody []byte
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(_ context.Context, _ string, body []byte) error {
			updateBody = append([]byte(nil), body...)
			return nil
		},
		GetNotificationChannelFn: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"` + testNotificationChannelID + `"}`), nil
		},
	}
	result, err := newTestHandler(mock).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id": testNotificationChannelID,
		"config": notificationConfig("slack", map[string]any{
			"apiUrl": "https://hooks.slack.test/secret", "channel": "#ops", "title": "", "text": "",
		}),
	}))
	if err != nil || result.IsError {
		t.Fatalf("update failed: err=%v result=%v", err, result)
	}
	var body map[string]any
	if err := json.Unmarshal(updateBody, &body); err != nil {
		t.Fatal(err)
	}
	spec := body["config"].(map[string]any)["spec"].(map[string]any)
	if _, ok := spec["title"]; ok {
		t.Fatalf("unset title was retained: %s", updateBody)
	}
	if _, ok := spec["text"]; ok {
		t.Fatalf("unset text was retained: %s", updateBody)
	}
	if channel, ok := spec["channel"]; !ok || channel != "#ops" {
		t.Fatalf("ordinary channel was not preserved: %s", updateBody)
	}
}

func TestHandleCreateNotificationChannel_TestOutcomeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name             string
		testErr          error
		wantStatus       string
		wantSuccess      bool
		wantSuccessField bool
		wantNote         string
	}{
		{
			name: "definitive HTTP response is failed", testErr: &client.HTTPStatusError{StatusCode: http.StatusBadGateway, Body: `provider rejected request`},
			wantStatus: "failed", wantSuccessField: true, wantNote: "test notification failed",
		},
		{
			name: "transport timeout is unknown", testErr: context.DeadlineExceeded,
			wantStatus: "unknown", wantNote: "may have been processed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testCalls := 0
			mock := &client.MockClient{
				CreateNotificationChannelFn: func(context.Context, []byte) (json.RawMessage, error) { return notificationChannelJSON("webhook"), nil },
				TestNotificationChannelFn: func(context.Context, []byte) error {
					testCalls++
					return tc.testErr
				},
			}
			result, err := newTestHandler(mock).handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", map[string]any{
				"name": "channel", "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}), "test": true,
			}))
			if err != nil || result.IsError {
				t.Fatalf("create should remain successful: err=%v result=%v", err, result)
			}
			if testCalls != 1 {
				t.Fatalf("test calls = %d, want 1", testCalls)
			}
			if len(result.Content) != 2 {
				t.Fatalf("content blocks = %d, want payload and note", len(result.Content))
			}
			note, ok := mcp.AsTextContent(result.Content[1])
			if !ok {
				t.Fatalf("note content = %#v", result.Content[1])
			}
			if strings.Contains(textContent(t, result)+note.Text, tc.testErr.Error()) {
				t.Fatal("raw upstream error was echoed")
			}
			testResult := result.StructuredContent.(map[string]any)["testNotification"].(map[string]any)
			if testResult["requested"] != true || testResult["status"] != tc.wantStatus {
				t.Fatalf("test result = %#v", testResult)
			}
			success, present := testResult["success"]
			if present != tc.wantSuccessField || (present && success != tc.wantSuccess) {
				t.Fatalf("test result success = %#v, present=%t", success, present)
			}
			if !strings.Contains(note.Text, tc.wantNote) {
				t.Fatalf("result note = %q, want %q", note.Text, tc.wantNote)
			}
		})
	}
}

func TestNotificationChannelFailuresLogOnlySafeMetadata(t *testing.T) {
	const secret = "https://hooks.example.test/provider-secret-canary"
	credentialError := func() error {
		return &client.HTTPStatusError{
			StatusCode: http.StatusBadRequest,
			Body:       `{"status":"error","error":{"code":"invalid_input","type":"invalid-input","message":"parse \"` + secret + `\": net/url: invalid control character"}}`,
		}
	}

	tests := []struct {
		name       string
		wantLog    string
		invoke     func(*Handler) *mcp.CallToolResult
		wantIsErr  bool
		wantStatus string
	}{
		{
			name:    "create",
			wantLog: "Failed to create notification channel",
			invoke: func(h *Handler) *mcp.CallToolResult {
				h.clientOverride = &client.MockClient{CreateNotificationChannelFn: func(context.Context, []byte) (json.RawMessage, error) {
					return nil, credentialError()
				}}
				result, _ := h.handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", map[string]any{
					"name": "channel", "config": notificationConfig("webhook", map[string]any{"url": secret}),
				}))
				return result
			},
			wantIsErr: true,
		},
		{
			name:    "update",
			wantLog: "Failed to update notification channel",
			invoke: func(h *Handler) *mcp.CallToolResult {
				h.clientOverride = &client.MockClient{UpdateNotificationChannelFn: func(context.Context, string, []byte) error {
					return credentialError()
				}}
				result, _ := h.handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", map[string]any{
					"id": testNotificationChannelID, "config": notificationConfig("webhook", map[string]any{"url": secret}),
				}))
				return result
			},
			wantIsErr: true,
		},
		{
			name:    "test",
			wantLog: "Notification channel test failed after write",
			invoke: func(h *Handler) *mcp.CallToolResult {
				h.clientOverride = &client.MockClient{
					CreateNotificationChannelFn: func(context.Context, []byte) (json.RawMessage, error) {
						return notificationChannelJSON("webhook"), nil
					},
					TestNotificationChannelFn: func(context.Context, []byte) error { return credentialError() },
				}
				result, _ := h.handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", map[string]any{
					"name": "channel", "config": notificationConfig("webhook", map[string]any{"url": secret}), "test": true,
				}))
				return result
			},
			wantStatus: "failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := &Handler{logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
			result := tc.invoke(h)
			if result == nil || result.IsError != tc.wantIsErr {
				t.Fatalf("result = %#v, want IsError=%t", result, tc.wantIsErr)
			}
			if tc.wantIsErr {
				structured := resultStructuredMap(t, result)
				if structured["code"] != CodeValidationFailed || structured["status"] != http.StatusBadRequest {
					t.Fatalf("coded result = %#v", structured)
				}
			} else {
				testResult := result.StructuredContent.(map[string]any)["testNotification"].(map[string]any)
				if testResult["status"] != tc.wantStatus || testResult["success"] != false {
					t.Fatalf("testNotification = %#v", testResult)
				}
			}
			if strings.Contains(logs.String(), secret) {
				t.Fatalf("notification channel log leaked provider URL: %s", logs.String())
			}
			if !strings.Contains(logs.String(), `"msg":"`+tc.wantLog+`"`) || !strings.Contains(logs.String(), `"status":400`) {
				t.Fatalf("notification channel log lost safe status metadata: %s", logs.String())
			}
		})
	}
}

func TestHandleUpdateNotificationChannel_PostWriteAuthErrorIsCoded(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		testRequested bool
		wantCode      string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: CodeUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, wantCode: CodePermissionDenied},
		{name: "requested test aborted before send", status: http.StatusUnauthorized, testRequested: true, wantCode: CodeUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testCalls := 0
			mock := &client.MockClient{
				UpdateNotificationChannelFn: func(context.Context, string, []byte) error { return nil },
				GetNotificationChannelFn: func(context.Context, string) (json.RawMessage, error) {
					return nil, &client.HTTPStatusError{StatusCode: tc.status, Body: `{"status":"error"}`}
				},
				TestNotificationChannelFn: func(context.Context, []byte) error {
					testCalls++
					return nil
				},
			}
			args := map[string]any{
				"id": testNotificationChannelID, "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}),
			}
			if tc.testRequested {
				args["test"] = true
			}
			result, err := newTestHandler(mock).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", args))
			if err != nil || !result.IsError {
				t.Fatalf("auth failure not returned: err=%v result=%v", err, result)
			}
			structured := resultStructuredMap(t, result)
			if structured["code"] != tc.wantCode || structured["mutationCommitted"] != true || structured["id"] != testNotificationChannelID {
				t.Fatalf("structured auth result = %#v", structured)
			}
			testResult := structured["testNotification"].(map[string]any)
			if testResult["status"] != "skipped" || testResult["requested"] != tc.testRequested {
				t.Fatalf("testNotification = %#v", testResult)
			}
			if _, present := testResult["success"]; present {
				t.Fatalf("unattempted testNotification has success: %#v", testResult)
			}
			if testCalls != 0 {
				t.Fatalf("test calls = %d, want 0", testCalls)
			}
			if tc.testRequested && !strings.Contains(testResult["reason"].(string), "not attempted") {
				t.Fatalf("testNotification reason = %#v", testResult)
			}
		})
	}
}

func TestHandleCreateNotificationChannel_TestAuthErrorKeepsCommittedID(t *testing.T) {
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(context.Context, []byte) (json.RawMessage, error) {
			return notificationChannelJSON("webhook"), nil
		},
		TestNotificationChannelFn: func(context.Context, []byte) error {
			return &client.HTTPStatusError{StatusCode: http.StatusUnauthorized, Body: `{"status":"error"}`}
		},
	}
	result, err := newTestHandler(mock).handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", map[string]any{
		"name": "channel", "config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"}), "test": true,
	}))
	if err != nil || !result.IsError {
		t.Fatalf("auth failure not returned: err=%v result=%v", err, result)
	}
	structured := resultStructuredMap(t, result)
	if structured["code"] != CodeUnauthorized || structured["mutationCommitted"] != true || structured["id"] != testNotificationChannelID {
		t.Fatalf("structured auth result = %#v", structured)
	}
	testResult := structured["testNotification"].(map[string]any)
	if testResult["status"] != "failed" || testResult["requested"] != true || testResult["success"] != false {
		t.Fatalf("testNotification = %#v", testResult)
	}
}

func TestHandleNotificationChannel_CommittedParseErrorReportsTestNotAttempted(t *testing.T) {
	for _, tc := range []struct {
		operation     string
		testRequested bool
	}{
		{operation: "create"},
		{operation: "create", testRequested: true},
		{operation: "update"},
		{operation: "update", testRequested: true},
	} {
		name := tc.operation + "/test_false"
		if tc.testRequested {
			name = tc.operation + "/test_true"
		}
		t.Run(name, func(t *testing.T) {
			testCalls := 0
			committedErr := func() error {
				id := testNotificationChannelID
				if tc.operation == "update" {
					id = ""
				}
				return &client.CommittedNotificationChannelError{
					Operation: tc.operation, ID: id, Name: "channel", DisplayName: "Channel", Err: errors.New("bad shape"),
				}
			}
			mock := &client.MockClient{
				CreateNotificationChannelFn: func(context.Context, []byte) (json.RawMessage, error) { return nil, committedErr() },
				UpdateNotificationChannelFn: func(context.Context, string, []byte) error { return committedErr() },
				TestNotificationChannelFn: func(context.Context, []byte) error {
					testCalls++
					return nil
				},
			}
			args := map[string]any{"config": notificationConfig("webhook", map[string]any{"url": "https://example.test/hook"})}
			if tc.operation == "create" {
				args["name"] = "channel"
			} else {
				args["id"] = testNotificationChannelID
			}
			if tc.testRequested {
				args["test"] = true
			}

			var result *mcp.CallToolResult
			var err error
			if tc.operation == "create" {
				result, err = newTestHandler(mock).handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", args))
			} else {
				result, err = newTestHandler(mock).handleUpdateNotificationChannel(testCtx(), makeToolRequest("signoz_update_notification_channel", args))
			}
			if err != nil || !result.IsError {
				t.Fatalf("committed parse failure not surfaced: err=%v result=%v", err, result)
			}
			structured := resultStructuredMap(t, result)
			if structured["code"] != CodeUpstreamError || structured["mutationCommitted"] != true || structured["id"] != testNotificationChannelID {
				t.Fatalf("structured result = %#v", structured)
			}
			testResult := structured["testNotification"].(map[string]any)
			if testResult["status"] != "skipped" || testResult["requested"] != tc.testRequested {
				t.Fatalf("testNotification = %#v", testResult)
			}
			if _, present := testResult["success"]; present {
				t.Fatalf("unattempted testNotification has success: %#v", testResult)
			}
			if tc.testRequested && !strings.Contains(testResult["reason"].(string), "not attempted") {
				t.Fatalf("testNotification reason = %#v", testResult)
			}
			if testCalls != 0 {
				t.Fatalf("test calls = %d, want 0", testCalls)
			}
		})
	}
}
