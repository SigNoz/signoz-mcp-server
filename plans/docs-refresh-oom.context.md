# Feature: Docs refresh OOM hardening (HTTP mode, 512Mi) — Context & Discussion

## Original Prompt
> Let's implement all 5 one by one, don't comment on public issue yet, use opus 5 subagent when needed. At the end get a review from astra /codex.

Refers to the five-step plan recorded in the internal tracker (SigNoz/nerve-pod#231) for the
public report SigNoz/signoz-mcp-server#305.

## Reference Links
- Public report: https://github.com/SigNoz/signoz-mcp-server/issues/305
- Internal tracker: https://github.com/SigNoz/nerve-pod/issues/231
- Prior work: `plans/mcp-oom-hardening.*` (scorch swap, PR #189); closed PR #195 (cgroup-derived GOMEMLIMIT)

## Key Decisions & Discussion Log

### 2026-09-16 — Report verified; measurements on the embedded corpus (746 pages, 6 MiB body text)
- Every code claim in #305 checks out against `main`.
- Full `BuildIndex`: 403 MiB alloc churn, 25 MiB resident after, **333 MiB peak heap**.
- `Swap` (2nd build while 1st live): **356 MiB peak**, 49 MiB double-resident.
- Live-index `Batch` of 1% / 5% / 20% of pages: 40 / 57 / 106 MiB peak.
- Not in the report: the 24h forced refresh bypasses the sitemap no-op and rebuilds daily; fetch
  concurrency is already capped at 20 so fetching is not the spike.

### 2026-09-16 — GOMEMLIMIT alone does not bound the build peak
- Measured build+swap under soft limits: unset → 413 MiB peak; 256MiB → 326; 128MiB → 308;
  96MiB → 294 (GC count 13 → 197). The peak is *live* data inside the single bleve batch (all
  746 analysed documents held until `idx.Batch` commits), not reclaimable garbage.
- Consequence: step 2 stays (steady-state backstop, cheap) but is not the main lever the
  tracker assumed. The honest framing goes into README and the plan.

### 2026-09-16 — Chunked batches bound the peak; in-memory scorch never merges
- `BuildIndex` with `idx.Batch` every N pages: N=0 (today) 325 MiB peak / 17 MiB resident;
  N=200 183/16; **N=100 124/20**; N=50 113/29; N=25 139/50; N=10 283/191. Search latency stays
  sub-millisecond up to N=50. Same hit counts.
- bleve v2.5.4 `scorch.Open` starts the merger and persister only when `path != ""`. An
  in-memory index keeps one segment per batch forever, which is why tiny chunks inflate
  resident memory and why an incrementally-updated live index must be compacted periodically
  (rebuild + swap). `ForceMerge` cannot be used: nothing reads `forceMergeRequestCh` in-memory.
- Decision: chunk size 100 for `BuildIndex`; incremental live-index updates only for deltas up to
  25% of the corpus; compaction via chunked rebuild on the forced (24h) refresh or after a
  bounded number of incremental applies.

### 2026-09-16 — Step decomposition (one commit per step on `fix/docs-refresh-oom`)
1. Quick wins: interval `0` disables scheduled refresh; log at refresh start; README memory note.
2. Revive #195 `pkg/memlimit` (fail-open, cgroup-derived, `GOMEMLIMIT` env wins). Documented as
   a backstop, not the fix, per the measurement above.
3. Conditional fetches (`If-None-Match` / 304 reuse) and content gating (skip index rebuild when
   no page changed) on both scheduled and forced paths. Introduces a registry method that
   publishes a new snapshot sharing the live index, which step 4 also needs.
4. Chunked `BuildIndex` plus incremental live-index deltas with compaction.
5. `TestGuardrail_DocsIndexBuildPeakHeap` asserting the embedded-corpus rebuild peak stays
   under a budget in `guardrails/policy.go`.

### 2026-09-16 — Step 3 landed: conditional fetches and content gating
- Live probe (read-only subagent, 11 requests): signoz.io serves stable weak ETags for docs
  markdown and returns 304 on `If-None-Match`; no `Last-Modified`. Conditional path is real.
- `Fetcher.FetchConditional` + `FetchStatusNotModified`; the refresher type-asserts an optional
  `conditionalFetcher` so `mapFetcher` test fakes keep compiling. Forced refreshes never revalidate.
- `diffSnapshots` compares indexed content only (title, section fields, headings, body); FetchedAt
  and SourceETag are bookkeeping. Empty delta → `PublishSnapshot` (new snapshot, same live index),
  outcomes `unchanged` / `forced-unchanged`. `TestSingleflightSerialization` updated: its forced
  pass re-fetches an identical body and is now `forced-unchanged`.
- `IndexRegistry` gained a refcounted `indexHandle` shared across entry generations;
  `closeWhenDrained` closes the bleve index only when the last entry referencing it drains.
- Test fixture lesson: the baseline snapshot must be built from entries parsed out of the served
  sitemap, otherwise section breadcrumbs differ from a live fetch and the diff reports changes.

### 2026-09-16 — Step 4 landed: chunked build and incremental live-index deltas
- `BuildIndex` now commits every 100 pages; per-page document construction moved into
  `indexDocument` so `ApplyDelta` and a rebuild produce the same documents.
- `IndexRegistry` gained `writeMu` (serializes `Swap` / `PublishSnapshot` / `ApplyDelta`),
  `ApplyDelta`, `CanApplyDelta`, and an `incrementalApplies` counter on `indexHandle`.
  `ApplyDelta` takes an extra handle reference under `r.mu` before building the batch so a
  draining previous generation cannot close the index mid-apply.
- `refresh()` picks: forced → `Swap` (`forced-full-rebuilt`); small delta on a young handle →
  `ApplyDelta` (`applied-delta`); otherwise `Swap` (`rebuilt`). Thresholds
  `deltaApplyMaxFraction = 0.25`, `maxIncrementalApplies = 24`.
- Measured on the embedded corpus (746 pages), heap over a ~9 MiB post-load baseline, two runs:

  | Moment | Peak heap | Resident after |
  | --- | --- | --- |
  | Chunked `BuildIndex` | 133 / 148 MiB | ~30 MiB |
  | `Swap` with the first index live | 186 / 167 MiB | ~30 MiB |
  | `ApplyDelta` 1% (7 pages) | 45 / 47 MiB | +0 MiB |
  | `ApplyDelta` 5% (37 pages) | 60 / 63 MiB | +1 MiB |
  | `ApplyDelta` 20% (149 pages) | 114 / 112 MiB | +0 MiB |

  Against the pre-step-4 baseline (333 MiB build, 356 MiB swap) that is a ~2.3x cut in the
  startup peak and a ~2x cut in the rebuild peak, and a small scheduled delta now costs single
  digit MiB over the live index instead of a full rebuild.
- Deviations from the plan: none functionally. The plan's predicted numbers (125 MiB build,
  150 MiB forced compaction) came in slightly higher (133 to 148, and 167 to 186 for a swap
  with a live index); the README table quotes the rounded measured values rather than the
  predictions. The README recommendation drops from `768Mi` to `512Mi`.
- Tests: new `internal/docs/refresh_delta_test.go` covers add/change/remove through
  `ApplyDelta`, snapshot publication and generation, handle sharing, the four refresh
  decisions (applied-delta, over-fraction rebuild, forced rebuild, apply-cap compaction),
  8 readers against 10 concurrent applies under `-race`, and a 250-page chunked build.
  `go test ./internal/docs/ -race -count=1` is green.

### 2026-09-16 — Astra review of the branch and fixes applied
- Blocker: `Close` could run while `ApplyDelta`/`Swap` held `writeMu`, so a writer could publish
  after shutdown (leaking a fresh index or draining a shared handle twice). Fix: `Close` takes
  `writeMu`; writers and shutdown are now mutually exclusive. Test seam `applyDeltaBeforePublish`
  plus `TestCloseWaitsForInFlightApplyDelta`.
- `GOMEMLIMIT=off` looked identical to "unset" at the runtime level and was overridden. Fix: any
  non-empty `GOMEMLIMIT` env short-circuits `Configure`. Test added.
- `NewRefresher` reset a 1h full-refresh interval to 24h when the incremental schedule was
  disabled (it compared against the 6h placeholder). Fix: ordering fallback applies only when both
  schedules run. Test added.
- Content gating left the `last_fetched_at` stored field stale for pages that were re-downloaded
  unchanged. Fix: `FetchDoc` serves `last_fetched_at` from the published snapshot (per-entry
  `fetchedAt` map), falling back to the stored field. Assertion added to the forced-refresh test.
- README: `SIGNOZ_DOCS_REFRESH_INTERVAL=0` alone leaves the daily forced refresh running; docs now
  say to set both to `0`. Sitemap no-op, the 25-apply compaction cap, and the full list of
  completion log lines are described; the OOM diagnosis wording is softened to "most likely".
- Kept: sampled peak budget (224 MiB vs 113 to 148 measured) plus the deterministic
  `indexBatchSize <= 128` assertion, per the reviewer's recommendation not to tighten the sample.

### 2026-09-16 — Step 2 (GOMEMLIMIT) dropped at owner's request
- Owner: "drop gomemlimit for now as it's not useful for this case." Grounds: the soft limit cuts
  the build peak by only ~20% (live batch data, not garbage), chunked indexing is the real fix,
  and `cl.gcp.deployments` sets a 1024Mi request with no memory limit, so cgroup detection would
  fail open there anyway.
- Removed `pkg/memlimit`, the `main.go` hook, the architecture.md section, the
  `SIGNOZ_GOMEMLIMIT_RATIO` README row, and the auto-set sentence in the README recommendation.
  The review-fix test for `GOMEMLIMIT=off` went with the package.
- Not reopening #195. If self-hosted operators need a soft limit they can set `GOMEMLIMIT` directly.

## Open Questions
- [x] Revive #195 in code or Helm/README only? → Neither, for now. Revived in code during step 2,
  then dropped (see 2026-09-16 entry below): not useful for the batch peak, and a no-op on the
  cloud deployments, which set no memory limit. README mentions `GOMEMLIMIT` as optional only.
- [x] Does signoz.io return usable `ETag` / honour `If-None-Match` for docs markdown? → Yes.
  Weak ETags (`W/"<sha1>"`), stable across requests and edge nodes, 304 with empty body on
  `If-None-Match`. No `Last-Modified`, so `If-Modified-Since` is not an option. `Vary: Accept` is
  set on the markdown variant; always send `Accept: text/markdown` (the fetcher already does).
- [ ] Keep the 24h forced refresh once content hashing lands? → Keep; it becomes the compaction
  point for the incrementally-updated index.
- [ ] Reply on the public issue once the branch is reviewed.
