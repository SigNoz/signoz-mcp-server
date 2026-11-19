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
revalidate pages on each sitemap-only fallback tick.

## Files to Modify

- `internal/docs/` handles scheduling, manifest reconciliation, and projected sitemap entries.
- `internal/config/` maps existing refresh settings to the single schedule.
- `cmd/build-docs-index/` reuses matching corpus records and fetches each URL once.
- The release workflows rebuild and validate the embedded corpus with a shared script.
- `README.md` and `docs/architecture.md` describe the refresh policy.

## Key Decisions

### 2026-11-19 — Acknowledge reconciled manifests

An ETag becomes the conditional request validator only after the sitemap and pages
reconcile. Successful updates can publish while a page is pending. The next tick reads
the manifest again and skips any page whose stored ETag already matches.

### 2026-11-19 — Use one manifest-first schedule

Keep the six-hour default and its jitter. When the manifest is missing or invalid,
conditionally check each unique sitemap URL on every tick, even when the sitemap is unchanged.
The deprecated full-refresh setting preserves the single timer cadence for legacy-only deployments.

### 2026-11-19 — Refresh the embedded corpus during release prep

Pre-release includes the rebuilt corpus when docs tests pass. Post-release does the same
when reconciling a skipped version bump for the next release. A failed refresh keeps
the committed corpus and logs a warning so website or ranking regressions do not block releases.
The search tests compare HTML and Markdown URLs for the same page using the existing thresholds.

### 2026-11-19 — Ship the refreshed corpus in this PR

The published sitemap grew from 746 to 980 unique pages. Store the full snapshot and raise
the compressed size cap from 2 MiB to 2.5 MiB to accommodate the 2.15 MiB artifact. Pages
are now indexed under their Markdown URLs, so direct fetch also tries the equivalent HTML
or Markdown URL when the requested form is absent. Update the golden expectations for
verified docs that moved or cover the requested topic directly.

## Reference Links

- [SigNoz/signoz.io#4530](https://github.com/SigNoz/signoz.io/pull/4530)

## Verification

- Regression tests passed for conditional scheduled requests, every-tick fallback on missing
  and invalid manifests, legacy-only cadence, sitemap and page cache skew, real 404 handling,
  and duplicate URLs. Live scheduled ticks returned manifest 304s without page requests.
- All local CI targets passed with Go 1.26.11. Dependency verification used a clean module cache,
  and protocol checks used alternate ports.
- The Go fetcher parsed 980 entries from the live website manifest, received a bodyless 304,
  and matched the live sitemap and a sampled Markdown page to the advertised ETags.
  The refresh suite replays the recorded Docker entry and sitemap ETag without network access.

- Replaying the initial candidate through the release script confirmed that it restores
  both committed files when validation fails.
- The embedded sitemap ETag and all 980 page ETags matched the live manifest. Two refreshes
  from the fresh snapshot received manifest responses of 200 and 304 and made zero sitemap
  or page requests. All golden search gates passed. The local index build measured
  170 MiB peak and 55 MiB resident under the existing 224 MiB and 64 MiB limits.
- With scheduled refresh disabled, a fresh stdio server ranked the FastAPI guide first,
  returned identical Markdown for its HTML and Markdown URLs, and exposed the page in the sitemap.

## Outcome

The replacement carries Manika Malhotra’s original commit and all three review fixes.
The PR ships the fresh embedded corpus.
Release prep attempts future corpus updates, with the committed snapshot retained when validation fails.
Fleet-wide request and byte savings still need verification after rollout under
[SigNoz/nerve-pod#590](https://github.com/SigNoz/nerve-pod/issues/590).
