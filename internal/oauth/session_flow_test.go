package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	testRedirectURI = "http://127.0.0.1:4567/callback"
	testVerifier    = "s3cr3t-pkce-verifier-that-is-long-enough-for-rfc7636"
	testOrgID       = "0196f1a2-0000-7000-8000-000000000001"
	testEmail       = "ada@example.com"
)

// fakeInstance is a SigNoz instance's session API. It records what the
// MCP server sends so tests assert the upstream contract.
type fakeInstance struct {
	*httptest.Server

	mu            sync.Mutex
	contextRef    string
	passwordBody  map[string]string
	rotateAuth    string
	rotateBody    map[string]string
	deletedAuth   []string
	ssoEnabled    bool
	ssoWarning    string
	rotateRejects bool
}

func newFakeInstance(t *testing.T) *fakeInstance {
	t.Helper()
	d := &fakeInstance{}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.Close)
	return d
}

func (d *fakeInstance) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	writeData := func(data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v2/sessions/context":
		d.contextRef = r.URL.Query().Get("ref")
		org := map[string]any{
			"id": testOrgID, "name": "Acme",
			"authNSupport": map[string]any{"password": []any{map[string]any{"provider": "email_password"}}, "callback": []any{}},
		}
		if d.ssoEnabled {
			org["authNSupport"].(map[string]any)["callback"] = []any{map[string]any{
				"provider": "oidc",
				"url":      "https://idp.example.com/authorize?state=" + url.QueryEscape(d.contextRef),
			}}
		}
		if d.ssoWarning != "" {
			org["warning"] = map[string]any{"code": d.ssoWarning, "message": "not allowed"}
		}
		writeData(map[string]any{"exists": true, "orgs": []any{org}})
	case "POST /api/v2/sessions/email_password":
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &d.passwordBody)
		if d.passwordBody["password"] != "correct horse" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unauthenticated","message":"invalid password"}}`))
			return
		}
		writeData(map[string]any{"tokenType": "bearer", "accessToken": "access-1", "refreshToken": "refresh-1", "expiresIn": 1800})
	case "POST /api/v2/sessions/rotate":
		d.rotateAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &d.rotateBody)
		if d.rotateRejects {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unauthenticated","message":"invalid token pair"}}`))
			return
		}
		writeData(map[string]any{"tokenType": "bearer", "accessToken": "access-2", "refreshToken": "refresh-2", "expiresIn": 7200})
	case "DELETE /api/v2/sessions":
		d.deletedAuth = append(d.deletedAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

type sessionFlow struct {
	t          *testing.T
	cfg        *config.Config
	mux        *http.ServeMux
	instance   *fakeInstance
	clientID   string
	challenge  string
	csrfCookie *http.Cookie
	csrfToken  string
}

func newSessionFlow(t *testing.T) *sessionFlow {
	t.Helper()
	instance := newFakeInstance(t)
	cfg := &config.Config{
		OAuthEnabled:         true,
		OAuthTokenSecret:     "0123456789abcdef0123456789abcdef",
		OAuthIssuerURL:       "https://mcp.example.com",
		InstanceURLAllowlist: util.ParseInstanceURLAllowlist("127.0.0.1"),
		AccessTokenTTL:       time.Hour,
		RefreshTokenTTL:      24 * time.Hour,
		AuthCodeTTL:          10 * time.Minute,
	}
	handler := NewHandler(logpkg.New("error"), cfg, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/register", handler.HandleRegisterClient)
	mux.HandleFunc("GET /oauth/authorize", handler.HandleAuthorizePage)
	mux.HandleFunc("POST /oauth/authorize", handler.HandleAuthorizeSubmit)
	mux.HandleFunc("POST /oauth/token", handler.HandleToken)
	mux.HandleFunc("POST /oauth/revoke", handler.HandleRevoke)
	mux.HandleFunc("GET /oauth/sso-callback/{nonce}", handler.HandleSSOCallback)

	clientID, err := EncryptClientID([]string{testRedirectURI}, "Claude", time.Now(), []byte(cfg.OAuthTokenSecret))
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(testVerifier))
	f := &sessionFlow{
		t: t, cfg: cfg, mux: mux, instance: instance, clientID: clientID,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}
	f.openAuthorizePage()
	return f
}

func (f *sessionFlow) serve(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	f.mux.ServeHTTP(rr, req)
	return rr
}

func (f *sessionFlow) openAuthorizePage() string {
	query := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {testRedirectURI},
		"state": {"state-123"}, "code_challenge": {f.challenge}, "code_challenge_method": {"S256"},
	}
	rr := f.serve(httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil))
	require.Equal(f.t, http.StatusOK, rr.Code, rr.Body.String())
	f.captureCSRF(rr)
	return rr.Body.String()
}

