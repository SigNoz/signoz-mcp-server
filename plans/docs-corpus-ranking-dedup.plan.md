# Plan: Docs Corpus Ranking and Deduplication

## Status
Done

## Context
The docs search index currently compensates for duplicate serialized pages at runtime and no longer meets its intended recall@3 floor after corpus drift. The change should store one page per canonical URL, retain all section-filter metadata, report the true indexed document count, and improve ranking against the complete golden set.

## Approach
1. Move the existing per-URL merge representation into `PageRecord` by adding `SectionSlugs` and `SectionMap`, then bump `CorpusSchemaVersion`.
2. Centralize deterministic page normalization so the live build, runtime refresh, manifests, metrics, and index construction all operate on one record per canonical URL while retaining alternate section breadcrumbs. Keep first-seen navigation metadata stable while allowing a fresher duplicate to replace fetched payload fields.
3. Add English-analyzed breadcrumb and docs-relative URL-token fields at boost 2.5 each, retaining the existing title/headings/body/query-string weights after a full golden-set sweep.
4. Restore the recall@3 threshold to 0.9 and add focused regression tests for named ranking misses, schema normalization, refresh fallback, section filtering, and unique document metrics.
5. Migrate the existing May 20 embedded corpus and manifest in place so the schema/ranking comparison is not confounded by live documentation drift.

## Files to Modify
- `internal/docs/types.go` — add merged section metadata and bump the schema version.
- `internal/docs/index.go` — normalize unique pages and index/rank breadcrumb and URL-token text.
- `internal/docs/refresh.go` — emit normalized unique snapshots and preserve all prior section associations on fallback.
- `internal/docs/metrics.go` — keep document count and size aligned with the normalized snapshot.
- `internal/docs/index_test.go` — cover deterministic merge behavior and ranking fields.
- `internal/docs/verification_test.go` — cover refresh/metric/schema behavior for unique URLs.
- `internal/docs/golden_test.go` — restore recall@3 >= 0.9 and expose useful miss diagnostics.
- `cmd/build-docs-index/main.go` and tests — normalize pages before writing corpus artifacts.
- `.github/workflows/docs-index-refresh.yml` — gate generated corpus PRs on integrity and golden-set ranking.
- `internal/docs/assets/corpus.gob.gz` and `internal/docs/assets/corpus.manifest.json` — write the normalized schema-v2 corpus.

## Verification
- Run the targeted docs index, refresh, verification, builder, and golden-set tests.
- Confirm the embedded snapshot page count equals the number of canonical unique URLs and that alternate section filters round-trip.
- Confirm recall@3 is at least 0.9 and precision@1 remains at least 0.7 over all 45 golden queries.
- Run the repository's local CI workflow and inspect the final diff for unrelated corpus changes.
