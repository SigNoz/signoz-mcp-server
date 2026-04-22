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
	"sync/atomic"
	"testing"
	"time"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

type fakeSession struct {
	id string
	ch chan mcp.JSONRPCNotification
}

func (f fakeSession) Initialize()                                         {}
func (f fakeSession) Initialized() bool                                   { return true }
func (f fakeSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return f.ch }
func (f fakeSession) SessionID() string                                   { return f.id }

func newAnalyticsTestContext(ctx context.Context, sessionID string) context.Context {
	base := mcpgoserver.NewMCPServer("test", "1.0.0")
	return base.WithContext(ctx, fakeSession{id: sessionID, ch: make(chan mcp.JSONRPCNotification, 1)})
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

func spanAttrValue(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func singleHook[T any](t *testing.T, hooks []T, name string) T {
	t.Helper()
	if len(hooks) != 1 {
		t.Fatalf("%s hooks = %d, want 1", name, len(hooks))
	}
	return hooks[0]
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

func TestAuthMiddlewareFallsBackToRawAPIKey(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer raw-api-key")
	req.Header.Set("X-SigNoz-URL", "https://1.1.1.1")

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
	if rr.Header().Get("X-API-Key") != "raw-api-key" {
		t.Fatalf("api key = %q, want %q", rr.Header().Get("X-API-Key"), "raw-api-key")
	}
	if rr.Header().Get("X-SigNoz-URL") != "https://1.1.1.1" {
		t.Fatalf("signoz URL = %q, want %q", rr.Header().Get("X-SigNoz-URL"), "https://1.1.1.1")
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

func TestBuildHooks_APIKeyAnalyticsUseServiceAccountIdentity(t *testing.T) {
	var requests atomic.Int32
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v1/service_accounts/me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v1/service_accounts/me")
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "test-api-key" {
			t.Fatalf("SIGNOZ-API-KEY = %q, want %q", r.Header.Get("SIGNOZ-API-KEY"), "test-api-key")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-123","name":"ingest-bot","email":"service@example.com","orgId":"org-456"}}`))
	}))
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		APIKey:          "test-api-key",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-api-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)

	session := fakeSession{id: "sess-api", ch: make(chan mcp.JSONRPCNotification, 1)}
	hooks.RegisterSession(ctx, session)
	hooks.UnregisterSession(ctx, session)

	waitForCondition(t, time.Second, func() bool {
		identifyCalls, trackCalls := spy.snapshot()
		// Identity caching is exercised directly in the client package; here
		// we only assert the hooks fire the right analytics events.
		return requests.Load() >= 1 && len(identifyCalls) == 1 && len(trackCalls) == 1
	}, "timed out waiting for async API-key analytics")

	identifyCalls, trackCalls := spy.snapshot()

	identify := identifyCalls[0]
	if identify.groupID != "org-456" || identify.userID != "sa-123" {
		t.Fatalf("identify user args = (%q, %q), want (%q, %q)", identify.groupID, identify.userID, "org-456", "sa-123")
	}
	if identify.attrs[analytics.AttrOrgID] != "org-456" || identify.attrs[analytics.AttrPrincipal] != "service_account" || identify.attrs[analytics.AttrEmail] != "service@example.com" {
		t.Fatalf("identify attrs = %#v, want orgId, principal, and email", identify.attrs)
	}
	if identify.attrs[analytics.AttrName] != "ingest-bot" {
		t.Fatalf("identify name = %v, want ingest-bot", identify.attrs[analytics.AttrName])
	}

	track := trackCalls[0]
	if track.groupID != "org-456" || track.userID != "sa-123" || track.event != analytics.EventSessionUnregistered {
		t.Fatalf("track call = (%q, %q, %q), want (%q, %q, %q)", track.groupID, track.userID, track.event, "org-456", "sa-123", analytics.EventSessionUnregistered)
	}
	if track.attrs[analytics.AttrSessionID] != "sess-api" {
		t.Fatalf("session id attr = %v, want %q", track.attrs[analytics.AttrSessionID], "sess-api")
	}
	if track.attrs[analytics.AttrOrgID] != "org-456" || track.attrs[analytics.AttrPrincipal] != "service_account" || track.attrs[analytics.AttrEmail] != "service@example.com" {
		t.Fatalf("track attrs = %#v, want orgId, principal, and email", track.attrs)
	}
	if track.attrs[analytics.AttrName] != "ingest-bot" {
		t.Fatalf("track name = %v, want ingest-bot", track.attrs[analytics.AttrName])
	}
}

func TestUserScopedAnalyticsUseJWTIdentity(t *testing.T) {
	var requests atomic.Int32
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v2/users/me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v2/users/me")
		}
		if r.Header.Get("Authorization") != "Bearer jwt-token" {
			t.Fatalf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer jwt-token")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"user-123","displayName":"Ada Lovelace","email":"user@example.com","orgId":"org-123"}}`))
	}))
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "Bearer jwt-token")
	ctx = util.SetAuthHeader(ctx, "Authorization")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-jwt")

	singleHook(t, hooks.OnAfterInitialize, "OnAfterInitialize")(ctx, nil, &mcp.InitializeRequest{}, &mcp.InitializeResult{})

	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "signoz_list_services",
			Arguments: map[string]any{
				"searchContext": "list services",
			},
		},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		identifyCalls, trackCalls := spy.snapshot()
		return requests.Load() >= 1 && len(identifyCalls) == 1 && len(trackCalls) == 2
	}, "timed out waiting for async JWT analytics")

	identifyCalls, trackCalls := spy.snapshot()

	identify := identifyCalls[0]
	if identify.groupID != "org-123" || identify.userID != "user-123" {
		t.Fatalf("identify user args = (%q, %q), want (%q, %q)", identify.groupID, identify.userID, "org-123", "user-123")
	}
	if identify.attrs[analytics.AttrOrgID] != "org-123" || identify.attrs[analytics.AttrPrincipal] != "user" || identify.attrs[analytics.AttrEmail] != "user@example.com" {
		t.Fatalf("identify attrs = %#v, want orgId, principal, and email", identify.attrs)
	}
	if identify.attrs[analytics.AttrName] != "Ada Lovelace" {
		t.Fatalf("identify name = %v, want Ada Lovelace", identify.attrs[analytics.AttrName])
	}

	var registered analyticsCall
	var toolCall analyticsCall
	for _, call := range trackCalls {
		switch call.event {
		case analytics.EventSessionRegistered:
			registered = call
		case analytics.EventToolCalled:
			toolCall = call
		}
	}

	if registered.event != analytics.EventSessionRegistered || registered.groupID != "org-123" || registered.userID != "user-123" {
		t.Fatalf("registered track call = (%q, %q, %q), want (%q, %q, %q)", registered.groupID, registered.userID, registered.event, "org-123", "user-123", analytics.EventSessionRegistered)
	}
	if registered.attrs[analytics.AttrSessionID] != "sess-jwt" {
		t.Fatalf("registered session attr = %v, want %q", registered.attrs[analytics.AttrSessionID], "sess-jwt")
	}

	if toolCall.event != analytics.EventToolCalled || toolCall.groupID != "org-123" || toolCall.userID != "user-123" {
		t.Fatalf("tool track call = (%q, %q, %q), want (%q, %q, %q)", toolCall.groupID, toolCall.userID, toolCall.event, "org-123", "user-123", analytics.EventToolCalled)
	}
	if toolCall.attrs[analytics.AttrToolName] != "signoz_list_services" {
		t.Fatalf("tool name attr = %v, want %q", toolCall.attrs[analytics.AttrToolName], "signoz_list_services")
	}
	if toolCall.attrs[analytics.AttrToolIsError] != false {
		t.Fatalf("tool error attr = %v, want false", toolCall.attrs[analytics.AttrToolIsError])
	}
	if toolCall.attrs[analytics.AttrSearchContext] != "list services" {
		t.Fatalf("tool searchContext attr = %v, want %q", toolCall.attrs[analytics.AttrSearchContext], "list services")
	}
	if toolCall.attrs[analytics.AttrOrgID] != "org-123" || toolCall.attrs[analytics.AttrPrincipal] != "user" || toolCall.attrs[analytics.AttrEmail] != "user@example.com" {
		t.Fatalf("tool attrs = %#v, want orgId, principal, and email", toolCall.attrs)
	}
}

