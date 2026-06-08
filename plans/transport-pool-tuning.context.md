# Feature: Tune the shared HTTP transport connection pool — Context & Discussion

## Original Prompt
> Also does existing cache not useful? Let's remove that if not
> (resolved decision: "Tune transport instead")

## Reference Links
- Issue [#69](https://github.com/SigNoz/signoz-mcp-server/issues/69) (closed not-needed — connection reuse already global).
- Related/closed: [#187](https://github.com/SigNoz/signoz-mcp-server/pull/187), [#193](https://github.com/SigNoz/signoz-mcp-server/pull/193) (cache-by-URL, dropped for unique-URL-per-tenant topology).

## Key Decisions & Discussion Log

### 2026-06-08 — Finding: the client cache never did connection pooling
- Every `SigNoz` client was built with `otelhttp.NewTransport(http.DefaultTransport)`
  (`client.go`), so all clients share Go's global `http.DefaultTransport` and its single
  connection pool. Connection reuse to a SigNoz host already happens process-wide, independent
  of the `handler.clientCache` (whose only warm state is the analytics `/me` identity, used
  only when analytics is enabled). This also invalidated #69's premise and #193's perf rationale.
- Decision (user): keep the client cache as-is; instead **tune the shared transport's idle-conn
  limits**, which is the real lever for connection reuse under concurrency.

### 2026-06-08 — Approach
- Introduce a package-level `sharedTransport` (a clone of `http.DefaultTransport`) reused by
  every client, so the single-shared-pool property is preserved while the limits are raised.
  Must NOT give each client its own transport — that would fragment the pool.
- Values: `MaxIdleConnsPerHost: 2 → 20`, `MaxIdleConns: 100 → 200`. Conservative starting
  points: per-host cap aids a hot host; global cap bounds idle FDs across many tenant hosts.
  Hardcoded for now (tunable later via env if metrics warrant).
- Stacking: standalone PR off `main` (independent of #192 stateless; touches only `client.go`).

## Open Questions
- [x] Remove the client cache? → No; tune the transport instead (cache still dedups `/me`).
- [ ] Make the limits env-configurable? → Deferred; revisit if prod metrics show connection
  churn or idle-FD pressure at a specific tenant scale.
