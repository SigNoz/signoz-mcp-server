package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type analyticsCall struct {
	groupID string
	userID  string
	event   string
	attrs   map[string]any
}

type spyAnalytics struct {
	mu            sync.Mutex
	enabled       bool
	identifyCalls []analyticsCall
	trackCalls    []analyticsCall
}

func (s *spyAnalytics) Enabled() bool                                   { return s.enabled }
func (s *spyAnalytics) Start(context.Context) error                     { return nil }
func (s *spyAnalytics) Stop(context.Context) error                      { return nil }
func (s *spyAnalytics) Send(context.Context, ...analyticstypes.Message) {}
func (s *spyAnalytics) TrackUser(_ context.Context, groupID, userID, event string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackCalls = append(s.trackCalls, analyticsCall{
		groupID: groupID,
		userID:  userID,
		event:   event,
		attrs:   attrs,
	})
}
func (s *spyAnalytics) IdentifyUser(_ context.Context, groupID, userID string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identifyCalls = append(s.identifyCalls, analyticsCall{
		groupID: groupID,
		userID:  userID,
		attrs:   attrs,
	})
}

func (s *spyAnalytics) snapshot() (identify []analyticsCall, track []analyticsCall) {
	s.mu.Lock()
	defer s.mu.Unlock()

	identify = append([]analyticsCall(nil), s.identifyCalls...)
	track = append([]analyticsCall(nil), s.trackCalls...)
	return identify, track
}

var _ analytics.Analytics = (*spyAnalytics)(nil)

func newAnalyticsTestContext(ctx context.Context, sessionID string) context.Context {
	return ctx
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, failureMessage string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal(failureMessage)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type logStringer interface {
	String() string
}

func newBufferedLogger(w io.Writer, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(logpkg.NewContextHandler(base))
}

func parseJSONLogLines(t *testing.T, buf logStringer) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func rawJSONLogLines(buf logStringer) []string {
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func logRecordByMessage(t *testing.T, buf logStringer, msg string) (map[string]any, string) {
	t.Helper()

	records := parseJSONLogLines(t, buf)
	lines := rawJSONLogLines(buf)
	if len(records) != len(lines) {
		t.Fatalf("parsed records = %d, raw lines = %d", len(records), len(lines))
	}
	for i, rec := range records {
		if rec["msg"] == msg {
			return rec, lines[i]
		}
	}
	t.Fatalf("log message %q not found in %v", msg, records)
	return nil, ""
}

func spanAttrValue(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func startTestMCPSpan(ctx context.Context, method string) (context.Context, trace.Span) {
	return otel.Tracer("signoz-mcp-server").Start(ctx, method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(otelpkg.MCPMethodKey.String(method)),
	)
}

func callReceiving(t *testing.T, server *MCPServer, ctx context.Context, method string, req mcp.Request, result mcp.Result, err error) (mcp.Result, error) {
	t.Helper()
	isRegisteredTool := func(name string) bool { return name == "probe" }
	return server.receivingMiddleware(isRegisteredTool)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return result, err
	})(ctx, method, req)
}

func toolRequest(name string, arguments string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(arguments)}}
}

