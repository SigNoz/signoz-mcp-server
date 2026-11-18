package mcp_server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/trace"
)

const (
	authModeOAuthSession = "oauth-session"

	authFailureRejectedSession     = "rejected_signoz_session"
	authFailureInstanceUnreachable = "instance_unreachable"

	sessionValidationTTL  = 30 * time.Second
	sessionValidationSize = 10_000
)

// authResolution is the credential a request carries. signozURL is set when
// the credential names its own instance (OAuth tokens); otherwise the
// X-SigNoz-URL header or SIGNOZ_URL decides.
type authResolution struct {
	cred      util.Credential
	signozURL string
	mode      string
}

// authFailure ends the request. challenge, when non-empty, is the extra
// WWW-Authenticate parameter list; oauthChallenge sends the bare challenge.
type authFailure struct {
	status         int
	reason         string
	mode           string
	message        string
	logMessage     string
	signozURL      string
	challenge      string
	oauthChallenge bool
}

// authResolver returns (nil, nil) for a credential it doesn't own. First match
// wins, so a future trusted-gateway resolver (behind an explicit flag) can go first.
type authResolver func(ctx context.Context, r *http.Request) (*authResolution, *authFailure)

func (m *MCPServer) authResolvers() []authResolver {
	return []authResolver{
		m.resolveAPIKeyHeader,
		m.resolveOAuthBearer,
		m.resolveBearerPassthrough,
		m.resolveConfigAPIKey,
	}
}

func (m *MCPServer) resolveCredential(ctx context.Context, r *http.Request) (*authResolution, *authFailure) {
	for _, resolve := range m.authResolvers() {
		if res, fail := resolve(ctx, r); res != nil || fail != nil {
			return res, fail
		}
	}
	return nil, &authFailure{
		status:         http.StatusUnauthorized,
		reason:         authFailureMissingCredential,
		mode:           authModeNone,
		message:        "Authorization or SIGNOZ-API-KEY header required",
		logMessage:     "No API key found in headers or environment",
		oauthChallenge: true,
	}
}

// resolveAPIKeyHeader forwards a SIGNOZ-API-KEY header as-is.
func (m *MCPServer) resolveAPIKeyHeader(_ context.Context, r *http.Request) (*authResolution, *authFailure) {
	value := r.Header.Get("SIGNOZ-API-KEY")
	if value == "" {
		return nil, nil
	}
	return &authResolution{cred: util.APIKeyCredential(stripBearerPrefix(value)), mode: authModeSignozAPIKeyHeader}, nil
}

// resolveOAuthBearer accepts tokens this server issued: SigNoz session
// tokens, and encrypted tokens carrying an API key.
func (m *MCPServer) resolveOAuthBearer(ctx context.Context, r *http.Request) (*authResolution, *authFailure) {
	header := r.Header.Get("Authorization")
	if header == "" || !m.config.OAuthEnabled {
		return nil, nil
	}
	token := stripBearerPrefix(header)

	if oauth.IsSessionAccessToken(token) {
		signozURL, accessToken, ok := oauth.ParseSessionAccessToken(token)
		return m.resolveSessionToken(ctx, signozURL, accessToken, ok)
	}

	apiKey, signozURL, _, _, err := oauth.DecryptToken(token, []byte(m.config.OAuthTokenSecret))
	switch {
	case err == nil:
		return &authResolution{cred: util.APIKeyCredential(apiKey), signozURL: signozURL, mode: authModeOAuthAccessToken}, nil
	case errors.Is(err, oauth.ErrExpiredToken):
		// Once server-issued, so the embedded URL is a trusted tenant value.
		return nil, &authFailure{
			status:     http.StatusUnauthorized,
			reason:     authFailureExpiredOAuthToken,
			mode:       authModeOAuthAccessToken,
			message:    "OAuth access token expired",
			logMessage: "OAuth access token expired",
			signozURL:  signozURL,
			challenge:  `error="invalid_token", error_description="access token expired"`,
		}
	default:
		return nil, nil
	}
}

