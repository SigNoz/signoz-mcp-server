package mcp_server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
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

	server := &MCPServer{logger: zap.NewNop(), config: cfg, analytics: noopanalytics.New()}
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

	server := &MCPServer{logger: zap.NewNop(), config: cfg, analytics: noopanalytics.New()}
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

	server := &MCPServer{logger: zap.NewNop(), config: cfg, analytics: noopanalytics.New()}
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

	server := &MCPServer{logger: zap.NewNop(), config: cfg, analytics: noopanalytics.New()}
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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "Bearer jwt-token")
	ctx = util.SetAuthHeader(ctx, "Authorization")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-jwt")

	hooks.OnAfterInitialize[0](ctx, nil, &mcp.InitializeRequest{}, &mcp.InitializeResult{})

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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: false}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-api-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-disabled")

	hooks.OnAfterInitialize[0](ctx, nil, &mcp.InitializeRequest{}, &mcp.InitializeResult{})

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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)

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
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("tool call took %v, want less than %v", elapsed, 150*time.Millisecond)
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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "test-key")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-client")

	hooks.OnAfterInitialize[0](ctx, nil, &mcp.InitializeRequest{
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

func TestUnregisterSessionHookClearsClientInfo(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, APIKey: "k", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-cleanup")

	hooks.OnAfterInitialize[0](ctx, nil,
		&mcp.InitializeRequest{Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "cursor", Version: "0.9"},
		}}, &mcp.InitializeResult{})

	if got := mcpServer.lookupClientInfo("sess-cleanup"); got.Name != "cursor" {
		t.Fatalf("pre-unregister ClientInfo = %+v, want name=cursor", got)
	}

	session := fakeSession{id: "sess-cleanup", ch: make(chan mcp.JSONRPCNotification, 1)}
	hooks.OnUnregisterSession[0](ctx, session)

	if got := mcpServer.lookupClientInfo("sess-cleanup"); got.Name != "" {
		t.Fatalf("post-unregister ClientInfo = %+v, want empty", got)
	}
}

func TestPromptFetchedEvent(t *testing.T) {
	sigNoz := meEndpointServer(t)
	defer sigNoz.Close()

	cfg := &config.Config{URL: sigNoz.URL, APIKey: "k", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-prompt")

	hooks.OnAfterGetPrompt[0](ctx, nil,
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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)
	hooks := mcpServer.buildHooks()

	ctx := context.Background()
	ctx = util.SetAPIKey(ctx, "k")
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, sigNoz.URL)
	ctx = newAnalyticsTestContext(ctx, "sess-res")

	hooks.OnAfterReadResource[0](ctx, nil,
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
	handler := tools.NewHandler(zap.NewNop(), cfg)
	spy := &spyAnalytics{enabled: true}
	mcpServer := NewMCPServer(zap.NewNop(), handler, cfg, spy)

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

func newTestMCPServerForAuth(t *testing.T, cfg *config.Config) *MCPServer {
	t.Helper()
	return &MCPServer{
		config: cfg,
		logger: zap.NewNop(),
	}
}

type capturedCtx struct {
	apiKey     string
	authHeader string
	signozURL  string
	called     bool
}

func captureNext(captured *capturedCtx) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.called = true
		captured.apiKey, _ = util.GetAPIKey(r.Context())
		captured.authHeader, _ = util.GetAuthHeader(r.Context())
		captured.signozURL, _ = util.GetSigNozURL(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func claudeManagedAgentTokenForTest(t *testing.T, url, key string) string {
	t.Helper()
	payload := `{"headers":{"X-SigNoz-URL":"` + url + `","KEY":"` + key + `"}}`
	return "mcp_" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestAuthMiddleware_ClaudeManagedAgentToken_FlagOff_FallsThrough(t *testing.T) {
	cfg := &config.Config{
		ClaudeManagedAgentTokenEnabled: false,
		URL:                            "https://configured.signoz.cloud",
	}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := claudeManagedAgentTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// With flag off, the mcp_ branch is skipped. The token falls into the
	// legacy "raw API key" path (no OAuth, no JWT, no customURL), which sets
	// the bearer body as the api key and uses the configured URL.
	if !captured.called {
		t.Fatalf("next handler was not called; status=%d body=%q", rec.Code, rec.Body.String())
	}
	if captured.apiKey != token {
		t.Errorf("apiKey = %q, want %q (raw token, untouched by mcp_ branch)", captured.apiKey, token)
	}
	if captured.signozURL != "https://configured.signoz.cloud" {
		t.Errorf("signozURL = %q, want configured URL", captured.signozURL)
	}
}

func TestAuthMiddleware_ClaudeManagedAgentToken_FlagOn_ValidToken(t *testing.T) {
	cfg := &config.Config{ClaudeManagedAgentTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := claudeManagedAgentTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if !captured.called {
		t.Fatal("next handler was not called")
	}
	if captured.apiKey != "sk_xxx" {
		t.Errorf("apiKey = %q, want sk_xxx", captured.apiKey)
	}
	if captured.authHeader != "SIGNOZ-API-KEY" {
		t.Errorf("authHeader = %q, want SIGNOZ-API-KEY", captured.authHeader)
	}
	if captured.signozURL != "https://tenant.signoz.cloud" {
		t.Errorf("signozURL = %q, want https://tenant.signoz.cloud", captured.signozURL)
	}
}

func TestAuthMiddleware_ClaudeManagedAgentToken_FlagOn_TokenWinsOverHeader(t *testing.T) {
	cfg := &config.Config{ClaudeManagedAgentTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := claudeManagedAgentTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-SigNoz-URL", "https://conflicting.signoz.cloud")
	req.Header.Set("SIGNOZ-API-KEY", "conflicting_key")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if captured.signozURL != "https://tenant.signoz.cloud" {
		t.Errorf("signozURL = %q, want token's URL https://tenant.signoz.cloud", captured.signozURL)
	}
	if captured.apiKey != "sk_xxx" {
		t.Errorf("apiKey = %q, want token's key sk_xxx", captured.apiKey)
	}
}

func TestAuthMiddleware_ClaudeManagedAgentToken_FlagOn_MalformedReturns401(t *testing.T) {
	cfg := &config.Config{ClaudeManagedAgentTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer mcp_!!!not-base64!!!")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %q", rec.Code, rec.Body.String())
	}
	if captured.called {
		t.Error("next handler should NOT be called on malformed mcp_ token")
	}
	if !strings.Contains(rec.Body.String(), "bad base64") {
		t.Errorf("body = %q, want 'bad base64' error", rec.Body.String())
	}
}

func TestAuthMiddleware_ClaudeManagedAgentToken_FlagOn_NonMCPBearerUnaffected(t *testing.T) {
	cfg := &config.Config{
		ClaudeManagedAgentTokenEnabled: true,
		URL:                            "https://configured.signoz.cloud",
	}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer plain_pat_token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if captured.apiKey != "plain_pat_token" {
		t.Errorf("apiKey = %q, want plain_pat_token (legacy path)", captured.apiKey)
	}
	if captured.signozURL != "https://configured.signoz.cloud" {
		t.Errorf("signozURL = %q, want configured URL", captured.signozURL)
	}
}
