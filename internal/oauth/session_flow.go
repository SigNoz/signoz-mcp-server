package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	ssoCookiePrefix = "signoz_mcp_sso_"
	ssoCallbackPath = "/oauth/sso-callback/"

	stepStart    = "start"
	stepDiscover = "discover"
	stepPassword = "password"
	stepAPIKey   = "apikey"
	stepSignIn   = "signin"
)

// Failure reasons for SigNoz sign-in, emitted as mcp.auth.failure_reason.
const (
	authFailureSignInRejected     = "signin_rejected"
	authFailureSSOCallbackInvalid = "sso_callback_invalid"
	authFailureSessionRejected    = "session_rejected"
	authFailureSSORequestTooLarge = "sso_request_too_large"
)

// maxSSOCookieBytes keeps the pending-authorization cookie under the ~4096
// bytes browsers allow for a cookie's name and value.
const maxSSOCookieBytes = 3800

type orgView struct {
	ID       string
	Name     string
	Password bool
	SSO      []ssoView
	Warning  string
}

type ssoView struct {
	Label string
	URL   string
}

func (h *Handler) sessionClient(signozURL string) *client.SessionClient {
	signozURL = h.config.ResolveInstanceURL(signozURL)
	var headers map[string]string
	if signozURL == h.config.URL {
		headers = h.config.CustomHeaders
	}
	return client.NewSessionClient(h.logger, signozURL, headers)
}

// normalizeInstanceURL validates the instance field and the allowlist. It
// renders the error page itself and returns ok=false when the request ends.
func (h *Handler) normalizeInstanceURL(w http.ResponseWriter, r *http.Request, data authorizeTemplateData, raw string) (*http.Request, string, bool) {
	normalized, err := util.NormalizeSigNozURLFormInput(raw)
	if err != nil {
		data.SignozURL = raw
		h.renderAuthorizeError(w, r, http.StatusBadRequest, data, "Enter a valid SigNoz URL, for example signoz.example.com.", "invalid_request", "")
		return r, "", false
	}
	r = r.WithContext(util.SetSigNozURL(r.Context(), normalized))
	data.SignozURL = normalized
	if !h.config.AllowsInstanceURL(normalized) {
		h.renderAuthorizeError(w, r, http.StatusForbidden, data, util.InstanceURLNotPermittedMessage(), "access_denied", authFailureDisallowedSignozURL)
		return r, "", false
	}
	return r, normalized, true
}

func (h *Handler) handleDiscoverSubmit(w http.ResponseWriter, r *http.Request, params authorizeTemplateData) {
	data := params
	data.Email = strings.TrimSpace(r.FormValue("email"))
	if _, err := mail.ParseAddress(data.Email); err != nil || strings.ContainsAny(data.Email, "<> ") {
		data.SignozURL = strings.TrimSpace(r.FormValue("signoz_url"))
		h.renderAuthorizeError(w, r, http.StatusBadRequest, data, "Enter the email you sign in to SigNoz with.", "invalid_request", "")
		return
	}
	r, signozURL, ok := h.normalizeInstanceURL(w, r, data, r.FormValue("signoz_url"))
	if !ok {
		return
	}
	data.SignozURL = signozURL
	h.renderSignIn(w, r, http.StatusOK, data, "", "")
}