// resolveSessionToken routes a session token to its instance and checks
// that SigNoz still accepts it. The instance comes from the token's
// readable prefix, so it is allowlisted before any request is sent.
func (m *MCPServer) resolveSessionToken(ctx context.Context, signozURL, accessToken string, parsed bool) (*authResolution, *authFailure) {
	invalid := &authFailure{
		status:     http.StatusUnauthorized,
		reason:     authFailureInvalidOAuthToken,
		mode:       authModeOAuthSession,
		message:    "OAuth access token is invalid",
		logMessage: "OAuth session token names an invalid instance",
		challenge:  `error="invalid_token", error_description="access token is invalid"`,
	}
	if !parsed {
		return nil, invalid
	}
	signozURL = m.config.ResolveInstanceURL(signozURL)
	if !m.config.AllowsInstanceURL(signozURL) {
		return nil, &authFailure{
			status:     http.StatusForbidden,
			reason:     authFailureDisallowedSignozURL,
			mode:       authModeOAuthSession,
			message:    util.InstanceURLNotPermittedMessage(),
			logMessage: "Tenant SigNoz URL is not permitted by the server allowlist",
			signozURL:  signozURL,
		}
	}

	ctx = util.SetSigNozURL(ctx, signozURL)
	if err := m.validateSession(ctx, signozURL, accessToken); err != nil {
		if errors.Is(err, client.ErrUnauthorized) {
			return nil, &authFailure{
				status:     http.StatusUnauthorized,
				reason:     authFailureRejectedSession,
				mode:       authModeOAuthSession,
				message:    "OAuth access token expired",
				logMessage: "SigNoz rejected the session token",
				signozURL:  signozURL,
				challenge:  `error="invalid_token", error_description="access token expired"`,
			}
		}
		return nil, &authFailure{
			status:     http.StatusBadGateway,
			reason:     authFailureInstanceUnreachable,
			mode:       authModeOAuthSession,
			message:    "Couldn't reach the SigNoz instance to check the access token",
			logMessage: "SigNoz session validation failed",
			signozURL:  signozURL,
		}
	}
	return &authResolution{cred: util.BearerCredential(accessToken), signozURL: signozURL, mode: authModeOAuthSession}, nil
}

// validateSession asks SigNoz whether the token is live, remembering a yes
// for sessionValidationTTL so a burst of MCP calls costs one round trip.
func (m *MCPServer) validateSession(ctx context.Context, signozURL, accessToken string) error {
	m.sessionCacheOnce.Do(func() {
		m.sessionCache = expirable.NewLRU[string, struct{}](sessionValidationSize, nil, sessionValidationTTL)
	})
	sum := sha256.Sum256([]byte(signozURL + "\x00" + accessToken))
	key := hex.EncodeToString(sum[:])
	if _, ok := m.sessionCache.Get(key); ok {
		return nil
	}
	var headers map[string]string
	if signozURL == m.config.URL {
		headers = m.config.CustomHeaders
	}
	if err := client.NewSessionClient(m.logger, signozURL, headers).ValidateSession(ctx, accessToken); err != nil {
		return err
	}
	m.sessionCache.Add(key, struct{}{})
	return nil
}

// resolveBearerPassthrough forwards any other bearer to SigNoz on the
// Authorization header, where SigNoz classifies it.
func (m *MCPServer) resolveBearerPassthrough(_ context.Context, r *http.Request) (*authResolution, *authFailure) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return nil, nil
	}
	token := stripBearerPrefix(header)
	// With OAuth on, a stale bearer and no SigNoz URL would otherwise mask
	// the challenge that starts a fresh flow.
	if m.config.OAuthEnabled && r.Header.Get("X-SigNoz-URL") == "" && m.config.URL == "" {
		return nil, &authFailure{
			status:     http.StatusUnauthorized,
			reason:     authFailureInvalidOAuthToken,
			mode:       authModeAuthorizationBearer,
			message:    "OAuth access token is invalid",
			logMessage: "Bearer token did not match OAuth token format and no SigNoz URL is available for legacy fallback",
			challenge:  `error="invalid_token", error_description="access token is invalid"`,
		}
	}
	return &authResolution{cred: util.BearerCredential(token), mode: authModeAuthorizationBearer}, nil
}

// resolveConfigAPIKey falls back to the server's own SIGNOZ_API_KEY.
func (m *MCPServer) resolveConfigAPIKey(_ context.Context, _ *http.Request) (*authResolution, *authFailure) {
	if m.config.APIKey == "" {
		return nil, nil
	}
	return &authResolution{cred: util.APIKeyCredential(m.config.APIKey), mode: authModeConfigAPIKey}, nil
}

func (m *MCPServer) writeAuthFailure(ctx context.Context, w http.ResponseWriter, r *http.Request, fail *authFailure) {
	if fail.signozURL != "" {
		ctx = util.SetSigNozURL(ctx, fail.signozURL)
		if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
			trace.SpanFromContext(ctx).SetAttributes(attr)
		}
	}
	var attrs []slog.Attr
	if fail.reason == authFailureDisallowedSignozURL {
		attrs = append(attrs, slog.String("mcp.tenant_url", fail.signozURL))
	}
	m.logAuthFailure(ctx, r, fail.status, fail.reason, fail.mode, fail.logMessage, attrs...)
	switch {
	case fail.challenge != "":
		m.setOAuthChallenge(w, fail.challenge)
	case fail.oauthChallenge:
		m.setOAuthChallenge(w, "")
	}
	http.Error(w, fail.message, fail.status)
}

// sessionCacheState is embedded in MCPServer; zero value is ready to use.
type sessionCacheState struct {
	sessionCacheOnce sync.Once
	sessionCache     *expirable.LRU[string, struct{}]
}