func TestNormalizeSigNozURL_RejectsPathQueryFragment(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{
			name:    "URL with path",
			url:     "https://tenant.example.com/dashboard/123",
			wantErr: "without a path",
		},
		{
			name:    "URL with query parameters",
			url:     "https://tenant.example.com?orgId=1",
			wantErr: "without query parameters",
		},
		{
			name:    "URL with path and query",
			url:     "https://tenant.example.com/dashboard/123?orgId=1",
			wantErr: "without a path",
		},
		{
			name:    "URL with fragment",
			url:     "https://tenant.example.com#section",
			wantErr: "without a fragment",
		},
		{
			name: "trailing slash is allowed",
			url:  "https://tenant.example.com/",
		},
		{
			name: "bare origin is allowed",
			url:  "https://tenant.example.com",
		},
		{
			name: "origin with port is allowed",
			url:  "https://tenant.example.com:8080",
		},
		{
			name:    "ftp scheme rejected",
			url:     "ftp://tenant.example.com",
			wantErr: "not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := util.NormalizeSigNozURL(tt.url)
			if tt.wantErr == "" {
				// These cases may still fail due to DNS resolution of
				// the fake host, which is fine — we only care that the
				// path/query/fragment check itself does not fire.
				if err != nil {
					for _, keyword := range []string{"without a path", "without query", "without a fragment"} {
						if strings.Contains(err.Error(), keyword) {
							t.Errorf("unexpected rejection: %v", err)
						}
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNormalizeSigNozURL_CanonicalizesOrigin(t *testing.T) {
	// These tests use 1.1.1.1 (Cloudflare DNS) which resolves to a public IP,
	// so the full validation pipeline succeeds without DNS issues.
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "strips default https port",
			url:  "https://1.1.1.1:443",
			want: "https://1.1.1.1",
		},
		{
			name: "strips default http port",
			url:  "http://1.1.1.1:80",
			want: "http://1.1.1.1",
		},
		{
			name: "keeps non-default port",
			url:  "https://1.1.1.1:8080",
			want: "https://1.1.1.1:8080",
		},
		{
			name: "lowercases scheme",
			url:  "HTTPS://1.1.1.1",
			want: "https://1.1.1.1",
		},
		{
			name: "strips trailing slash",
			url:  "https://1.1.1.1/",
			want: "https://1.1.1.1",
		},
		{
			name: "bare origin unchanged",
			url:  "https://1.1.1.1",
			want: "https://1.1.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.NormalizeSigNozURL(tt.url)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("normalizeSigNozURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestAuthMiddlewareAcceptsOAuthBearerToken(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	token, err := oauth.EncryptToken(
		"oauth-api-key",
		"https://oauth.example.com",
		"client-1",
		time.Now().UTC().Add(time.Hour),
		[]byte(cfg.OAuthTokenSecret),
	)
	if err != nil {
		t.Fatalf("EncryptToken() error = %v", err)
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	// req.Header.Set("X-SigNoz-URL", "https://1.1.1.1")

	rr := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, _ := util.GetAPIKey(r.Context())
		signozURL, _ := util.GetSigNozURL(r.Context())
		w.Header().Set("X-API-Key", apiKey)
		w.Header().Set("X-SigNoz-URL", signozURL)
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Header().Get("X-API-Key") != "oauth-api-key" {
		t.Fatalf("api key = %q, want %q", rr.Header().Get("X-API-Key"), "oauth-api-key")
	}
	if rr.Header().Get("X-SigNoz-URL") != "https://oauth.example.com" {
		t.Fatalf("signoz URL = %q, want %q", rr.Header().Get("X-SigNoz-URL"), "https://oauth.example.com")
	}
}

// TestAuthMiddlewareHonorsAuthorizationIngress verifies an Authorization token
// is forwarded upstream as Authorization: Bearer regardless of shape — opaque
// and JWT-shaped tokens must behave identically.
func TestAuthMiddlewareHonorsAuthorizationIngress(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	// authHeader is the raw Authorization value; wantAPIKey is what must be
	// forwarded upstream. The "Bearer " scheme is stripped case-insensitively
	// and always re-added, so opaque, JWT-shaped, prefix-less, and lowercase
	// inputs all converge on a single canonical "Bearer <token>" form.
	cases := []struct {
		name       string
		authHeader string
		wantAPIKey string
	}{
		{name: "opaque token", authHeader: "Bearer opaque-session-token", wantAPIKey: "Bearer opaque-session-token"},
		{name: "jwt-shaped token", authHeader: "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", wantAPIKey: "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig"},
		{name: "opaque token starting with eyJ", authHeader: "Bearer eyJ-not-a-jwt", wantAPIKey: "Bearer eyJ-not-a-jwt"},
		{name: "no Bearer prefix", authHeader: "opaque-session-token", wantAPIKey: "Bearer opaque-session-token"},
		{name: "lowercase bearer scheme", authHeader: "bearer opaque-session-token", wantAPIKey: "Bearer opaque-session-token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", tc.authHeader)
			req.Header.Set("X-SigNoz-URL", "https://1.1.1.1")

			rr := httptest.NewRecorder()
			server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiKey, _ := util.GetAPIKey(r.Context())
				authHeader, _ := util.GetAuthHeader(r.Context())
				signozURL, _ := util.GetSigNozURL(r.Context())
				w.Header().Set("X-API-Key", apiKey)
				w.Header().Set("X-Auth-Header", authHeader)
				w.Header().Set("X-SigNoz-URL", signozURL)
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if got := rr.Header().Get("X-API-Key"); got != tc.wantAPIKey {
				t.Fatalf("api key = %q, want %q", got, tc.wantAPIKey)
			}
			if got := rr.Header().Get("X-Auth-Header"); got != "Authorization" {
				t.Fatalf("auth header = %q, want %q", got, "Authorization")
			}
			if got := rr.Header().Get("X-SigNoz-URL"); got != "https://1.1.1.1" {
				t.Fatalf("signoz URL = %q, want %q", got, "https://1.1.1.1")
			}
		})
	}
}

// TestAuthMiddlewareHonorsAuthorizationWithConfigURL pins the two non-customURL
// Authorization branches: a bearer token that is not a server-issued OAuth
// token is honored as Authorization when a SigNoz URL comes from config,
// whether OAuth is enabled (decrypt-fail path) or disabled.
func TestAuthMiddlewareHonorsAuthorizationWithConfigURL(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "oauth disabled",
			cfg:  config.Config{URL: "https://signoz.example.com"},
		},
		{
			name: "oauth enabled, non-oauth bearer token",
			cfg: config.Config{
				OAuthEnabled:     true,
				OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
				OAuthIssuerURL:   "https://mcp.example.com",
				URL:              "https://signoz.example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			server := &MCPServer{logger: logpkg.New("error"), config: &cfg, analytics: noopanalytics.New()}
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer opaque-session-token")
			// No X-SigNoz-URL: forces the config-URL branches.

			rr := httptest.NewRecorder()
			server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiKey, _ := util.GetAPIKey(r.Context())
				authHeader, _ := util.GetAuthHeader(r.Context())
				w.Header().Set("X-API-Key", apiKey)
				w.Header().Set("X-Auth-Header", authHeader)
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if got := rr.Header().Get("X-API-Key"); got != "Bearer opaque-session-token" {
				t.Fatalf("api key = %q, want %q", got, "Bearer opaque-session-token")
			}
			if got := rr.Header().Get("X-Auth-Header"); got != "Authorization" {
				t.Fatalf("auth header = %q, want %q", got, "Authorization")
			}
		})
	}
}

func TestAuthMiddlewareRejectsInstanceURLNotInAllowlist(t *testing.T) {
	cfg := &config.Config{
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("*.us.signoz.cloud"),
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("SIGNOZ-API-KEY", "pat-token")
	req.Header.Set("X-SigNoz-URL", "https://1.1.1.1")

	rr := httptest.NewRecorder()
	nextCalled := false
	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if nextCalled {
		t.Fatalf("next handler must not run for a disallowed SigNoz URL")
	}
}

func TestAuthMiddlewareAllowsInstanceURLInAllowlist(t *testing.T) {
	cfg := &config.Config{
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("*.us.signoz.cloud"),
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("SIGNOZ-API-KEY", "pat-token")
	req.Header.Set("X-SigNoz-URL", "https://demo.us.signoz.cloud")

	rr := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signozURL, _ := util.GetSigNozURL(r.Context())
		w.Header().Set("X-SigNoz-URL", signozURL)
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("X-SigNoz-URL"); got != "https://demo.us.signoz.cloud" {
		t.Fatalf("signoz URL = %q, want %q", got, "https://demo.us.signoz.cloud")
	}
}

func TestAuthMiddlewareRejectsInvalidOAuthBearerWithoutSigNozURL(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer stale-token")

	rr := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	wantHeader := `Bearer error="invalid_token", error_description="access token is invalid", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`
	if rr.Header().Get("WWW-Authenticate") != wantHeader {
		t.Fatalf("WWW-Authenticate = %q, want %q", rr.Header().Get("WWW-Authenticate"), wantHeader)
	}
}

func TestAuthMiddlewarePropagatesAssistantCorrelationHeaders(t *testing.T) {
	cfg := &config.Config{}
	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}

	cases := []struct {
		name              string
		clientSource      string
		threadID          string
		executionID       string
		wantClientSource  string
		wantThreadPresent bool
		wantExecPresent   bool
	}{
		{
			name:              "ai-assistant with full correlation",
			clientSource:      "ai-assistant",
			threadID:          "thread-abc",
			executionID:       "exec-xyz",
			wantClientSource:  "ai-assistant",
			wantThreadPresent: true,
			wantExecPresent:   true,
		},
		{
			name:              "missing client source defaults to user-client",
			clientSource:      "",
			threadID:          "",
			executionID:       "",
			wantClientSource:  util.ClientSourceUserClient,
			wantThreadPresent: false,
			wantExecPresent:   false,
		},
		{
			name:              "blank client source value defaults to user-client",
			clientSource:      "   ",
			threadID:          "",
			executionID:       "",
			wantClientSource:  util.ClientSourceUserClient,
			wantThreadPresent: false,
			wantExecPresent:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("SIGNOZ-API-KEY", "test-key")
			req.Header.Set("X-SigNoz-URL", "https://tenant.example.com")
			if tc.clientSource != "" {
				req.Header.Set("X-SigNoz-Client-Source", tc.clientSource)
			}
			if tc.threadID != "" {
				req.Header.Set("X-SigNoz-Assistant-Thread-Id", tc.threadID)
			}
			if tc.executionID != "" {
				req.Header.Set("X-SigNoz-Assistant-Execution-Id", tc.executionID)
			}

			var gotSource, gotThread, gotExec string
			var threadPresent, execPresent bool

			rr := httptest.NewRecorder()
			server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSource, _ = util.GetClientSource(r.Context())
				gotThread, threadPresent = util.GetAssistantThreadID(r.Context())
				gotExec, execPresent = util.GetAssistantExecutionID(r.Context())
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if gotSource != tc.wantClientSource {
				t.Fatalf("clientSource = %q, want %q", gotSource, tc.wantClientSource)
			}
			if threadPresent != tc.wantThreadPresent {
				t.Fatalf("thread present = %v, want %v", threadPresent, tc.wantThreadPresent)
			}
			if tc.wantThreadPresent && gotThread != tc.threadID {
				t.Fatalf("thread = %q, want %q", gotThread, tc.threadID)
			}
			if execPresent != tc.wantExecPresent {
				t.Fatalf("exec present = %v, want %v", execPresent, tc.wantExecPresent)
			}
			if tc.wantExecPresent && gotExec != tc.executionID {
				t.Fatalf("exec = %q, want %q", gotExec, tc.executionID)
			}
		})
	}
}

func TestAuthMiddlewareReturnsOAuthChallengeWhenMissingAuth(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:   true,
		OAuthIssuerURL: "https://mcp.example.com",
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()

	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	wantHeader := `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`
	if rr.Header().Get("WWW-Authenticate") != wantHeader {
		t.Fatalf("WWW-Authenticate = %q, want %q", rr.Header().Get("WWW-Authenticate"), wantHeader)
	}
}

func TestAuthMiddlewareLogsAndSpansAuthFailureTelemetry(t *testing.T) {
	var buf lockedBuffer
	logger := newBufferedLogger(&buf, slog.LevelDebug)
	cfg := &config.Config{
		OAuthEnabled:   true,
		OAuthIssuerURL: "https://mcp.example.com",
	}
	server := &MCPServer{logger: logger, config: cfg, analytics: noopanalytics.New()}

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "HTTP POST /mcp")

	req := httptest.NewRequest(http.MethodPost, "https://mcp.us.signoz.cloud/mcp", nil).WithContext(ctx)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.2")
	req.Header.Set("User-Agent", "claude-code/2.1.133 (cli)")
	req.Header.Set("Mcp-Session-Id", "mcp-session-test")
	rr := httptest.NewRecorder()

	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)
	span.End()

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	records := parseJSONLogLines(t, &buf)
	if len(records) == 0 {
		t.Fatal("expected auth failure log line")
	}
	rec := records[len(records)-1]
	if rec["msg"] != "No API key found in headers or environment" {
		t.Fatalf("msg = %v, want missing API key log", rec["msg"])
	}
	if rec["mcp.auth.failure_reason"] != authFailureMissingCredential {
		t.Fatalf("mcp.auth.failure_reason = %v, want %s", rec["mcp.auth.failure_reason"], authFailureMissingCredential)
	}
	if rec["mcp.auth.mode"] != authModeNone {
		t.Fatalf("mcp.auth.mode = %v, want %s", rec["mcp.auth.mode"], authModeNone)
	}
	if rec["http.response.status_code"] != float64(http.StatusUnauthorized) {
		t.Fatalf("http.response.status_code = %v, want %d", rec["http.response.status_code"], http.StatusUnauthorized)
	}
	if rec["http.request.method"] != http.MethodPost {
		t.Fatalf("http.request.method = %v, want POST", rec["http.request.method"])
	}
	if rec["url.path"] != "/mcp" {
		t.Fatalf("url.path = %v, want /mcp", rec["url.path"])
	}
	if rec["server.address"] != "mcp.us.signoz.cloud" {
		t.Fatalf("server.address = %v, want mcp.us.signoz.cloud", rec["server.address"])
	}
	if rec["client.address"] != "198.51.100.7" {
		t.Fatalf("client.address = %v, want 198.51.100.7", rec["client.address"])
	}
	if rec["user_agent.original"] != "claude-code/2.1.133 (cli)" {
		t.Fatalf("user_agent.original = %v, want claude-code user agent", rec["user_agent.original"])
	}
	if _, ok := rec["mcp.session.id"]; ok {
		t.Fatalf("mcp.session.id must not be logged in stateless mode: %#v", rec)
	}
	if rec["mcp.client_source"] != util.ClientSourceUserClient {
		t.Fatalf("mcp.client_source = %v, want %s", rec["mcp.client_source"], util.ClientSourceUserClient)
	}

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	attrs := spans[0].Attributes()
	for key, want := range map[attribute.Key]string{
		"mcp.auth.failure_reason": authFailureMissingCredential,
		"mcp.auth.mode":           authModeNone,
		"http.request.method":     http.MethodPost,
		"url.path":                "/mcp",
		"server.address":          "mcp.us.signoz.cloud",
		"client.address":          "198.51.100.7",
		"user_agent.original":     "claude-code/2.1.133 (cli)",
	} {
		got, ok := spanAttrValue(attrs, key)
		if !ok {
			t.Fatalf("span attr %s missing", key)
		}
		if got.AsString() != want {
			t.Fatalf("span attr %s = %q, want %q", key, got.AsString(), want)
		}
	}
	if _, ok := spanAttrValue(attrs, attribute.Key("mcp.session.id")); ok {
		t.Fatal("mcp.session.id must not be attached from an incoming header")
	}
	gotStatus, ok := spanAttrValue(attrs, "http.response.status_code")
	if !ok {
		t.Fatal("span attr http.response.status_code missing")
	}
	if gotStatus.AsInt64() != int64(http.StatusUnauthorized) {
		t.Fatalf("span attr http.response.status_code = %d, want %d", gotStatus.AsInt64(), http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareAuthFailureTelemetryBranches(t *testing.T) {
	const tokenSecret = "0123456789abcdef0123456789abcdef"
	expiredToken, err := oauth.EncryptToken(
		"oauth-api-key",
		"https://oauth.example.com",
		"client-1",
		time.Now().UTC().Add(-time.Hour),
		[]byte(tokenSecret),
	)
	if err != nil {
		t.Fatalf("EncryptToken() error = %v", err)
	}

	tests := []struct {
		name       string
		cfg        config.Config
		setup      func(*http.Request)
		wantStatus int
		wantReason string
		wantMode   string
	}{
		{
			name: "invalid OAuth bearer without fallback URL",
			cfg: config.Config{
				OAuthEnabled:     true,
				OAuthTokenSecret: tokenSecret,
				OAuthIssuerURL:   "https://mcp.example.com",
			},
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer stale-token")
			},
			wantStatus: http.StatusUnauthorized,
			wantReason: authFailureInvalidOAuthToken,
			wantMode:   authModeAuthorizationBearer,
		},
		{
			name: "expired OAuth bearer",
			cfg: config.Config{
				OAuthEnabled:     true,
				OAuthTokenSecret: tokenSecret,
				OAuthIssuerURL:   "https://mcp.example.com",
			},
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+expiredToken)
			},
			wantStatus: http.StatusUnauthorized,
			wantReason: authFailureExpiredOAuthToken,
			wantMode:   authModeOAuthAccessToken,
		},
		{
			name: "invalid SigNoz URL",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer raw-api-key")
				req.Header.Set("X-SigNoz-URL", "https://tenant.example.com/path")
			},
			wantStatus: http.StatusBadRequest,
			wantReason: authFailureInvalidSignozURL,
			wantMode:   authModeAuthorizationBearer,
		},
		{
			name: "missing SigNoz URL",
			setup: func(req *http.Request) {
				req.Header.Set("SIGNOZ-API-KEY", "api-key")
			},
			wantStatus: http.StatusBadRequest,
			wantReason: authFailureMissingSignozURL,
			wantMode:   authModeSignozAPIKeyHeader,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf lockedBuffer
			cfg := tt.cfg
			server := &MCPServer{logger: newBufferedLogger(&buf, slog.LevelDebug), config: &cfg, analytics: noopanalytics.New()}

			spanRecorder := tracetest.NewSpanRecorder()
			tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
			ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "HTTP POST /mcp")

			req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil).WithContext(ctx)
			tt.setup(req)
			rr := httptest.NewRecorder()

			server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("next handler should not be called")
			})).ServeHTTP(rr, req)
			span.End()

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			records := parseJSONLogLines(t, &buf)
			if len(records) == 0 {
				t.Fatal("expected auth failure log line")
			}
			rec := records[len(records)-1]
			if rec["mcp.auth.failure_reason"] != tt.wantReason {
				t.Fatalf("log mcp.auth.failure_reason = %v, want %s", rec["mcp.auth.failure_reason"], tt.wantReason)
			}
			if rec["mcp.auth.mode"] != tt.wantMode {
				t.Fatalf("log mcp.auth.mode = %v, want %s", rec["mcp.auth.mode"], tt.wantMode)
			}

			spans := spanRecorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d, want 1", len(spans))
			}
			attrs := spans[0].Attributes()
			gotReason, ok := spanAttrValue(attrs, "mcp.auth.failure_reason")
			if !ok {
				t.Fatal("span attr mcp.auth.failure_reason missing")
			}
			if gotReason.AsString() != tt.wantReason {
				t.Fatalf("span mcp.auth.failure_reason = %q, want %q", gotReason.AsString(), tt.wantReason)
			}
			gotMode, ok := spanAttrValue(attrs, "mcp.auth.mode")
			if !ok {
				t.Fatal("span attr mcp.auth.mode missing")
			}
			if gotMode.AsString() != tt.wantMode {
				t.Fatalf("span mcp.auth.mode = %q, want %q", gotMode.AsString(), tt.wantMode)
			}
		})
	}
}

