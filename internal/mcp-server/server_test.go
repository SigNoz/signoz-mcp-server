package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestAuthMiddlewareOAuthBearerIgnoresCustomURL(t *testing.T) {
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

	for _, customURL := range []string{"", "https://oauth.example.com", "https://other.example.com", "://malformed"} {
		t.Run(customURL, func(t *testing.T) {
			server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if customURL != "" {
				req.Header.Set("X-SigNoz-URL", customURL)
			}

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
			if got := rr.Header().Get("X-API-Key"); got != "oauth-api-key" {
				t.Fatalf("api key = %q, want %q", got, "oauth-api-key")
			}
			if got := rr.Header().Get("X-Auth-Header"); got != "SIGNOZ-API-KEY" {
				t.Fatalf("auth header = %q, want %q", got, "SIGNOZ-API-KEY")
			}
			if got := rr.Header().Get("X-SigNoZ-URL"); got != "https://oauth.example.com" {
				t.Fatalf("signoz URL = %q, want %q", got, "https://oauth.example.com")
			}
		})
	}
}

func TestAuthMiddlewareExpiredOAuthBearerDoesNotDowngradeWithCustomURL(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}
	token, err := oauth.EncryptToken(
		"oauth-api-key",
		"https://oauth.example.com",
		"client-1",
		time.Now().UTC().Add(-time.Hour),
		[]byte(cfg.OAuthTokenSecret),
	)
	if err != nil {
		t.Fatalf("EncryptToken() error = %v", err)
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-SigNoz-URL", "https://other.example.com")

	rr := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	wantHeader := `Bearer error="invalid_token", error_description="access token expired", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`
	if got := rr.Header().Get("WWW-Authenticate"); got != wantHeader {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, wantHeader)
	}
}

func TestAuthMiddlewareOAuthBearerEnforcesEmbeddedURLAllowlist(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:         true,
		OAuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:       "https://mcp.example.com",
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("*.us.signoz.cloud"),
	}
	token, err := oauth.EncryptToken(
		"oauth-api-key",
		"https://blocked.example.com",
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
	// A caller-supplied allowed tenant cannot override the tenant encrypted in
	// the server-issued token.
	req.Header.Set("X-SigNoz-URL", "https://allowed.us.signoz.cloud")

	rr := httptest.NewRecorder()
	server.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
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
		{
			name:              "unrecognized client source collapses to other",
			clientSource:      "attacker-controlled-source",
			threadID:          "",
			executionID:       "",
			wantClientSource:  util.ClientSourceOther,
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

func TestAuthMiddlewareRejectsInvalidOrMissingSigNozURL(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*http.Request)
		wantStatus int
	}{
		{
			name: "invalid SigNoz URL",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer raw-api-key")
				req.Header.Set("X-SigNoz-URL", "https://tenant.example.com/path")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing SigNoz URL",
			setup: func(req *http.Request) {
				req.Header.Set("SIGNOZ-API-KEY", "api-key")
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &MCPServer{logger: logpkg.New("error"), config: &config.Config{}, analytics: noopanalytics.New()}
			req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
			tt.setup(req)
			rr := httptest.NewRecorder()

			server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("next handler should not be called")
			})).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
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

func TestReceivingMiddlewarePassesToolOutcomesThroughAndRecoversPanics(t *testing.T) {
	tests := []struct {
		name       string
		result     *mcp.CallToolResult
		err        error
		panicValue any
	}{
		{name: "success", result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}},
		{name: "coded error", result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "denied"}}, StructuredContent: map[string]any{"code": tools.CodePermissionDenied}}},
		{name: "go error", err: errors.New("boom")},
		{name: "cancelled", err: context.Canceled},
		{name: "panic", panicValue: "secret-panic-canary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := newBufferedLogger(&logs, slog.LevelDebug)
			cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
			server := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), nil)
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
		})
	}
}

func TestReceivingMiddlewareKeepsUnknownToolNamesOutOfMetricsAndSpans(t *testing.T) {
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
	_, gotErr := callReceiving(t, server, context.Background(), "tools/call", toolRequest(requestedName, `{"searchContext":"preserved"}`), nil, rpcErr)
	if !errors.Is(gotErr, rpcErr) {
		t.Fatalf("error = %v, want %v", gotErr, rpcErr)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
	if !found || len(toolCalls.DataPoints) != 1 {
		t.Fatalf("mcp.tool.calls = %#v, found=%t; want one datapoint", toolCalls.DataPoints, found)
	}
	if strings.Contains(toolCalls.DataPoints[0].Attributes.Encoded(attribute.DefaultEncoder()), requestedName) {
		t.Fatalf("metric attributes contain attacker-controlled tool name")
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if strings.Contains(spans[0].Name, requestedName) {
		t.Fatalf("span name = %q, want bounded unknown-tool name", spans[0].Name)
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