func TestAnalyticsDisabledSkipsIdentityLookup(t *testing.T) {
	var requests atomic.Int32
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Fatalf("unexpected identity request: %s", r.URL.Path)
	}))
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		APIKey:          "test-api-key",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: false}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-api-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-disabled")

	singleHook(t, hooks.OnAfterInitialize, "OnAfterInitialize")(ctx, nil, &mcp.InitializeRequest{}, &mcp.InitializeResult{})

	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "signoz_list_services",
			Arguments: map[string]any{
				"searchContext": "list services",
			},
		},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	session := fakeSession{id: "sess-disabled", ch: make(chan mcp.JSONRPCNotification, 1)}
	hooks.RegisterSession(ctx, session)
	hooks.UnregisterSession(ctx, session)

	identifyCalls, trackCalls := spy.snapshot()
	if requests.Load() != 0 {
		t.Fatalf("identity requests = %d, want %d", requests.Load(), 0)
	}
	if len(identifyCalls) != 0 {
		t.Fatalf("identify user calls = %d, want %d", len(identifyCalls), 0)
	}
	if len(trackCalls) != 0 {
		t.Fatalf("track user calls = %d, want %d", len(trackCalls), 0)
	}
}

func TestToolCallReturnsBeforeAsyncAnalyticsCompletes(t *testing.T) {
	var requests atomic.Int32
	identityStarted := make(chan struct{}, 1)

	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case identityStarted <- struct{}{}:
		default:
		}
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"user-123","displayName":"Ada Lovelace","email":"user@example.com","orgId":"org-123"}}`))
	}))
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "Bearer jwt-token")
	ctx = util.SetAuthHeader(ctx, "Authorization")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-jwt")

	middleware := mcpServer.loggingMiddleware()

	start := time.Now()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "signoz_list_services",
			Arguments: map[string]any{
				"searchContext": "list services",
			},
		},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= 190*time.Millisecond {
		t.Fatalf("tool call took %v, want less than %v", elapsed, 190*time.Millisecond)
	}

	select {
	case <-identityStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async identity request to start")
	}

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return requests.Load() == 1 && len(trackCalls) == 1
	}, "timed out waiting for async tool analytics")

	_, trackCalls := spy.snapshot()
	toolCall := trackCalls[0]
	if toolCall.event != analytics.EventToolCalled {
		t.Fatalf("track event = %q, want %q", toolCall.event, analytics.EventToolCalled)
	}
	if toolCall.attrs[analytics.AttrEmail] != "user@example.com" {
		t.Fatalf("tool attrs = %#v, want email", toolCall.attrs)
	}
}

// meEndpointServer stubs the SigNoz /me endpoint with a stable identity.
func meEndpointServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","email":"svc@example.com","orgId":"org-1"}}`))
	}))
}

