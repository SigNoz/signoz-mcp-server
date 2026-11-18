package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const sessionRequestTimeout = 15 * time.Second

// SessionClient calls a SigNoz instance's session API (/api/v2/sessions). It
// holds no credential: sign-in calls are unauthenticated and the rest take
// the SigNoz access token per call.
type SessionClient struct {
	signoz *SigNoz
}

// SessionToken is a SigNoz session token pair. ExpiresIn counts seconds until
// SigNoz stops accepting the access token and requires rotation.
type SessionToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// SessionContext lists the orgs an email can sign in to and how.
type SessionContext struct {
	Exists bool
	Orgs   []OrgSignIn
}

type OrgSignIn struct {
	ID       string
	Name     string
	Password bool
	SSO      []SSOSignIn
	// WarningCode is set when SigNoz could not offer this org's SSO, for
	// example origin_not_allowed when the callback origin is not allowed.
	WarningCode string
}

type SSOSignIn struct {
	Provider string
	URL      string
}

func NewSessionClient(log *slog.Logger, baseURL string, customHeaders map[string]string) *SessionClient {
	return &SessionClient{signoz: NewClient(log, baseURL, "", "Authorization", customHeaders)}
}

// GetSessionContext asks which sign-in methods email has. ref is the URL
// SigNoz returns SSO sign-ins to; it keeps only ref's scheme, host and path.
func (c *SessionClient) GetSessionContext(ctx context.Context, email, ref string) (*SessionContext, error) {
	req, err := apiclient.NewGetSessionContextRequest(c.signoz.apiServer())
	if err != nil {
		return nil, fmt.Errorf("create session context request: %w", err)
	}
	// The spec omits these query parameters; SigNoz reads them from the URL.
	query := req.URL.Query()
	query.Set("email", email)
	query.Set("ref", ref)
	req.URL.RawQuery = query.Encode()

	var payload apitypes.AuthtypesSessionContext
	if err := c.call(ctx, req, "", &payload); err != nil {
		return nil, err
	}

	out := &SessionContext{Exists: payload.Exists != nil && *payload.Exists}
	if payload.Orgs == nil {
		return out, nil
	}
	for _, org := range *payload.Orgs {
		view := OrgSignIn{ID: deref(org.Id), Name: deref(org.Name)}
		if org.AuthNSupport != nil {
			view.Password = org.AuthNSupport.Password != nil && len(*org.AuthNSupport.Password) > 0
			if org.AuthNSupport.Callback != nil {
				for _, cb := range *org.AuthNSupport.Callback {
					provider := ""
					if cb.Provider != nil {
						provider = string(*cb.Provider)
					}
					view.SSO = append(view.SSO, SSOSignIn{Provider: provider, URL: deref(cb.Url)})
				}
			}
		}
		if org.Warning != nil {
			view.WarningCode = org.Warning.Code
		}
		if view.ID == "" {
			c.signoz.logger.WarnContext(ctx, "SigNoz session context returned an org without an id")
			continue
		}
		out.Orgs = append(out.Orgs, view)
	}
	return out, nil
}

// CreateSessionByPassword signs in with email and password. A wrong password
// returns an error wrapping ErrUnauthorized or a 4xx *HTTPStatusError.
func (c *SessionClient) CreateSessionByPassword(ctx context.Context, email, password, orgID string) (*SessionToken, error) {
	req, err := apiclient.NewCreateSessionByEmailPasswordRequest(c.signoz.apiServer(), apitypes.AuthtypesPostableEmailPasswordSession{
		Email:    &email,
		Password: &password,
		OrgId:    &orgID,
	})
	if err != nil {
		return nil, fmt.Errorf("create password session request: %w", err)
	}
	return c.token(ctx, req, "")
}

// RotateSession exchanges the current pair for a new one. Replaying the
// previous pair shortly after a rotation returns the current pair.
func (c *SessionClient) RotateSession(ctx context.Context, accessToken, refreshToken string) (*SessionToken, error) {
	req, err := apiclient.NewRotateSessionRequest(c.signoz.apiServer(), apitypes.AuthtypesPostableRotateToken{RefreshToken: &refreshToken})
	if err != nil {
		return nil, fmt.Errorf("create rotate session request: %w", err)
	}
	return c.token(ctx, req, accessToken)
}

// DeleteSession ends the SigNoz session the access token belongs to.
func (c *SessionClient) DeleteSession(ctx context.Context, accessToken string) error {
	req, err := apiclient.NewDeleteSessionRequest(c.signoz.apiServer())
	if err != nil {
		return fmt.Errorf("create delete session request: %w", err)
	}
	return c.call(ctx, req, accessToken, nil)
}

// ValidateSession checks that SigNoz accepts the access token.
func (c *SessionClient) ValidateSession(ctx context.Context, accessToken string) error {
	req, err := apiclient.NewGetMyUserRequest(c.signoz.apiServer())
	if err != nil {
		return fmt.Errorf("create session validation request: %w", err)
	}
	return c.call(ctx, req, accessToken, nil)
}

func (c *SessionClient) token(ctx context.Context, req *http.Request, accessToken string) (*SessionToken, error) {
	var payload apitypes.AuthtypesGettableToken
	if err := c.call(ctx, req, accessToken, &payload); err != nil {
		return nil, err
	}
	token := &SessionToken{
		AccessToken:  deref(payload.AccessToken),
		RefreshToken: deref(payload.RefreshToken),
	}
	if payload.ExpiresIn != nil {
		token.ExpiresIn = *payload.ExpiresIn
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		c.signoz.logger.WarnContext(ctx, "SigNoz session response is missing a token",
			slog.String("url.path", req.URL.Path))
		return nil, fmt.Errorf("SigNoz session response from %s is missing a token", req.URL.Path)
	}
	return token, nil
}

// call sends req and decodes the success envelope's data into dest. Request
// and response bodies carry passwords and tokens, so only the path is logged.
func (c *SessionClient) call(ctx context.Context, req *http.Request, accessToken string, dest any) error {
	ctx = c.signoz.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, sessionRequestTimeout)
	defer cancel()

	req = req.WithContext(ctx)
	c.signoz.setRequestHeaders(ctx, req, false)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	} else {
		req.Header.Del("Authorization")
	}

	resp, err := c.signoz.httpClient.Do(req)
	if err != nil {
		c.signoz.logger.WarnContext(ctx, "SigNoz session request failed",
			slog.String("url.path", req.URL.Path), logpkg.ErrAttr(err))
		return fmt.Errorf("failed to reach SigNoz API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read SigNoz session response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d", ErrUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound && isHTMLBody(body):
		return fmt.Errorf("%w: status %d", ErrInstanceNotFound, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return newHTTPStatusError(resp.StatusCode, body)
	}
	if dest == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
		c.signoz.logger.WarnContext(ctx, "SigNoz session response is not a success envelope",
			slog.String("url.path", req.URL.Path), slog.Int("response.body.size_bytes", len(body)))
		return fmt.Errorf("unexpected SigNoz session response from %s", req.URL.Path)
	}
	if err := json.Unmarshal(envelope.Data, dest); err != nil {
		c.signoz.logger.WarnContext(ctx, "SigNoz session response data does not match the expected shape",
			slog.String("url.path", req.URL.Path), logpkg.ErrAttr(err))
		return fmt.Errorf("decode SigNoz session response from %s: %w", req.URL.Path, err)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
