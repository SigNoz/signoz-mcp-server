# Plan: Docs refresh OOM hardening (HTTP mode, 512Mi)

Status: In Progress (steps 1, 3, 4, 5 implemented on `fix/docs-refresh-oom`; step 2 dropped; PR pending)
Issue: https://github.com/SigNoz/signoz-mcp-server/issues/305 (public), https://github.com/SigNoz/nerve-pod/issues/231 (internal tracker)
PR: https://github.com/SigNoz/signoz-mcp-server/pull/308

## Context

Public report #305: a `v0.14.0` HTTP-mode container with a 512Mi limit is OOM-killed every 5.5 to
6.5 hours, matching the 6h ± 30m docs refresh cadence. Every code claim in the report checks out
against `main`. Two things the report missed: the 24h forced refresh bypasses the sitemap no-op and
rebuilds daily, and fetch concurrency is already capped at 20, so fetching is not the spike.

Measured on the embedded corpus (746 pages, 6 MiB body text) before any change:

| Operation | Alloc churn | Resident after | Peak heap |
| --- | --- | --- | --- |
| Full `BuildIndex` | 403 MiB | 25 MiB | 333 MiB |
| `Swap` (2nd build, 1st live) | 403 MiB | 49 MiB | 356 MiB |
| Live-index `Batch`, 1% / 5% / 20% of pages | 6 / 21 / 81 MiB | 25 to 29 MiB | 40 / 57 / 106 MiB |

The peak is live data inside one bleve batch (every analysed document is held until `idx.Batch`
commits), not reclaimable garbage. Under `GOMEMLIMIT` soft limits the build+swap peak only moved
from 413 MiB (unset) to 326 / 308 / 294 MiB at 256 / 128 / 96 MiB while GC count rose 13 → 197.