func TestClientInfoAttachesToToolCallEvent(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		APIKey:          "test-key",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-client")

	singleHook(t, hooks.OnAfterInitialize, "OnAfterInitialize")(ctx, nil, &mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "claude-desktop", Version: "1.2.3"},
		},
	}, &mcp.InitializeResult{})

	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_list_services"},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 2
	}, "timed out waiting for tool analytics")

	_, trackCalls := spy.snapshot()
	var toolCall analyticsCall
	for _, c := range trackCalls {
		if c.event == analytics.EventToolCalled {
			toolCall = c
		}
	}
	if toolCall.attrs[analytics.AttrClientName] != "claude-desktop" {
		t.Fatalf("clientName = %v, want claude-desktop", toolCall.attrs[analytics.AttrClientName])
	}
	if toolCall.attrs[analytics.AttrClientVersion] != "1.2.3" {
		t.Fatalf("clientVersion = %v, want 1.2.3", toolCall.attrs[analytics.AttrClientVersion])
	}

	mcpServer.forgetClientInfo("sess-client")
	if mcpServer.lookupClientInfo("sess-client").Name != "" {
		t.Fatalf("expected ClientInfo to be cleared after forgetClientInfo")
	}
}

func TestBuildHooks_NonToolMethodsRecordSpanAndMetrics(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetSigNozURL(ctx, "https://tenant.example.com")
	ctx = newAnalyticsTestContext(ctx, "sess-init")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)

	req := &mcp.InitializeRequest{}
	result := &mcp.InitializeResult{}
	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-1", mcp.MethodInitialize, req)
	singleHook(t, hooks.OnSuccess, "OnSuccess")(ctx, "req-1", mcp.MethodInitialize, req, result)
	span.End()

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	callDataPoint := methodCalls.DataPoints[0]
	if callDataPoint.Value != 1 {
		t.Fatalf("mcp.method.calls value = %d, want 1", callDataPoint.Value)
	}
	methodName, ok := callDataPoint.Attributes.Value(attribute.Key("mcp.method.name"))
	if !ok || methodName.AsString() != string(mcp.MethodInitialize) {
		t.Fatalf("mcp.method.name = %v, want %q", methodName, mcp.MethodInitialize)
	}
	if _, ok := callDataPoint.Attributes.Value(attribute.Key("error.type")); ok {
		t.Fatal("error.type should be absent on successful method call")
	}
	if attr, ok := callDataPoint.Attributes.Value(otelpkg.MCPTenantURLKey); !ok || attr.AsString() != "https://tenant.example.com" {
		t.Fatalf("mcp.method.calls mcp.tenant_url = %v, want tenant URL", attr)
	}

	methodDuration, found := oteltest.FindFloat64HistogramMetric(metrics, "mcp.method.duration")
	if !found {
		t.Fatal("mcp.method.duration metric not found")
	}
	if len(methodDuration.DataPoints) != 1 {
		t.Fatalf("mcp.method.duration datapoints = %d, want 1", len(methodDuration.DataPoints))
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "MCP initialize" {
		t.Fatalf("span name = %q, want %q", spans[0].Name, "MCP initialize")
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPMethodKey); !ok || attr.AsString() != string(mcp.MethodInitialize) {
		t.Fatalf("span mcp.method.name = %v, want %q", attr, mcp.MethodInitialize)
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPSessionIDKey); !ok || attr.AsString() != "sess-init" {
		t.Fatalf("span mcp.session.id = %v, want %q", attr, "sess-init")
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPTenantURLKey); !ok || attr.AsString() != "https://tenant.example.com" {
		t.Fatalf("span mcp.tenant_url = %v, want tenant URL", attr)
	}
}

