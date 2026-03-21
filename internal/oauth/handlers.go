package oauth

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
)

const csrfCookieName = "signoz_mcp_oauth_csrf"

//go:embed static/authorize.html
var authorizeTemplateFS embed.FS

var authorizePageTemplate = template.Must(template.ParseFS(authorizeTemplateFS, "static/authorize.html"))

type Handler struct {
	logger            *zap.Logger
	config            *config.Config
	tokenSecret       []byte
	authorizeTemplate *template.Template
}

type registerClientRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type registerClientResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type authorizeTemplateData struct {
	ClientID            string
	ClientName          string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	CSRFToken           string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func NewHandler(logger *zap.Logger, cfg *config.Config) *Handler {
	return &Handler{
		logger:            logger,
		config:            cfg,
		tokenSecret:       []byte(cfg.OAuthTokenSecret),
		authorizeTemplate: authorizePageTemplate,
	}
}

func (h *Handler) HandleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 h.issuerURL() + "/mcp",
		"authorization_servers":    []string{h.issuerURL()},
		"bearer_methods_supported": []string{"header"},
	})
}

func (h *Handler) HandleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                h.issuerURL(),
		"authorization_endpoint":                h.issuerURL() + "/oauth/authorize",
		"token_endpoint":                        h.issuerURL() + "/oauth/token",
		"registration_endpoint":                 h.issuerURL() + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

func (h *Handler) HandleRegisterClient(w http.ResponseWriter, r *http.Request) {
	var req registerClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be valid JSON")
		return
	}

	req.ClientName = strings.TrimSpace(req.ClientName)
	if req.ClientName == "" {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name is required")
		return
	}
	if len(req.RedirectURIs) == 0 {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect URI is required")
		return
	}

	for _, redirectURI := range req.RedirectURIs {
		if err := validateRedirectURI(redirectURI); err != nil {
			h.writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}

	createdAt := time.Now().UTC()
	clientID, err := EncryptClientID(req.RedirectURIs, req.ClientName, createdAt, h.tokenSecret)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to register client")
		return
	}

	h.writeJSON(w, http.StatusCreated, registerClientResponse{
		ClientID:                clientID,
		ClientIDIssuedAt:        createdAt.Unix(),
		ClientName:              req.ClientName,
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
}

func (h *Handler) HandleAuthorizePage(w http.ResponseWriter, r *http.Request) {
	params, err := h.validateAuthorizeRequest(r.URL.Query())
	if err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	csrfToken, err := randomURLSafeString(32)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate CSRF token")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/oauth/authorize",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.isSecure(),
		MaxAge:   int(h.config.AuthCodeTTL.Seconds()),
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.authorizeTemplate.Execute(w, authorizeTemplateData{
		ClientID:            params.ClientID,
		ClientName:          params.ClientName,
		RedirectURI:         params.RedirectURI,
		State:               params.State,
		CodeChallenge:       params.CodeChallenge,
		CodeChallengeMethod: params.CodeChallengeMethod,
		Scope:               params.Scope,
		CSRFToken:           csrfToken,
	}); err != nil {
		h.logger.Error("failed to render authorization page", zap.Error(err))
	}
}

func (h *Handler) HandleAuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "form data is required")
		return
	}

	if !h.validateCSRF(r) {
		h.writeOAuthError(w, http.StatusForbidden, "access_denied", "invalid CSRF token")
		return
	}

	params, err := h.validateAuthorizeRequest(r.PostForm)
	if err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	signozURL := strings.TrimSpace(r.FormValue("signoz_url"))
	if apiKey == "" {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "api_key is required")
		return
	}
	normalizedURL, err := normalizeSigNozURL(signozURL)
	if err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("invalid SigNoz URL: %v", err))
		return
	}

	code, err := EncryptAuthorizationCode(
		apiKey,
		normalizedURL,
		params.ClientID,
		params.RedirectURI,
		params.CodeChallenge,
		params.CodeChallengeMethod,
		time.Now().UTC().Add(h.config.AuthCodeTTL),
		h.tokenSecret,
	)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate authorization code")
		return
	}

	redirectURL, err := addAuthorizeResponse(params.RedirectURI, code, params.State)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to build redirect URL")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/oauth/authorize",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.isSecure(),
		MaxAge:   -1,
	})
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *Handler) HandleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "form data is required")
		return
	}

	switch r.FormValue("grant_type") {
	case "authorization_code":
		h.handleAuthorizationCodeGrant(w, r)
	case "refresh_token":
		h.handleRefreshTokenGrant(w, r)
	default:
		h.writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (h *Handler) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	code := strings.TrimSpace(r.FormValue("code"))
	redirectURI := strings.TrimSpace(r.FormValue("redirect_uri"))
	codeVerifier := strings.TrimSpace(r.FormValue("code_verifier"))

	if clientID == "" || code == "" || redirectURI == "" || codeVerifier == "" {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id, code, redirect_uri, and code_verifier are required")
		return
	}

	apiKey, signozURL, authClientID, authRedirectURI, codeChallenge, codeChallengeMethod, _, err := DecryptAuthorizationCode(code, h.tokenSecret)
	if err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if authClientID != clientID || authRedirectURI != redirectURI {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code does not match the client or redirect URI")
		return
	}
	if !ValidatePKCE(codeVerifier, codeChallenge, codeChallengeMethod) {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE validation failed")
		return
	}

	h.issueTokenPair(w, clientID, apiKey, signozURL)
}

