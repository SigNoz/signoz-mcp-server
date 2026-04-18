package mcp_server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/internal/telemetry"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type analyticsCall struct {
	group string
	user  string
	event string
	attrs map[string]any
}

type spyAnalytics struct {
	enabled           bool
	identifyUserCalls []analyticsCall
	trackUserCalls    []analyticsCall
	identifyGroupHits int
	trackGroupHits    int
}

func (s *spyAnalytics) Enabled() bool                                  { return s.enabled }
func (s *spyAnalytics) Start(context.Context) error                     { return nil }
func (s *spyAnalytics) Stop(context.Context) error                      { return nil }
func (s *spyAnalytics) Send(context.Context, ...analyticstypes.Message) {}
func (s *spyAnalytics) TrackGroup(context.Context, string, string, map[string]any) {
	s.trackGroupHits++
}
func (s *spyAnalytics) IdentifyGroup(context.Context, string, map[string]any) {
	s.identifyGroupHits++
}
func (s *spyAnalytics) TrackUser(_ context.Context, group, user, event string, attrs map[string]any) {
	s.trackUserCalls = append(s.trackUserCalls, analyticsCall{
		group: group,
		user:  user,
		event: event,
		attrs: attrs,
	})
}
func (s *spyAnalytics) IdentifyUser(_ context.Context, group, user string, attrs map[string]any) {
	s.identifyUserCalls = append(s.identifyUserCalls, analyticsCall{
		group: group,
		user:  user,
		attrs: attrs,
	})
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
	requests := 0
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/service_accounts/me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v1/service_accounts/me")
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "test-api-key" {
			t.Fatalf("SIGNOZ-API-KEY = %q, want %q", r.Header.Get("SIGNOZ-API-KEY"), "test-api-key")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-123","email":"service@example.com","orgId":"org-456"}}`))
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

	if requests != 2 {
		t.Fatalf("identity requests = %d, want %d", requests, 2)
	}
	if spy.identifyGroupHits != 0 || spy.trackGroupHits != 0 {
		t.Fatalf("expected no group analytics calls, got identify=%d track=%d", spy.identifyGroupHits, spy.trackGroupHits)
	}
	if len(spy.identifyUserCalls) != 1 {
		t.Fatalf("identify user calls = %d, want %d", len(spy.identifyUserCalls), 1)
	}
	if len(spy.trackUserCalls) != 1 {
		t.Fatalf("track user calls = %d, want %d", len(spy.trackUserCalls), 1)
	}

	identify := spy.identifyUserCalls[0]
	if identify.group != "org-456" || identify.user != "sa-123" {
		t.Fatalf("identify user args = (%q, %q), want (%q, %q)", identify.group, identify.user, "org-456", "sa-123")
	}
	if identify.attrs["org_id"] != "org-456" || identify.attrs["principal"] != "service_account" || identify.attrs["service_email"] != "service@example.com" {
		t.Fatalf("identify attrs = %#v, want org_id, principal, and service_email", identify.attrs)
	}

	track := spy.trackUserCalls[0]
	if track.group != "org-456" || track.user != "sa-123" || track.event != "MCP Unregistered" {
		t.Fatalf("track call = (%q, %q, %q), want (%q, %q, %q)", track.group, track.user, track.event, "org-456", "sa-123", "MCP Unregistered")
	}
	if track.attrs[string(telemetry.MCPSessionIDKey)] != "sess-api" {
		t.Fatalf("session id attr = %v, want %q", track.attrs[string(telemetry.MCPSessionIDKey)], "sess-api")
	}
	if track.attrs["org_id"] != "org-456" || track.attrs["principal"] != "service_account" || track.attrs["service_email"] != "service@example.com" {
		t.Fatalf("track attrs = %#v, want org_id, principal, and service_email", track.attrs)
	}
}

func TestUserScopedAnalyticsUseJWTIdentity(t *testing.T) {
	requests := 0
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v2/users/me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v2/users/me")
		}
		if r.Header.Get("Authorization") != "Bearer jwt-token" {
			t.Fatalf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer jwt-token")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"user-123","email":"user@example.com","orgId":"org-123"}}`))
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

	if requests != 2 {
		t.Fatalf("identity requests = %d, want %d", requests, 2)
	}
	if spy.identifyGroupHits != 0 || spy.trackGroupHits != 0 {
		t.Fatalf("expected no group analytics calls, got identify=%d track=%d", spy.identifyGroupHits, spy.trackGroupHits)
	}
	if len(spy.identifyUserCalls) != 1 {
		t.Fatalf("identify user calls = %d, want %d", len(spy.identifyUserCalls), 1)
	}
	if len(spy.trackUserCalls) != 2 {
		t.Fatalf("track user calls = %d, want %d", len(spy.trackUserCalls), 2)
	}

	identify := spy.identifyUserCalls[0]
	if identify.group != "org-123" || identify.user != "user-123" {
		t.Fatalf("identify user args = (%q, %q), want (%q, %q)", identify.group, identify.user, "org-123", "user-123")
	}
	if identify.attrs["org_id"] != "org-123" || identify.attrs["principal"] != "user" || identify.attrs["user_email"] != "user@example.com" {
		t.Fatalf("identify attrs = %#v, want org_id, principal, and user_email", identify.attrs)
	}

	registered := spy.trackUserCalls[0]
	if registered.event != "MCP Registered" || registered.group != "org-123" || registered.user != "user-123" {
		t.Fatalf("registered track call = (%q, %q, %q), want (%q, %q, %q)", registered.group, registered.user, registered.event, "org-123", "user-123", "MCP Registered")
	}
	if registered.attrs[string(telemetry.MCPSessionIDKey)] != "sess-jwt" {
		t.Fatalf("registered session attr = %v, want %q", registered.attrs[string(telemetry.MCPSessionIDKey)], "sess-jwt")
	}

	toolCall := spy.trackUserCalls[1]
	if toolCall.event != "Tool Call" || toolCall.group != "org-123" || toolCall.user != "user-123" {
		t.Fatalf("tool track call = (%q, %q, %q), want (%q, %q, %q)", toolCall.group, toolCall.user, toolCall.event, "org-123", "user-123", "Tool Call")
	}
	if toolCall.attrs[string(telemetry.GenAIToolNameKey)] != "signoz_list_services" {
		t.Fatalf("tool name attr = %v, want %q", toolCall.attrs[string(telemetry.GenAIToolNameKey)], "signoz_list_services")
	}
	if toolCall.attrs[string(telemetry.MCPToolIsErrorKey)] != false {
		t.Fatalf("tool error attr = %v, want false", toolCall.attrs[string(telemetry.MCPToolIsErrorKey)])
	}
	if toolCall.attrs["org_id"] != "org-123" || toolCall.attrs["principal"] != "user" || toolCall.attrs["user_email"] != "user@example.com" {
		t.Fatalf("tool attrs = %#v, want org_id, principal, and user_email", toolCall.attrs)
	}
}

func TestAnalyticsDisabledSkipsIdentityLookup(t *testing.T) {
	requests := 0
	sigNoz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
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

	if requests != 0 {
		t.Fatalf("identity requests = %d, want %d", requests, 0)
	}
	if spy.identifyGroupHits != 0 || spy.trackGroupHits != 0 {
		t.Fatalf("expected no group analytics calls, got identify=%d track=%d", spy.identifyGroupHits, spy.trackGroupHits)
	}
	if len(spy.identifyUserCalls) != 0 {
		t.Fatalf("identify user calls = %d, want %d", len(spy.identifyUserCalls), 0)
	}
	if len(spy.trackUserCalls) != 0 {
		t.Fatalf("track user calls = %d, want %d", len(spy.trackUserCalls), 0)
	}
}
