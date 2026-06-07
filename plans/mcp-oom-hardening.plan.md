# Plan: MCP Server OOM Hardening

## Status
In Progress — code complete & validated locally (scorch −90% resident, limit clamp verified vs staging); pending review/commit + deploy-side GOMEMLIMIT/limit changes in infra repo.

## Context
Prod `signoz-mcp-server` OOMKills on a ~1.5 GiB-limited, multi-tenant shared pod. Measured root condition: the in-memory docs **bleve index** (legacy upsidedown/gtreap backend) costs ~323 MiB resident + ~1.5 GiB allocation churn per 6h/24h rebuild — ~42% of steady state and a latent per-replica rebuild spike — and there is no `GOMEMLIMIT` soft ceiling, so any transient burst goes straight to SIGKILL. Separately, raw-data tools accept an uncapped `limit`, making a single large query an unbounded memory vector.

## Approach
1. **Docs index backend → scorch (in-memory).** In `internal/docs/index.go` `BuildIndex`, replace `bleve.NewMemOnly(newIndexMapping())` with `bleve.NewUsing("", newIndexMapping(), scorch.Name, scorch.Name, nil)`. scorch is far more memory-efficient than upsidedown/gtreap and is already bleve's on-disk default. No other code changes — everything uses the generic `bleve.Index` interface. Validate search still works via the existing docs test suite; re-run `memprofile_test.go` to quantify the resident + churn reduction.
2. **Cap `limit` in raw-data tools.** In `internal/handler/tools` clamp `search_logs` / `search_traces` `limit` to a max (start 10000) so a single tenant query cannot buffer an unbounded response. Log/annotate when clamped.
3. **Deploy recommendations (separate infra repo, not this PR):** set `GOMEMLIMIT≈1280MiB` (+`GOGC=50`) and raise the container memory limit 1.5→2 GiB. Documented in the PR description.

## Files to Modify
- `internal/docs/index.go` — import `index/scorch`; swap `NewMemOnly` → in-memory scorch in `BuildIndex`.
- `internal/handler/tools/logs_helper.go` + `traces_helper.go` (or shared `intArg` call sites) — clamp `limit` to a max constant.
- `plans/mcp-oom-hardening.*` — this pair (committed alongside).

## Follow-ups (out of scope for this PR — Codex review, 2026-06-07)
- `signoz_search_traces` exposes an `offset` param that is parsed but ignored (pre-existing no-op; `BuildTracesQueryPayload` hardcodes `Offset: 0`). Either wire it intentionally or remove the dead param. (Offset wiring was added then reverted per owner decision to keep this PR scoped.)
- Bound `signoz_execute_builder_query`: tenant controls `limit` inside the raw query-builder JSON, so it bypasses the clamp — an equivalent large-response vector. Needs query-level inspection/clamp.
- Clamp `aggregate_logs` / `aggregate_traces` `limit` (groups; smaller payloads, lower priority — reuse `clampLimit`).
- Consider a client-layer response guard. NOTE: a hard `io.LimitReader` byte cap at `client.go` would truncate valid JSON mid-stream → parse errors; prefer per-query limits over a blunt byte cap.
- README: add a detailed `signoz_search_traces` parameter section (currently only a table row); note the trace `limit` cap + the newly-honored `offset`.
- Deploy/infra repo: `GOMEMLIMIT≈1280MiB` (+`GOGC=50`) and raise container limit 1.5→2 GiB.

## Verification
- `go build ./...` and `go test ./internal/docs ./internal/handler/tools` pass (update snippet/score goldens only if scorch legitimately changes them).
- Re-run `go test ./internal/docs -run TestMemProfileDocsIndex -v` → resident heap and churn/build should drop materially vs the upsidedown baseline (323 MiB / 1.5 GiB).
- Re-run the real-backend load harness with a huge `limit` → confirm the clamp caps the response.
- Throwaway harnesses (`memprofile_test.go`, `loadprofile_test.go`) are NOT committed unless converted to proper benchmarks.
