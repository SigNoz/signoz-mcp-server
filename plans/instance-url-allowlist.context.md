# Feature: Tenant URL Allowlist — Context & Discussion

## Original Prompt
> I see many non signoz cloud users (tenant url not ending with signoz.cloud or signoz.io) are also using hosted signoz mcp, is it true?
>
> [investigation confirmed: ~19 distinct non-cloud tenant URLs/day reach the hosted MCP via `mcp.tenant_url`; only ~5 make successful tool calls, the rest are mostly HTTP 202 transport chatter + expired-token retries]
>
> But we don't expect self hosted signoz users to use hosted mcp server.
>
> Let's implement an option to whitelist URLs from ENV vars. Eg: Whitelist *.us.signoz.cloud then domain like demo.us.signoz.cloud should work

## Reference Links
- Investigation finding: non-cloud tenants reach hosted MCP because nothing restricts the client-supplied SigNoz URL.

## Key Decisions & Discussion Log
### 2026-06-07 — why this is possible today
- The hosted MCP accepts an arbitrary SigNoz backend URL from the client via two paths: the OAuth authorize form (`signoz_url` field, `internal/oauth/handlers.go:195`) and the `X-SigNoz-URL` header (`internal/mcp-server/server.go:1254`).
- `pkg/util/url.go:NormalizeSigNozURL` only rejects the literal hosts `localhost`, `0.0.0.0`, `::`. There is **no** domain allowlist and **no** SSRF/private-range guard anywhere.
- Result: self-hosted users simply run the normal Cloud setup flow against their own public SigNoz URL and the hosted MCP proxies to it.

### 2026-06-07 — design decisions
- Env var name: `SIGNOZ_TENANT_URL_ALLOWLIST` (follows existing `SIGNOZ_*` convention; comma-separated like `SIGNOZ_CUSTOM_HEADERS`).
- Wildcard semantics: `*.suffix` matches any host ending in `.suffix` (spans multiple labels, so `*.signoz.cloud` covers regional `x.us.signoz.cloud`); apex (`suffix` itself) is NOT matched; exact entries match a single host. Hostname-only, case-insensitive.
- Backward compatibility: empty/unset allowlist ⇒ allow every host (no change for existing single-tenant or unrestricted deployments).
- Enforcement at three chokepoints: OAuth authorize form submit (UX + prevents token issuance + prevents credential-probe SSRF), authMiddleware OAuth-token path, authMiddleware X-SigNoz-URL header path. Operator `SIGNOZ_URL` config path is exempt (operator-trusted, not client-supplied).
- New auth-failure reason `disallowed_signoz_url`, HTTP 403, surfaced on the existing `mcp.auth.failures` metric + span + structured log so rejections are observable in SigNoz.
- The allowlist, when configured, also mitigates the latent SSRF gap (private/loopback/metadata hosts) for the covered paths. A dedicated private-range guard is out of scope for this PR but recommended as follow-up.
- `manifest.json` unchanged: it configures the stdio desktop extension (single-tenant); the allowlist is a hosted multi-tenant server concern.

### 2026-06-07 — Codex (gpt-5.5, xhigh) review of PR #190 + fixes
- Codex verdict: REQUEST CHANGES. No Critical/High; matcher core confirmed sound (wildcard end-anchored; trailing-dot/IDN/userinfo all fail closed).
- Medium (fixed): the OAuth `/token` endpoint (`handleAuthorizationCodeGrant` / `handleRefreshTokenGrant` → `issueTokenPair`) minted tokens without an allowlist check, so a refresh token issued before the allowlist was tightened still got a 200 + token-issued analytics for a now-disallowed tenant (the access token was already blocked at `/mcp`). Added the allowlist check in `issueTokenPair`, returning `invalid_grant` (prompts client re-auth, where the form rejects up front) before minting tokens or emitting analytics.
- Low (fixed): OAuth form/token rejections did not emit the `disallowed_signoz_url` signal the README promised (they use the `mcp.oauth.failures` metric keyed by `oauth.error_code`, not `mcp.auth.failures`/`mcp.auth.failure_reason`). Threaded an optional `mcp.auth.failure_reason` attribute through `recordOAuthFailure`/`writeOAuthError` and a `FailureReason` field on `authorizeTemplateData`, so allowlist rejections at all paths are alertable on `mcp.auth.failure_reason=disallowed_signoz_url`. Also seeded `tenant_url` on ctx before the form rejection so the metric carries tenant attribution.
- Nit (fixed): `allowlist.go` comment claimed ports are stripped from entries; corrected — ports are NOT stripped (host-only match), entries must be bare hosts.
- Token-endpoint status decision: `invalid_grant` (400), not 403 — spec-correct for the token endpoint per RFC 6749 §5.2 and it makes clients re-run the authorize flow. The `disallowed_signoz_url` failure-reason label is consistent across all paths regardless of the client-facing code.
- Tests added: refresh-token grant disallowed (400 + reason metric), authorize-form disallowed (403 + reason metric), matcher trailing-dot + port-in-entry edge cases.