func TestAuthMiddlewareExpiredOAuthLogsDebugAndRecordsAuthFailureMetric(t *testing.T) {
	const tokenSecret = "0123456789abcdef0123456789abcdef"
	expiredToken, err := oauth.EncryptToken(
		"oauth-api-key",
		"https://oauth.example.com",
		"client-1",
		time.Now().UTC().Add(-time.Hour),
		[]byte(tokenSecret),
	)
	if err != nil {
		t.Fatalf("EncryptToken() error = %v", err)
	}

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown meter provider: %v", err)
		}
	}()
	meters, err := otelpkg.NewMeters(meterProvider)
	if err != nil {
		t.Fatalf("new meters: %v", err)
	}

	var buf lockedBuffer
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: tokenSecret,
		OAuthIssuerURL:   "https://mcp.example.com",
	}
	server := &MCPServer{logger: newBufferedLogger(&buf, slog.LevelDebug), config: cfg, analytics: noopanalytics.New(), meters: meters}

	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	rr := httptest.NewRecorder()

	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	rec, _ := logRecordByMessage(t, &buf, "OAuth access token expired")
	if rec["level"] != "DEBUG" {
		t.Fatalf("level = %v, want DEBUG", rec["level"])
	}
	if rec["mcp.auth.failure_reason"] != authFailureExpiredOAuthToken {
		t.Fatalf("mcp.auth.failure_reason = %v, want %s", rec["mcp.auth.failure_reason"], authFailureExpiredOAuthToken)
	}
	if rec["mcp.auth.mode"] != authModeOAuthAccessToken {
		t.Fatalf("mcp.auth.mode = %v, want %s", rec["mcp.auth.mode"], authModeOAuthAccessToken)
	}
	if rec["mcp.tenant_url"] != "https://oauth.example.com" {
		t.Fatalf("mcp.tenant_url = %v, want OAuth token tenant", rec["mcp.tenant_url"])
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	authFailures, found := oteltest.FindInt64SumMetric(metrics, "mcp.auth.failures")
	if !found {
		t.Fatal("mcp.auth.failures metric not found")
	}
	if len(authFailures.DataPoints) != 1 {
		t.Fatalf("mcp.auth.failures datapoints = %d, want 1", len(authFailures.DataPoints))
	}
	dp := authFailures.DataPoints[0]
	if dp.Value != 1 {
		t.Fatalf("mcp.auth.failures value = %d, want 1", dp.Value)
	}
	if attr, ok := dp.Attributes.Value(attribute.Key("mcp.auth.failure_reason")); !ok || attr.AsString() != authFailureExpiredOAuthToken {
		t.Fatalf("metric mcp.auth.failure_reason = %v, want %s", attr, authFailureExpiredOAuthToken)
	}
	if attr, ok := dp.Attributes.Value(attribute.Key("mcp.auth.mode")); !ok || attr.AsString() != authModeOAuthAccessToken {
		t.Fatalf("metric mcp.auth.mode = %v, want %s", attr, authModeOAuthAccessToken)
	}
	if attr, ok := dp.Attributes.Value(otelpkg.MCPTenantURLKey); !ok || attr.AsString() != "https://oauth.example.com" {
		t.Fatalf("metric mcp.tenant_url = %v, want OAuth token tenant", attr)
	}
	if attr, ok := dp.Attributes.Value(otelpkg.MCPClientSourceKey); !ok || attr.AsString() != util.ClientSourceUserClient {
		t.Fatalf("metric mcp.client_source = %v, want %s", attr, util.ClientSourceUserClient)
	}
}