// renderSignIn asks the instance how this email signs in and renders the
// password and SSO options. Each render starts a fresh SSO correlation.
func (h *Handler) renderSignIn(w http.ResponseWriter, r *http.Request, status int, data authorizeTemplateData, errorMessage, failureReason string) {
	nonce, err := randomURLSafeString(16)
	if err != nil {
		h.writeOAuthError(r, w, http.StatusInternalServerError, "server_error", "failed to start sign-in")
		return
	}
	ref := h.issuerURL() + ssoCallbackPath + nonce

	sessionContext, err := h.sessionClient(data.SignozURL).GetSessionContext(r.Context(), data.Email, ref)
	if err != nil {
		h.renderInstanceError(w, r, data, err)
		return
	}

	views := make([]orgView, 0, len(sessionContext.Orgs))
	hasSSO := false
	for _, org := range sessionContext.Orgs {
		view := orgView{ID: org.ID, Name: org.Name, Password: org.Password}
		for _, sso := range org.SSO {
			if !isHTTPURL(sso.URL) {
				continue
			}
			view.SSO = append(view.SSO, ssoView{Label: ssoLabel(sso.Provider), URL: sso.URL})
		}
		if org.WarningCode == "origin_not_allowed" {
			view.Warning = "This SigNoz instance doesn't allow single sign-on from " + h.issuerHost() + ". Sign in with your password, or ask your SigNoz admin to add " + h.issuerHost() + " to the instance's allowed origins."
		}
		hasSSO = hasSSO || len(view.SSO) > 0
		views = append(views, view)
	}

	ssoUnavailable := ""
	if hasSSO {
		pending, err := EncryptPendingAuthorization(PendingAuthorization{
			Nonce:               nonce,
			ClientID:            data.ClientID,
			RedirectURI:         data.RedirectURI,
			State:               data.State,
			CodeChallenge:       data.CodeChallenge,
			CodeChallengeMethod: data.CodeChallengeMethod,
			Scope:               data.Scope,
			SignozURL:           data.SignozURL,
			ExpiresAt:           time.Now().Add(h.config.AuthCodeTTL),
		}, h.tokenSecret)
		if err != nil {
			h.writeOAuthError(r, w, http.StatusInternalServerError, "server_error", "failed to start sign-in")
			return
		}
		cookieName := ssoCookiePrefix + nonce
		if len(cookieName)+1+len(pending) > maxSSOCookieBytes {
			// A browser silently drops an oversized cookie, which would fail
			// the callback after the user already signed in at the IdP.
			ssoUnavailable = ssoTooLargeMessage(data.State)
			h.recordOAuthFailure(r.Context(), r, http.StatusRequestEntityTooLarge, "invalid_request", ssoUnavailable,
				failureReasonAttrs(authFailureSSORequestTooLarge)...)
			for i := range views {
				views[i].SSO = nil
			}
		} else {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    pending,
				Path:     h.basePath() + ssoCallbackPath + nonce,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   h.isSecure(),
				MaxAge:   int(h.config.AuthCodeTTL.Seconds()),
			})
		}
	}

	data.Orgs = make([]orgView, 0, len(views))
	for _, view := range views {
		if view.Password || len(view.SSO) > 0 {
			data.Orgs = append(data.Orgs, view)
		}
	}
	if len(data.Orgs) == 0 {
		message := "This SigNoz instance offers no sign-in method for that email. Check the instance URL, or use an API key."
		if ssoUnavailable != "" {
			message = ssoUnavailable
		}
		h.renderAuthorizeError(w, r, http.StatusNotFound, data, message, "invalid_request", authFailureSignInRejected)
		return
	}
	if errorMessage == "" && ssoUnavailable != "" {
		// Password sign-in still works, so the page renders normally with the
		// reason SSO is missing; the failure was recorded above.
		data.ErrorMessage = ssoUnavailable
	}

	data.Step = stepSignIn
	data.InstanceHost = hostOf(data.SignozURL)
	if errorMessage != "" {
		h.renderAuthorizeError(w, r, status, data, errorMessage, "access_denied", failureReason)
		return
	}
	h.renderAuthorizePage(w, r, status, data)
}

