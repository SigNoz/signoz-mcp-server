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

## Open Questions
- [x] Revive #195 in code or Helm/README only? → In code, fail-open, with `GOMEMLIMIT` env
  still taking precedence (self-hosted users get a sane default; our infra keeps the downward-API env).
- [ ] Does signoz.io return usable `ETag` / honour `If-None-Match` for docs markdown? (Live check
  during step 3; code must work either way.)
- [ ] Keep the 24h forced refresh once content hashing lands? → Keep; it becomes the compaction
  point for the incrementally-updated index.
- [ ] Reply on the public issue once the branch is reviewed.
