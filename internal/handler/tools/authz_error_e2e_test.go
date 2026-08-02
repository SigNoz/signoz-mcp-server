package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/server"
)

// TestAuthorizationErrors_EndToEndMCPWire exercises the production SigNoz
// client, one read handler, one write handler, registered-tool decorators, and
// MCP JSON-RPC serialization against a deterministic local upstream. This is
// the cross-boundary regression for malformed authorization bodies: diagnostics
// remain bounded in server logs but never reach the agent-facing wire result.
func TestAuthorizationErrors_EndToEndMCPWire(t *testing.T) {
	const (
		readCanary       = "read-auth-log-canary"
		readTail         = "read-auth-log-tail"
		recognizedCanary = "recognized-auth-secret-canary"
		suggestionCanary = "suggestion-auth-secret-canary"
		detailCanary     = "detail-auth-secret-canary"
		urlCanary        = "url-auth-secret-canary"
		writeCanary      = "write-auth-log-canary"
		writeTail        = "write-auth-log-tail"
	)
	var readRequests atomic.Int32
	var writeRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(signozclient.SignozApiKey); got != "synthetic-test-key" {
			t.Errorf("upstream auth header = %q, want synthetic test key", got)
		}
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/stats":
			attempt := readRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			switch attempt {
			case 1:
				// Valid proxy JSON is still unrecognized without a verified
				// renderer tuple and must not reach MCP text/fields.
				_, _ = w.Write([]byte(`{"status":"error","message":"` + readCanary + strings.Repeat("x", 5000) + readTail + `"}`))
			case 2:
				// A recognized renderer may preserve guidance, but credential-
				// shaped fields, signed URLs, suggestions, and details must be
				// removed or redacted without losing the following recovery prose.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "error",
					"error": map[string]any{
						"type":        "session.id:" + recognizedCanary,
						"code":        "api.key:" + recognizedCanary,
						"message":     `permission check failed; headers={"Authorization":"Basic ` + recognizedCanary + ` suffix-canary"}; contact your administrator`,
						"url":         "https://signoz.io/docs?X-Amz-Signature=" + urlCanary,
						"suggestions": []string{"api.key=" + suggestionCanary + "; retry after access is fixed"},
						"errors": []map[string]any{{
							"message":     "session.id=" + detailCanary + "; keep this detail",
							"suggestions": []string{"Cookie: sid=" + detailCanary + "; contact support"},
						}},
					},
				})
			default:
				t.Errorf("unexpected extra stats request %d", attempt)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/dashboards/dashboard-probe":
			writeRequests.Add(1)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html>" + writeCanary + strings.Repeat("y", 5000) + writeTail + "</html>"))
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	realClient := signozclient.NewClient(logger, upstream.URL, "synthetic-test-key", signozclient.SignozApiKey, nil)
	h := &Handler{logger: logger, clientOverride: realClient}
	sdkServer := server.NewMCPServer("authz-wire-test", "0.0.0", server.WithToolCapabilities(false))
	h.RegisterOrgOverviewHandlers(sdkServer)
	h.RegisterDashboardHandlers(sdkServer)

	ctx := util.SetSigNozURL(context.Background(), upstream.URL)
	read := callToolWireResult(t, sdkServer, ctx, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"signoz_get_org_overview","arguments":{}}}`)
	assertAuthorizationWireResult(t, read, http.StatusUnauthorized, CodeUnauthorized, readCanary, "Affected operation: `signoz_get_org_overview` failed authentication.")

	recognized := callToolWireResult(t, sdkServer, ctx, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"signoz_get_org_overview","arguments":{}}}`)
	if !recognized.IsError || len(recognized.Content) == 0 || recognized.StructuredContent["code"] != CodeUnauthorized {
		t.Fatalf("recognized-envelope auth response lost classification: %s", recognized.encoded)
	}
	for _, leaked := range []string{recognizedCanary, suggestionCanary, detailCanary, urlCanary, "suffix-canary", "api.key:", "session.id:"} {
		if strings.Contains(recognized.encoded, leaked) {
			t.Fatalf("recognized-envelope auth response leaked %q: %s", leaked, recognized.encoded)
		}
	}
	if !strings.Contains(recognized.encoded, "［REDACTED］") || !strings.Contains(recognized.Content[0].Text, "Affected operation: `signoz_get_org_overview` failed authentication.") {
		t.Fatalf("recognized-envelope response lacks sanitization/recovery: %s", recognized.encoded)
	}
	for _, preserved := range []string{"contact your administrator", "retry after access is fixed", "keep this detail", "contact support"} {
		if !strings.Contains(recognized.encoded, preserved) {
			t.Fatalf("recognized-envelope response lost post-secret guidance %q: %s", preserved, recognized.encoded)
		}
	}

	write := callToolWireResult(t, sdkServer, ctx, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"signoz_delete_dashboard","arguments":{"id":"dashboard-probe"}}}`)
	assertAuthorizationWireResult(t, write, http.StatusForbidden, CodePermissionDenied, writeCanary, "write operation (`signoz_delete_dashboard`)")

	if got := readRequests.Load(); got != 2 {
		t.Fatalf("stats requests = %d, want exactly two calls with no retries", got)
	}
	if got := writeRequests.Load(); got != 1 {
		t.Fatalf("dashboard requests = %d, want exactly one non-retried DELETE", got)
	}

	serverLogs := logs.String()
	for _, canary := range []string{readCanary, writeCanary} {
		if !strings.Contains(serverLogs, canary) {
			t.Fatalf("bounded server diagnostics do not contain %q", canary)
		}
	}
	for _, tail := range []string{readTail, writeTail} {
		if strings.Contains(serverLogs, tail) {
			t.Fatalf("server diagnostics were not bounded; found tail %q", tail)
		}
	}
	if !strings.Contains(serverLogs, "...(truncated)") {
		t.Fatal("server diagnostics do not mark the bounded authorization body as truncated")
	}
}

type authorizationWireResult struct {
	IsError           bool           `json:"isError"`
	Content           []wireContent  `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
	encoded           string
}

type wireContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func callToolWireResult(t *testing.T, sdkServer *server.MCPServer, ctx context.Context, request string) authorizationWireResult {
	t.Helper()
	response := sdkServer.HandleMessage(ctx, json.RawMessage(request))
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode MCP response: %v", err)
	}
	var envelope struct {
		Result authorizationWireResult `json:"result"`
		Error  any                     `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode MCP response %s: %v", encoded, err)
	}
	if envelope.Error != nil {
		t.Fatalf("authorization failure became a JSON-RPC error: %s", encoded)
	}
	envelope.Result.encoded = string(encoded)
	return envelope.Result
}

func assertAuthorizationWireResult(t *testing.T, result authorizationWireResult, status int, code, unsafeBody, recovery string) {
	t.Helper()
	if !result.IsError {
		t.Fatalf("MCP result is not an error: %s", result.encoded)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "text" {
		t.Fatalf("MCP result has no text error content: %s", result.encoded)
	}
	if got := result.StructuredContent["code"]; got != code {
		t.Fatalf("code = %v, want %s; response=%s", got, code, result.encoded)
	}
	gotStatus := result.StructuredContent["status"]
	number, ok := gotStatus.(json.Number)
	if !ok || number.String() != strconv.Itoa(status) {
		t.Fatalf("status = %#v, want numeric %d; response=%s", gotStatus, status, result.encoded)
	}
	for _, key := range []string{"upstreamCode", "upstreamMessage", "upstreamType", "upstreamAuth"} {
		if _, exists := result.StructuredContent[key]; exists {
			t.Fatalf("unsafe body produced %s: %s", key, result.encoded)
		}
	}
	if strings.Contains(result.encoded, unsafeBody) || strings.Contains(result.encoded, "<html>") {
		t.Fatalf("MCP wire result leaked authorization body: %s", result.encoded)
	}
	if !strings.Contains(result.Content[0].Text, recovery) {
		t.Fatalf("error text lacks operation recovery %q: %s", recovery, result.Content[0].Text)
	}
	for _, unsupportedInference := range []string{"viewer-level", "editor access", "admin access"} {
		if strings.Contains(strings.ToLower(result.Content[0].Text), unsupportedInference) {
			t.Fatalf("error text inferred an unobserved role: %s", result.Content[0].Text)
		}
	}
}
