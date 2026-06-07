# Feature: Cache SigNoz clients by URL (#69) — Context & Discussion

## Original Prompt
> Now let's implement caching(#69) on top of this PR

(Where "this PR" = #192, the stateless-transport change on branch `feat/stateless-transport`.)

## Reference Links
- [Issue #69](https://github.com/SigNoz/signoz-mcp-server/issues/69) — "Why is the client storing the apikey? … incomplete attempt to cache the http client."
- [PR #187 (closed)](https://github.com/SigNoz/signoz-mcp-server/pull/187) — akshaysw's original implementation, reused here.
- Stacks on [PR #192](https://github.com/SigNoz/signoz-mcp-server/pull/192) (stateless transport).

## Key Decisions & Discussion Log

### 2026-06-07 — Decision: implement #69 stacked on the stateless PR, reusing #187
- Earlier the plan was to do #69 after #192 merged; changed to stacking it on #192 now.
- The client cache (#69) is INDEPENDENT of statelessness — but complementary: statelessness
  makes every request independent, so a per-URL shared HTTP connection pool matters more, not
  less. (See [[stateless-server.context]] / the closed-#187 discussion.)
- #187 already implemented exactly this and was CI-green. Decision: REUSE it rather than
  reimplement — cherry-pick its 11 code/test commits (preserving akshaysw's authorship), since
  every file it touches (`client.go`, `client_test.go`, `handler.go`, `handler_test.go`,
  `util/context.go`, `oauth/handlers.go`) is disjoint from the stateless changes. Cherry-pick
  applied with zero conflicts.
- PR structure: SEPARATE stacked PR (base `feat/stateless-transport`), to keep #192 focused.
  Merges in order (#192 first; this PR then retargets to `main`).
- `docs/architecture.md`: #187's doc commit was NOT cherry-picked (would clash with the
  stateless section); replicated its cache-description edits by hand and added a "Client
  Caching" section alongside the "Stateless Transport" section.

## What changed (from #187, reused)
- `SigNoz` client is credential-free: dropped `apiKey`/`authHeaderName`; credentials read from
  request context and stamped per outbound request via `credentialsFromContext`. Missing API
  key → fail closed (error before any HTTP call).
- Client cache keyed by `lowercase(signozURL)` (was `HashTenantKey(apiKey, signozURL)`), so
  different API keys on the same URL share one client + connection pool.
- Analytics `/me` identity cache moved from a single per-client value to a map keyed by
  `HashCredential(apiKey, authHeader)` (renamed from `HashTenantKey`).
- OAuth validation path seeds credentials onto ctx before `ValidateCredentials`.
- Tests migrated to context-supplied credentials; added: per-URL sharing, fail-closed,
  per-credential identity isolation.

## Open Questions
- [x] Reuse #187 vs reimplement? → Reuse (cherry-pick), preserves authorship + tested code.
- [x] Stack vs fold into #192? → Separate stacked PR (base `feat/stateless-transport`).
- [ ] When #192 merges, retarget this PR to `main` and correct the #187 closing comment
  (it inaccurately cited #190/#191 as superseding — the real successor is this PR).
