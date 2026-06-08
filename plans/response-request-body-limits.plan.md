# Plan: Response & Request Body Limits

## Status
In Progress — code complete & tested (build/vet/full suite green); Codex (gpt-5.5/xhigh) reviewed; builder-query clamp removed per owner (response guard suffices). Stacked on PR #189.

## Context
Follow-up to PR #189 (scorch index + search_logs/search_traces raw clamp). Three unbounded-response vectors remained on the shared, memory-limited multi-tenant pod, and issue #70 flagged that request/response body size is undefined. This PR closes the remaining response vectors and the inbound request-body gap (fully closing #70 item 3).

## Approach
1. **Client-layer response guard** (`internal/client/client.go`): in `doRequest` (shared executor for every backend call), read `io.LimitReader(resp.Body, maxResponseBytes+1)` where `maxResponseBytes = 64 MiB`; if the body exceeds the cap, return a clear error (terminal, not retried) rather than truncating. One chokepoint bounds every tool, including future ones.
2. **Aggregate group clamp** (`internal/handler/tools/aggregate_helper.go`): `parseAggregateArgs` clamps `limit` via `clampLimit` and records `AggregateRequest.LimitClamped`; handlers return via `aggregateResult`, which **surfaces** a non-pagination note ("narrow time/filters/groupBy") when clamped.
3. **Inbound request body cap** (`internal/config/config.go` + `internal/mcp-server/server.go`): `MaxRequestBytes` config (env `MCP_MAX_REQUEST_BYTES`, default 4 MiB, positive-only). `maxBytesMiddleware` (outermost `/mcp` middleware, so it also bounds the method-span peek/reconstruct): rejects a declared over-cap `Content-Length` early with **413**; otherwise `http.MaxBytesReader` bounds the chunked/unknown-length stream (an over-cap read surfaces as mcp-go's JSON-RPC parse error). `limit <= 0` is a defensive no-op for programmatic configs.

Result wrappers share a `clampedResult(payload, clamped, note)` core: `rawSearchResult` (offset pagination) and `aggregateResult` (narrow).

**Not done — `execute_builder_query` clamp (removed per owner):** considered clamping the `limit` embedded in the caller's QB JSON, but the 64 MiB response guard already bounds it; clamping rewrote the caller's authored query for a ~64→16 MiB gain that wasn't worth the surface area. The response guard is its sole bound.

## Files to Modify
- `internal/client/client.go` — `maxResponseBytes` const + bounded read/error in `doRequest`.
- `internal/client/client_test.go` — oversize-rejected + large-under-cap-allowed tests.
- `internal/handler/tools/aggregate_helper.go` — `clampedResult`/`aggregateResult` helpers; clamp `limit` + `LimitClamped` in `parseAggregateArgs`.
- `internal/handler/tools/logs.go` / `traces.go` — aggregate handlers return via `aggregateResult`; `limit` param description (max 10000).
- `internal/handler/tools/aggregate_helper_test.go` — `TestParseAggregateArgs_LimitClamped`.
- `internal/handler/tools/limit_clamp_test.go` — `aggregateResult` note-as-separate-block test.
- `internal/config/config.go` — `MaxRequestBytes` field, env const, default 4 MiB.
- `internal/mcp-server/server.go` — `maxBytesMiddleware`, applied to `/mcp`.
- `internal/mcp-server/server_test.go` — middleware over/under cap + disabled-when-zero tests.
- `README.md` — `MCP_MAX_REQUEST_BYTES` env row; aggregate `limit` max.
- `plans/response-request-body-limits.*` — this pair.

## Out of Scope (separate)
- #70 item 1 (timeouts): already handled — comment on the issue.
- #70 item 2 (stateful/stateless streamable-HTTP): own investigation/PR.
- Deploy/infra: `GOMEMLIMIT≈1280MiB`, container limit 1.5→2 GiB (tracked in `mcp-oom-hardening.plan.md`).

## Verification
- `go build ./...`, `go vet ./...`, `go test ./...` all green.
- New tests execute and pass: `TestDoRequest_RejectsOversizeResponse` / `_AllowsLargeUnderCapResponse`; `TestClampBuilderQueryLimits_*`; `TestParseAggregateArgs_LimitClamped`; `TestMaxBytesMiddleware*`.
- Codex (gpt-5.5/xhigh) review of the diff.
