package tools

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

const testNotificationChannelID = "019947a7-f200-7000-8000-000000000001"

// TestNotificationChannelFailuresLogOnlySafeMetadata pins the credential
// contract from CLAUDE.md through the full production path (real client against
// a canned upstream): when SigNoz rejects a channel write or a post-write test
// send with a body that echoes the provider URL, the handler's logs carry only
// safe metadata (message, status), never the secret-bearing URL, while the tool
// result still surfaces the coded failure.
func TestNotificationChannelFailuresLogOnlySafeMetadata(t *testing.T) {
	const secret = "https://hooks.example.test/provider-secret-canary"
	secretEchoBody := `{"status":"error","error":{"code":"invalid_input","type":"invalid-input","message":"parse \"` + secret + `\": net/url: invalid control character"}}`
	channelBody := `{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"url":"https://example.test/hook"}},"id":"` + testNotificationChannelID + `","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}}`

	channelArgs := func(test bool) map[string]any {
		return map[string]any{
			"name":   "channel",
			"config": map[string]any{"kind": "webhook", "spec": map[string]any{"url": secret}},
			"test":   test,
		}
	}

	t.Run("create failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(secretEchoBody))
		}))
		t.Cleanup(srv.Close)

		var logs bytes.Buffer
		h := &Handler{
			logger:         slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			clientOverride: signozclient.NewClient(slog.New(slog.NewJSONHandler(&logs, nil)), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
		}

		result, err := h.handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", channelArgs(false)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("create against a 400 upstream must be an error result, got success")
		}
		structured := resultStructuredMap(t, result)
		if structured["code"] != CodeValidationFailed || structured["status"] != http.StatusBadRequest {
			t.Fatalf("coded result = %#v, want VALIDATION_FAILED with status 400", structured)
		}
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("notification channel log leaked provider URL: %s", logs.String())
		}
		if !strings.Contains(logs.String(), `"msg":"Failed to create notification channel"`) || !strings.Contains(logs.String(), `"status":400`) {
			t.Fatalf("notification channel log lost safe status metadata: %s", logs.String())
		}
	})

	t.Run("test send failure after committed write", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/notification_channels/test") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(secretEchoBody))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(channelBody))
		}))
		t.Cleanup(srv.Close)

		var logs bytes.Buffer
		h := &Handler{
			logger:         slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			clientOverride: signozclient.NewClient(slog.New(slog.NewJSONHandler(&logs, nil)), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
		}

		result, err := h.handleCreateNotificationChannel(testCtx(), makeToolRequest("signoz_create_notification_channel", channelArgs(true)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("committed write with failed test send must stay a success result, got error: %v", result.Content)
		}
		structured, ok := result.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("structured content = %#v, want object", result.StructuredContent)
		}
		var testNotification map[string]any
		if raw, ok := structured["testNotification"].(map[string]any); ok {
			testNotification = raw
		} else if raw, err := json.Marshal(structured["testNotification"]); err == nil {
			_ = json.Unmarshal(raw, &testNotification)
		}
		if testNotification["status"] != "failed" || testNotification["success"] != false {
			t.Fatalf("testNotification = %#v, want status=failed success=false", structured["testNotification"])
		}
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("notification channel log leaked provider URL: %s", logs.String())
		}
		if !strings.Contains(logs.String(), `"msg":"Notification channel test failed after write"`) {
			t.Fatalf("notification channel log lost the post-write test failure marker: %s", logs.String())
		}
	})
}
