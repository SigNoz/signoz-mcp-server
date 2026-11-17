package mcp_server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type seenCredential struct {
	header, value, signozURL string
}

func runAuth(t *testing.T, server *MCPServer, headers map[string]string) (*httptest.ResponseRecorder, *seenCredential) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	var seen *seenCredential
	server.authMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cred := util.GetCredential(r.Context())
		signozURL, _ := util.GetSigNozURL(r.Context())
		seen = &seenCredential{header: cred.Header, value: cred.Value, signozURL: signozURL}
	})).ServeHTTP(rr, req)
	return rr, seen
}

func TestSessionBearerForwardsSigNozSessionToItsDeployment(t *testing.T) {
	var meCalls atomic.Int32
	deployment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/users/me" {
			http.NotFound(w, r)
			return
		}
		meCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer live-access" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unauthenticated","message":"token expired"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"u1","orgId":"o1"}}`))
	}))
	defer deployment.Close()

	cfg := &config.Config{
		OAuthEnabled:         true,
		OAuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:       "https://mcp.example.com",
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("127.0.0.1"),
	}
	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}

	live := oauth.FormatSessionAccessToken(deployment.URL, "live-access")
	for range 3 {
		rr, seen := runAuth(t, server, map[string]string{
			"Authorization": "Bearer " + live,
			// The token's deployment wins over a header naming another.
			"X-SigNoz-URL": "https://other.example.com",
		})
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.Equal(t, &seenCredential{header: "Authorization", value: "Bearer live-access", signozURL: deployment.URL}, seen)
	}
	require.EqualValues(t, 1, meCalls.Load(), "validation is cached across a burst of requests")

	rr, seen := runAuth(t, server, map[string]string{"Authorization": "Bearer " + oauth.FormatSessionAccessToken(deployment.URL, "ended-access")})
	require.Nil(t, seen)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Contains(t, rr.Header().Get("WWW-Authenticate"), `error="invalid_token"`, "a rejected session makes the client refresh")

	before := meCalls.Load()
	rr, seen = runAuth(t, server, map[string]string{"Authorization": "Bearer " + oauth.FormatSessionAccessToken("https://evil.example.com", "live-access")})
	require.Nil(t, seen)
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, before, meCalls.Load(), "a disallowed deployment is never dialed")
}

func TestCredentialPathsResolveSideBySide(t *testing.T) {
	cfg := &config.Config{
		URL:              "https://signoz.example.com",
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}
	server := &MCPServer{logger: logpkg.New("error"), config: cfg, analytics: noopanalytics.New()}

	rr, seen := runAuth(t, server, map[string]string{"SIGNOZ-API-KEY": "sa-key"})
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, &seenCredential{header: "SIGNOZ-API-KEY", value: "sa-key", signozURL: cfg.URL}, seen)

	legacy, err := oauth.EncryptToken("sa-key-in-token", cfg.URL, "client", time.Now().Add(time.Hour), []byte(cfg.OAuthTokenSecret))
	require.NoError(t, err)
	rr, seen = runAuth(t, server, map[string]string{"Authorization": "Bearer " + legacy})
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, &seenCredential{header: "SIGNOZ-API-KEY", value: "sa-key-in-token", signozURL: cfg.URL}, seen)

	noURL := &MCPServer{logger: logpkg.New("error"), config: &config.Config{
		OAuthEnabled: true, OAuthTokenSecret: cfg.OAuthTokenSecret, OAuthIssuerURL: cfg.OAuthIssuerURL,
	}, analytics: noopanalytics.New()}
	rr, seen = runAuth(t, noURL, nil)
	require.Nil(t, seen)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Equal(t, `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`, rr.Header().Get("WWW-Authenticate"))
}