func (f *sessionFlow) captureCSRF(rr *httptest.ResponseRecorder) {
	matches := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(rr.Body.String())
	require.Len(f.t, matches, 2, "csrf token missing")
	f.csrfToken = matches[1]
	for _, c := range rr.Result().Cookies() {
		if c.Name == csrfCookieName {
			f.csrfCookie = c
		}
	}
}

func (f *sessionFlow) submit(fields url.Values) *httptest.ResponseRecorder {
	form := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {testRedirectURI}, "state": {"state-123"},
		"code_challenge": {f.challenge}, "code_challenge_method": {"S256"}, "csrf_token": {f.csrfToken},
	}
	for k, v := range fields {
		form[k] = v
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(f.csrfCookie)
	rr := f.serve(req)
	if rr.Code == http.StatusOK || rr.Code >= 400 && strings.Contains(rr.Body.String(), "csrf_token") {
		f.captureCSRF(rr)
	}
	return rr
}

func (f *sessionFlow) discover() *httptest.ResponseRecorder {
	return f.submit(url.Values{"step": {"discover"}, "signoz_url": {f.instance.URL}, "email": {testEmail}})
}

func (f *sessionFlow) exchange(code, verifier string) *httptest.ResponseRecorder {
	return f.postForm("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {f.clientID}, "code": {code},
		"redirect_uri": {testRedirectURI}, "code_verifier": {verifier},
	})
}

func (f *sessionFlow) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.serve(req)
}

func codeFromRedirect(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusFound, rr.Code, rr.Body.String())
	location, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:4567", location.Host)
	require.Equal(t, "state-123", location.Query().Get("state"))
	code := location.Query().Get("code")
	require.NotEmpty(t, code)
	return code
}

func decodeTokens(t *testing.T, rr *httptest.ResponseRecorder) tokenResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	var tokens tokenResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tokens))
	return tokens
}

func TestAuthorizePageOffersSignInAndAPIKey(t *testing.T) {
	f := newSessionFlow(t)
	page := f.openAuthorizePage()
	require.Contains(t, page, `name="email"`)
	require.Contains(t, page, `name="api_key"`)
	require.Contains(t, page, "Claude (127.0.0.1:4567)", "consent must show the redirect host beside the self-asserted name")
}

func TestPasswordSignInIssuesSessionTokens(t *testing.T) {
	f := newSessionFlow(t)

	page := f.discover()
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	require.Contains(t, page.Body.String(), `name="password"`)
	require.Contains(t, page.Body.String(), "127.0.0.1")

	wrong := f.submit(url.Values{"step": {"password"}, "signoz_url": {f.instance.URL}, "email": {testEmail}, "org_id": {testOrgID}, "password": {"nope"}})
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	require.Contains(t, html.UnescapeString(wrong.Body.String()), "That email and password didn't work")

	ok := f.submit(url.Values{"step": {"password"}, "signoz_url": {f.instance.URL}, "email": {testEmail}, "org_id": {testOrgID}, "password": {"correct horse"}})
	code := codeFromRedirect(t, ok)
	require.Equal(t, map[string]string{"email": testEmail, "password": "correct horse", "orgId": testOrgID}, f.instance.passwordBody)

	require.Equal(t, http.StatusBadRequest, f.exchange(code, strings.Repeat("x", 43)).Code, "a wrong PKCE verifier must fail")

	tokens := decodeTokens(t, f.exchange(code, testVerifier))
	signozURL, access, parsed := ParseSessionAccessToken(tokens.AccessToken)
	require.True(t, parsed, tokens.AccessToken)
	require.Equal(t, f.instance.URL, signozURL)
	require.Equal(t, "access-1", access)
	require.InDelta(t, 1800, tokens.ExpiresIn, 5, "expires_in follows SigNoz's expiresIn when it is under the cap")

	session, clientID, err := DecryptSessionRefreshToken(tokens.RefreshToken, []byte(f.cfg.OAuthTokenSecret))
	require.NoError(t, err)
	require.Equal(t, f.clientID, clientID)
	require.Equal(t, "refresh-1", session.RefreshToken)
}

