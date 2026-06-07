# Feature: Response & Request Body Limits — Context & Discussion

## Original Prompt
> (Follow-up to PR #189 / mcp-oom-hardening.) "Do we need to apply any more limits in responses?
> ... Let's work on next steps in new PR." Then: "Should we also implement these?
> https://github.com/SigNoz/signoz-mcp-server/issues/70"

## Reference Links
- Predecessor PR #189 (`fix/docs-index-scorch-oom-hardening`): scorch index + search_logs/search_traces raw limit clamp.
- Predecessor plan: `plans/mcp-oom-hardening.*` (its "Follow-ups" section is the seed for this PR).
- Issue #70: "timeout and body size limit is not defined" — read/write timeout, stateful/stateless, request/response body size limit.

## Key Decisions & Discussion Log
### 2026-06-07 — Audit of remaining unbounded-response vectors (post-#189)
- `execute_builder_query`: caller embeds `limit` inside opaque QB-v5 JSON → bypasses the #189 search clamp. **Highest remaining row-vector.**
- `aggregate_logs` / `aggregate_traces`: group `limit` (default 10) unclamped. Low payload risk (groups ≪ raw rows) but trivial to bound.
- `client.go doRequest` (shared executor for EVERY backend call): `io.ReadAll(resp.Body)` with no size guard → every tool buffers the full upstream response into memory.
- Already bounded (no action): `get_alert_history` (validated 1–1000), `list_*` (org-scale), `get_trace_details` (hardcoded 1000 spans), `query_metrics` (backend bounds points).

### 2026-06-07 — Scope decision (owner)
- Owner chose **"All three"**: execute_builder_query clamp + client-layer response guard + aggregate clamp.
- Stacked on #189's branch (reuses `clampLimit` / `MaxRawResultLimit` / `rawSearchResult`); base = `fix/docs-index-scorch-oom-hardening` until #189 merges, then rebase to `main`.

### 2026-06-07 — Issue #70 assessment + scope (owner)
- **#70 item 1 (read/write timeouts): already handled** — `ReadHeaderTimeout: 10s`, `ReadTimeout: 30s`, `MaxHeaderBytes: 1MB`; `WriteTimeout`/`IdleTimeout` deliberately 0 (documented: SSE streaming). Will comment on #70 confirming. Optional future: finite `IdleTimeout` to reap dead keep-alive conns.
- **#70 item 2 (stateful/stateless): separate PR** — server uses `NewStreamableHTTPServer` without `WithStateLess(true)` (stateful). Stateless is worth evaluating for the memory-limited multi-tenant pod but is a behavior change needing its own investigation.
- **#70 item 3 (request/response body size): closed by this PR.** Response half = the doRequest guard; owner approved folding in the **request half** (inbound `MaxBytesReader` on `/mcp`), default 4 MiB, configurable via `MCP_MAX_REQUEST_BYTES`.

### 2026-06-07 — Design notes
- **Response guard** lives in `doRequest` (one chokepoint for all tools). Reads `maxResponseBytes+1` (64 MiB) and **errors rather than truncates** — never hands back invalid JSON. Oversize is terminal (not retried).
- **execute_builder_query clamp** only fires when resolved `requestType == "raw"` (scalar/time_series `limit` bounds aggregation groups, not rows). Zero/unset limit left for backend default. Reuses `clampLimit`; surfaces the existing pagination note via `rawSearchResult` (offset IS valid for raw builder queries).
- **aggregate clamp** is silent (no note): aggregations have no offset pagination, and >10000 groups is far outside normal usage — clamping is a pure memory bound.
- **Request body cap** middleware is the outermost `/mcp` middleware so the limit also governs the body that `methodSpanMiddleware` peeks (1 MiB peek < 4 MiB cap → composes) and reconstructs downstream. `limit <= 0` disables (no-op) so tests/configs without the field set are unaffected.

### 2026-06-07 — Codex (gpt-5.5 / xhigh) review + responses
- Codex: no blockers. Confirmed value-type `QuerySpec` mutation persists (reassigned to `Queries[i].Spec`), type assertion safely skips promql/clickhouse_sql/builder_formula/raw-JSON specs, `MaxBytesReader` caps end-to-end through the method-span peek (no double-count), and SSE GET is unharmed.
- **Fixed (should-fix):** builder clamp only covered `requestType=="raw"`, but traces also have `requestType=="trace"` (raw-like, querybuilder.go:246) and scalar/time_series group limits bypassed it. **Removed the requestType gate — now clamps every `builder_query` `QuerySpec.Limit > 10000`** (rows for raw/trace, groups for scalar/time_series; matches the aggregate tools' group cap). Note reworded to type-agnostic via `builderQueryResult`.
- **Fixed (should-fix):** silent aggregate clamp was a correctness surprise. **Now surfaced** — `AggregateRequest.LimitClamped` + `aggregateResult` appends a non-pagination note ("narrow time/filters/groupBy"). Refactored result wrappers to a shared `clampedResult(payload, clamped, note)` core (rawSearchResult / aggregateResult / builderQueryResult).
- **Fixed (should-fix):** the "respond 413" claim was false — mcp-go maps a body read error to a JSON-RPC parse error (HTTP 400), not 413. **Added an early `ContentLength > limit` → 413 path** (clean rejection when length is declared) and kept `MaxBytesReader` for chunked/unknown-length; corrected the comment.
- **Noted (should-fix, comment-only):** `MCP_MAX_REQUEST_BYTES=0` can't disable via env because `getEnvInt` rejects ≤0 → falls back to 4 MiB. Chosen resolution: **keep env positive-only (no disable foot-gun for a memory-safety feature)**; the middleware `limit<=0` guard is documented as defensive for programmatically-constructed configs (tests). README does not advertise 0-disable.
- **Fixed (nit):** oversize response error now includes `resp.StatusCode` and less query-specific wording (it fires on any backend call).
- **Decided (nit):** manifest.json change NOT needed — its `user_config`/`env` only exposes a curated UI subset (url/api_key/log_level); all other operational env vars (CLIENT_CACHE_SIZE, SIGNOZ_DOCS_*, etc.) are README-only, so `MCP_MAX_REQUEST_BYTES` is consistent there. Tool descriptions are behavior-agnostic.

## Open Questions
- [x] Which limits in this PR? — **All three** + request body cap (#70 item 3).
- [x] Builder clamp: raw-only or all requestTypes? — **All `builder_query` specs** (covers trace + aggregate bypasses Codex found).
- [x] Aggregate clamp: silent or surfaced? — **Surfaced** (reversed the earlier silent decision per Codex).
- [x] Request cap env-disable? — **No** (positive-only; guard is defensive for tests).
- [x] Response guard: hard byte cap vs error? — **Error** (read cap+1, reject) so JSON is never truncated.
- [x] Aggregate clamp: surface a note? — **No** (silent); offset note is inaccurate for aggregations.
- [x] Request body cap value / configurability? — **4 MiB default, `MCP_MAX_REQUEST_BYTES`**.
- [ ] #70 item 2 (stateful/stateless) — defer to its own investigation/PR.