func TestAuthMiddlewareMissingCredentialsLogsDebugAndRecordsAuthFailureMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown meter provider: %v", err)
		}
	}()
	meters, err := otelpkg.NewMeters(meterProvider)
	if err != nil {
		t.Fatalf("new meters: %v", err)
	}

	var buf lockedBuffer
	cfg := &config.Config{
		OAuthEnabled:   true,
		OAuthIssuerURL: "https://mcp.example.com",
	}
	server := &MCPServer{logger: newBufferedLogger(&buf, slog.LevelDebug), config: cfg, analytics: noopanalytics.New(), meters: meters}

	req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
	req.Header.Set("X-SigNoz-Client-Source", "attacker-controlled-source")
	rr := httptest.NewRecorder()

	server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	rec, _ := logRecordByMessage(t, &buf, "No API key found in headers or environment")
	if rec["level"] != "DEBUG" {
		t.Fatalf("level = %v, want DEBUG", rec["level"])
	}
	if rec["mcp.auth.failure_reason"] != authFailureMissingCredential {
		t.Fatalf("mcp.auth.failure_reason = %v, want %s", rec["mcp.auth.failure_reason"], authFailureMissingCredential)
	}
	if rec["mcp.auth.mode"] != authModeNone {
		t.Fatalf("mcp.auth.mode = %v, want %s", rec["mcp.auth.mode"], authModeNone)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	authFailures, found := oteltest.FindInt64SumMetric(metrics, "mcp.auth.failures")
	if !found {
		t.Fatal("mcp.auth.failures metric not found")
	}
	if len(authFailures.DataPoints) != 1 {
		t.Fatalf("mcp.auth.failures datapoints = %d, want 1", len(authFailures.DataPoints))
	}
	dp := authFailures.DataPoints[0]
	if dp.Value != 1 {
		t.Fatalf("mcp.auth.failures value = %d, want 1", dp.Value)
	}
	if attr, ok := dp.Attributes.Value(attribute.Key("mcp.auth.failure_reason")); !ok || attr.AsString() != authFailureMissingCredential {
		t.Fatalf("metric mcp.auth.failure_reason = %v, want %s", attr, authFailureMissingCredential)
	}
	if attr, ok := dp.Attributes.Value(attribute.Key("mcp.auth.mode")); !ok || attr.AsString() != authModeNone {
		t.Fatalf("metric mcp.auth.mode = %v, want %s", attr, authModeNone)
	}
	if attr, ok := dp.Attributes.Value(otelpkg.MCPClientSourceKey); !ok || attr.AsString() != util.ClientSourceOther {
		t.Fatalf("metric mcp.client_source = %v, want %s", attr, util.ClientSourceOther)
	}
}

