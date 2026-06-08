# Feature: Auto-set GOMEMLIMIT from the container memory limit — Context & Discussion

## Original Prompt
> Ok what other improvement can we make? → (chose) "GOMEMLIMIT auto-set"

## Reference Links
- Memory model / OOM: `memory/project_mcp_oom.md` — bleve docs index ~323 MiB resident +
  ~1.5 GiB rebuild churn; **no GOMEMLIMIT**; a 2026-06-06 production OOM (sub-60s burst).
- Go 1.25 release notes: GOMAXPROCS is now cgroup-CPU-aware by default (so no automaxprocs needed).

## Key Decisions & Discussion Log

### 2026-06-08 — Why GOMEMLIMIT, and scope
- `grep`: no `GOMEMLIMIT`/`SetMemoryLimit` anywhere; the runtime leaves the soft limit off
  (math.MaxInt64) unless the `GOMEMLIMIT` env is set. Setting it makes the GC reclaim more
  aggressively as the heap approaches the limit, reducing cgroup OOM-kill risk.
- GOMAXPROCS: already cgroup-aware on Go ≥1.25 (`go.mod` says `go 1.25.5`) → no action.
- Honest scope: GOMEMLIMIT backstops steady-state / gradual-growth OOM and earlier reclaim. It
  does NOT prevent a genuine transient allocation spike larger than the limit — the docs-index
  rebuild churn (~1.5 GiB) still warrants its own fix (the deferred "shrink rebuild peak" item).

### 2026-06-08 — Implementation choices
- Dependency: `automemlimit` is the standard lib but NOT vendored, and adding it needs network.
  Decision: hand-roll a small, **fail-open** cgroup reader (v2 `memory.max`, v1
  `memory.limit_in_bytes`) — if no finite limit is found, or GOMEMLIMIT was already set
  explicitly, leave the runtime default untouched. Never errors.
- Respect explicit override: the Go runtime applies the `GOMEMLIMIT` env before `main`; detect
  via `debug.SetMemoryLimit(-1) != math.MaxInt64` and skip auto-setting.
- Ratio: default 0.9 (headroom for non-heap memory), override via `SIGNOZ_GOMEMLIMIT_RATIO`.
- Floor: ignore limits < 64 MiB (misread/pathological) to avoid GC thrash.
- Placement: `pkg/memlimit`, called from `cmd/server/main.go` right after the logger is created
  (before the heavy docs-index build / server start).
- Caveat (cgroup namespace): reads the standard root cgroup paths, correct for the common
  containerized case (private cgroupns); fails open otherwise. Documented in code.

## Open Questions
- [x] automemlimit vs hand-roll? → hand-roll, fail-open (no new dep).
- [x] GOMAXPROCS too? → no, runtime handles it on Go ≥1.25.
- [ ] Pair with the docs-index rebuild-peak fix later (the transient-spike OOM root cause).
