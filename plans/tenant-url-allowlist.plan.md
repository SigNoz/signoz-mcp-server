# Plan: Tenant URL Allowlist

## Status
In Progress

## Context
The hosted, multi-tenant MCP server proxies to whatever SigNoz backend URL the
client supplies (OAuth authorize form `signoz_url`, or `X-SigNoz-URL` header).
There is no restriction on which URLs are accepted, so self-hosted SigNoz users
(and arbitrary hosts) can point the hosted MCP at their own backends — which is
not the intended use of the hosted service, and is also a latent SSRF surface.

This adds an opt-in env-var allowlist of permitted tenant hostnames. When set,
only matching hosts are accepted; when unset, behavior is unchanged.

## Approach

### Matching (`pkg/util/allowlist.go`)
- `TenantURLAllowlist` holds a set of exact lowercased hostnames plus a list of
  dot-anchored suffixes (from `*.suffix` patterns).
- `ParseTenantURLAllowlist(raw string)` splits on `,`, trims, lowercases,
  strips any scheme/path an operator pasted, and classifies each entry as exact
  or `*.`-wildcard.
- `Configured()` reports whether any patterns were supplied.
- `AllowsHost(host)` — empty allowlist ⇒ true; else exact-match OR
  `strings.HasSuffix(host, ".suffix") && len(host) > len(".suffix")`.
- `AllowsURL(rawURL)` — parses the normalized origin and delegates to
  `AllowsHost(parsed.Hostname())`.

### Config (`internal/config/config.go`)
- Const `TenantURLAllowlistEnv = "SIGNOZ_TENANT_URL_ALLOWLIST"`.
- `Config.TenantURLAllowlist util.TenantURLAllowlist`, parsed in `LoadConfig`.
- Log an info line at startup when an allowlist is configured.

### Enforcement (`internal/mcp-server/server.go`)
- New const `authFailureDisallowedSignozURL = "disallowed_signoz_url"`.
- Helper `enforceTenantURLAllowlist(ctx, w, r, signozURL, authMode) bool` —
  returns true if allowed; else `logAuthFailure(... 403 ...)`, writes 403, false.
- Call it in the OAuth-token branch (after tenant URL is set in ctx) and in the
  `X-SigNoz-URL` header branch (gated so the operator `SIGNOZ_URL` path is exempt).

### Enforcement (`internal/oauth/handlers.go`)
- After `NormalizeSigNozURLFormInput` succeeds, reject with a 403 authorize page
  (`access_denied`) when `h.config.TenantURLAllowlist.AllowsURL(normalizedURL)`
  is false — before `validateSigNozCredentials` probes the backend.
- In `issueTokenPair` (shared by the authorization_code and refresh_token
  grants), reject with `invalid_grant` before minting tokens or emitting
  token-issued analytics, so existing refresh tokens for now-disallowed tenants
  are refused at the token endpoint, not just at `/mcp`.
- Telemetry: `recordOAuthFailure`/`writeOAuthError` take an optional
  `mcp.auth.failure_reason` attribute, and `authorizeTemplateData` gains a
  `FailureReason` field, so allowlist rejections at the OAuth endpoints are
  alertable on the same `disallowed_signoz_url` reason as the `/mcp` paths.

## Files to Modify
- `pkg/util/allowlist.go` — new matcher type + parser.
- `pkg/util/allowlist_test.go` — table-driven matching tests.
- `internal/config/config.go` — env const, field, parse, startup log.
- `internal/mcp-server/server.go` — failure const, enforce helper, 2 call sites.
- `internal/oauth/handlers.go` — allowlist check on authorize form submit.
- `internal/mcp-server/server_test.go` — reject (403) + allow (200) middleware tests.
- `README.md` — env var table row + short multi-tenant note.
- `plans/tenant-url-allowlist.*` — this pair.

`manifest.json` intentionally unchanged (stdio desktop-extension config; the
allowlist is a hosted multi-tenant concern).

## Verification
- `go test ./pkg/util/... ./internal/mcp-server/... ./internal/oauth/...`
- Unit: `*.us.signoz.cloud` allows `demo.us.signoz.cloud`, rejects `1.1.1.1`,
  `evil.eu.signoz.cloud`, apex `us.signoz.cloud`, `notus.signoz.cloud`.
- Middleware: `X-SigNoz-URL: https://1.1.1.1` with allowlist `*.us.signoz.cloud`
  ⇒ 403 `disallowed_signoz_url`; `https://demo.us.signoz.cloud` ⇒ 200.
- Empty allowlist ⇒ existing behavior (1.1.1.1 still 200).
- Manual: set env on staging, confirm a non-cloud OAuth setup is rejected at the
  authorize form and the `mcp.auth.failures{reason="disallowed_signoz_url"}`
  metric increments.