func TestBuildHooks_NonToolMethodErrorsRecordErrorType(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-err")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodResourcesRead)
	req := &mcp.ReadResourceRequest{}
	methodErr := fmt.Errorf("resources %w", mcpgoserver.ErrUnsupported)

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-err", mcp.MethodResourcesRead, req)
	singleHook(t, hooks.OnError, "OnError")(ctx, "req-err", mcp.MethodResourcesRead, req, methodErr)
	span.End()

	if _, ok := mcpServer.methodObs.Load(methodObservationKey(ctx, "req-err", mcp.MethodResourcesRead, req)); ok {
		t.Fatal("expected method observation to be cleaned up after OnError")
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	dp := methodCalls.DataPoints[0]
	if attr, ok := dp.Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "unsupported" {
		t.Fatalf("error.type = %v, want unsupported", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status code = %v, want Error", spans[0].Status.Code)
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("span events = %d, want 1", len(spans[0].Events))
	}
	if spans[0].Events[0].Name != "exception" {
		t.Fatalf("span event name = %q, want %q", spans[0].Events[0].Name, "exception")
	}
	if attr, ok := spanAttrValue(spans[0].Events[0].Attributes, attribute.Key("exception.message")); !ok || attr.AsString() != methodErr.Error() {
		t.Fatalf("exception.message = %v, want %q", attr, methodErr.Error())
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, attribute.Key("error.type")); !ok || attr.AsString() != "unsupported" {
		t.Fatalf("span error.type = %v, want unsupported", attr)
	}
}

func TestBuildHooks_NonToolMethodSuccessEndsSpanWithoutMeters(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), nil)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-no-meters")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-no-meters", mcp.MethodInitialize, req)
	singleHook(t, hooks.OnSuccess, "OnSuccess")(ctx, "req-no-meters", mcp.MethodInitialize, req, &mcp.InitializeResult{})
	span.End()

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "MCP initialize" {
		t.Fatalf("span name = %q, want %q", spans[0].Name, "MCP initialize")
	}
}

func TestBuildHooks_NonToolMethodObservationContextCleanupCleansUp(t *testing.T) {
	var logBuf lockedBuffer
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := newBufferedLogger(&logBuf, slog.LevelDebug)
	mcpServer := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters)
	mcpServer.methodObsTombstoneTTL = 10 * time.Millisecond
	hooks := mcpServer.buildHooks()

	baseCtx, cancel := context.WithCancel(newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-context-cleanup"))
	defer cancel()
	ctx := baseCtx
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}
	key := methodObservationKey(ctx, "req-context-cleanup", mcp.MethodInitialize, req)

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-context-cleanup", mcp.MethodInitialize, req)
	cancel()

	var metrics metricdata.ResourceMetrics
	waitForCondition(t, time.Second, func() bool {
		_, ok := mcpServer.methodObs.Load(key)
		if ok {
			return false
		}

		metrics = metricdata.ResourceMetrics{}
		if err := reader.Collect(context.Background(), &metrics); err != nil {
			t.Fatalf("collect metrics: %v", err)
		}

		methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
		if !found || len(methodCalls.DataPoints) != 1 {
			return false
		}

		for _, rec := range parseJSONLogLines(t, &logBuf) {
			if rec["msg"] == "mcp method observation ended without success/error hook" {
				return true
			}
		}

		return false
	}, "timed out waiting for method observation context cleanup")
	span.End()

	if _, ok := mcpServer.methodObs.Load(key); ok {
		t.Fatal("expected timed-out method observation to be removed")
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	if attr, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("error.type = %v, want internal", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status code = %v, want Error", spans[0].Status.Code)
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("span events = %d, want 1", len(spans[0].Events))
	}
	if spans[0].Events[0].Name != "exception" {
		t.Fatalf("span event name = %q, want %q", spans[0].Events[0].Name, "exception")
	}
	if attr, ok := spanAttrValue(spans[0].Events[0].Attributes, attribute.Key("exception.message")); !ok || !strings.Contains(attr.AsString(), "request context ended before success/error hook") {
		t.Fatalf("exception.message = %v, want context cleanup message", attr)
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("span error.type = %v, want internal", attr)
	}

	records := parseJSONLogLines(t, &logBuf)
	var foundWarn bool
	for _, rec := range records {
		if rec["msg"] == "mcp method observation ended without success/error hook" {
			foundWarn = true
			if rec["level"] != "WARN" {
				t.Fatalf("level = %v, want WARN", rec["level"])
			}
		}
	}
	if !foundWarn {
		t.Fatalf("timeout warning log not found in %v", records)
	}
}

func TestMethodObservationLateExpireNoOpsAfterFinish(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-race-finish")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}
	key := methodObservationKey(ctx, "req-race-finish", mcp.MethodInitialize, req)

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-race-finish", mcp.MethodInitialize, req)
	singleHook(t, hooks.OnSuccess, "OnSuccess")(ctx, "req-race-finish", mcp.MethodInitialize, req, &mcp.InitializeResult{})
	span.End()

	mcpServer.expireMethodObservation(key)

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	if methodCalls.DataPoints[0].Value != 1 {
		t.Fatalf("mcp.method.calls value = %d, want 1", methodCalls.DataPoints[0].Value)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if _, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); ok {
		t.Fatal("error.type should be absent after successful finish")
	}
}

func TestMethodObservationLateFinishNoOpsAfterExpire(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-race-expire")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}
	key := methodObservationKey(ctx, "req-race-expire", mcp.MethodInitialize, req)

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-race-expire", mcp.MethodInitialize, req)
	mcpServer.expireMethodObservation(key)

	singleHook(t, hooks.OnSuccess, "OnSuccess")(ctx, "req-race-expire", mcp.MethodInitialize, req, &mcp.InitializeResult{})
	span.End()

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	if methodCalls.DataPoints[0].Value != 1 {
		t.Fatalf("mcp.method.calls value = %d, want 1", methodCalls.DataPoints[0].Value)
	}
	if attr, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("error.type = %v, want internal", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("span events = %d, want 1", len(spans[0].Events))
	}
	if spans[0].Events[0].Name != "exception" {
		t.Fatalf("span event name = %q, want %q", spans[0].Events[0].Name, "exception")
	}
	if attr, ok := spanAttrValue(spans[0].Events[0].Attributes, attribute.Key("exception.message")); !ok || !strings.Contains(attr.AsString(), "request context ended before success/error hook") {
		t.Fatalf("exception.message = %v, want context cleanup message", attr)
	}
}

