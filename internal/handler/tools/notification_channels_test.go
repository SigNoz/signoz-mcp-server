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
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/stretchr/testify/require"
)

const testNotificationChannelID = "019947a7-f200-7000-8000-000000000001"

func TestListNotificationChannels_MalformedSearchContextDoesNotBlock(t *testing.T) {
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"channels":[],"total":0}}`))
	})
	s := newMCPTestServer()
	h.RegisterNotificationChannelHandlers(s)

	result := callTestToolFromJSONRPC(testCtx(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"signoz_list_notification_channels","arguments":{"limit":"7","searchContext":{"unexpected":true}}}}`))
	require.False(t, result.IsError, "searchContext must not block listing channels: %s", textContent(t, result))
	require.Contains(t, textContent(t, result), `"limit":7`)
	require.Contains(t, textContent(t, result), `Input validation notice: parameter \"searchContext\"`)

	result = callTestToolFromJSONRPC(testCtx(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"signoz_list_notification_channels","arguments":{"unexpected":true,"searchContext":{"unexpected":true}}}}`))
	require.Equal(t, CodeValidationFailed, resultCode(t, result))
	require.Contains(t, resultText(t, result), `"unexpected" is not a parameter of this tool`)

	calls := requests()
	require.Len(t, calls, 1, "only the valid list request should reach SigNoz")
	require.Equal(t, http.MethodGet, calls[0].Method)
	require.Equal(t, "/api/v2/notification_channels", calls[0].Path)
	require.Contains(t, calls[0].Query, "limit=7")
	require.NotContains(t, calls[0].Query, "searchContext")
}

// TestNotificationChannelFailuresKeepTheProviderSecretOutOfLogs pins the
// credential contract from CLAUDE.md through the full production path (real
// client against a canned upstream): when SigNoz rejects a channel write or a
// post-write test send with a body that echoes the provider URL, the handler's
// logs never carry the secret-bearing URL, while the tool result still surfaces
// the coded failure.
func TestNotificationChannelFailuresKeepTheProviderSecretOutOfLogs(t *testing.T) {
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
	})
}

// SigNoz names an alert rule's own routing policy with the rule's ID, so the
// error says how to tell a rule from a routing policy and re-route each.
func TestDeleteNotificationChannel_InUseSaysTheIDsCanBeAlertRules(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"invalid_input","message":"channel \"ops\" cannot be deleted because it is used by the following routing policies: [01a1feb2-2b14-7360-bab7-1f5130e30541]"}}`))
	})
	result, err := h.handleDeleteNotificationChannel(testCtx(), makeToolRequest("signoz_delete_notification_channel", map[string]any{"id": "0199c47d-f61b-7833-bc5f-c0730f12f046"}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("a channel in use must fail: %v", result.Content)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := mcp.AsTextContent(content); ok {
			text.WriteString(block.Text + "\n")
		}
	}
	// SigNoz's refusal is kept; the recovery says a UUID name is an alert rule to check with
	// signoz_get_alert and re-route with signoz_update_alert, and any other name a routing policy.
	for _, want := range []string{"used by the following routing policies", "UUID", "signoz_get_alert", "signoz_update_alert", "routing policy in SigNoz settings"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("delete error lacks %q:\n%s", want, text.String())
		}
	}
}