func (h *Handler) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	refreshTokenValue := strings.TrimSpace(r.FormValue("refresh_token"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	if refreshTokenValue == "" {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	apiKey, signozURL, refreshClientID, _, err := DecryptRefreshToken(refreshTokenValue, h.tokenSecret)
	if err != nil {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	if clientID != "" && refreshClientID != clientID {
		h.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token does not belong to this client")
		return
	}

	h.issueTokenPair(w, refreshClientID, apiKey, signozURL)
}

func (h *Handler) issueTokenPair(w http.ResponseWriter, clientID, apiKey, signozURL string) {
	accessTokenExpiresAt := time.Now().UTC().Add(h.config.AccessTokenTTL)
	accessToken, err := EncryptToken(apiKey, signozURL, clientID, accessTokenExpiresAt, h.tokenSecret)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to create access token")
		return
	}

	refreshTokenValue, err := EncryptRefreshToken(
		apiKey,
		signozURL,
		clientID,
		time.Now().UTC().Add(h.config.RefreshTokenTTL),
		h.tokenSecret,
	)
	if err != nil {
		h.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to create refresh token")
		return
	}

	h.writeTokenResponse(w, tokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(h.config.AccessTokenTTL.Seconds()),
		RefreshToken: refreshTokenValue,
	})
}

func (h *Handler) validateAuthorizeRequest(values url.Values) (authorizeTemplateData, error) {
	clientID := strings.TrimSpace(values.Get("client_id"))
	redirectURI := strings.TrimSpace(values.Get("redirect_uri"))
	codeChallenge := strings.TrimSpace(values.Get("code_challenge"))
	codeChallengeMethod := strings.TrimSpace(values.Get("code_challenge_method"))

	if responseType := values.Get("response_type"); responseType != "" && responseType != "code" {
		return authorizeTemplateData{}, fmt.Errorf("response_type must be code")
	}
	if clientID == "" || redirectURI == "" {
		return authorizeTemplateData{}, fmt.Errorf("client_id and redirect_uri are required")
	}
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		return authorizeTemplateData{}, fmt.Errorf("code_challenge and code_challenge_method=S256 are required")
	}

	redirectURIs, clientName, _, err := DecryptClientID(clientID, h.tokenSecret)
	if err != nil {
		return authorizeTemplateData{}, fmt.Errorf("client_id is not registered")
	}
	if !registeredRedirectURI(redirectURIs, redirectURI) {
		return authorizeTemplateData{}, fmt.Errorf("redirect_uri does not match the registered client")
	}

	return authorizeTemplateData{
		ClientID:            clientID,
		ClientName:          clientName,
		RedirectURI:         redirectURI,
		State:               values.Get("state"),
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Scope:               values.Get("scope"),
	}, nil
}

func (h *Handler) validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return false
	}
	formToken := strings.TrimSpace(r.FormValue("csrf_token"))
	return formToken != "" && cookie.Value == formToken
}

func (h *Handler) writeTokenResponse(w http.ResponseWriter, resp tokenResponse) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	h.writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("failed to write JSON response", zap.Error(err))
	}
}

func (h *Handler) issuerURL() string {
	return strings.TrimSuffix(h.config.OAuthIssuerURL, "/")
}

// isSecure derives the Secure cookie flag from the issuer URL scheme rather
// than r.TLS, which is nil behind a TLS-terminating reverse proxy.
func (h *Handler) isSecure() bool {
	return strings.HasPrefix(h.config.OAuthIssuerURL, "https://")
}

func registeredRedirectURI(redirectURIs []string, redirectURI string) bool {
	for _, candidate := range redirectURIs {
		if candidate == redirectURI {
			return true
		}
	}
	return false
}

func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("redirect URI is malformed: %w", err)
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("redirect URI must include a scheme")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect URI fragments are not allowed")
	}

	host := strings.ToLower(parsed.Hostname())
	switch parsed.Scheme {
	case "http":
		// HTTP only allowed for loopback addresses (RFC 8252 §7.3)
		if host != "localhost" && host != "127.0.0.1" && host != "[::1]" {
			return fmt.Errorf("HTTP redirect URIs are only allowed for localhost, 127.0.0.1, or [::1]")
		}
	case "https":
		if host == "" {
			return fmt.Errorf("redirect URI host is required")
		}
	default:
		// Allow private-use URI schemes for native desktop apps (RFC 8252 §7.1).
		// MCP clients like Cursor and Claude Desktop use custom schemes
		// (e.g., cursor://, claude://) for OAuth callbacks.
	}

	return nil
}

func addAuthorizeResponse(redirectURI, code, state string) (string, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}

	query := parsed.Query()
	query.Set("code", code)
	if state != "" {
		query.Set("state", state)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func randomURLSafeString(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func normalizeSigNozURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("malformed URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme %q not allowed, must be http or https", parsed.Scheme)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a path")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without query parameters")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a fragment")
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	if host == "localhost" || host == "0.0.0.0" || host == "::" {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	origin := scheme + "://" + host
	if port != "" {
		origin += ":" + port
	}
	return origin, nil
}
