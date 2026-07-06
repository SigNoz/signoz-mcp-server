# Plan: Opaque User-Token Auth

## Status
Done

## Context
The MCP auth middleware decides which header to forward upstream to SigNoz: `SIGNOZ-API-KEY: <pat>` (service-account) or `Authorization: Bearer <token>` (user/session). For the `Authorization` ingress it currently sniffs token **shape** via `isJWTToken()` (`server.go:1124`, sole caller `:1284`): JWT-shaped → forward `Authorization`; otherwise → reroute to `SIGNOZ-API-KEY`.

That shape-sniff is the **opaque-user-token bug**: an opaque (non-JWT) user/session token is rerouted to `SIGNOZ-API-KEY`, which SigNoz rejects (401). It is also exactly the heuristic the **JWT-detection-deprecation issue** wants to delete. The two issues share one root cause.

**Verified backend contract (high confidence, from SigNoz source + docs):** SigNoz discriminates credentials by **header name**, not shape. `Authorization` → tokenizer (JWT *or* opaque user tokens); `SIGNOZ-API-KEY` → service-account PATs. A PAT on `Authorization` is **rejected**; an opaque user token on `Authorization` is **accepted**. So MCP should stop second-guessing the header — the header name is canonical. The only casualty is the legacy/undocumented path where a client puts a PAT in `Authorization` (diagram rows #5/#6), which today works only because MCP reroutes it.

## Behavior table — before vs after (honor-the-header end state)
| # | Client sends | Today → upstream | After → upstream | Change |
|---|---|---|---|---|
| 1 | `SIGNOZ-API-KEY: pat` | `SIGNOZ-API-KEY: pat` | same | — |
| 2 | `SIGNOZ-API-KEY: Bearer pat` | `SIGNOZ-API-KEY: pat` | same | — |
| 3 | `Authorization: Bearer <JWT>` | `Authorization: Bearer <JWT>` | same | — |
| 4 | `Authorization: <JWT>` (no prefix) | `Authorization: Bearer <JWT>` | same | — |
| **#10** | `Authorization: Bearer <opaque user token>` | `SIGNOZ-API-KEY: <token>` → **401** | `Authorization: Bearer <token>` → **200** | **FIXED** |
| 5 | `Authorization: pat` (legacy) | `SIGNOZ-API-KEY: pat` → 200 | `Authorization: Bearer pat` → **401** | **BREAKS** (needs bridge) |
| 6 | `Authorization: Bearer pat` (legacy) | `SIGNOZ-API-KEY: pat` → 200 | `Authorization: Bearer pat` → **401** | **BREAKS** (needs bridge) |
| 7 | no headers, env `SIGNOZ_API_KEY` | `SIGNOZ-API-KEY: <env>` | same | — |
| 9 | stdio `SIGNOZ_API_KEY=pat` | `SIGNOZ-API-KEY: pat` | same | — |

## Approach
**Clean cutover** (decided 2026-06-25): the #12 deprecation notice already went out ~3 months ago and the owner confirms no Cloud users use the legacy PAT-via-`Authorization` route, so we honor the ingress header directly with **no measurement gate and no try-both fallback**. #10 is fixed and #12 is completed in one PR. Legacy rows #5/#6 are intentionally retired (must move to `SIGNOZ-API-KEY`).

1. **Honor the ingress header** for every raw (non-OAuth) `Authorization` token. Replace the `isJWTToken` if/else at `server.go:1283-1296` and apply the same to the other two raw-token branches — OAuth-disabled no-`customURL` `else` (`:1338-1342`) and OAuth-decrypt-fail legacy fallback (`:1332-1335`). In each: `apiKey = "Bearer " + token`; `SetAuthHeader(ctx, "Authorization")`; `authMode = authModeAuthorizationBearer`. **Load-bearing invariant:** `client.go:271/360` does `req.Header.Set(authHeaderName, apiKey)` verbatim — it does NOT add `"Bearer "`, so the middleware must bake the prefix into `apiKey`.
2. **Delete `isJWTToken`** (`server.go:1124-1126`, now caller-less) — the concrete deliverable of **#12**. Collapse `authModeAuthorizationJWT` and `authModeAuthorizationAPIKey` into the single `authModeAuthorizationBearer` (remove the now-unused constants).
3. **Cache-key hardening:** include the auth-header name in `HashTenantKey` (`pkg/util/context.go:154`, caller `handler.go:81`) so mode isolation is explicit, not an accident of the `"Bearer "` prefix.
4. **Untouched:** OAuth-decrypt success (`:1297-1306`), OAuth-expired rejection, the invalid-OAuth-no-URL rejection guard, and the OAuth setup-form validation (`oauth/handlers.go:333`) all stay on `SIGNOZ-API-KEY` — they are service-account-backed and orthogonal to #10.

Deferred (not in this PR): a WARN/metric when an `authModeAuthorizationBearer` request gets an upstream 401 (would spot any legacy straggler) — crosses into the client layer and isn't needed given zero Cloud traffic on the route.

## Files to Modify
- `internal/mcp-server/server.go` — `:1283-1296` honor-header (primary fix); `:1326-1342` consistency for the other two Authorization branches; `:1124-1126` delete `isJWTToken` (#12); `:1128-1135` authMode constants (distinct legacy mode + telemetry); `:1262-1267` update the contract comment.
- `pkg/util/context.go` — `HashTenantKey` to include auth-header name (`:154`).
- `internal/handler/tools/handler.go` — pass auth-header into the cache key at `:81` (consumes the `context.go` change). `GetClient` is otherwise mode-agnostic; no logic change.
- `internal/client/client.go` — no change for Phase 1 (already honors `authHeaderName`, switches `/me` endpoint). Bridge **(B)** would add one-time mode resolution here.
- **Tests:**
  - `internal/mcp-server/server_test.go` — `TestAuthMiddlewareFallsBackToRawAPIKey` pins rows #5/#6; update to assert honored `Authorization` + `"Bearer "`-prefixed key (and rename). `TestAuthMiddlewareAuthFailureTelemetryBranches/invalid_SigNoz_URL` (`:756`) pins the old authMode; update. **Add** a parametrized test proving a JWT-shaped *and* an opaque token via `Authorization` both forward identically (the #10 regression guard). Other auth tests are review-only.
  - `internal/client/client_test.go` — optional table row with an opaque (non-`eyJ`) value in `Authorization` mode to lock out future shape-sniffing.
- **Docs/metadata (per CLAUDE.md doc-sync):**
  - `docs/architecture.md` — rewrite the auth-flow Mermaid (`RAWKEY`/`PARSE` nodes ~`:44-45`) and Backward-Compatibility section (`:172`) to the honor-the-header model + note the #5/#6 change.
  - `README.md` — review for stale `Authorization`/`Bearer` routing claims (likely no functional change; state the review outcome in the PR).
  - `manifest.json` — **no change** (no tool/resource/config added or renamed); state explicitly in PR.
  - **SigNoz/agent-skills** — **no change**: this alters only internal upstream-forwarding classification, not the client-facing tool contract; state outcome in PR, no companion PR needed.

## Verification
- Unit: updated `server_test.go` (honored Authorization for opaque + JWT), cache-key includes auth-header, telemetry split asserted.
- **Live E2E (delegate to subagent against staging, per CLAUDE.md; never print credentials):**
  - opaque user token via `Authorization: Bearer` → tool call returns 200 (the #10 repro now passes); confirm forwarded header is `Authorization`.
  - service-account PAT via `SIGNOZ-API-KEY` → still 200 (rows #1/#7/#9 unchanged).
  - service-account PAT via `Authorization: Bearer` → operationally confirm the source-derived **reject** (decides whether bridge B is required); the `e2e_family*_test.go` helpers already build Authorization-mode clients and are the natural harness.
  - delete nothing it didn't create; report which fields round-tripped.
- Observability: confirm the new `mcp.auth.mode` value and the upstream-401 WARN/metric fire on a simulated legacy PAT-via-`Authorization` request.
