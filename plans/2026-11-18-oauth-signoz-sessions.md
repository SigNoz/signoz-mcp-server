# Plan: OAuth sign-in with SigNoz sessions

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/456

## Context

The OAuth authorize page asks users to paste a SigNoz service-account API key, which only
admins can create. Users should instead sign in with their own SigNoz login, by password or
SSO, and the MCP client should act as that user. The server stays the OAuth authorization
server, one host for many deployments. SigNoz gains no new endpoints: the server
calls the deployment's existing session API (`/api/v2/sessions/*`). The `SIGNOZ-API-KEY` path is
unchanged.

Verified in SigNoz source (`pkg/modules/session`, `pkg/types/authtypes`, `pkg/tokenizer`,
`pkg/identn/tokenizeridentn`):

- `GET /api/v2/sessions/context?email&ref` lists orgs with password and callback (SSO) login
  URLs. SSO is offered only when `ref` passes `global.allowed_origins` (an empty list allows
  everything); otherwise the org falls back to password with a `warning`.
- `POST /api/v2/sessions/email_password {email,password,orgId}` returns
  `{tokenType,accessToken,refreshToken,expiresIn}`.
- SSO callbacks redirect to `ref`'s scheme, host and path with the token as query parameters,
  dropping `ref`'s own query. Errors redirect to the deployment's `/login`, not to `ref`.
- An access token stops working at the rotation interval (`expiresIn` counts down to it).
  Deployments configure the interval, so nothing here assumes a value.
  `POST /api/v2/sessions/rotate` takes the access token as bearer and `{refreshToken}`;
  replaying the previous pair within the rotation duration (60s) returns the current pair, so
  concurrent refreshes are safe.
- `DELETE /api/v2/sessions` revokes the bearer's session.

## Approach

- Request auth on `/mcp` becomes an ordered resolver chain (first match wins): `SIGNOZ-API-KEY`
  header, OAuth bearer (session token, then legacy encrypted token), raw `Authorization`
  passthrough, server API key. A future trusted-gateway resolver slots in at the front behind an
  explicit flag. Neither credential → the existing 401 challenge.
- Outbound calls take a `util.Credential` (API key or bearer). The context keeps the header name
  and value, so tool code is unchanged.
- Access token: `sz1.<base64url(deployment origin)>.<SigNoz access token>`. The origin is
  re-checked against the allowlist on every request; the token is validated with
  `GET /api/v2/users/me`, cached by token hash for 30s.
- Refresh token and authorization code: AES-GCM blobs (new blob types) carrying the SigNoz URL,
  both SigNoz tokens and the client binding. Refresh calls SigNoz rotate. `expires_in` is
  `min(SigNoz expiresIn, OAUTH_ACCESS_TOKEN_TTL)`.
- Authorize page: step 1 asks for deployment and email; step 2 shows consent (client name with
  its redirect host) and, per org, a password form and/or SSO buttons.
- SSO correlation: `ref = <issuer>/oauth/sso-callback/<nonce>`; the encrypted pending
  authorization lives in an HttpOnly, SameSite=Lax cookie scoped to that path and named after the
  nonce. The callback requires the cookie and the nonce to match.
- `POST /oauth/revoke` (RFC 7009) deletes the SigNoz session; it always answers 200.
- Encrypted access tokens, codes and refresh tokens with an API key inside keep working, and the
  page still issues them when the user picks the API-key option.

## Files to Modify

- `skaff.yml`, `internal/apiclient/` — generated session operations
- `internal/client/session.go` — session API calls (context, password, rotate, delete, me)
- `pkg/util/credential.go` — credential abstraction
- `internal/oauth/` — session tokens, authorize steps, SSO callback, refresh, revoke, template
- `internal/mcp-server/auth_resolvers.go`, `server.go` — resolver chain, routes
- `README.md`, `docs/architecture.md` — flow and token docs

## Key Decisions

