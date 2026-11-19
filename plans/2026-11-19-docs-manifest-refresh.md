# Plan: Refresh the Docs Index from the Website Manifest

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/590
PR: https://github.com/SigNoz/signoz-mcp-server/pull/458

## Context

The daily forced refresh requests every Markdown page per instance. The website now
publishes ETags for its sitemap and rendered Markdown. Manika Malhotra implemented the
MCP consumer in [SigNoz/signoz-mcp-server#454](https://github.com/SigNoz/signoz-mcp-server/pull/454).
The review reproduced cache skew, incomplete manifests, and duplicate page requests.

## Approach

Use the manifest to skip unchanged pages. Check the sitemap ETag before applying membership
changes, keep the last-good page when an advertised ETag fails to match, and retry unresolved
pages on the next tick. Confirm sitemap-listed omissions by requesting the Markdown. Fetch
each URL once and preserve all of its section memberships. Use one jittered timer and
revalidate pages periodically in the sitemap-only fallback.

## Files to Modify

- `internal/docs/` handles scheduling, manifest reconciliation, and projected sitemap entries.
- `internal/config/` maps existing refresh settings to the single schedule.
- `cmd/build-docs-index/` reuses matching corpus records and fetches each URL once.
- `README.md` and `docs/architecture.md` describe the refresh policy.

## Key Decisions

### 2026-11-19 — Acknowledge reconciled manifests

An ETag becomes the conditional request validator only after the sitemap and pages
reconcile. Successful updates can publish while a page is pending. The next tick reads
the manifest again and skips any page whose stored ETag already matches.

### 2026-11-19 — Use one manifest-first schedule

Keep the six-hour default and its jitter. Check elapsed time in the sitemap-only fallback
to catch edits absent from the sitemap. The legacy full-refresh setting supplies that
fallback period. Deployments that enabled only the legacy schedule keep its cadence and
revalidate sitemap pages on every fallback tick, including early ticks caused by jitter.

## Reference Links

- [SigNoz/signoz.io#4530](https://github.com/SigNoz/signoz.io/pull/4530)

## Verification

- Regression tests passed for conditional scheduled requests, elapsed-time fallback,
  legacy-only cadence, sitemap and page cache skew, real 404 handling, and duplicate URLs.
  Two live scheduled ticks returned manifest 304s with the fallback period already elapsed.
- All local CI targets passed with Go 1.26.11. Dependency verification used a clean module cache,
  and protocol checks used alternate ports.
- The Go fetcher parsed 980 entries from the live website manifest, received a bodyless 304,
  and matched the live sitemap and a sampled Markdown page to the advertised ETags.
  The refresh suite replays the recorded Docker entry and sitemap ETag without network access.

## Outcome

The replacement carries Manika Malhotra’s original commit and all three review fixes.
Fleet-wide request and byte savings still need verification after rollout under
[SigNoz/nerve-pod#590](https://github.com/SigNoz/nerve-pod/issues/590).