func TestSessionRefreshRotatesAndRevokeEndsSession(t *testing.T) {
	f := newSessionFlow(t)
	f.discover()
	code := codeFromRedirect(t, f.submit(url.Values{"step": {"password"}, "signoz_url": {f.instance.URL}, "email": {testEmail}, "org_id": {testOrgID}, "password": {"correct horse"}}))
	first := decodeTokens(t, f.exchange(code, testVerifier))

	refreshed := decodeTokens(t, f.postForm("/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {f.clientID}, "refresh_token": {first.RefreshToken},
	}))
	require.Equal(t, "Bearer access-1", f.instance.rotateAuth, "rotate needs the access token as bearer")
	require.Equal(t, map[string]string{"refreshToken": "refresh-1"}, f.instance.rotateBody)
	_, access, _ := ParseSessionAccessToken(refreshed.AccessToken)
	require.Equal(t, "access-2", access)
	require.Equal(t, 3600, refreshed.ExpiresIn, "expires_in is capped at the configured access TTL")

	otherClient := f.postForm("/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"someone-else"}, "refresh_token": {refreshed.RefreshToken},
	})
	require.Equal(t, http.StatusBadRequest, otherClient.Code)

	revoke := f.postForm("/oauth/revoke", url.Values{"token": {refreshed.RefreshToken}, "client_id": {f.clientID}})
	require.Equal(t, http.StatusOK, revoke.Code, revoke.Body.String())
	require.Equal(t, []string{"Bearer access-2"}, f.instance.deletedAuth)

	f.instance.rotateRejects = true
	ended := f.postForm("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshed.RefreshToken}})
	require.Equal(t, http.StatusBadRequest, ended.Code)
	require.Contains(t, ended.Body.String(), "invalid_grant", "an ended SigNoz session sends the client back to sign-in")
}

func TestSSOCallbackCorrelatesPendingAuthorization(t *testing.T) {
	f := newSessionFlow(t)
	f.instance.ssoEnabled = true
	page := f.discover()
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	require.Contains(t, html.UnescapeString(page.Body.String()), "https://idp.example.com/authorize")

	ref, err := url.Parse(f.instance.contextRef)
	require.NoError(t, err)
	require.Equal(t, "mcp.example.com", ref.Host)
	require.True(t, strings.HasPrefix(ref.Path, ssoCallbackPath))
	nonce := strings.TrimPrefix(ref.Path, ssoCallbackPath)

	var pendingCookie *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == ssoCookiePrefix+nonce {
			pendingCookie = c
		}
	}
	require.NotNil(t, pendingCookie, "discover must set the per-nonce pending cookie")
	require.True(t, pendingCookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, pendingCookie.SameSite)
	require.Equal(t, ref.Path, pendingCookie.Path)

	// SigNoz replaces ref's query with the token parameters.
	tokenQuery := "?tokenType=bearer&accessToken=sso-access&refreshToken=sso-refresh&expiresIn=600"
	callback := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		return f.serve(req)
	}

	require.Equal(t, http.StatusBadRequest, callback(ref.Path+tokenQuery, nil).Code, "no pending cookie: not this browser's sign-in")
	forged := &http.Cookie{Name: ssoCookiePrefix + "other", Value: pendingCookie.Value}
	require.Equal(t, http.StatusBadRequest, callback(ssoCallbackPath+"other"+tokenQuery, forged).Code, "a cookie for another nonce must not match")

	code := codeFromRedirect(t, callback(ref.Path+tokenQuery, pendingCookie))
	tokens := decodeTokens(t, f.exchange(code, testVerifier))
	_, access, _ := ParseSessionAccessToken(tokens.AccessToken)
	require.Equal(t, "sso-access", access)
	require.InDelta(t, 600, tokens.ExpiresIn, 5)
}

