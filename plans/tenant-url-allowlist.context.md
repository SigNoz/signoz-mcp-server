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

## Open Questions
- [x] Should `*.signoz.cloud` match multi-label regional subdomains like `x.us.signoz.cloud`? — Yes; wildcard matches on dot-anchored suffix across labels.
- [x] Should the operator's own `SIGNOZ_URL` be subject to the allowlist? — No; it is operator-configured and trusted, exempt from the check.
- [ ] Follow-up (separate PR): add a private/loopback/link-local/metadata SSRF guard in `NormalizeSigNozURL` regardless of allowlist.
