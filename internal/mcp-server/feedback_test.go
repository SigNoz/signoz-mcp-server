package mcp_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// submitFeedbackOverHTTP sends one signoz_submit_feedback call through the
// production HTTP handler against a SigNoz that knows the caller as user
// sa-1 (ada@example.com) in org org-1. Usage analytics is on, so the result
// shows what reached the usage sink and what reached the feedback sink.
func submitFeedbackOverHTTP(t *testing.T) (usage, feedback []analyticsCall) {
	t.Helper()
	signoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1","name":"Ada","email":"ada@example.com"}}`))
	}))
	t.Cleanup(signoz.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode: "http", URL: signoz.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20,
		Feedback: analytics.FeedbackConfig{Enabled: true},
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

func TestFeedback_SelfHostedSigNozSendsTheReportUnderAHashedOrg(t *testing.T) {
	usage, feedback := submitFeedbackOverHTTP(t)

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

	event := feedback[0]
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

// Identity is attached only when the call targets a SigNoz Cloud host. Hosts
// that merely look like one must stay on the hashed side.
func TestFeedback_OnlyASigNozCloudInstanceIsIdentified(t *testing.T) {
	identity := &signozclient.AnalyticsIdentity{OrgID: "org-1", UserID: "user-1", Principal: "user", Name: "Ada", Email: "ada@example.com"}
	tests := []struct {
		name       string
		signozURL  string
		identified bool
	}{
		{name: "SigNoz Cloud instance", signozURL: "https://acme.us.signoz.cloud", identified: true},
		{name: "SigNoz Cloud instance with port and upper case", signozURL: "https://Acme.EU.SigNoz.Cloud:443/", identified: true},
		{name: "self-hosted SigNoz", signozURL: "https://signoz.internal.example.com"},
		{name: "cloud domain as a subdomain of another host", signozURL: "https://acme.signoz.cloud.example.com"},
		{name: "domain that only ends with the same letters", signozURL: "https://notsignoz.cloud"},
		{name: "cloud domain in the path", signozURL: "https://example.com/acme.us.signoz.cloud"},
		{name: "cloud domain as userinfo", signozURL: "https://acme.us.signoz.cloud@example.com"},
		{name: "URL without a scheme", signozURL: "acme.us.signoz.cloud"},
		{name: "IPv6 literal whose zone ends in the cloud domain", signozURL: "http://[::1%25.acme.signoz.cloud]:8080"},
		{name: "IPv6 literal with a doubly escaped zone", signozURL: "http://[::1%2525.acme.signoz.cloud]:8080"},
		{name: "non-ASCII letter that lowercases into the cloud domain", signozURL: "https://acme.s\u0130gnoz.cloud"},
		{name: "punycode lookalike of the cloud domain", signozURL: "https://acme.xn--signoz-qyd.cloud"},
		{name: "no URL on the call", signozURL: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := logpkg.New("error")
			cfg := &config.Config{Feedback: analytics.FeedbackConfig{Enabled: true}}
			sink := &spyAnalytics{enabled: true}
			server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, &spyAnalytics{}, nil)
			server.EnableFeedback(sink)

			ctx := t.Context()
			if tt.signozURL != "" {
				ctx = util.SetSigNozURL(ctx, tt.signozURL)
			}
			server.recordFeedback(ctx, identity, tools.Feedback{Message: "the docs for alerts are unclear"})

			_, tracked := sink.snapshot()
			if len(tracked) != 1 {
				t.Fatalf("feedback sink got %d events, want 1", len(tracked))
			}
			event := tracked[0]
			if !tt.identified {
				if event.attrs["deployment"] != "self_hosted" {
					t.Fatalf("deployment = %v, want self_hosted", event.attrs["deployment"])
				}
				attrs, err := json.Marshal(event.attrs)
				if err != nil {
					t.Fatal(err)
				}
				payload := event.userID + " " + event.groupID + " " + string(attrs)
				for _, identifying := range []string{"org-1", "user-1", "Ada", "ada@example.com", "example.com", "signoz.cloud"} {
					if strings.Contains(payload, identifying) {
						t.Fatalf("report about %q carried %q: %s", tt.signozURL, identifying, payload)
					}
				}
				return
			}
			if event.groupID != "org-1" || event.userID != "user-1" {
				t.Fatalf("cloud report attributed to group %q user %q, want org-1 and user-1", event.groupID, event.userID)
			}
			want := map[string]any{"deployment": "cloud", "orgId": "org-1", "email": "ada@example.com", "tenantUrl": tt.signozURL}
			for key, value := range want {
				if event.attrs[key] != value {
					t.Fatalf("cloud report %s = %v, want %v", key, event.attrs[key], value)
				}
			}
		})
	}
}

func eventNames(calls []analyticsCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.event)
	}
	return names
}
