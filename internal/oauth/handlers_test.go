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

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func TestOAuthAuthorizationFlow(t *testing.T) {
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "snz-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","message":"Unauthorized"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AccessTokenTTL:   time.Hour,
		RefreshTokenTTL:  24 * time.Hour,
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
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
		"signoz_url":            {signozServer.URL},
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
	if apiKey != "snz-api-key" || signozURL != signozServer.URL || clientID != registered.ClientID {
		t.Fatalf("decrypted token payload mismatch: apiKey=%q signozURL=%q clientID=%q", apiKey, signozURL, clientID)
	}
	if expiresAt.Before(time.Now().UTC()) {
		t.Fatalf("access token already expired at %v", expiresAt)
	}

	refreshAPIKey, refreshSignozURL, refreshClientID, refreshExpiresAt, err := DecryptRefreshToken(tokenResult.RefreshToken, []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("DecryptRefreshToken() error = %v", err)
	}
	if refreshAPIKey != "snz-api-key" || refreshSignozURL != signozServer.URL || refreshClientID != registered.ClientID {
		t.Fatalf("decrypted refresh token payload mismatch: apiKey=%q signozURL=%q clientID=%q", refreshAPIKey, refreshSignozURL, refreshClientID)
	}
	if refreshExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("refresh token already expired at %v", refreshExpiresAt)
	}
}

// TestAuthorizeSubmitRejectsJSON404FromServiceAccountsMe verifies a JSON 404
// from the service-account endpoint is surfaced as an unreachable-instance
// error rather than being retried against another endpoint.
func TestAuthorizeSubmitRejectsJSON404FromServiceAccountsMe(t *testing.T) {
	saRequests := 0
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		saRequests++
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"error","message":"not found"}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AccessTokenTTL:   time.Hour,
		RefreshTokenTTL:  24 * time.Hour,
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/register", handler.HandleRegisterClient)
	mux.HandleFunc("GET /oauth/authorize", handler.HandleAuthorizePage)
	mux.HandleFunc("POST /oauth/authorize", handler.HandleAuthorizeSubmit)

	// Register client.
	registerRR := httptest.NewRecorder()
	mux.ServeHTTP(registerRR, httptest.NewRequest(http.MethodPost, "/oauth/register",
		bytes.NewBufferString(`{"client_name":"Claude","redirect_uris":["http://127.0.0.1:4567/callback"]}`)))
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRR.Code, registerRR.Body.String())
	}
	var registered registerClientResponse
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode register response: %v", err)
	}

	// Build PKCE challenge.
	verifier := "s3cr3t-pkce-verifier-that-is-long-enough-for-rfc7636"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// GET /oauth/authorize to obtain CSRF token.
	authorizeURL := "/oauth/authorize?response_type=code&client_id=" + url.QueryEscape(registered.ClientID) +
		"&redirect_uri=" + url.QueryEscape("http://127.0.0.1:4567/callback") +
		"&state=state-sa-404&code_challenge=" + url.QueryEscape(challenge) +
		"&code_challenge_method=S256"
	authorizeRR := httptest.NewRecorder()
	mux.ServeHTTP(authorizeRR, httptest.NewRequest(http.MethodGet, authorizeURL, nil))
	if authorizeRR.Code != http.StatusOK {
		t.Fatalf("authorize GET status = %d", authorizeRR.Code)
	}

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatal("csrf token not found in authorize page")
	}
	csrfCookie := authorizeRR.Result().Cookies()[0]

	// POST /oauth/authorize — this is the step that validates credentials.
	form := url.Values{
		"client_id":             {registered.ClientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-sa-404"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {signozServer.URL},
		"api_key":               {"snz-api-key"},
	}
	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(csrfCookie)
	submitRR := httptest.NewRecorder()
	mux.ServeHTTP(submitRR, submitReq)

	if submitRR.Code != http.StatusBadGateway {
		t.Fatalf("authorize POST status = %d, want %d; body = %s", submitRR.Code, http.StatusBadGateway, submitRR.Body.String())
	}
	if submitRR.Header().Get("Location") != "" {
		t.Fatalf("unexpected redirect location %q", submitRR.Header().Get("Location"))
	}
	if !strings.Contains(submitRR.Body.String(), "We couldn&#39;t reach that SigNoz instance. Check the URL and try again.") {
		t.Fatalf("authorize POST body = %s", submitRR.Body.String())
	}
	if saRequests != 1 {
		t.Fatalf("service_accounts/me calls = %d, want 1", saRequests)
	}
}

