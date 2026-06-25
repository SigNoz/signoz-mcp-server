# Feature: Opaque User-Token Auth — Context & Discussion

## Original Prompt
> Suggest a solution for the opaque-user-token auth bug, and confirm whether the JWT-detection-deprecation issue is related. (Both tracked in the internal issue tracker.)

## Reference Links
- Internal issue — bug: MCP rejects opaque user tokens by forwarding them as `SIGNOZ-API-KEY`.
- Internal issue — cleanup: deprecate the JWT token-detection logic.
- [signoz-mcp-server#86 — feat: add JWT token support and refactor authentication header handling](https://github.com/SigNoz/signoz-mcp-server/pull/86) (the PR that introduced `isJWTToken` + the 9-row behavior table)
- SigNoz backend auth pipeline: `pkg/identn/{resolver,config}.go`, `pkg/identn/{tokenizeridentn,apikeyidentn}/provider.go`, `pkg/tokenizer/{jwttokenizer,opaquetokenizer}`, `pkg/modules/serviceaccount/implserviceaccount/module.go`, `tests/integration/tests/serviceaccount/03_auth.py` (github.com/SigNoz/signoz)

## Key Decisions & Discussion Log

### 2026-06-25 — #10 and #12 are the same root cause
- `isJWTToken()` (`internal/mcp-server/server.go:1124`, sole caller at `:1284`) is a **backward-compat shim**: its only job is to route legacy clients that put a service-account PAT in the `Authorization` header (diagram rows #5/#6) over to `SIGNOZ-API-KEY`.
- That same shim causes #10: an **opaque (non-JWT) user/session token** sent as `Authorization: Bearer <token>` fails the JWT shape test, is rerouted to `SIGNOZ-API-KEY`, and SigNoz returns 401. Shape alone cannot distinguish an opaque user token from an opaque PAT.
- **#12 ("deprecate jwt token detection") is the planned removal of that shim.** Removing it *is* the fix for #10. Resolve them together.

### 2026-06-25 — Verified the pivotal backend contract (two independent investigations, high confidence)
Question: does the SigNoz backend accept a service-account PAT presented as `Authorization: Bearer <pat>`?
- **Answer: NO (rejected).** SigNoz classifies credentials **purely by header name**, not token shape:
  - `Authorization` (and `Sec-WebSocket-Protocol`) → **tokenizer** provider → JWT signature verify OR opaque token-store DB lookup. Accepts user/session tokens (JWT *and* opaque). Rejects PATs (a PAT is neither a signed JWT nor a token-store row).
  - `SIGNOZ-API-KEY` → **service-account** module → api-key-table lookup. Accepts PATs only. Rejects user tokens.
  - There is **no shape-sniffing and no cross-header fallback** upstream — `isJWTToken` has no counterpart in the backend.
- Sources: `pkg/identn/config.go` (header→provider map), `pkg/identn/resolver.go` (first-Test()-wins), `apikeyidentn/provider.go` (`extractToken` reads only SIGNOZ-API-KEY), `implserviceaccount/module.go` (`GetFactorAPIKeyByKey`), `opaquetokenizer/provider.go` (`GetByAccessToken`), `tests/integration/tests/serviceaccount/03_auth.py` (`test_service_account_key_forbidden_on_user_me`), signoz.io service-accounts doc (PAT only via `SIGNOZ-API-KEY`), `pkg/signoz/openapi.go` (two separate security schemes).
- **Consequence:** opaque user tokens via `Authorization` already work upstream → #10's fix (stop rerouting them) is correct and safe. BUT honoring the header is **not zero-breakage**: legacy rows #5/#6 (PAT via `Authorization`) currently work *only because* MCP reroutes them to `SIGNOZ-API-KEY`; removing the reroute sends them upstream as `Authorization: Bearer <pat>` where SigNoz rejects them. So #12's removal needs a migration bridge.

### 2026-06-25 — Adversarial design review findings (must address regardless of chosen bridge)
1. Honoring the header also flips the **identity-validation endpoint** (`client.go:200-205`: `Authorization`→`/api/v2/users/me`, principal=user; `SIGNOZ-API-KEY`→`/api/v1/service_accounts/me`, principal=service_account). Correct for real user tokens; a deprecated Bearer-PAT would be mislabeled `principal=user` — acceptable since that path is being removed.
2. **Latent cache-key bug:** `HashTenantKey(apiKey, signozURL)` (`handler.go:81`, `context.go:154`) omits the auth-header name. Today modes don't collide only because the stored `apiKey` differs by the literal `"Bearer "` prefix. Any future normalization would silently serve a wrong-mode client. **Include `authHeader` in the cache key regardless of bridge chosen.**
3. **Scope:** the bug repro is the `customURL != ""` branch (`:1283-1296`), but two other `Authorization` branches also force `SIGNOZ-API-KEY` — the OAuth-disabled no-customURL `else` (`:1338-1342`) and the OAuth-decrypt-fail legacy fallback (`:1326-1336`). #10 persists for config-`SIGNOZ_URL` clients unless the fix is applied consistently across all three (or the contract explicitly documents "honoring only applies with X-SigNoz-URL").
4. **OAuth path is orthogonal** — the OAuth setup form validates and stores a service-account API key; keep the decrypted-token path on `SIGNOZ-API-KEY`. Do **not** thread an auth-mode into the encrypted OAuth payload (wrong scope; would force a token-format migration).
5. **Observability (CLAUDE.md cross-contract rule):** fixtures cannot catch backend drift on Bearer-PAT acceptance; the failure is a silent 401. Emit a distinct `mcp.auth.mode` for "Authorization-ingress, non-JWT" and a WARN/metric on "Authorization-ingress request got upstream 401" so the deprecation window is observable.

## Key Decisions & Discussion Log (cont.)

### 2026-06-25 — Bridge decision: clean fix (Option A, no telemetry gate, no fallback)
- Owner: "We already gave deprecation notice 3 months back so let's do clean fix." The #12 deprecation notice predates today by ~3 months (issue filed 2026-03-24) — well past the stated 1-month window.
- **Decision: do the clean honor-the-header cutover now.** No Phase-0 measurement gate and no try-both fallback (Option B) — the deprecation window has already elapsed, so legacy rows #5/#6 (PAT via `Authorization`) are intentionally broken and must use `SIGNOZ-API-KEY`. Option C remains rejected.
- Apply honoring to **all three** `Authorization` raw-token branches (consistent contract; fixes #10 for config-`SIGNOZ_URL` clients too). Collapse the JWT/APIKey auth-modes into a single `authModeAuthorizationBearer`. Delete `isJWTToken` (completes #12).
- Keep the cache-key hardening (`HashTenantKey` to include the auth-header name) — cheap, makes the mode-isolation invariant explicit rather than accidental.
- OAuth-decrypted path stays on `SIGNOZ-API-KEY` (orthogonal, unchanged).
- Owner added: "no cloud users are using that route anyway" — legacy PAT-via-`Authorization` (rows #5/#6) has effectively zero real traffic, so the cutover carries no practical breakage risk for Cloud.

### 2026-06-25 — Implemented + Codex review
- Implemented the clean cutover: honor the ingress header across all three Authorization branches; deleted `isJWTToken` and the unused `authorization-jwt`/`authorization-api-key` auth-modes (consolidated on `authorization-bearer`); `HashTenantKey` now includes the auth-header name; OAuth-decrypted path unchanged. All unit tests pass.
- Codex (gpt-5.5, xhigh, read-only) review: **no blocker/high findings**; confirmed no regression to OAuth success / expired-challenge / invalid-OAuth-no-URL / SIGNOZ-API-KEY / env-fallback / missing-credential paths. Two mediums, both addressed:
  1. `Bearer ` prefix stripping was case-sensitive → added `stripBearerPrefix` (case-insensitive per RFC 7235, matching SigNoz's own parser); fixes lowercase `bearer` double-prefix + a latent OAuth-decrypt miss.
  2. Test gaps → added no-prefix + lowercase-bearer table cases and a new `TestAuthMiddlewareHonorsAuthorizationWithConfigURL` pinning the OAuth-disabled and OAuth-decrypt-fail config-URL branches.
- **Public-repo hygiene:** `signoz-mcp-server` is public; scrubbed the internal tracker name and private issue URLs from all code/docs/plan files added here. (Pre-existing committed leaks remain in `internal/handler/tools/param_schema_test.go` and `plans/param-consistency-cleanup.*` — flagged to owner, not cleaned here.)
- **Live E2E — PASSED** (read-only subagent, two staging instances, no resources created, no credential leak):
  - Direct upstream: JWT and opaque tokens both → HTTP 200 on `/api/v2/users/me` via `Authorization: Bearer`; opaque token via `SIGNOZ-API-KEY` on `/api/v1/service_accounts/me` → 401 (confirms it is a user token, not a PAT — exactly the old bug). JWT identity round-tripped (email/orgId match).
  - Through `/mcp` (HTTP multi-tenant, `X-SigNoz-URL` branch): `signoz_list_services` returned real data for both tokens, forwarded on `Authorization` to the correct tenant, no 401/isError. Logs showed only the per-tenant client creation and no SIGNOZ-API-KEY/OAuth/auth-failure routes.
  - Added a `DebugContext("Forwarding Authorization bearer token upstream")` line to the `customURL` branch (the only honored path that previously had no debug line) so the cutover is directly observable. Status → Done.

### 2026-06-25 — Removed new debug logs (owner request)
- Owner: "Why did we add new debug logs, remove them … Only remove new debug log, keep modified ones." Verified against origin/main: the `customURL` branch AND the OAuth-disabled `else` branch had **no** debug log before — both `"Forwarding Authorization bearer token upstream"` lines were new (the else-branch one slipped in during the earlier diff-fix). Removed both. Kept the one genuine modification: the OAuth-decrypt-fail branch message, updated from `"…falling back to raw API key"` → `"…forwarding as Authorization"`. The `config.APIKey`-branch log is untouched. Net debug-log delta vs main: that single message change.

## Open Questions
- [x] **Bridge strategy for legacy rows #5/#6** — RESOLVED 2026-06-25: clean cutover (Option A), deprecation window already elapsed.
- [x] **Apply to all three `Authorization` branches?** — RESOLVED: yes, all three.
- [ ] Live E2E probe against staging to *operationally* re-confirm the source-derived contract (PAT via `Authorization` → reject; opaque user token via `Authorization` → 200). Delegate to a subagent per CLAUDE.md; never print credentials. (Verification step, not a blocker for the code change.)
- [ ] Optional follow-up: WARN/metric when an `authModeAuthorizationBearer` request gets an upstream 401 (spots legacy stragglers post-cutover). Deferred — crosses into the client layer; not required for the clean fix.
