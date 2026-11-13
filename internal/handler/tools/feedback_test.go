package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

type deliveredFeedback struct {
	identity *signozclient.AnalyticsIdentity
	feedback Feedback
}

// newFeedbackServer registers every tool against a SigNoz upstream served by
// identity. A nil identity fails the test on any upstream request. Reports the
// tool accepts are appended to the returned slice.
func newFeedbackServer(t *testing.T, feedbackOn bool, identity http.HandlerFunc) (*mcp.Server, *[]deliveredFeedback) {
	t.Helper()
	if identity == nil {
		identity = func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("feedback call reached SigNoz: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
	upstream := httptest.NewServer(identity)
	t.Cleanup(upstream.Close)

	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	delivered := &[]deliveredFeedback{}
	if feedbackOn {
		h.SetFeedbackSink(func(_ context.Context, identity *signozclient.AnalyticsIdentity, feedback Feedback) {
			*delivered = append(*delivered, deliveredFeedback{identity: identity, feedback: feedback})
		})
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	return server, delivered
}

func TestSubmitFeedback_DeliversTheReportWithTheCallersOrg(t *testing.T) {
	server, delivered := newFeedbackServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			t.Errorf("feedback called %s, want only the identity lookup", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","orgId":"org-1"}}`))
	})

	result := callRegisteredTool(t, server, "signoz_submit_feedback", `{
		"message": "  signoz_search_logs returned VALIDATION_FAILED without naming the invalid key.  ",
		"category": "tool_error",
		"toolName": "signoz_search_logs",
		"model": "example-model-1"
	}`)

	if result.IsError {
		t.Fatalf("feedback call failed: %s", textContent(t, result))
	}
	if got := textContent(t, result); got != "Feedback queued for delivery to the SigNoz team." {
		t.Fatalf("result text = %q, want %q", got, "Feedback queued for delivery to the SigNoz team.")
	}
	if len(*delivered) != 1 {
		t.Fatalf("delivered %d reports, want 1", len(*delivered))
	}
	got := (*delivered)[0]
	want := Feedback{
		Message:  "signoz_search_logs returned VALIDATION_FAILED without naming the invalid key.",
		Category: "tool_error",
		ToolName: "signoz_search_logs",
		Model:    "example-model-1",
	}
	if got.feedback != want {
		t.Fatalf("delivered feedback = %+v, want %+v", got.feedback, want)
	}
	if got.identity.OrgID != "org-1" {
		t.Fatalf("delivered org = %q, want org-1 from the identity lookup", got.identity.OrgID)
	}
}

func TestSubmitFeedback_TurnedOffSendsNothing(t *testing.T) {
	server, delivered := newFeedbackServer(t, false, nil)

	result := callRegisteredTool(t, server, "signoz_submit_feedback", `{"message":"the docs for alerts are unclear"}`)

	if got := resultCode(t, result); got != CodeUnsupported {
		t.Fatalf("turned-off code = %q, want %q", got, CodeUnsupported)
	}
	if got := resultText(t, result); !strings.Contains(got, "Feedback is turned off on this server") || !strings.Contains(got, "Do not retry") {
		t.Fatalf("turned-off text = %q, want it to say feedback is off and not to retry", got)
	}
	if len(*delivered) != 0 {
		t.Fatalf("delivered %d reports while feedback is off, want 0", len(*delivered))
	}
}

func TestSubmitFeedback_RejectsInvalidReportsBeforeCallingSigNoz(t *testing.T) {
	server, delivered := newFeedbackServer(t, true, nil)
	tests := []struct {
		name      string
		arguments string
		wantText  string
	}{
		{name: "missing message", arguments: `{"category":"tool_error"}`, wantText: `"message" cannot be empty`},
		{name: "blank message", arguments: `{"message":"   "}`, wantText: `"message" cannot be empty`},
		{name: "message over the limit", arguments: `{"message":"` + strings.Repeat("a", 2001) + `"}`, wantText: `"message" must be at most 2000 characters`},
		{name: "unknown category", arguments: `{"message":"x","category":"bug"}`, wantText: `"category" must be one of: tool_error, confusing_output, missing_capability, documentation, other`},
		{name: "category of the wrong type", arguments: `{"message":"x","category":7}`, wantText: `"category" must be a string`},
		{name: "tool name over the limit", arguments: `{"message":"x","toolName":"` + strings.Repeat("t", 129) + `"}`, wantText: `"toolName" must be at most 128 characters`},
		{name: "model over the limit", arguments: `{"message":"x","model":"` + strings.Repeat("m", 129) + `"}`, wantText: `"model" must be at most 128 characters`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRegisteredTool(t, server, "signoz_submit_feedback", tt.arguments)
			if got := resultCode(t, result); got != CodeValidationFailed {
				t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
			}
			if got := resultText(t, result); !strings.Contains(got, tt.wantText) {
				t.Fatalf("text = %q, want it to contain %q", got, tt.wantText)
			}
		})
	}
	if len(*delivered) != 0 {
		t.Fatalf("delivered %d invalid reports, want 0", len(*delivered))
	}
}

func TestSubmitFeedback_SigNozAuthFailuresKeepTheirCodeAndStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode string
	}{
		{name: "rejected credentials", status: http.StatusUnauthorized, wantCode: CodeUnauthorized},
		{name: "missing permission", status: http.StatusForbidden, wantCode: CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, delivered := newFeedbackServer(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})

			result := callRegisteredTool(t, server, "signoz_submit_feedback", `{"message":"the docs for alerts are unclear"}`)

			if got := resultCode(t, result); got != tt.wantCode {
				t.Fatalf("code = %q, want %q", got, tt.wantCode)
			}
			structured, _ := result.StructuredContent.(map[string]any)
			if got := structured["status"]; got != float64(tt.status) {
				t.Fatalf("structured status = %v, want %d so the client can tell the failures apart", got, tt.status)
			}
			if len(*delivered) != 0 {
				t.Fatalf("delivered %d reports after SigNoz refused the caller, want 0", len(*delivered))
			}
		})
	}
}