func TestRun_HTTPCanceledBeforeListen(t *testing.T) {
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0", // OS picks a free port if the listener ever binds
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	logger := logpkg.New("error")
	handler := tools.NewHandler(logger, cfg)
	srv := NewMCPServer(logger, handler, cfg, noopanalytics.New(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Run should short-circuit before ListenAndServe

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s on pre-canceled context")
	}
}

// TestToolCallEventHasErrorType verifies error categorization lands on the
// analytics event (analytics scope). resultBytes is not an analytics field
// — see TestGuardrail_ToolCallSpanHasSerializedResultBytes for span coverage.

func TestReceivingMiddlewareToolOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		result        *mcp.CallToolResult
		err           error
		panicValue    any
		wantErrorType string
		wantErrorCode string
		wantLog       string
	}{
		{name: "success", result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, wantLog: "tool call finished"},
		{name: "coded error", result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "denied"}}, StructuredContent: map[string]any{"code": tools.CodePermissionDenied}}, wantErrorType: "tool_error", wantErrorCode: tools.CodePermissionDenied, wantLog: "tool call returned error result"},
		{name: "go error", err: errors.New("boom"), wantErrorType: "internal", wantLog: "tool call failed"},
		{name: "cancelled", err: context.Canceled, wantErrorType: "cancelled", wantLog: "tool call failed"},
		{name: "panic", panicValue: "secret-panic-canary", wantErrorType: "internal", wantLog: "tool call failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traceExporter := tracetest.NewInMemoryExporter()
			traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
			previousTracerProvider := otel.GetTracerProvider()
			otel.SetTracerProvider(traceProvider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previousTracerProvider)
				_ = traceProvider.Shutdown(context.Background())
			})

			reader := sdkmetric.NewManualReader()
			meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
			meters, err := otelpkg.NewMeters(meterProvider)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters)
			request := toolRequest("probe", `{"searchContext":"find services"}`)
			next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
				if tt.panicValue != nil {
					panic(tt.panicValue)
				}
				return tt.result, tt.err
			}
			result, gotErr := server.receivingMiddleware(func(name string) bool { return name == "probe" })(next)(context.Background(), "tools/call", request)
			if tt.panicValue != nil {
				var rpcErr *jsonrpc.Error
				if !errors.As(gotErr, &rpcErr) || rpcErr.Code != jsonrpc.CodeInternalError || rpcErr.Message != "Internal error" {
					t.Fatalf("panic error = %#v, want generic -32603", gotErr)
				}
				if strings.Contains(logs.String(), "secret-panic-canary") {
					t.Fatal("panic value leaked into logs")
				}
			} else if !errors.Is(gotErr, tt.err) {
				t.Fatalf("error = %v, want %v", gotErr, tt.err)
			}
			if tt.result != nil && result != tt.result {
				t.Fatal("middleware changed the handler result")
			}

			var metrics metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &metrics); err != nil {
				t.Fatal(err)
			}
			toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
			if !found || len(toolCalls.DataPoints) != 1 || toolCalls.DataPoints[0].Value != 1 {
				t.Fatalf("mcp.tool.calls = %#v, found=%t; want exactly one", toolCalls.DataPoints, found)
			}
			attrs := toolCalls.DataPoints[0].Attributes
			if got, _ := attrs.Value(otelpkg.GenAIToolNameKey); got.AsString() != "probe" {
				t.Fatalf("tool name = %v, want probe", got)
			}
			if tt.wantErrorType == "" {
				if _, ok := attrs.Value(attribute.Key("error.type")); ok {
					t.Fatal("successful call has error.type")
				}
			} else if got, _ := attrs.Value(attribute.Key("error.type")); got.AsString() != tt.wantErrorType {
				t.Fatalf("error.type = %v, want %q", got, tt.wantErrorType)
			}
			if tt.wantErrorCode != "" {
				if got, _ := attrs.Value(otelpkg.MCPToolErrorCodeKey); got.AsString() != tt.wantErrorCode {
					t.Fatalf("error code = %v, want %q", got, tt.wantErrorCode)
				}
			}
			wantLog := tt.wantLog
			if tt.panicValue != nil {
				wantLog = "mcp handler panic recovered"
			}
			if _, _ = logRecordByMessage(t, &logs, wantLog); false {
				t.Fatal("unreachable")
			}
			spans := traceExporter.GetSpans()
			if len(spans) != 1 || spans[0].Name != "tools/call probe" {
				t.Fatalf("spans = %#v, want one tools/call probe span", spans)
			}
		})
	}
}

