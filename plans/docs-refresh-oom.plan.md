# Plan: Docs refresh OOM hardening (HTTP mode, 512Mi)

## Status
In Progress — all five steps implemented on `fix/docs-refresh-oom`; pending review and PR.

## Context
Public report #305: a 512Mi HTTP-mode container is OOM-killed every ~6h by the scheduled docs
refresh. Measured on the embedded corpus, a full `BuildIndex` peaks at ~333 MiB heap and a
`Swap` at ~356 MiB while only ~25 MiB survives. The peak is live data inside one bleve batch,
so `GOMEMLIMIT` alone reduces it by only ~20%. Chunking the batch (100 pages) cuts the peak to
~124 MiB; applying small deltas to the live index costs 40 to 60 MiB. The first tick after every
restart and the 24h forced refresh are both unconditional full rebuilds today.

## Approach

### Step 1 — Quick wins
- `internal/config`: `SIGNOZ_DOCS_REFRESH_INTERVAL=0` (also `off`/`disabled`) sets
  `DocsRefreshDisabled`; `SIGNOZ_DOCS_FULL_REFRESH_INTERVAL=0` sets `DocsFullRefreshDisabled`.
  Non-positive parse no longer silently falls back to the default.
- `internal/docs.RefreshConfig` gains `DisableScheduled` / `DisableFullRefresh`; `run()` skips the
  corresponding timer. `Trigger` keeps working (recovery path when the embedded corpus fails).
- `refresh()` logs `docs refresh starting` (forced, current pages) before fetching and
  `docs refresh rebuilding index` (entries) before the build, so an OOM mid-rebuild leaves a trace.
- README: env table rows; new "Memory footprint" note under HTTP Mode with the measured peaks,
  a recommended floor, and the `GOMEMLIMIT` / interval knobs.

### Step 2 — GOMEMLIMIT backstop (revive #195)
- `pkg/memlimit.Configure(ctx, logger)`: respects an explicit `GOMEMLIMIT`, reads cgroup v2 then
  v1, ignores limits under 64 MiB, sets `ratio × limit` (default 0.9, `SIGNOZ_GOMEMLIMIT_RATIO`),
  logs the decision, never errors. Called from `cmd/server/main.go` after the logger exists.
- Docs: `docs/architecture.md` section and README env row. State plainly that it bounds
  steady-state growth, not the batch peak.

### Step 3 — Conditional fetches and content gating
- `Fetcher.FetchConditional(ctx, url, etag)` sends `If-None-Match`; 304 → new
  `FetchStatusNotModified`. `pageFetcher` stays as is; the refresher type-asserts an optional
  `conditionalFetcher` so test fakes keep compiling. Forced refreshes never send `If-None-Match`.
- `buildSnapshot`: on `NotModified` reuse the prior record (same as the 404 fallback); counts as
  success for the failure gate.
- `refresh()`: after building the candidate snapshot compute `diffSnapshots(current, next)`
  (added / changed / removed by canonical URL, comparing title, headings, body, section fields).
  Empty diff → `registry.PublishSnapshot(next)` (new snapshot, same live index, no rebuild),
  outcome `unchanged` / `forced-unchanged`. Non-empty → existing `Swap` (step 4 narrows this).
- `IndexRegistry`: introduce a refcounted `indexHandle` shared between entries so
  `closeWhenDrained` closes the bleve index only when the last entry referencing it drains.

### Step 4 — Chunked build and incremental live-index deltas (done)
- `BuildIndex` commits `idx.Batch` every `indexBatchSize = 100` pages and a final partial batch.
  Per-page document construction moved to `indexDocument(page) (canonical, doc, ok)` so a
  delta-applied index is byte-identical to a rebuilt one. The duplicate-canonical-URL error stays.
- `registry.ApplyDelta(ctx, next, delta)`: one batch with `Index` for added/changed and
  `Delete` for removed canonical URLs on the live index, then a new `IndexEntry` sharing the
  handle (`shareIndex`) with `snapshot = next` and generation + 1, and `incrementalApplies++`
  on the handle. It takes an extra handle reference under `r.mu` before building the batch and
  releases it on every path, so the index cannot close mid-apply. It falls back to `Swap` when
  there is no current entry and errors when the registry is closed.
- `registry.CanApplyDelta(delta, nextPages)` gates the path: non-empty delta,
  `len(added)+len(changed)+len(removed) <= deltaApplyMaxFraction (0.25) * nextPages`, and the
  handle's `incrementalApplies < maxIncrementalApplies (24)`.
- `IndexRegistry.writeMu` serializes `Swap` / `PublishSnapshot` / `ApplyDelta` against each
  other; readers keep using the `mu` RWMutex.
- `refresh()` decision on a non-empty delta: `forced` → `Swap` (`forced-full-rebuilt`, the
  compaction point); else `CanApplyDelta` → `ApplyDelta` (`applied-delta`); else `Swap`
  (`rebuilt`). A forced refresh with an empty delta stays `forced-unchanged`; the apply cap,
  not the daily forced tick, is what bounds segment growth.
- Outcomes: `applied-delta` added to the `DocsRefreshes` counter; generation still increments.
- `results` buffering in `buildSnapshot` stays (6 MiB × 3 is not worth the complexity).

### Step 5 — Guardrail (done)
- `TestGuardrail_DocsIndexBuildPeakHeap` in `internal/docs`: samples `HeapInuse` while building
  the embedded corpus and asserts the peak over baseline stays under
  `guardrails.DocsIndexBuildPeakHeapBudgetBytes` (224 MiB; measured 133 to 148) and resident under
  `DocsIndexResidentBudgetBytes` (64 MiB; measured ~30), and `indexBatchSize <= MaxDocsIndexBatchSize` (128). Add to `guardrails/tests.txt` and the
  README invariants list.

## Files to Modify
- `internal/config/config.go` — disable flags, env parsing
- `internal/docs/refresh.go` — disable flags in `run()`, start logs, conditional fetch, diff, delta path
- `internal/docs/fetcher.go` — `FetchConditional`, `FetchStatusNotModified`
- `internal/docs/index.go` — `indexHandle`, `PublishSnapshot`, `ApplyDelta`, chunked `BuildIndex`
- `internal/docs/types.go` — new fetch status
- `internal/docs/verification_test.go`, new `refresh_delta_test.go`, `guardrail_test.go` — tests
- `internal/mcp-server/server.go` — honour disable flags
- `pkg/memlimit/memlimit.go`, `pkg/memlimit/memlimit_test.go`, `cmd/server/main.go` — step 2
- `guardrails/policy.go`, `guardrails/tests.txt`, `guardrails/README.md` — step 5
- `README.md`, `docs/architecture.md` — docs

## Verification
- `go test ./...`, `go build ./cmd/server`, `make fmt goimports`.
- Guardrail suite: `go test -count=1 -run '^TestGuardrail_' ./...` and the `tests.txt` inventory check.
- Measurement harness re-run after step 4 (embedded corpus, 746 pages, heap over a post-load
  baseline of ~9 MiB): chunked `BuildIndex` peak 133 to 148 MiB, resident ~30 MiB; `Swap` with
  the first index live peak 167 to 186 MiB; `ApplyDelta` peak 45 / 60 / 113 MiB for 1% / 5% /
  20% of pages with ~1 MiB resident growth per apply.
- Live check that signoz.io honours `If-None-Match` (subagent, read-only).
- Final review by the astra agent.
