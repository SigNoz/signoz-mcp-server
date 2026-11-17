package oauth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/attribute"
)

func (h *Handler) handleSessionCodeGrant(w http.ResponseWriter, r *http.Request, code SessionAuthorizationCode, decryptErr error, clientID, redirectURI, codeVerifier string) {
	if decryptErr != nil {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	r = r.WithContext(util.SetSigNozURL(r.Context(), code.Session.SignozURL))
	if code.ClientID != clientID || code.RedirectURI != redirectURI {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "authorization code does not match the client or redirect URI")
		return
	}
	if !ValidatePKCE(codeVerifier, code.CodeChallenge, code.CodeChallengeMethod) {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "PKCE validation failed")
		return
	}
	h.issueSessionTokenPair(w, r, clientID, code.Session, "authorization_code")
}

func (h *Handler) handleSessionRefreshGrant(w http.ResponseWriter, r *http.Request, session Session, refreshClientID string, decryptErr error, clientID string) {
	if decryptErr != nil {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	r = r.WithContext(util.SetSigNozURL(r.Context(), session.SignozURL))
	if clientID != "" && refreshClientID != clientID {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "refresh token does not belong to this client")
		return
	}
	if !h.config.AllowsInstanceURL(session.SignozURL) {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", util.InstanceURLNotPermittedMessage(),
			attribute.String("mcp.auth.failure_reason", authFailureDisallowedSignozURL))
		return
	}

	token, err := h.sessionClient(session.SignozURL).RotateSession(r.Context(), session.AccessToken, session.RefreshToken)
	if err != nil {
		if errors.Is(err, client.ErrUnauthorized) || isClientError(err) {
			// SigNoz ended the session (signed out, evicted, or past its
			// lifetime): the client must sign in again.
			h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "the SigNoz session has ended; sign in again",
				attribute.String("mcp.auth.failure_reason", authFailureSessionRejected))
			return
		}
		h.writeOAuthError(r, w, http.StatusServiceUnavailable, "temporarily_unavailable", "couldn't reach the SigNoz instance to refresh the session",
			attribute.String("mcp.auth.failure_reason", authFailureInstanceUnreachable))
		return
	}

	h.issueSessionTokenPair(w, r, refreshClientID, sessionFromToken(session.SignozURL, token), "refresh_token")
}

func (h *Handler) issueSessionTokenPair(w http.ResponseWriter, r *http.Request, clientID string, session Session, grantType string) {
	if !h.config.AllowsInstanceURL(session.SignozURL) {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", util.InstanceURLNotPermittedMessage(),
			attribute.String("mcp.auth.failure_reason", authFailureDisallowedSignozURL))
		return
	}

	refreshToken, err := EncryptSessionRefreshToken(session, clientID, time.Now().Add(h.config.RefreshTokenTTL), h.tokenSecret)
	if err != nil {
		h.writeOAuthError(r, w, http.StatusInternalServerError, "server_error", "failed to create refresh token")
		return
	}

	h.writeTokenResponse(w, tokenResponse{
		AccessToken:  FormatSessionAccessToken(session.SignozURL, session.AccessToken),
		TokenType:    "Bearer",
		ExpiresIn:    h.accessTokenLifetime(session),
		RefreshToken: refreshToken,
	})

	h.emitSession(r.Context(), analytics.EventOAuthTokenIssued, session, map[string]any{
		analytics.AttrTenantURL: session.SignozURL,
		analytics.AttrGrantType: grantType,
	})
}

// HandleRevoke answers 200 for unknown or ended tokens, as RFC 7009 requires.
// Tokens carrying an API key can't be revoked server-side; they expire.
func (h *Handler) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_request", "form data is required")
		return
	}
	value := strings.TrimSpace(r.FormValue("token"))
	if value == "" {
		h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	clientID := strings.TrimSpace(r.FormValue("client_id"))

	var signozURL, accessToken string
	if parsedURL, parsedToken, ok := ParseSessionAccessToken(value); ok {
		signozURL, accessToken = parsedURL, parsedToken
	} else if session, refreshClientID, err := DecryptSessionRefreshToken(value, h.tokenSecret); err == nil || errors.Is(err, ErrExpiredToken) {
		if clientID != "" && refreshClientID != clientID {
			h.writeOAuthError(r, w, http.StatusBadRequest, "invalid_grant", "token does not belong to this client")
			return
		}
		signozURL, accessToken = session.SignozURL, session.AccessToken
	}

	if accessToken != "" {
		signozURL = h.config.ResolveInstanceURL(signozURL)
	}
	if accessToken != "" && h.config.AllowsInstanceURL(signozURL) {
		ctx := util.SetSigNozURL(r.Context(), signozURL)
		if err := h.sessionClient(signozURL).DeleteSession(ctx, accessToken); err != nil && !errors.Is(err, client.ErrUnauthorized) {
			h.writeOAuthError(r, w, http.StatusServiceUnavailable, "temporarily_unavailable", "couldn't reach the SigNoz instance to end the session")
			return
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