func TestReceivingMiddlewareUnknownToolIsBoundedAndCountedOnce(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	previousTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracerProvider)
		_ = traceProvider.Shutdown(context.Background())
	})

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	logger := logpkg.New("error")
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters)
	requestedName := "attacker-" + strings.Repeat("x", 16*1024)
	// Deliberately avoid the official SDK's current display wording: bounded
	// telemetry classification must come from checked registration state.
	rpcErr := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tool lookup failed"}
	ctx := util.SetClientSource(context.Background(), "ai-assistant")
	_, gotErr := callReceiving(t, server, ctx, "tools/call", toolRequest(requestedName, `{"searchContext":"preserved"}`), nil, rpcErr)
	if !errors.Is(gotErr, rpcErr) {
		t.Fatalf("error = %v, want %v", gotErr, rpcErr)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
	if !found || len(toolCalls.DataPoints) != 1 || toolCalls.DataPoints[0].Value != 1 {
		t.Fatalf("mcp.tool.calls = %#v, found=%t; want exactly one", toolCalls.DataPoints, found)
	}
	toolDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.tool.call.duration")
	if !found || len(toolDuration.DataPoints) != 1 || toolDuration.DataPoints[0].Count != 1 {
		t.Fatalf("mcp.tool.call.duration = %#v, found=%t; want exactly one", toolDuration.DataPoints, found)
	}
	for key, want := range map[attribute.Key]string{
		otelpkg.GenAIToolNameKey:   unknownToolName,
		otelpkg.MCPClientSourceKey: "ai-assistant",
	} {
		got, ok := toolCalls.DataPoints[0].Attributes.Value(key)
		if !ok || got.AsString() != want {
			t.Fatalf("metric %s = %v, want %q", key, got, want)
		}
		got, ok = toolDuration.DataPoints[0].Attributes.Value(key)
		if !ok || got.AsString() != want {
			t.Fatalf("duration metric %s = %v, want %q", key, got, want)
		}
	}
	if strings.Contains(toolCalls.DataPoints[0].Attributes.Encoded(attribute.DefaultEncoder()), requestedName) {
		t.Fatalf("metric attributes contain attacker-controlled tool name")
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "tools/call "+unknownToolName || strings.Contains(spans[0].Name, requestedName) {
		t.Fatalf("span name = %q, want bounded unknown-tool name", spans[0].Name)
	}
	for key, want := range map[attribute.Key]string{
		otelpkg.MCPMethodKey:          "tools/call",
		otelpkg.GenAIToolNameKey:      unknownToolName,
		otelpkg.GenAIOperationNameKey: "execute_tool",
	} {
		got, ok := spanAttrValue(spans[0].Attributes, key)
		if !ok || got.AsString() != want {
			t.Fatalf("span %s = %v, want %q", key, got, want)
		}
	}
}

func TestReceivingMiddlewareUnknownMethodUsesBoundedDimensionsOnce(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	previousTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracerProvider)
		_ = traceProvider.Shutdown(context.Background())
	})

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	logger := logpkg.New("error")
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters)
	requestedMethod := "attacker/" + strings.Repeat("method", 2048)
	rpcErr := &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}
	ctx := util.SetClientSource(context.Background(), "ai-assistant")
	_, gotErr := callReceiving(t, server, ctx, requestedMethod, nil, nil, rpcErr)
	if !errors.Is(gotErr, rpcErr) {
		t.Fatalf("error = %v, want %v", gotErr, rpcErr)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found || len(methodCalls.DataPoints) != 1 || methodCalls.DataPoints[0].Value != 1 {
		t.Fatalf("mcp.method.calls = %#v, found=%t; want exactly one", methodCalls.DataPoints, found)
	}
	methodDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.method.duration")
	if !found || len(methodDuration.DataPoints) != 1 || methodDuration.DataPoints[0].Count != 1 {
		t.Fatalf("mcp.method.duration = %#v, found=%t; want exactly one", methodDuration.DataPoints, found)
	}
	for key, want := range map[attribute.Key]string{
		otelpkg.MCPMethodKey:       otelpkg.UnknownMCPMethod,
		otelpkg.MCPClientSourceKey: "ai-assistant",
	} {
		got, ok := methodCalls.DataPoints[0].Attributes.Value(key)
		if !ok || got.AsString() != want {
			t.Fatalf("metric %s = %v, want %q", key, got, want)
		}
		got, ok = methodDuration.DataPoints[0].Attributes.Value(key)
		if !ok || got.AsString() != want {
			t.Fatalf("duration metric %s = %v, want %q", key, got, want)
		}
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != otelpkg.UnknownMCPMethod {
		t.Fatalf("spans = %#v, want one %q span", spans, otelpkg.UnknownMCPMethod)
	}
	method, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPMethodKey)
	if !ok || method.AsString() != otelpkg.UnknownMCPMethod || strings.Contains(spans[0].Name, requestedMethod) {
		t.Fatalf("span method = %v, want %q", method, otelpkg.UnknownMCPMethod)
	}
}