func TestMethodObservationLateOnErrorNoOpsAfterExpire(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-race-error")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}
	key := methodObservationKey(ctx, "req-race-error", mcp.MethodInitialize, req)

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-race-error", mcp.MethodInitialize, req)
	mcpServer.expireMethodObservation(key)

	// OnError firing after expiry must NOT synthesize a second datapoint via a
	// fallback. Client-disconnect races would otherwise double-count.
	singleHook(t, hooks.OnError, "OnError")(ctx, "req-race-error", mcp.MethodInitialize, req, context.Canceled)
	span.End()

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1 (expire-then-OnError must not double-count)", len(methodCalls.DataPoints))
	}
	if methodCalls.DataPoints[0].Value != 1 {
		t.Fatalf("mcp.method.calls value = %d, want 1", methodCalls.DataPoints[0].Value)
	}
	if attr, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("error.type = %v, want internal (from expire, not OnError)", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1 (span must not be ended twice)", len(spans))
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("span events = %d, want 1 (single exception from expire, none from OnError fallback)", len(spans[0].Events))
	}
}

func TestMethodObservationConcurrentFinishAndExpireDedupes(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	ctx := newAnalyticsTestContext(util.SetSigNozURL(context.Background(), "https://tenant.example.com"), "sess-race-concurrent")
	ctx, span := mcpServer.startMethodSpan(ctx, mcp.MethodInitialize)
	req := &mcp.InitializeRequest{}
	key := methodObservationKey(ctx, "req-race-concurrent", mcp.MethodInitialize, req)
	onSuccess := singleHook(t, hooks.OnSuccess, "OnSuccess")

	singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, "req-race-concurrent", mcp.MethodInitialize, req)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		onSuccess(ctx, "req-race-concurrent", mcp.MethodInitialize, req, &mcp.InitializeResult{})
	}()
	go func() {
		defer wg.Done()
		<-start
		mcpServer.expireMethodObservation(key)
	}()
	close(start)
	wg.Wait()
	span.End()

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	var total int64
	for _, dp := range methodCalls.DataPoints {
		total += dp.Value
	}
	if total != 1 {
		t.Fatalf("mcp.method.calls total = %d, want 1", total)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if len(spans[0].Events) > 1 {
		t.Fatalf("span events = %d, want <= 1", len(spans[0].Events))
	}
}

func TestMethodSpanMiddleware_PropagatesMethodSpanToRequestContext(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	mcpServer := NewMCPServer(logpkg.New("error"), nil, &config.Config{}, noopanalytics.New(), nil)
	var seenSpan trace.SpanContext

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSpan = trace.SpanFromContext(r.Context()).SpanContext()
		trace.SpanFromContext(r.Context()).End()
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req = req.WithContext(util.SetSigNozURL(req.Context(), "https://tenant.example.com"))
	rr := httptest.NewRecorder()

	mcpServer.methodSpanMiddleware(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !seenSpan.IsValid() {
		t.Fatal("expected request context to carry a valid method span")
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].SpanContext.SpanID() != seenSpan.SpanID() {
		t.Fatalf("request context span ID = %s, exported span ID = %s", seenSpan.SpanID(), spans[0].SpanContext.SpanID())
	}
	if spans[0].Name != "MCP initialize" {
		t.Fatalf("span name = %q, want %q", spans[0].Name, "MCP initialize")
	}
}

func TestMethodSpanMiddleware_SkipsUnknownMethodNames(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	mcpServer := NewMCPServer(logpkg.New("error"), nil, &config.Config{}, noopanalytics.New(), nil)
	var sawValidSpan bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawValidSpan = trace.SpanFromContext(r.Context()).SpanContext().IsValid()
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"attacker/custom-cardinality","params":{}}`))
	rr := httptest.NewRecorder()

	mcpServer.methodSpanMiddleware(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if sawValidSpan {
		t.Fatal("did not expect a span for an unknown method")
	}
	if spans := traceExporter.GetSpans(); len(spans) != 0 {
		t.Fatalf("span count = %d, want 0", len(spans))
	}
}

func TestMethodSpanMiddleware_RequestContextCleanupEndsMethodSpan(t *testing.T) {
	var logBuf lockedBuffer
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := newBufferedLogger(&logBuf, slog.LevelDebug)
	mcpServer := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := &mcp.InitializeRequest{}
		singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(r.Context(), "req-timeout-http", mcp.MethodInitialize, req)
		w.WriteHeader(http.StatusOK)
	})

	baseCtx, cancel := context.WithCancel(util.SetSigNozURL(context.Background(), "https://tenant.example.com"))
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"req-timeout-http","method":"initialize","params":{}}`))
	req = req.WithContext(baseCtx)
	rr := httptest.NewRecorder()

	mcpServer.methodSpanMiddleware(next).ServeHTTP(rr, req)
	cancel()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var metrics metricdata.ResourceMetrics
	waitForCondition(t, time.Second, func() bool {
		metrics = metricdata.ResourceMetrics{}
		if err := reader.Collect(context.Background(), &metrics); err != nil {
			t.Fatalf("collect metrics: %v", err)
		}

		methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
		if !found || len(methodCalls.DataPoints) != 1 {
			return false
		}

		for _, rec := range parseJSONLogLines(t, &logBuf) {
			if rec["msg"] == "mcp method observation ended without success/error hook" {
				return true
			}
		}

		return false
	}, "timed out waiting for HTTP-backed method observation context cleanup")

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if attr, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("error.type = %v, want internal", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status code = %v, want Error", spans[0].Status.Code)
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, attribute.Key("error.type")); !ok || attr.AsString() != "internal" {
		t.Fatalf("span error.type = %v, want internal", attr)
	}
}

