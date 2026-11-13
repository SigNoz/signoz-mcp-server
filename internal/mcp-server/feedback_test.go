package mcp_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
// sa-1 (ada@example.com) in org org-1. Usage analytics is on, so the result
// shows what reached the usage sink and what reached the feedback sink.
func submitFeedbackOverHTTP(t *testing.T, deployment string) (usage, feedback []analyticsCall) {
	t.Helper()
	signoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1","name":"Ada","email":"ada@example.com"}}`))
	}))
	t.Cleanup(signoz.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode: "http", URL: signoz.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20,
		Feedback: analytics.FeedbackConfig{Enabled: true, Deployment: deployment},
	}
	usageSpy := &spyAnalytics{enabled: true}
	feedbackSpy := &spyAnalytics{enabled: true}
	server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, usageSpy, nil)
	server.EnableFeedback(feedbackSpy)
	sdkServer := server.newSDKServer()
	server.registerHandlers(sdkServer)

	body := protocolRequestJSON(t, 1, "tools/call", modernParams(nil, map[string]any{
		"name": "signoz_submit_feedback",
		"arguments": map[string]any{
			"message":  "signoz_search_logs did not say which filter key was invalid.",
			"category": "tool_error",
			"toolName": "signoz_search_logs",
			"model":    "example-model-1",
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
	_, usage = usageSpy.snapshot()
	_, feedback = feedbackSpy.snapshot()
	return usage, feedback
}

// requireFeedbackKeptApartFromUsage fails unless the feedback sink got exactly
// the report and the usage sink got its own events without the report.
func requireFeedbackKeptApartFromUsage(t *testing.T, usage, feedback []analyticsCall) analyticsCall {
	t.Helper()
	if len(feedback) != 1 || feedback[0].event != "MCP Feedback: Submitted" {
		t.Fatalf("feedback sink got %v, want only the feedback report (no usage events)", eventNames(feedback))
	}
	usageEvents := eventNames(usage)
	if !slices.Contains(usageEvents, "MCP Tool: Called") {
		t.Fatalf("usage analytics lost the tool-call event; got %v", usageEvents)
	}
	if slices.Contains(usageEvents, "MCP Feedback: Submitted") {
		t.Fatalf("usage analytics got the feedback report, which belongs to the feedback sink only; got %v", usageEvents)
	}
	return feedback[0]
}

func TestFeedback_SelfHostedSendsTheReportUnderAHashedOrg(t *testing.T) {
	usage, feedback := submitFeedbackOverHTTP(t, analytics.DeploymentSelfHosted)
	event := requireFeedbackKeptApartFromUsage(t, usage, feedback)

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
			t.Fatalf("self-hosted feedback carried %q: %s", identifying, payload)
		}
	}
	want := map[string]any{
		"feedbackMessage":  "signoz_search_logs did not say which filter key was invalid.",
		"feedbackCategory": "tool_error",
		"feedbackToolName": "signoz_search_logs",
		"feedbackModel":    "example-model-1",
		"deployment":       "self_hosted",
		"clientName":       "protocol-test",
		"clientVersion":    "1",
	}
	for key, value := range want {
		if event.attrs[key] != value {
			t.Fatalf("self-hosted feedback %s = %v, want %v", key, event.attrs[key], value)
		}
	}
}

func TestFeedback_CloudDeploymentAttachesTheCallersIdentity(t *testing.T) {
	usage, feedback := submitFeedbackOverHTTP(t, analytics.DeploymentCloud)
	event := requireFeedbackKeptApartFromUsage(t, usage, feedback)

	if event.groupID != "org-1" || event.userID != "sa-1" {
		t.Fatalf("feedback attributed to group %q user %q, want org-1 and sa-1 like the hosted usage events", event.groupID, event.userID)
	}
	want := map[string]any{
		"feedbackMessage": "signoz_search_logs did not say which filter key was invalid.",
		"deployment":      "cloud",
		"orgId":           "org-1",
		"email":           "ada@example.com",
	}
	for key, value := range want {
		if event.attrs[key] != value {
			t.Fatalf("cloud feedback %s = %v, want %v", key, event.attrs[key], value)
		}
	}
}

func eventNames(calls []analyticsCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.event)
	}
	return names
}