func TestToolErrorType(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		result *mcp.CallToolResult
		want   string
	}{
		{name: "no error", want: ""},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: "timeout"},
		{name: "cancelled", err: context.Canceled, want: "cancelled"},
		{name: "generic go error", err: errors.New("boom"), want: "internal"},
		{
			name: "structured permission denied",
			result: &mcp.CallToolResult{IsError: true,
				Content:           []mcp.Content{&mcp.TextContent{Text: "arbitrary display text"}},
				StructuredContent: map[string]any{"code": tools.CodePermissionDenied}},
			want: "permission_denied",
		},
		{
			name: "structured rate limited",
			result: &mcp.CallToolResult{IsError: true,
				Content:           []mcp.Content{&mcp.TextContent{Text: "not a rate-limit phrase"}},
				StructuredContent: map[string]any{"code": tools.CodeRateLimited}},
			want: "rate_limited",
		},
		{
			name: "display text is not classified",
			result: &mcp.CallToolResult{IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "unexpected status 503 upstream"}}},
			want: "tool_error",
		},
		{
			name:   "result error generic",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "missing field"}}},
			want:   "tool_error",
		},
		{
			name:   "non-error result",
			result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolErrorType(tt.err, tt.result)
			if got != tt.want {
				t.Errorf("toolErrorType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMethodErrorTypeCancellationAndDeadline(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{name: "cancelled", err: context.Canceled, want: "cancelled"},
		{name: "wrapped cancelled", err: fmt.Errorf("request stopped: %w", context.Canceled), want: "cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "timeout"},
		{name: "wrapped deadline", err: fmt.Errorf("request stopped: %w", context.DeadlineExceeded), want: "timeout"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := methodErrorType(tt.err); got != tt.want {
				t.Fatalf("methodErrorType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRun_HTTPShutdownRaceDuringStartup verifies the production shutdown
// flow: main cancels the run ctx (signal.NotifyContext) and then calls
// Shutdown on the MCPServer. The atomic.Pointer handoff must ensure
// ListenAndServe either returns http.ErrServerClosed promptly or is
// never called at all, so Run exits well within the shutdown budget.
func TestRun_HTTPShutdownRaceDuringStartup(t *testing.T) {
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	logger := logpkg.New("error")
	handler := tools.NewHandler(logger, cfg)
	srv := NewMCPServer(logger, handler, cfg, noopanalytics.New(), nil)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- srv.Run(runCtx)
	}()

	waitForCondition(t, 5*time.Second, func() bool {
		return srv.httpServer.Load() != nil
	}, "timed out waiting for HTTP server startup publication")

	cancel()
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error after Shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit within 5s of Shutdown")
	}
}

// TestMaxBytesMiddlewareRejectsOversizeBody verifies the inbound request-body
// cap: an over-cap body declared via Content-Length is rejected early with 413
// (inner not reached); an over-cap body of unknown length is bounded by
// MaxBytesReader so the downstream read fails; an under-cap body is readable in
// full.
func TestMaxBytesMiddlewareRejectsOversizeBody(t *testing.T) {
	server := &MCPServer{logger: logpkg.New("error"), config: &config.Config{MaxRequestBytes: 16}, analytics: noopanalytics.New()}

	var innerCalled bool
	var readErr error
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	t.Run("declared length over cap -> 413", func(t *testing.T) {
		innerCalled, readErr = false, nil
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 100)))
		rr := httptest.NewRecorder()
		server.maxBytesMiddleware(inner).ServeHTTP(rr, req)
		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
		}
		if innerCalled {
			t.Fatal("inner handler must not be called for a declared over-cap body")
		}
	})

	t.Run("unknown length over cap -> read error", func(t *testing.T) {
		innerCalled, readErr = false, nil
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 100)))
		req.ContentLength = -1 // simulate chunked / unknown length
		server.maxBytesMiddleware(inner).ServeHTTP(httptest.NewRecorder(), req)
		if !innerCalled {
			t.Fatal("inner handler should run for an unknown-length body")
		}
		if readErr == nil {
			t.Fatal("expected read error for over-cap streamed body, got nil")
		}
	})

	t.Run("under cap -> ok", func(t *testing.T) {
		innerCalled, readErr = false, nil
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hello"))
		server.maxBytesMiddleware(inner).ServeHTTP(httptest.NewRecorder(), req)
		if !innerCalled || readErr != nil {
			t.Fatalf("under-cap body: innerCalled=%v readErr=%v, want true/nil", innerCalled, readErr)
		}
	})
}

// TestMaxBytesMiddlewareDisabledWhenZero verifies a zero/unset cap is a no-op
// (so tests / configs that don't set MaxRequestBytes are not silently capped).
func TestMaxBytesMiddlewareDisabledWhenZero(t *testing.T) {
	server := &MCPServer{logger: logpkg.New("error"), config: &config.Config{MaxRequestBytes: 0}, analytics: noopanalytics.New()}

	var readErr error
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 1000)))
	server.maxBytesMiddleware(inner).ServeHTTP(httptest.NewRecorder(), req)
	if readErr != nil {
		t.Fatalf("zero cap should not limit body, got err: %v", readErr)
	}
}