func TestSSOCallbackWithoutTokensReturnsErrorToClient(t *testing.T) {
	f := newSessionFlow(t)
	f.instance.ssoEnabled = true
	page := f.discover()
	ref, _ := url.Parse(f.instance.contextRef)
	nonce := strings.TrimPrefix(ref.Path, ssoCallbackPath)
	req := httptest.NewRequest(http.MethodGet, ref.Path, nil)
	for _, c := range page.Result().Cookies() {
		if c.Name == ssoCookiePrefix+nonce {
			req.AddCookie(c)
		}
	}
	rr := f.serve(req)
	require.Equal(t, http.StatusFound, rr.Code)
	location, _ := url.Parse(rr.Header().Get("Location"))
	require.Equal(t, "access_denied", location.Query().Get("error"))
	require.Equal(t, "state-123", location.Query().Get("state"))
}

func TestSignInExplainsSSOBlockedByAllowedOrigins(t *testing.T) {
	f := newSessionFlow(t)
	f.instance.ssoWarning = "origin_not_allowed"
	page := f.discover()
	require.Equal(t, http.StatusOK, page.Code)
	body := html.UnescapeString(page.Body.String())
	require.Contains(t, body, "doesn't allow single sign-on from mcp.example.com")
	require.Contains(t, body, `name="password"`)
}

func TestAuthorizationCodeWithAPIKeyStillExchanges(t *testing.T) {
	f := newSessionFlow(t)
	code, err := EncryptAuthorizationCode("snz-api-key", f.instance.URL, f.clientID, testRedirectURI, f.challenge, "S256", time.Now().Add(time.Minute), []byte(f.cfg.OAuthTokenSecret))
	require.NoError(t, err)
	tokens := decodeTokens(t, f.exchange(code, testVerifier))
	apiKey, _, _, _, err := DecryptToken(tokens.AccessToken, []byte(f.cfg.OAuthTokenSecret))
	require.NoError(t, err)
	require.Equal(t, "snz-api-key", apiKey)
}

func TestRevokeNeverDialsANonASCIIInstanceHost(t *testing.T) {
	f := newSessionFlow(t)
	// The wildcard matches the lowercased host, but net/http would dial its
	// different punycode form, so the token must be rejected before any dial.
	f.cfg.InstanceURLAllowlist = util.ParseInstanceURLAllowlist("127.0.0.1,*.example.com")
	token := FormatSessionAccessToken("https://a.sİgnoz.example.com", "forged-access")

	rr := f.postForm("/oauth/revoke", url.Values{"token": {token}})

	require.Equal(t, http.StatusOK, rr.Code, "an unusable token is a no-op revocation, not an upstream call: %s", rr.Body.String())
	require.Empty(t, f.instance.deletedAuth)
}

func TestSSOIsWithheldWhenThePendingCookieWouldBeTooLarge(t *testing.T) {
	f := newSessionFlow(t)
	f.instance.ssoEnabled = true
	longState := strings.Repeat("s", 3000)

	page := f.submit(url.Values{"step": {"discover"}, "signoz_url": {f.instance.URL}, "email": {testEmail}, "state": {longState}})

	require.Equal(t, http.StatusOK, page.Code, "password sign-in still works, so the page renders")
	body := html.UnescapeString(page.Body.String())
	require.Contains(t, body, "the MCP client's state parameter is too long (3000 characters)")
	require.NotContains(t, body, "https://idp.example.com/authorize", "an SSO button would fail after the IdP sign-in")
	require.Contains(t, body, `name="password"`)
	for _, c := range page.Result().Cookies() {
		require.False(t, strings.HasPrefix(c.Name, ssoCookiePrefix), "a browser would drop the oversized cookie")
	}
}
