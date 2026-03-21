package oauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
)

func TestOAuthAuthorizationFlow(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AccessTokenTTL:   time.Hour,
		RefreshTokenTTL:  24 * time.Hour,
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(zap.NewNop(), cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", handler.HandleProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", handler.HandleAuthorizationServerMetadata)
	mux.HandleFunc("POST /oauth/register", handler.HandleRegisterClient)
	mux.HandleFunc("GET /oauth/authorize", handler.HandleAuthorizePage)
	mux.HandleFunc("POST /oauth/authorize", handler.HandleAuthorizeSubmit)
	mux.HandleFunc("POST /oauth/token", handler.HandleToken)

	registerReq := httptest.NewRequest(http.MethodPost, "/oauth/register", bytes.NewBufferString(`{"client_name":"Claude","redirect_uris":["http://127.0.0.1:4567/callback"]}`))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRR := httptest.NewRecorder()
	mux.ServeHTTP(registerRR, registerReq)

	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRR.Code, registerRR.Body.String())
	}

	var registered registerClientResponse
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	redirectURIs, clientName, createdAt, err := DecryptClientID(registered.ClientID, []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("DecryptClientID() error = %v", err)
	}
	if clientName != "Claude" {
		t.Fatalf("client name = %q, want %q", clientName, "Claude")
	}
	if len(redirectURIs) != 1 || redirectURIs[0] != "http://127.0.0.1:4567/callback" {
		t.Fatalf("redirect URIs = %v", redirectURIs)
	}
	if createdAt.Unix() != registered.ClientIDIssuedAt {
		t.Fatalf("client_id issued at = %d, want %d", registered.ClientIDIssuedAt, createdAt.Unix())
	}

	verifier := "s3cr3t-pkce-verifier-that-is-long-enough-for-rfc7636"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authorizeURL := "/oauth/authorize?response_type=code&client_id=" + url.QueryEscape(registered.ClientID) +
		"&redirect_uri=" + url.QueryEscape("http://127.0.0.1:4567/callback") +
		"&state=" + url.QueryEscape("state-123") +
		"&code_challenge=" + url.QueryEscape(challenge) +
		"&code_challenge_method=S256&scope=" + url.QueryEscape("openid profile")

	authorizeReq := httptest.NewRequest(http.MethodGet, authorizeURL, nil)
	authorizeRR := httptest.NewRecorder()
	mux.ServeHTTP(authorizeRR, authorizeReq)

	if authorizeRR.Code != http.StatusOK {
		t.Fatalf("authorize GET status = %d, body = %s", authorizeRR.Code, authorizeRR.Body.String())
	}

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}
	csrfToken := matches[1]

	authorizeResult := authorizeRR.Result()
	if len(authorizeResult.Cookies()) == 0 {
		t.Fatalf("expected CSRF cookie to be set")
	}
	csrfCookie := authorizeResult.Cookies()[0]

	form := url.Values{
		"client_id":             {registered.ClientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"openid profile"},
		"csrf_token":            {csrfToken},
		"signoz_url":            {"https://tenant.example.com"},
		"api_key":               {"snz-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(csrfCookie)
	submitRR := httptest.NewRecorder()
	mux.ServeHTTP(submitRR, submitReq)

	if submitRR.Code != http.StatusFound {
		t.Fatalf("authorize POST status = %d, body = %s", submitRR.Code, submitRR.Body.String())
	}

	location := submitRR.Header().Get("Location")
	redirected, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if redirected.Query().Get("state") != "state-123" {
		t.Fatalf("state = %q, want %q", redirected.Query().Get("state"), "state-123")
	}
	code := redirected.Query().Get("code")
	if code == "" {
		t.Fatalf("authorization code missing from redirect location %q", location)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {registered.ClientID},
		"code":          {code},
		"redirect_uri":  {"http://127.0.0.1:4567/callback"},
		"code_verifier": {verifier},
	}

	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", bytes.NewBufferString(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRR := httptest.NewRecorder()
	mux.ServeHTTP(tokenRR, tokenReq)

	if tokenRR.Code != http.StatusOK {
		t.Fatalf("token status = %d, body = %s", tokenRR.Code, tokenRR.Body.String())
	}

	var tokenResult tokenResponse
	if err := json.Unmarshal(tokenRR.Body.Bytes(), &tokenResult); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	if tokenResult.TokenType != "Bearer" || tokenResult.AccessToken == "" || tokenResult.RefreshToken == "" {
		t.Fatalf("unexpected token response: %+v", tokenResult)
	}

	apiKey, signozURL, clientID, expiresAt, err := DecryptToken(tokenResult.AccessToken, []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("DecryptToken() error = %v", err)
	}
	if apiKey != "snz-api-key" || signozURL != "https://tenant.example.com" || clientID != registered.ClientID {
		t.Fatalf("decrypted token payload mismatch: apiKey=%q signozURL=%q clientID=%q", apiKey, signozURL, clientID)
	}
	if expiresAt.Before(time.Now().UTC()) {
		t.Fatalf("access token already expired at %v", expiresAt)
	}

	refreshAPIKey, refreshSignozURL, refreshClientID, refreshExpiresAt, err := DecryptRefreshToken(tokenResult.RefreshToken, []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("DecryptRefreshToken() error = %v", err)
	}
	if refreshAPIKey != "snz-api-key" || refreshSignozURL != "https://tenant.example.com" || refreshClientID != registered.ClientID {
		t.Fatalf("decrypted refresh token payload mismatch: apiKey=%q signozURL=%q clientID=%q", refreshAPIKey, refreshSignozURL, refreshClientID)
	}
	if refreshExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("refresh token already expired at %v", refreshExpiresAt)
	}
}