func TestAuthorizeSubmitRejectsInvalidSigNozCredentials(t *testing.T) {
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","message":"Unauthorized"}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&state=state-123&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

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

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {csrfToken},
		"signoz_url":            {signozServer.URL},
		"api_key":               {"bad-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(authorizeResult.Cookies()[0])
	submitRR := httptest.NewRecorder()

	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusUnauthorized {
		t.Fatalf("authorize POST status = %d, want %d, body = %s", submitRR.Code, http.StatusUnauthorized, submitRR.Body.String())
	}
	if !strings.Contains(submitRR.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q, want HTML", submitRR.Header().Get("Content-Type"))
	}
	if submitRR.Header().Get("Location") != "" {
		t.Fatalf("unexpected redirect location %q", submitRR.Header().Get("Location"))
	}
	if !strings.Contains(submitRR.Body.String(), "We couldn&#39;t sign in to that SigNoz instance. Check the URL and API key, then try again.") {
		t.Fatalf("authorize POST body = %s", submitRR.Body.String())
	}
	if !strings.Contains(submitRR.Body.String(), `value="`+signozServer.URL+`"`) {
		t.Fatalf("authorize POST should preserve signoz_url, body = %s", submitRR.Body.String())
	}
	if strings.Contains(submitRR.Body.String(), `value="bad-api-key"`) {
		t.Fatalf("authorize POST should not echo the api key, body = %s", submitRR.Body.String())
	}
}

func TestAuthorizeSubmitStripsSigNozURLPathQueryFragment(t *testing.T) {
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "snz-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&state=state-123&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}
	authorizeResult := authorizeRR.Result()
	if len(authorizeResult.Cookies()) == 0 {
		t.Fatalf("expected CSRF cookie to be set")
	}

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {signozServer.URL + "/home?orgId=123#logs"},
		"api_key":               {"snz-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(authorizeResult.Cookies()[0])
	submitRR := httptest.NewRecorder()
	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusFound {
		t.Fatalf("authorize POST status = %d, want %d, body = %s", submitRR.Code, http.StatusFound, submitRR.Body.String())
	}
	redirected, err := url.Parse(submitRR.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	code := redirected.Query().Get("code")
	if code == "" {
		t.Fatalf("authorization code missing from redirect %q", submitRR.Header().Get("Location"))
	}

	_, signozURL, _, _, _, _, _, err := DecryptAuthorizationCode(code, []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("DecryptAuthorizationCode() error = %v", err)
	}
	if signozURL != signozServer.URL {
		t.Fatalf("authorization code signozURL = %q, want %q", signozURL, signozServer.URL)
	}
}

func TestRegisterClientAcceptsIPv6LoopbackRedirectURI(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
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

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
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
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/service_accounts/me" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "snz-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","message":"Unauthorized"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com/signoz-mcp",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
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
		"signoz_url":            {signozServer.URL},
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

func TestTokenRejectsUnsupportedGrantType(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", bytes.NewBufferString("grant_type=bogus"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["error"] != "unsupported_grant_type" {
		t.Fatalf("error body = %s, want error=unsupported_grant_type", rr.Body.String())
	}
}

// TestAuthorizeSubmitExpiredWorkspaceRendersInstanceNotFound covers the
// expired/deactivated cloud workspace case: the instance URL answers with the
// ingress's HTML 404 page instead of a SigNoz API response. The user must get
// a permanent error pointing at workspace status — not "try again".
func TestAuthorizeSubmitExpiredWorkspaceRendersInstanceNotFound(t *testing.T) {
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><title>404 : This page does not exist :/</title></head>
<body>Either the workspace has expired or the workspace does not exist.</body></html>`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&state=state-123&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {signozServer.URL},
		"api_key":               {"some-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(authorizeRR.Result().Cookies()[0])
	submitRR := httptest.NewRecorder()
	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusNotFound {
		t.Fatalf("authorize POST status = %d, want %d, body = %s", submitRR.Code, http.StatusNotFound, submitRR.Body.String())
	}
	if !strings.Contains(submitRR.Body.String(), "the workspace may have been deactivated") {
		t.Fatalf("authorize POST body should point at workspace status, body = %s", submitRR.Body.String())
	}
	if strings.Contains(submitRR.Body.String(), "Check the URL and try again") {
		t.Fatalf("authorize POST body should not suggest retrying, body = %s", submitRR.Body.String())
	}
}

// TestAuthorizeSubmitTransientFailureStaysTemporarilyUnavailable pins the
// transient path: a 503 from the instance keeps the retryable 502
// temporarily_unavailable response.
func TestAuthorizeSubmitTransientFailureStaysTemporarilyUnavailable(t *testing.T) {
	signozServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error","message":"upgrade in progress"}`))
	}))
	defer signozServer.Close()

	cfg := &config.Config{
		OAuthEnabled:     true,
		OAuthTokenSecret: "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:   "https://mcp.example.com",
		AuthCodeTTL:      10 * time.Minute,
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&state=state-123&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {signozServer.URL},
		"api_key":               {"some-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(authorizeRR.Result().Cookies()[0])
	submitRR := httptest.NewRecorder()
	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusBadGateway {
		t.Fatalf("authorize POST status = %d, want %d, body = %s", submitRR.Code, http.StatusBadGateway, submitRR.Body.String())
	}
	if !strings.Contains(submitRR.Body.String(), "Check the URL and try again") {
		t.Fatalf("authorize POST body should keep the retry guidance, body = %s", submitRR.Body.String())
	}
}

func TestRefreshTokenGrantRejectsDisallowedInstanceURL(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:         true,
		OAuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:       "https://mcp.example.com",
		AccessTokenTTL:       time.Hour,
		RefreshTokenTTL:      24 * time.Hour,
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("*.us.signoz.cloud"),
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)

	// A long-lived refresh token issued for a SigNoz instance that is no longer allowed.
	refreshToken, err := EncryptRefreshToken("api-key", "https://selfhosted.example.com", "client-123",
		time.Now().UTC().Add(time.Hour), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptRefreshToken() error = %v", err)
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "access_token") {
		t.Fatalf("disallowed SigNoz URL must not be issued tokens, body = %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_grant") {
		t.Fatalf("expected invalid_grant error, body = %s", rr.Body.String())
	}
}

func TestAuthorizeSubmitRejectsDisallowedInstanceURL(t *testing.T) {
	cfg := &config.Config{
		OAuthEnabled:         true,
		OAuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:       "https://mcp.example.com",
		AuthCodeTTL:          10 * time.Minute,
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("*.us.signoz.cloud"),
	}

	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	clientID, err := EncryptClientID([]string{"http://127.0.0.1:4567/callback"}, "Claude", time.Now().UTC(), []byte(cfg.OAuthTokenSecret))
	if err != nil {
		t.Fatalf("EncryptClientID() error = %v", err)
	}

	authorizeReq := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?response_type=code&client_id="+url.QueryEscape(clientID)+
			"&redirect_uri="+url.QueryEscape("http://127.0.0.1:4567/callback")+
			"&state=state-123&code_challenge=challenge&code_challenge_method=S256",
		nil,
	)
	authorizeRR := httptest.NewRecorder()
	handler.HandleAuthorizePage(authorizeRR, authorizeReq)

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(authorizeRR.Body.String())
	if len(matches) != 2 {
		t.Fatalf("csrf token not found in authorize page: %s", authorizeRR.Body.String())
	}

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:4567/callback"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {matches[1]},
		"signoz_url":            {"https://selfhosted.example.com"}, // disallowed; rejected before any credential probe
		"api_key":               {"some-api-key"},
	}

	submitReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize", bytes.NewBufferString(form.Encode()))
	submitReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	submitReq.AddCookie(authorizeRR.Result().Cookies()[0])
	submitRR := httptest.NewRecorder()
	handler.HandleAuthorizeSubmit(submitRR, submitReq)

	if submitRR.Code != http.StatusForbidden {
		t.Fatalf("authorize POST status = %d, want %d, body = %s", submitRR.Code, http.StatusForbidden, submitRR.Body.String())
	}
}