Prior work: `plans/mcp-oom-hardening.*` (scorch swap, PR #189) and closed PR #195
(cgroup-derived GOMEMLIMIT, closed in favour of a downward-API env that was never added to
`cl.gcp.deployments`).

Original prompt: "Let's implement all 5 one by one, don't comment on public issue yet, use opus 5
subagent when needed. At the end get a review from astra /codex."

Resolved questions:

- Does signoz.io serve usable ETags for docs markdown? Yes. Weak ETags (`W/"<sha1>"`), stable
  across requests and edge nodes, 304 with empty body on `If-None-Match`, no `Last-Modified`.
  `Vary: Accept` is set on the markdown variant; the fetcher already sends `Accept: text/markdown`.
- Revive #195 in code or Helm/README only? Neither for now; see the 2026-09-16 drop decision.
- Keep the 24h forced refresh once content gating lands? Yes; it is the compaction point for the
  incrementally updated index.

Open: reply on the public issue once the PR is up.

## Approach

### Step 1 — Quick wins
- `internal/config`: `SIGNOZ_DOCS_REFRESH_INTERVAL=0` (also `off`/`disabled`/`false`) sets
  `DocsRefreshDisabled`; `SIGNOZ_DOCS_FULL_REFRESH_INTERVAL=0` sets `DocsFullRefreshDisabled`.
  The "full shorter than incremental" fallback applies only when both schedules run.
- `internal/docs.RefreshConfig` gains `DisableScheduled` / `DisableFullRefresh`; `run()` uses nil
  timer channels for disabled schedules. `Trigger` keeps working for the empty-corpus recovery path.
- `refresh()` logs `docs refresh starting`, `docs refresh fetching pages`, and
  `docs refresh rebuilding index` before the work they name, so an OOM mid-refresh leaves a trace.
- README: env rows and a "Memory footprint" section under HTTP Mode.

### Step 2 — GOMEMLIMIT backstop (dropped)
Implemented as `pkg/memlimit` (revived #195), then removed. See Key Decisions.

### Step 3 — Conditional fetches and content gating
- `Fetcher.FetchConditional(ctx, url, etag)` sends `If-None-Match`; 304 yields
  `FetchStatusNotModified`. The refresher type-asserts an optional `conditionalFetcher` so test
  fakes only implement `Fetch`. Forced refreshes never revalidate.
- `buildSnapshot` reuses the prior record on `NotModified` (same projection as the 404 fallback);
  it counts as success for the failure-threshold gate.
- `diffSnapshots(current, next)` lists added / changed / removed pages by canonical URL, comparing
  title, section fields, headings, and body. `FetchedAt` and `SourceETag` are bookkeeping.
- Empty delta → `IndexRegistry.PublishSnapshot(next)`: new snapshot over the same live index, no
  rebuild, outcomes `unchanged` / `forced-unchanged`. The next scheduled tick then hits the
  sitemap-hash no-op.
- `IndexEntry` generations share a refcounted `indexHandle`; `closeWhenDrained` closes the bleve
  index only when the last entry referencing it drains. `FetchDoc` serves `last_fetched_at` from
  the published snapshot (per-entry `fetchedAt` map) so content gating never leaves it stale.

### Step 4 — Chunked build and incremental live-index deltas
- `BuildIndex` commits every `indexBatchSize = 100` pages plus a final partial batch. Per-page
  document construction lives in `indexDocument` so a delta-applied index equals a rebuilt one.
- `IndexRegistry.ApplyDelta(ctx, next, delta)`: one batch of `Index` (added/changed) and `Delete`
  (removed) on the live index, then a new generation sharing the handle with `incrementalApplies++`.
  `CanApplyDelta` gates it: touched pages ≤ `deltaApplyMaxFraction (0.25) × len(next.Pages)` and
  `incrementalApplies < maxIncrementalApplies (24)`.
- `refresh()` on a non-empty delta: forced → `Swap` (`forced-full-rebuilt`, the compaction point);
  else `CanApplyDelta` → `ApplyDelta` (`applied-delta`); else `Swap` (`rebuilt`). A forced refresh
  with an empty delta stays `forced-unchanged`; the apply cap bounds segment growth.
- `writeMu` serializes `Swap`, `PublishSnapshot`, `ApplyDelta`, and `Close`.
- The `results` buffering in `buildSnapshot` stays; 6 MiB × 3 is not worth the complexity.

### Step 5 — Guardrail
`TestGuardrail_DocsIndexBuildPeakHeap` (`internal/docs`) samples `HeapInuse` while building the
embedded corpus and asserts peak over baseline ≤ `guardrails.DocsIndexBuildPeakHeapBudgetBytes`
(224 MiB), resident ≤ `DocsIndexResidentBudgetBytes` (64 MiB), and
`indexBatchSize ≤ MaxDocsIndexBatchSize` (128). Listed in `guardrails/tests.txt` and the README
invariants.

## Files to Modify

- `internal/config/config.go` — disable flags, `getEnvRefreshInterval`
- `internal/docs/refresh.go` — disable flags in `run()`, start logs, conditional fetch, `diffSnapshots`, delta decision
- `internal/docs/fetcher.go` — `FetchConditional`
- `internal/docs/types.go` — `FetchStatusNotModified`
- `internal/docs/index.go` — `indexHandle`, `PublishSnapshot`, `ApplyDelta`, `CanApplyDelta`, chunked `BuildIndex`, `indexDocument`, `writeMu`, snapshot-backed `last_fetched_at`
- `internal/mcp-server/server.go` — pass disable flags
- `internal/docs/refresh_schedule_test.go`, `refresh_conditional_test.go`, `refresh_delta_test.go`, `review_fixes_test.go`, `guardrail_test.go` — tests
- `internal/config/docs_refresh_interval_test.go` — tests
- `guardrails/policy.go`, `guardrails/tests.txt`, `guardrails/README.md` — step 5
- `README.md` — env rows, Memory footprint section

## Key Decisions

### 2026-09-16 — Chunk size 100; incremental deltas capped; compaction by rebuild
- Decision: `BuildIndex` commits every 100 pages; live-index deltas only for ≤ 25% of pages and
  at most 24 applies per index; larger deltas, the cap, and forced refreshes rebuild.
- Rationale: chunk sweep (peak / resident MiB): N=0 325/17; N=200 183/16; N=100 124/20; N=50
  113/29; N=25 139/50; N=10 283/191. Search stays sub-millisecond to N=50 with identical hits.
  bleve v2.5.4 `scorch.Open` starts merger and persister only when `path != ""`, so an in-memory
  index keeps one segment per batch forever; tiny chunks and unbounded deltas inflate residency.
  `ForceMerge` cannot be used because nothing reads `forceMergeRequestCh` in-memory.
- Alternatives rejected: streaming pages into the index as they arrive (removes only the 6 MiB × 3
  body copies); relying on GOMEMLIMIT (see below).

### 2026-09-16 — Content gating compares indexed content only
- Decision: `diffSnapshots` ignores `FetchedAt` and `SourceETag`; unchanged content publishes a
  new snapshot over the live index instead of rebuilding, on scheduled and forced paths alike.
- Rationale: the first tick after every restart and the daily forced refresh were unconditional
  rebuilds. Forced now means "re-fetch everything", not "re-index unconditionally".
- Test fixture lesson: build the baseline snapshot from entries parsed out of the served sitemap,
  otherwise section breadcrumbs differ from a live fetch and the diff reports changes.
  `TestSingleflightSerialization` now expects `forced-unchanged` for its identical-body forced pass.

### 2026-09-16 — Astra review: one blocker and five should-fix items, all fixed
- `Close` could interleave with an in-flight `ApplyDelta`/`Swap` and drain a shared handle twice or
  leak a fresh index → `Close` takes `writeMu`; `TestCloseWaitsForInFlightApplyDelta` via the
  `applyDeltaBeforePublish` seam.
- Disabled incremental schedule reset a custom full-refresh interval to 24h → ordering fallback
  only when both schedules run.
- Content gating left the stored `last_fetched_at` stale → served from the published snapshot.
- README: setting only `SIGNOZ_DOCS_REFRESH_INTERVAL=0` keeps the daily forced refresh; both env
  vars must be `0`. Sitemap no-op, the 24-apply cap, and completion log lines described as
  examples; OOM diagnosis wording softened.
- Kept the sampled peak budget (224 MiB vs 113 to 148 measured) plus the deterministic batch-size
  assertion, per the reviewer's advice not to tighten the sample.
- `GOMEMLIMIT=off` was overridden by `pkg/memlimit` → fixed, then made moot by the drop below.

### 2026-09-16 — Step 2 (GOMEMLIMIT) dropped at owner's request
- Decision: remove `pkg/memlimit`, its `main.go` hook, the architecture.md section, and the
  `SIGNOZ_GOMEMLIMIT_RATIO` README row. README keeps `GOMEMLIMIT` as an optional operator knob.
- Rationale: owner said "drop gomemlimit for now as it's not useful for this case." The soft limit
  trims the build peak by ~20% only, chunked indexing is the real fix, and `cl.gcp.deployments`
  sets a 1024Mi request with no memory limit so cgroup detection would fail open there.
- Alternatives rejected: reopening #195; Helm/compose-only `GOMEMLIMIT` (our own manifests set no limit).

### 2026-09-16 — Impact on running cloud deployments
- No change until an image tag is bumped (regional production pins `v0.14.0`, mgmt `v0.11.0`).
- After upgrade: defaults unchanged, new log lines only; fewer rebuilds and ~60% lower peaks.
  KEDA scales these pods on 60% memory utilization of the request, so replica counts may sit
  closer to the minimum of 2. Roll to `staging/cl-us-central1-b` first and watch the refresh lines.

## Reference Links

- [Public report #305](https://github.com/SigNoz/signoz-mcp-server/issues/305)
- [Internal tracker nerve-pod#231](https://github.com/SigNoz/nerve-pod/issues/231)
- [PR #189 scorch swap](https://github.com/SigNoz/signoz-mcp-server/pull/189)
- [PR #195 cgroup GOMEMLIMIT (closed)](https://github.com/SigNoz/signoz-mcp-server/pull/195)
- Prior plan pair: `plans/mcp-oom-hardening.context.md`, `plans/mcp-oom-hardening.plan.md`

## Verification

- `go build ./...`, `go vet`, `make fmt goimports`: clean.
- `go test ./internal/docs/ -race -count=1`: green after every step and after the review fixes.
- `go test -count=1 -run '^TestGuardrail_' ./...`: green; `guardrails/tests.txt` sorted and
  matching `go test -list`. `TestGuardrail_DocsIndexBuildPeakHeap` passed five consecutive runs
  (113 to 139 MiB peak, 25 to 30 MiB resident) and fails when `indexBatchSize` is forced large.
- `go test ./...`: green except `internal/mcpcontract`, which also fails on `main` with the local
  Go 1.27.1 toolchain (error type renamed to `jsontext.Value`); unrelated to this branch.
- Measured after step 4 (embedded corpus, heap over a ~9 MiB post-load baseline, two runs):

  | Moment | Peak heap | Resident after |
  | --- | --- | --- |
  | Chunked `BuildIndex` | 133 / 148 MiB | ~30 MiB |
  | `Swap` with the first index live | 186 / 167 MiB | ~30 MiB |
  | `ApplyDelta` 1% (7 pages) | 45 / 47 MiB | +0 MiB |
  | `ApplyDelta` 5% (37 pages) | 60 / 63 MiB | +1 MiB |
  | `ApplyDelta` 20% (149 pages) | 114 / 112 MiB | +0 MiB |

- Live check (read-only subagent, 11 requests): signoz.io honours `If-None-Match` with 304.
- Review: astra agent, two passes; all findings fixed and re-verified.
- Gaps: no E2E against a memory-limited container; the guardrail and the harness above stand in.
  A staging rollout is the remaining live check.

## Outcome

PR #308 open. Shipped on the branch: steps 1, 3, 4, 5. Step 2 implemented then dropped (commits
`feat(memlimit)` and `revert(memlimit)` remain in history). No MCP contract changed, so the
agent-skills repo needs no companion change. Public issue not yet answered; nerve-pod#231 not yet
updated.