### 2026-06-07 — GitHub Codex bot (PR #190) comment + region-aware rejection message
- Codex GitHub bot (P2) on `allowlist.go`: `stripToHostPattern` kept the `:port`, so a pasted entry like `https://demo.us.signoz.cloud:8080` (or `*.us.signoz.cloud:443/`) could never match `url.Hostname()` (which drops the port) → legitimate tenants 403'd. Valid (fail-closed footgun, no security risk). Adopted the bot's fix — strip the port (and unwrap IPv6 brackets) in `stripToHostPattern` — instead of the earlier doc-only "ports not stripped" note, since the parser already advertises tolerating pasted full URLs.
- Product request: make the rejection guide users to their own region's MCP URL with a docs link. Added `util.TenantNotPermittedMessage(signozURL)` + `signozCloudRegion()`: for a `*.signoz.cloud` host it names the region and suggests `https://mcp.<region>.signoz.cloud/mcp`; otherwise generic guidance (cloud → region URL, self-hosted → run your own MCP). Both link `MCPDocsURL` (https://signoz.io/docs/ai/signoz-mcp-server/). Wired into all three rejection points (authMiddleware helper, OAuth form, issueTokenPair).

### 2026-06-07 — drop per-request log for allowlist rejections (metric-only)
- `disallowed_signoz_url` rejections initially logged at WARN on all paths. A misconfigured/wrong-region client can loop (cf. the 62k-202/day pattern), flooding WARN. Decision: drop the per-request log entirely; keep the metric (and span on `/mcp`) as the signal — extends the denoise rationale from PR #188 ([[reference_ai_assistant_telemetry]] context).
- `logAuthFailure` (server.go) early-returns before logging when `reason == authFailureDisallowedSignozURL` (span + `mcp.auth.failures` metric still emitted).
- `recordOAuthFailure` (handlers.go) emits the `mcp.oauth.failures` metric first, then skips the log when `extraAttrs` carry `mcp.auth.failure_reason=disallowed_signoz_url` (via `hasAttrValue`). Other OAuth failures still log at WARN/ERROR.

### 2026-06-07 — E2E test + drop region extraction from rejection message
- Ran a live E2E (local server, real JWT in header, local `initialize` so the staging backend is never dialed): 13 edge cases all correct — allow (exact/no-slash/UPPERCASE/:443/:8443), deny 403 (wrong-region/self-hosted/apex/trailing-dot/SSRF metadata IP), 400 (localhost rejected by NormalizeSigNozURL, missing URL), unset allowlist allows all, and zero rejection log lines at debug (metric-only confirmed).
- Finding: `signozCloudRegion` took the label before `.signoz.cloud`, which is correct for production (`tenant.us.signoz.cloud` → `us`) but mis-detects staging (`t.eu.staging.signoz.cloud` → `staging`). Decision: drop region extraction entirely — `TenantNotPermittedMessage()` now returns one generic message (Cloud → `mcp.<region>.signoz.cloud/mcp`, self-hosted → run your own, + docs link). Always correct, never names a wrong region. Removed `signozCloudRegion`.

### 2026-06-07 — rename: drop "tenant" from the public surface
- A reviewer noted "tenant" reads as internal jargon for an open-source project. Discussion: "tenant"/"multi-tenant" is standard SaaS-architecture vocabulary, but users configuring this think in terms of their *SigNoz instance URL* (the wording the README/OAuth form already use), so dropping "tenant" improves clarity regardless. `SIGNOZ_URL_ALLOWLIST` was rejected — too easily confused with the operator's own `SIGNOZ_URL`.
- Renamed (feature is unreleased, so no back-compat/deprecation): env var `SIGNOZ_TENANT_URL_ALLOWLIST` → `SIGNOZ_INSTANCE_URL_ALLOWLIST`; Go symbols `TenantURLAllowlist`/`ParseTenantURLAllowlist`/`enforceTenantURLAllowlist`/`TenantNotPermittedMessage`/`TenantURLAllowlistEnv` → `Instance…`; config field/const, tests, README, and these plan files (renamed `tenant-url-allowlist.*` → `instance-url-allowlist.*`).
- Deliberately KEPT: the released `mcp.tenant_url` telemetry attribute and the pre-existing `AttrTenantURL`/`AppendTenantURL`/`MCPTenantURLKey` analytics — renaming those is a breaking telemetry change (live in dashboards), out of scope. "multi-tenant" as an architecture term also kept.
- Note: prior entries above intentionally retain the old `SIGNOZ_TENANT_URL_ALLOWLIST` / `TenantURLAllowlist` names — they are the accurate audit trail of what existed when written.

## Open Questions
- [x] Should `*.signoz.cloud` match multi-label regional subdomains like `x.us.signoz.cloud`? — Yes; wildcard matches on dot-anchored suffix across labels.
- [x] Should the operator's own `SIGNOZ_URL` be subject to the allowlist? — No; it is operator-configured and trusted, exempt from the check.
- [x] Should the OAuth token endpoint enforce the allowlist (not just the form and `/mcp`)? — Yes; enforced in `issueTokenPair` so existing refresh tokens for disallowed tenants are refused. (Codex PR #190 review.)
- [ ] Follow-up (separate PR): add a private/loopback/link-local/metadata SSRF guard in `NormalizeSigNozURL` regardless of allowlist.
