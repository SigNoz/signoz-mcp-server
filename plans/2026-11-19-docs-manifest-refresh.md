# Plan: Refresh the Docs Index from the Website Manifest

Status: In Progress
Issue: https://github.com/SigNoz/nerve-pod/issues/590
PR: https://github.com/SigNoz/signoz-mcp-server/pull/454 (original)

## Context

The daily forced refresh requests every Markdown page per instance. The website now
publishes ETags for its sitemap and rendered Markdown. Manika Malhotra implemented the
MCP consumer in [SigNoz/signoz-mcp-server#454](https://github.com/SigNoz/signoz-mcp-server/pull/454).
The review reproduced cache skew, incomplete manifests, and duplicate page requests.

## Approach

Use the manifest to skip unchanged pages. Check the sitemap ETag before applying membership
changes, keep the last-good page when an advertised ETag fails to match, and retry unresolved
pages on the next tick. Confirm sitemap-listed omissions by requesting the Markdown. Fetch
each URL once and preserve all of its section memberships. Keep the sitemap-only fallback.

## Files to Modify

- `internal/docs/` handles manifest reconciliation and projected sitemap entries.
- `cmd/build-docs-index/` reuses matching corpus records and fetches each URL once.
- `README.md` and `docs/architecture.md` describe the refresh policy.

## Key Decisions

### 2026-11-19 — Acknowledge reconciled manifests

An ETag becomes the conditional request validator only after the sitemap and pages
reconcile. Successful updates can publish while a page is pending. The next tick reads
the manifest again and skips any page whose stored ETag already matches.

## Reference Links

- [SigNoz/signoz.io#4530](https://github.com/SigNoz/signoz.io/pull/4530)

## Verification

- Exercise request counts, cache skew recovery, and real 404 handling through the refresher.
- Run the repository's local CI before opening the replacement PR.

## Outcome

The replacement carries the original author commit and the review fixes.
