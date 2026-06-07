# Plan: Response & Request Body Limits

## Status
In Progress — code complete & tested (build/vet/full suite green); Codex (gpt-5.5/xhigh) reviewed, all should-fix/nits addressed; pending commit. Stacked on PR #189.

## Context
Follow-up to PR #189 (scorch index + search_logs/search_traces raw clamp). Three unbounded-response vectors remained on the shared, memory-limited multi-tenant pod, and issue #70 flagged that request/response body size is undefined. This PR closes the remaining response vectors and the inbound request-body gap (fully closing #70 item 3).

## Approach
1. **Client-layer response guard** (`internal/client/client.go`): in `doRequest` (shared executor for every backend call), read `io.LimitReader(resp.Body, maxResponseBytes+1)` where `maxResponseBytes = 64 MiB`; if the body exceeds the cap, return a clear error (terminal, not retried) rather than truncating. One chokepoint bounds every tool, including future ones.
2. **execute_builder_query limit clamp** (`internal/handler/tools/query_builder.go`): after `Validate()`, `clampBuilderQueryLimits` walks `CompositeQuery.Queries` and clamps **every** `builder_query` `QuerySpec.Limit > MaxRawResultLimit` regardless of requestType (rows for raw/trace, groups for scalar/time_series; non-builder_query specs skipped via the type assertion). Returns via `builderQueryResult` (type-agnostic note). Closes the opaque-JSON door the #189 clamp couldn't reach — including the `trace` requestType and manual aggregate group limits.
3. **Aggregate group clamp** (`internal/handler/tools/aggregate_helper.go`): `parseAggregateArgs` clamps `limit` via `clampLimit` and records `AggregateRequest.LimitClamped`; handlers return via `aggregateResult`, which **surfaces** a non-pagination note ("narrow time/filters/groupBy") when clamped.
4. **Inbound request body cap** (`internal/config/config.go` + `internal/mcp-server/server.go`): `MaxRequestBytes` config (env `MCP_MAX_REQUEST_BYTES`, default 4 MiB, positive-only). `maxBytesMiddleware` (outermost `/mcp` middleware, so it also bounds the method-span peek/reconstruct): rejects a declared over-cap `Content-Length` early with **413**; otherwise `http.MaxBytesReader` bounds the chunked/unknown-length stream (an over-cap read surfaces as mcp-go's JSON-RPC parse error). `limit <= 0` is a defensive no-op for programmatic configs.

Result wrappers refactored to a shared `clampedResult(payload, clamped, note)` core: `rawSearchResult` (offset pagination), `aggregateResult` (narrow), `builderQueryResult` (mixed).

## Files to Modify
- `internal/client/client.go` — `maxResponseBytes` const + bounded read/error in `doRequest`.
- `internal/client/client_test.go` — oversize-rejected + large-under-cap-allowed tests.
- `internal/handler/tools/query_builder.go` — `clampBuilderQueryLimits` + `rawSearchResult` return + description note.
- `internal/handler/tools/aggregate_helper.go` — clamp `limit` in `parseAggregateArgs`.
- `internal/handler/tools/logs.go` / `traces.go` — aggregate `limit` param description (max 10000).
- `internal/handler/tools/builder_aggregate_clamp_test.go` (new) — builder clamp (raw/non-raw/unset) + aggregate clamp tests.
- `internal/config/config.go` — `MaxRequestBytes` field, env const, default 4 MiB.
- `internal/mcp-server/server.go` — `maxBytesMiddleware`, applied to `/mcp`.
- `internal/mcp-server/server_test.go` — middleware over/under cap + disabled-when-zero tests.
- `README.md` — `MCP_MAX_REQUEST_BYTES` env row; aggregate `limit` max; `execute_builder_query` row-limit note.
- `plans/response-request-body-limits.*` — this pair.

## Out of Scope (separate)
- #70 item 1 (timeouts): already handled — comment on the issue.
- #70 item 2 (stateful/stateless streamable-HTTP): own investigation/PR.
- Deploy/infra: `GOMEMLIMIT≈1280MiB`, container limit 1.5→2 GiB (tracked in `mcp-oom-hardening.plan.md`).

## Verification
- `go build ./...`, `go vet ./...`, `go test ./...` all green.
- New tests execute and pass: `TestDoRequest_RejectsOversizeResponse` / `_AllowsLargeUnderCapResponse`; `TestClampBuilderQueryLimits_*`; `TestParseAggregateArgs_LimitClamped`; `TestMaxBytesMiddleware*`.
- Codex (gpt-5.5/xhigh) review of the diff.
