# Plan: Auto-set GOMEMLIMIT from the container memory limit

## Status
In Progress

## Context
The server runs no soft memory limit (GOMEMLIMIT unset), so the GC only reclaims on its normal
schedule — under memory pressure the cgroup OOM-kills the pod before Go gives memory back. A
documented production OOM (2026-06-06) and the bleve-index-dominated memory model make a soft
limit a cheap, high-value backstop. (GOMAXPROCS is already cgroup-aware on Go ≥1.25.)

## Approach
New `pkg/memlimit` with `Configure(ctx, logger)`, called early in `cmd/server/main.go`:
1. If GOMEMLIMIT is already set (`debug.SetMemoryLimit(-1) != math.MaxInt64`) → log + skip.
2. Read the container memory limit: cgroup v2 `/sys/fs/cgroup/memory.max` (`"max"` = unlimited),
   else cgroup v1 `/sys/fs/cgroup/memory/memory.limit_in_bytes` (huge value = unlimited).
3. If no finite limit, or < 64 MiB → log + skip (fail open; preserves current behavior).
4. Else `debug.SetMemoryLimit(ratio * limit)`, ratio default 0.9 (env `SIGNOZ_GOMEMLIMIT_RATIO`),
   and log the decision.
Pure helpers (`parseMemMax`, `parseMemLimitV1`, `ratioFromEnv`, `readMemoryLimitFrom`) are split
out for unit testing without a real cgroup.

## Files to Modify
- `pkg/memlimit/memlimit.go` (new), `pkg/memlimit/memlimit_test.go` (new)
- `cmd/server/main.go` — call `memlimit.Configure(ctx, logger)` after the logger is created
- `docs/architecture.md` — env list + a short "Memory limit (GOMEMLIMIT)" note

## Verification
- `go build ./...`, `go vet ./...`, `go test ./pkg/memlimit/... ./... -count=1` — green.
- Unit tests cover parse + read (temp files) + ratio + the skip/set decision.
- Manual: run with `GOMEMLIMIT=512MiB` → logs "already set; leaving as-is"; run with a fake
  cgroup v2 file → logs the derived limit; run on macOS/no-cgroup → logs "left unset".
- Codex review. (E2E not meaningfully applicable — would need a memory-limited container to
  observe GC behavior; covered by unit tests + startup-log verification instead.)
