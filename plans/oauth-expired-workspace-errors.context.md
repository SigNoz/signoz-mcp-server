# Feature: OAuth expired-workspace errors — Context & Discussion

## Original Prompt
> What to do here? https://github.com/SigNoz/nerve-pod/issues/155
> Fix and then get codex 5.6 sol high review and then raise a PR

## Reference Links
- [nerve-pod#155 — OAuth authorize fails with `temporarily_unavailable` 502](https://github.com/SigNoz/nerve-pod/issues/155)

## Key Decisions & Discussion Log
### 2026-07-17 — Investigation (live telemetry)
- Queried hosted MCP server logs (`service.name = 'signoz-mcp-server'`, last 7d):
  23 WARN "SigNoz credential validation returned unexpected status" — 22× HTTP 404,
  1× HTTP 503, across 8 distinct `*.signoz.cloud` tenant URLs.
- The 404 bodies are the SigNoz Cloud ingress HTML page: "You have reached a
  workspace that does not exist! Either the workspace has expired or the
  workspace does not exist." — confirming the issue's hypothesis: these are
  expired/deactivated cloud workspaces, not typos or transient outages. DNS
  still resolves; the ingress serves the 404 page.
- Found a telemetry gap: `mcp.oauth.failures` has an **empty `mcp.tenant_url`**
  on the credential-validation failure path — the URL is only seeded into the
  request context on the allowlist-rejection path (`handlers.go`).

### 2026-07-17 — Design decisions
- **Detection signal**: HTTP 404 whose body is not JSON (HTML page) from the
  validation endpoints ⇒ no SigNoz API at that URL. A live SigNoz API always
  answers `/api/v1/user/me` / `/api/v1/service_accounts/me` with JSON, even on
  404. Detect via first non-whitespace byte `<` rather than matching page copy
  (copy can change; content shape is stable).
- New sentinel `client.ErrInstanceNotFound` returned from
  `evaluateValidationResponse`; `ValidateCredentials` also skips the
  service-account retry when the first 404 is HTML (the retry would just hit
  the same ingress page).
- **Handler mapping**: `ErrInstanceNotFound` ⇒ HTTP 404, OAuth error code
  `invalid_request` (permanent, per issue ask), message pointing at
  workspace/subscription status instead of "check the URL and try again".
  Everything else non-401 keeps 502 `temporarily_unavailable`.
- **Telemetry**: reuse existing `FailureReason` → `mcp.auth.failure_reason`
  mechanism with `instance_not_found` / `instance_unreachable` so
  expired-workspace noise is separable from real connectivity incidents.
- Seed `util.SetSigNozURL` into the request context right after allowlist
  check so every subsequent failure metric carries `mcp.tenant_url`.
- 4xx failures log at WARN (existing `recordOAuthFailure` behavior), so the
  reclassified expired-workspace failures also stop appearing as ERROR noise.

### 2026-07-17 — Codex review (gpt-5.6-sol, reasoning=high, read-only)
- Verdict: **APPROVE**, no blocker/major findings. Minors addressed:
  - `isHTMLBody` now strips a UTF-8 BOM before sniffing; comment documents
    that XML also classifies as "no SigNoz API here" and that empty/plain-text
    404s conservatively stay on the transient path.
  - Added the transient-path handler test (503 ⇒ 502 `temporarily_unavailable`
    + `instance_unreachable` + tenant attribution) promised by the plan.
  - Fixed a stale test comment ("user/me 502" → "user/me JSON 404").
- Codex confirmed: no OAuth wire-level concern (ErrorCode on this rendered
  form path is telemetry/display only, no redirect), and
  `GetAnalyticsIdentity` sharing `evaluateValidationResponse` only changes
  error identity/log text, not behavior.

## Open Questions
- [x] Are the failing URLs really expired deployments? — Yes; confirmed via
  ingress 404 "workspace does not exist" HTML bodies in the hosted server's
  own logs (see 2026-07-17 investigation entry).