func TestRegisterClientAcceptsIPv6LoopbackRedirectURI(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	handler := NewHandler(zap.NewNop(), cfg)
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", bytes.NewBufferString(`{"client_name":"Claude","redirect_uris":["http://[::1]:4567/callback"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleRegisterClient(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestRegisterClientRejectsUnsupportedCustomRedirectScheme(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	handler := NewHandler(zap.NewNop(), cfg)
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", bytes.NewBufferString(`{"client_name":"Claude","redirect_uris":["javascript:alert(1)"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleRegisterClient(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("register status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not supported") {
		t.Fatalf("register body = %q, want unsupported scheme error", rr.Body.String())
	}
}

func TestAuthorizePageUsesIssuerPathPrefixForFormAndCSRFCookie(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com/signoz-mcp",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(zap.NewNop(), cfg)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

	if authorizeRR.Code != http.StatusOK {
		t.Fatalf("authorize GET status = %d, body = %s", authorizeRR.Code, authorizeRR.Body.String())
	}
	if !strings.Contains(authorizeRR.Body.String(), `action="/signoz-mcp/oauth/authorize"`) {
		t.Fatalf("authorize page action missing issuer path prefix: %s", authorizeRR.Body.String())
	}

	authorizeResult := authorizeRR.Result()
	if len(authorizeResult.Cookies()) == 0 {
		t.Fatalf("expected CSRF cookie to be set")
	}
	csrfCookie := authorizeResult.Cookies()[0]
	if csrfCookie.Path != "/signoz-mcp/oauth/authorize" {
		t.Fatalf("csrf cookie path = %q, want %q", csrfCookie.Path, "/signoz-mcp/oauth/authorize")
	}

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {"https://tenant.example.com"},
		"api_key":               {"snz-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(csrfCookie)
	submitRR := httptest.NewRecorder()
	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusFound {
		t.Fatalf("authorize POST status = %d, body = %s", submitRR.Code, submitRR.Body.String())
	}
	if !strings.Contains(submitRR.Header().Get("Set-Cookie"), "Path=/signoz-mcp/oauth/authorize") {
		t.Fatalf("clearing CSRF cookie missing issuer path prefix: %s", submitRR.Header().Get("Set-Cookie"))
	}
}