func (h *Handler) handlePasswordSubmit(w http.ResponseWriter, r *http.Request, params authorizeTemplateData) {
	data := params
	data.Email = strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	orgID := strings.TrimSpace(r.FormValue("org_id"))

	r, signozURL, ok := h.normalizeInstanceURL(w, r, data, r.FormValue("signoz_url"))
	if !ok {
		return
	}
	data.SignozURL = signozURL
	if data.Email == "" || orgID == "" {
		h.renderAuthorizeError(w, r, http.StatusBadRequest, data, "Enter the email you sign in to SigNoz with.", "invalid_request", "")
		return
	}
	if password == "" {
		h.renderSignIn(w, r, http.StatusBadRequest, data, "Enter your password.", "")
		return
	}

	token, err := h.sessionClient(signozURL).CreateSessionByPassword(r.Context(), data.Email, password, orgID)
	if err != nil {
		if errors.Is(err, client.ErrUnauthorized) || isClientError(err) {
			h.renderSignIn(w, r, http.StatusUnauthorized, data, "That email and password didn't work for this SigNoz instance.", authFailureSignInRejected)
			return
		}
		h.renderInstanceError(w, r, data, err)
		return
	}

	h.completeSessionAuthorization(w, r, data, sessionFromToken(signozURL, token))
}

// HandleSSOCallback receives the SigNoz session SigNoz's SSO callback
// redirects with. The query carries SigNoz tokens: never log it.
func (h *Handler) HandleSSOCallback(w http.ResponseWriter, r *http.Request) {
	nonce := r.PathValue("nonce")
	cookieName := ssoCookiePrefix + nonce
	cookie, err := r.Cookie(cookieName)
	if nonce == "" || err != nil {
		h.writeCallbackError(w, r, http.StatusBadRequest, "This sign-in link expired or was opened in another browser. Start again from your MCP client.")
		return
	}
	pending, err := DecryptPendingAuthorization(cookie.Value, h.tokenSecret)
	if err != nil || subtle.ConstantTimeCompare([]byte(pending.Nonce), []byte(nonce)) != 1 {
		h.writeCallbackError(w, r, http.StatusBadRequest, "This sign-in link expired or was opened in another browser. Start again from your MCP client.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     h.basePath() + ssoCallbackPath + nonce,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.isSecure(),
		MaxAge:   -1,
	})

	r = r.WithContext(util.SetSigNozURL(r.Context(), pending.SignozURL))
	data := authorizeTemplateData{
		ClientID:            pending.ClientID,
		RedirectURI:         pending.RedirectURI,
		State:               pending.State,
		CodeChallenge:       pending.CodeChallenge,
		CodeChallengeMethod: pending.CodeChallengeMethod,
		Scope:               pending.Scope,
		SignozURL:           pending.SignozURL,
	}
	if !h.config.AllowsInstanceURL(pending.SignozURL) {
		h.redirectAuthorizeError(w, r, data, "access_denied", util.InstanceURLNotPermittedMessage(), authFailureDisallowedSignozURL)
		return
	}

	query := r.URL.Query()
	token := &client.SessionToken{
		AccessToken:  query.Get("accessToken"),
		RefreshToken: query.Get("refreshToken"),
	}
	token.ExpiresIn, _ = strconv.Atoi(query.Get("expiresIn"))
	if token.AccessToken == "" || token.RefreshToken == "" {
		h.redirectAuthorizeError(w, r, data, "access_denied", "SigNoz sign-in did not return a session", authFailureSSOCallbackInvalid)
		return
	}

	h.completeSessionAuthorization(w, r, data, sessionFromToken(pending.SignozURL, token))
}