func TestMethodSpanMiddleware_OnErrorWithoutBeforeAnyStillEndsSpan(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	mcpServer := NewMCPServer(logpkg.New("error"), tools.NewHandler(logpkg.New("error"), cfg), cfg, noopanalytics.New(), meters)
	hooks := mcpServer.buildHooks()
	methodErr := fmt.Errorf("initialize %w", mcpgoserver.ErrUnsupported)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		singleHook(t, hooks.OnError, "OnError")(r.Context(), "req-unmarshal", mcp.MethodInitialize, &mcp.InitializeRequest{}, methodErr)
		w.WriteHeader(http.StatusBadRequest)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"req-unmarshal","method":"initialize","params":{}}`))
	req = req.WithContext(util.SetSigNozURL(req.Context(), "https://tenant.example.com"))
	rr := httptest.NewRecorder()

	mcpServer.methodSpanMiddleware(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	methodCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.method.calls")
	if !found {
		t.Fatal("mcp.method.calls metric not found")
	}
	if len(methodCalls.DataPoints) != 1 {
		t.Fatalf("mcp.method.calls datapoints = %d, want 1", len(methodCalls.DataPoints))
	}
	if attr, ok := methodCalls.DataPoints[0].Attributes.Value(attribute.Key("error.type")); !ok || attr.AsString() != "unsupported" {
		t.Fatalf("error.type = %v, want unsupported", attr)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status code = %v, want Error", spans[0].Status.Code)
	}
	if attr, ok := spanAttrValue(spans[0].Attributes, attribute.Key("error.type")); !ok || attr.AsString() != "unsupported" {
		t.Fatalf("span error.type = %v, want unsupported", attr)
	}
}

func TestMethodSpanMiddleware_SkipsOversizedBodyAndPassesThrough(t *testing.T) {
	mcpServer := NewMCPServer(logpkg.New("error"), nil, &config.Config{}, noopanalytics.New(), nil)
	mcpServer.maxMethodSpanBodyBytes = 32
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	var nextCalled bool
	var seenBody string

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read forwarded body: %v", err)
		}
		seenBody = string(body)
		if trace.SpanFromContext(r.Context()).SpanContext().IsValid() {
			t.Fatal("did not expect a method span for oversized body")
		}
		w.WriteHeader(http.StatusOK)
	})

	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"payload":"this body is much larger than 32 bytes"}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	mcpServer.methodSpanMiddleware(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !nextCalled {
		t.Fatal("expected next handler to be called for oversized body")
	}
	if seenBody != payload {
		t.Fatalf("forwarded body = %q, want original payload", seenBody)
	}
	if spans := traceExporter.GetSpans(); len(spans) != 0 {
		t.Fatalf("span count = %d, want 0", len(spans))
	}
}

func TestLoggingMiddleware_ErrorResultLogsWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := newBufferedLogger(&buf, slog.LevelDebug)
	mcpServer := NewMCPServer(logger, nil, &config.Config{}, noopanalytics.New(), nil)

	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{mcp.TextContent{Text: "tool exploded"}},
		}, nil
	})(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_query"},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	records := parseJSONLogLines(t, &buf)
	for _, rec := range records {
		if rec["msg"] == "tool call returned error result" {
			if rec["level"] != "WARN" {
				t.Fatalf("level = %v, want WARN", rec["level"])
			}
			return
		}
	}
	t.Fatalf("tool error-result log not found in %v", records)
}

func TestLoggingMiddleware_GoErrorLogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := newBufferedLogger(&buf, slog.LevelDebug)
	mcpServer := NewMCPServer(logger, nil, &config.Config{}, noopanalytics.New(), nil)

	middleware := mcpServer.loggingMiddleware()
	expectedErr := errors.New("upstream failed")
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, expectedErr
	})(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_query"},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("middleware error = %v, want %v", err, expectedErr)
	}

	records := parseJSONLogLines(t, &buf)
	for _, rec := range records {
		if rec["msg"] == "tool call failed" {
			if rec["level"] != "ERROR" {
				t.Fatalf("level = %v, want ERROR", rec["level"])
			}
			return
		}
	}
	t.Fatalf("tool failure log not found in %v", records)
}

func TestLoggingMiddleware_PanicPathRecordsErrorMetricAndSpan(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

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

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, noopanalytics.New(), meters)

	// Build the same middleware composition mcp-go would build for production:
	// loggingMiddleware wraps recovery wraps the tool. If the tool panics,
	// recovery catches it and surfaces an error via the normal return path,
	// and loggingMiddleware's post-next() block records metrics + span error.
	panicTool := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("boom")
	}
	recovery := func(next mcpgoserver.ToolHandlerFunc) mcpgoserver.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic recovered in %s tool handler: %v", req.Params.Name, r)
				}
			}()
			return next(ctx, req)
		}
	}
	chain := mcpServer.loggingMiddleware()(recovery(panicTool))

	_, err = chain(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "panic_tool",
			Arguments: map[string]any{},
		},
	})
	if err == nil {
		t.Fatal("expected recovered panic to surface as error")
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	toolCalls, found := oteltest.FindInt64SumMetric(metrics, "mcp.tool.calls")
	if !found {
		t.Fatal("mcp.tool.calls metric not found")
	}
	if len(toolCalls.DataPoints) != 1 {
		t.Fatalf("mcp.tool.calls datapoints = %d, want 1", len(toolCalls.DataPoints))
	}
	dataPoint := toolCalls.DataPoints[0]
	if dataPoint.Value != 1 {
		t.Fatalf("mcp.tool.calls value = %d, want 1", dataPoint.Value)
	}
	toolName, ok := dataPoint.Attributes.Value(attribute.Key("gen_ai.tool.name"))
	if !ok {
		t.Fatal("gen_ai.tool.name attribute missing")
	}
	if got := toolName.AsString(); got != "panic_tool" {
		t.Fatalf("gen_ai.tool.name = %q, want %q", got, "panic_tool")
	}
	toolIsError, ok := dataPoint.Attributes.Value(attribute.Key("mcp.tool.is_error"))
	if !ok {
		t.Fatal("mcp.tool.is_error attribute missing")
	}
	if got := toolIsError.AsBool(); !got {
		t.Fatalf("mcp.tool.is_error = %t, want true", got)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status code = %v, want %v", spans[0].Status.Code, codes.Error)
	}
}

func TestUnregisterSessionHookClearsClientInfo(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, APIKey: "k", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-cleanup")

	singleHook(t, hooks.OnAfterInitialize, "OnAfterInitialize")(ctx, nil,
		&mcp.InitializeRequest{Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "cursor", Version: "0.9"},
		}}, &mcp.InitializeResult{})

	if got := mcpServer.lookupClientInfo("sess-cleanup"); got.Name != "cursor" {
		t.Fatalf("pre-unregister ClientInfo = %+v, want name=cursor", got)
	}

	session := fakeSession{id: "sess-cleanup", ch: make(chan mcp.JSONRPCNotification, 1)}
	singleHook(t, hooks.OnUnregisterSession, "OnUnregisterSession")(ctx, session)

	if got := mcpServer.lookupClientInfo("sess-cleanup"); got.Name != "" {
		t.Fatalf("post-unregister ClientInfo = %+v, want empty", got)
	}
}

func TestPromptFetchedEvent(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, APIKey: "k", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-prompt")

	singleHook(t, hooks.OnAfterGetPrompt, "OnAfterGetPrompt")(ctx, nil,
		&mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: "rca"}},
		&mcp.GetPromptResult{})

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 1
	}, "timed out waiting for prompt analytics")

	_, trackCalls := spy.snapshot()
	call := trackCalls[0]
	if call.event != analytics.EventPromptFetched {
		t.Fatalf("event = %q, want %q", call.event, analytics.EventPromptFetched)
	}
	if call.attrs[analytics.AttrPromptName] != "rca" {
		t.Fatalf("promptName attr = %v, want rca", call.attrs[analytics.AttrPromptName])
	}
	if call.attrs[analytics.AttrSessionID] != "sess-prompt" {
		t.Fatalf("sessionId attr = %v, want sess-prompt", call.attrs[analytics.AttrSessionID])
	}
}

func TestResourceFetchedEvent(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, APIKey: "k", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-res")

	singleHook(t, hooks.OnAfterReadResource, "OnAfterReadResource")(ctx, nil,
		&mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "signoz://dashboard/abc"}},
		&mcp.ReadResourceResult{})

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 1
	}, "timed out waiting for resource analytics")

	_, trackCalls := spy.snapshot()
	call := trackCalls[0]
	if call.event != analytics.EventResourceFetched {
		t.Fatalf("event = %q, want %q", call.event, analytics.EventResourceFetched)
	}
	if call.attrs[analytics.AttrResourceURI] != "signoz://dashboard/abc" {
		t.Fatalf("resourceUri attr = %v, want signoz://dashboard/abc", call.attrs[analytics.AttrResourceURI])
	}
}

func TestOAuthEventEmitter_InjectsCredentialsAndDispatches(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)

	mcpServer.trackOAuthEvent(context.Background(), analytics.EventOAuthTokenIssued,
		"oauth-api-key", sigNoz.URL,
		map[string]any{
			analytics.AttrTenantURL: sigNoz.URL,
			analytics.AttrGrantType: "authorization_code",
		})

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 1
	}, "timed out waiting for OAuth analytics")

	_, trackCalls := spy.snapshot()
	call := trackCalls[0]
	if call.event != analytics.EventOAuthTokenIssued {
		t.Fatalf("event = %q, want %q", call.event, analytics.EventOAuthTokenIssued)
	}
	if call.attrs[analytics.AttrGrantType] != "authorization_code" {
		t.Fatalf("grantType attr = %v, want authorization_code", call.attrs[analytics.AttrGrantType])
	}
	if call.groupID != "org-1" || call.userID != "sa-1" {
		t.Fatalf("identity (%q, %q), want (org-1, sa-1)", call.groupID, call.userID)
	}
}

// TestRun_HTTPCanceledBeforeListen verifies Run returns promptly when its
// context is already canceled before ListenAndServe would be called — the
// ctx.Err() guard in Run plus the atomic.Pointer handoff should prevent
// the listener from binding and the process from hanging on shutdown.
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

// TestRegisteredEventHasProtocolVersion verifies MCP Registered carries the
// protocolVersion attribute — needed to track which clients are on which
// MCP protocol revision as the spec evolves.
func TestRegisteredEventHasProtocolVersion(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		APIKey:          "test-key",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-registered")

	singleHook(t, hooks.OnAfterInitialize, "OnAfterInitialize")(ctx, nil, &mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: "2025-06-18",
			ClientInfo:      mcp.Implementation{Name: "claude-desktop", Version: "1.2.3"},
		},
	}, &mcp.InitializeResult{})

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 1
	}, "timed out waiting for registered event")

	_, trackCalls := spy.snapshot()
	ev := trackCalls[0]
	if ev.event != analytics.EventSessionRegistered {
		t.Fatalf("event = %q, want %q", ev.event, analytics.EventSessionRegistered)
	}
	if ev.attrs[analytics.AttrProtocolVersion] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 2025-06-18", ev.attrs[analytics.AttrProtocolVersion])
	}
}

// TestToolCallEventHasErrorType verifies error categorization lands on the
// analytics event (analytics scope). resultBytes is not an analytics field
// — see TestToolCallSpanHasResultBytes for the span + log coverage.
func TestToolCallEventHasErrorType(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{
		URL:             sigNoz.URL,
		APIKey:          "test-key",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
	}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, spy, nil)

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-tool")

	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "unexpected status 502 from upstream"}},
		}, nil
	})(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_list_services"},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		_, trackCalls := spy.snapshot()
		return len(trackCalls) == 1
	}, "timed out waiting for tool-call event")

	_, trackCalls := spy.snapshot()
	ev := trackCalls[0]
	if ev.event != analytics.EventToolCalled {
		t.Fatalf("event = %q, want %q", ev.event, analytics.EventToolCalled)
	}
	if ev.attrs[analytics.AttrErrorType] != "upstream_5xx" {
		t.Fatalf("errorType = %v, want upstream_5xx", ev.attrs[analytics.AttrErrorType])
	}
}

// TestToolCallSpanHasResultBytes verifies the tool-call span carries the
// approximate text-content size so SigNoz dashboards can correlate latency
// with payload size.
func TestToolCallSpanHasResultBytes(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	defer func() {
		otel.SetTracerProvider(prevTracerProvider)
	}()
	defer func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, noopanalytics.New(), nil)

	body := strings.Repeat("x", 512)
	middleware := mcpServer.loggingMiddleware()
	_, err := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: body}},
		}, nil
	})(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_list_services"},
	})
	if err != nil {
		t.Fatalf("middleware error = %v", err)
	}

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	size, ok := spanAttrValue(spans[0].Attributes, otelpkg.MCPToolResultBytesKey)
	if !ok {
		t.Fatalf("span missing %s", otelpkg.MCPToolResultBytesKey)
	}
	if size.AsInt64() != int64(len(body)) {
		t.Fatalf("%s = %d, want %d", otelpkg.MCPToolResultBytesKey, size.AsInt64(), len(body))
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
			name:   "result error 401",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{mcp.TextContent{Text: "unexpected status 401"}}},
			want:   "unauthorized",
		},
		{
			name:   "result error 404",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{mcp.TextContent{Text: "unexpected status 404 not found"}}},
			want:   "upstream_4xx",
		},
		{
			name:   "result error 503",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{mcp.TextContent{Text: "unexpected status 503 upstream"}}},
			want:   "upstream_5xx",
		},
		{
			name:   "result error generic",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{mcp.TextContent{Text: "missing field"}}},
			want:   "tool_error",
		},
		{
			name:   "non-error result",
			result: &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Text: "ok"}}},
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

func TestApproxResultBytes(t *testing.T) {
	tests := []struct {
		name     string
		result   *mcp.CallToolResult
		wantSize int64
	}{
		{name: "nil result", wantSize: 0},
		{name: "empty content", result: &mcp.CallToolResult{}, wantSize: 0},
		{
			name:     "single text",
			result:   &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Text: "hello"}}},
			wantSize: 5,
		},
		{
			name: "multiple text entries sum",
			result: &mcp.CallToolResult{Content: []mcp.Content{
				mcp.TextContent{Text: "abc"},
				mcp.TextContent{Text: "defg"},
			}},
			wantSize: 7,
		},
		{
			name: "large result is summed without truncation",
			result: &mcp.CallToolResult{Content: []mcp.Content{
				mcp.TextContent{Text: strings.Repeat("x", 10<<20)},
			}},
			wantSize: 10 << 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := approxResultBytes(tt.result)
			if size != tt.wantSize {
				t.Errorf("size = %d, want %d", size, tt.wantSize)
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

	waitForCondition(t, time.Second, func() bool {
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