### 2026-11-18 — Pending authorization in a cookie, nonce in the path

- Decision: the SSO `ref` path carries only a random nonce; the encrypted pending authorization
  is in a cookie named and scoped by that nonce.
- Rationale: SAML IdPs carry `ref` through RelayState, and a 1–2KB encrypted blob in the path
  risks IdP length limits. The cookie gives the same CSRF binding (a forged callback lacks it),
  and per-nonce names let parallel authorizations coexist.
- Alternatives rejected: the encrypted blob in the path plus a binding cookie.

### 2026-11-18 — Offer both sign-in and API key on the authorize page

- Decision: the first step offers SigNoz sign-in (deployment and email, then password or SSO)
  and, below it, the existing service-account API-key form. The API-key path issues the same
  encrypted tokens as before.
- Rationale: the user asked for both options on the page. The brief defaulted to removing the
  API-key form; the user overrode that.

## Known Limitations

- MCP tokens are ordinary SigNoz UI sessions: no audience or client binding on the SigNoz side,
  they count toward the per-user session cap (5, oldest evicted, so many MCP clients can sign a
  user out of the browser), and they share the UI's idle (7d) and max (30d) lifetimes.
- SSO needs the MCP origin in the deployment's `global.allowed_origins` when that list is set.
- `/mcp` caches a session's validation for 30s, so a revoked session keeps working that long.
- Revoking a refresh token that a later refresh superseded returns 200 without ending the
  current session, because SigNoz no longer knows the access token inside it.
- Authorization codes are stateless and replayable within their TTL; PKCE and the client and
  redirect binding limit this to the original client.

## Reference Links

- SigNoz session API: `pkg/modules/session/implsession`, `pkg/types/authtypes/{session,token}.go`
  in [SigNoz/signoz](https://github.com/SigNoz/signoz)
- [RFC 7009: OAuth 2.0 Token Revocation](https://www.rfc-editor.org/rfc/rfc7009)

## Verification

- `GOTOOLCHAIN=go1.26.11 make ci`: every step passes except `check-repo-docs`, which waits on
  this plan's Outcome.
- `internal/oauth/session_flow_test.go` runs a fake deployment and asserts the requests sent to
  it: password sign-in (wrong and right password, `email_password` body), PKCE rejection,
  `expires_in` from `expiresIn` and its cap, refresh via rotate (bearer plus refresh body),
  refresh client binding, revoke via `DELETE /api/v2/sessions`, an ended session returning
  `invalid_grant`, SSO callback correlation (missing cookie, cookie for another nonce, success),
  a callback without tokens returning `access_denied` to the client, the `origin_not_allowed`
  hint, and an API-key code still exchanging.
- `internal/mcp-server/auth_resolvers_test.go`: a session bearer reaches its deployment on
  `Authorization` while ignoring `X-SigNoz-URL`, validation is cached, a rejected session gets
  `invalid_token`, a disallowed deployment is never dialed, and the API-key header, encrypted
  token and bare challenge paths resolve alongside it.
- A mutation that skipped the nonce comparison failed the SSO correlation test.
- `tests/e2e/tests/test_oauth_sign_in.py` against the pinned SigNoz: password sign-in, code
  exchange into an `sz1.` token, a tool call, refresh rotation, revoke ending the session for
  both refresh and `/mcp`, and a wrong password re-rendering sign-in. 2 passed.
- 2026-11-18 live check against a live SigNoz deployment (password sign-in): discover, code
  exchange, `/mcp` tool calls returning real data, refresh rotation, revoke ending the session,
  and no secrets in the server log all passed. SSO was not offered by that deployment; the user
  will test SSO separately.

## Outcome

Done. Users can sign in with their own SigNoz account (password, or SSO where the deployment
allows the issuer origin) or keep using a service-account API key; `/mcp` resolves credentials
through an ordered resolver chain. Unit, e2e and live password checks pass. SSO end to end is
left to a manual test; the known limitations above ship as documented.