// completeSessionAuthorization issues a session-backed code and sends the
// browser back to the MCP client.
func (h *Handler) completeSessionAuthorization(w http.ResponseWriter, r *http.Request, data authorizeTemplateData, session Session) {
	code, err := EncryptSessionAuthorizationCode(SessionAuthorizationCode{
		Session:             session,
		ClientID:            data.ClientID,
		RedirectURI:         data.RedirectURI,
		CodeChallenge:       data.CodeChallenge,
		CodeChallengeMethod: data.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(h.config.AuthCodeTTL),
	}, h.tokenSecret)
	if err != nil {
		h.writeOAuthError(r, w, http.StatusInternalServerError, "server_error", "failed to generate authorization code")
		return
	}
	redirectURL, err := addAuthorizeResponse(data.RedirectURI, code, data.State)
	if err != nil {
		h.writeOAuthError(r, w, http.StatusInternalServerError, "server_error", "failed to build redirect URL")
		return
	}
	h.clearCSRFCookie(w)
	h.emitSession(r.Context(), analytics.EventOAuthAuthorizationSubmitted, session, map[string]any{
		analytics.AttrTenantURL: session.SignozURL,
	})
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// redirectAuthorizeError returns an RFC 6749 §4.1.2.1 error to the client.
func (h *Handler) redirectAuthorizeError(w http.ResponseWriter, r *http.Request, data authorizeTemplateData, code, description, failureReason string) {
	h.recordOAuthFailure(r.Context(), r, http.StatusFound, code, description, failureReasonAttrs(failureReason)...)
	parsed, err := url.Parse(data.RedirectURI)
	if err != nil {
		h.writeCallbackError(w, r, http.StatusBadRequest, "Sign-in failed. Start again from your MCP client.")
		return
	}
	query := parsed.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	if data.State != "" {
		query.Set("state", data.State)
	}
	parsed.RawQuery = query.Encode()
	http.Redirect(w, r, parsed.String(), http.StatusFound)
}

func (h *Handler) writeCallbackError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.recordOAuthFailure(r.Context(), r, status, "invalid_request", message, failureReasonAttrs(authFailureSSOCallbackInvalid)...)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}

// renderInstanceError maps a failure to reach the instance's session API.
func (h *Handler) renderInstanceError(w http.ResponseWriter, r *http.Request, data authorizeTemplateData, err error) {
	data.Step = ""
	switch {
	case errors.Is(err, client.ErrInstanceNotFound):
		h.renderAuthorizeError(w, r, http.StatusNotFound, data, "We couldn't find a SigNoz workspace at that URL. If your trial or subscription has ended, the workspace may have been deactivated. Check your workspace status or contact SigNoz support.", "invalid_request", authFailureInstanceNotFound)
	default:
		h.renderAuthorizeError(w, r, http.StatusBadGateway, data, "We couldn't reach that SigNoz instance. Check the URL and try again.", "temporarily_unavailable", authFailureInstanceUnreachable)
	}
}

// ssoTooLargeMessage explains why SSO is missing. The state parameter is the
// usual cause; otherwise the client's registration (redirect URIs and name,
// carried in its client_id) is.
func ssoTooLargeMessage(state string) string {
	if len(state) > maxSSOCookieBytes/4 {
		return "Single sign-on isn't available for this request: the MCP client's state parameter is too long (" +
			strconv.Itoa(len(state)) + " characters). Sign in with your password, or ask the client's maintainers to send a shorter state."
	}
	return "Single sign-on isn't available for this request: the MCP client's registration (its redirect URIs and name) is too large. Sign in with your password, or register the client with fewer redirect URIs."
}

func sessionFromToken(signozURL string, token *client.SessionToken) Session {
	return Session{
		SignozURL:       signozURL,
		AccessToken:     token.AccessToken,
		RefreshToken:    token.RefreshToken,
		AccessExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC(),
	}
}

// accessTokenLifetime advertises the shorter of SigNoz's remaining access
// validity and the configured cap, so clients refresh before SigNoz rejects.
func (h *Handler) accessTokenLifetime(session Session) int {
	limit := h.config.AccessTokenTTL
	if remaining := time.Until(session.AccessExpiresAt); remaining < limit {
		limit = remaining
	}
	if limit < 0 {
		return 0
	}
	return int(limit.Seconds())
}

func (h *Handler) emitSession(ctx context.Context, event string, session Session, props map[string]any) {
	h.emit(ctx, event, util.BearerCredential(session.AccessToken), session.SignozURL, props)
}

func isClientError(err error) bool {
	var statusErr *client.HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode >= 400 && statusErr.StatusCode < 500
}

func ssoLabel(provider string) string {
	switch strings.ToLower(provider) {
	case "google_auth", "google":
		return "Google"
	case "saml":
		return "SAML SSO"
	default:
		return "SSO"
	}
}

func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}
