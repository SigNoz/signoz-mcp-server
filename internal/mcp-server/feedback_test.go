package mcp_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// submitFeedbackOverHTTP sends one signoz_submit_feedback call through the
// production HTTP handler against a SigNoz that knows the caller as user
// sa-1 (ada@example.com) in org org-1, and returns what reached analytics.
func submitFeedbackOverHTTP(t *testing.T, analyticsConfig analytics.Config) []analyticsCall {
	t.Helper()
	signoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1","name":"Ada","email":"ada@example.com"}}`))
	}))
	t.Cleanup(signoz.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{TransportMode: "http", URL: signoz.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20, Analytics: analyticsConfig}
	spy := &spyAnalytics{enabled: true}
	server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, spy, nil)
	sdkServer := server.newSDKServer()
	server.registerHandlers(sdkServer)

	body := protocolRequestJSON(t, 1, "tools/call", modernParams(nil, map[string]any{
		"name": "signoz_submit_feedback",
		"arguments": map[string]any{
			"message":  "signoz_search_logs did not say which filter key was invalid.",
			"category": "tool_error",
			"toolName": "signoz_search_logs",
		},
	}))
	headers := modernHeaders("tools/call", "signoz_submit_feedback")
	headers.Set("SIGNOZ-API-KEY", "test-api-key")
	response := protocolPOST(t, server.buildHTTP(sdkServer).Handler, body, headers)
	if response.status != http.StatusOK || !strings.Contains(string(response.body), "Feedback queued for delivery") {
		t.Fatalf("feedback call: status %d, body %s", response.status, response.body)
	}
	if err := server.WaitForAnalytics(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, tracked := spy.snapshot()
	return tracked
}

func TestFeedback_AnonymousAnalyticsSendsOnlyTheReportUnderAHashedOrg(t *testing.T) {
	tracked := submitFeedbackOverHTTP(t, analytics.Config{Enabled: true, Anonymous: true})

	if len(tracked) != 1 {
		t.Fatalf("anonymous analytics sent %d events, want only the feedback report (no usage events)", len(tracked))
	}
	event := tracked[0]
	if event.event != "MCP Feedback: Submitted" {
		t.Fatalf("event = %q, want %q", event.event, "MCP Feedback: Submitted")
	}
	if event.userID == "" || event.userID != event.groupID {
		t.Fatalf("user %q and group %q, want the same non-empty hashed org", event.userID, event.groupID)
	}
	attrs, err := json.Marshal(event.attrs)
	if err != nil {
		t.Fatal(err)
	}
	payload := event.userID + " " + string(attrs)
	for _, identifying := range []string{"org-1", "sa-1", "Ada", "ada@example.com", "127.0.0.1", "test-api-key"} {
		if strings.Contains(payload, identifying) {
			t.Fatalf("anonymous feedback event carried %q: %s", identifying, payload)
		}
	}
	want := map[string]any{
		"feedbackMessage":  "signoz_search_logs did not say which filter key was invalid.",
		"feedbackCategory": "tool_error",
		"feedbackToolName": "signoz_search_logs",
		"clientName":       "protocol-test",
		"clientVersion":    "1",
	}
	for key, value := range want {
		if event.attrs[key] != value {
			t.Fatalf("anonymous feedback %s = %v, want %v", key, event.attrs[key], value)
		}
	}
}

func TestFeedback_OperatorAnalyticsKeepsUsageEventsAndIdentity(t *testing.T) {
	tracked := submitFeedbackOverHTTP(t, analytics.Config{Enabled: true})

	events := map[string]analyticsCall{}
	for _, call := range tracked {
		events[call.event] = call
	}
	if _, ok := events["MCP Tool: Called"]; !ok {
		t.Fatalf("operator analytics lost the tool-call usage event; got %d events: %v", len(tracked), eventNames(tracked))
	}
	feedback, ok := events["MCP Feedback: Submitted"]
	if !ok {
		t.Fatalf("operator analytics got no feedback event; got: %v", eventNames(tracked))
	}
	if feedback.groupID != "org-1" || feedback.userID != "sa-1" {
		t.Fatalf("feedback attributed to group %q user %q, want org-1 and sa-1 like every other event", feedback.groupID, feedback.userID)
	}
	if feedback.attrs["email"] != "ada@example.com" || feedback.attrs["orgId"] != "org-1" {
		t.Fatalf("feedback identity attrs = %v, want the email and orgId every other event carries", feedback.attrs)
	}
	if feedback.attrs["feedbackMessage"] != "signoz_search_logs did not say which filter key was invalid." {
		t.Fatalf("feedbackMessage = %v", feedback.attrs["feedbackMessage"])
	}
}

func eventNames(calls []analyticsCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.event)
	}
	return names
}
