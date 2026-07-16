# Feature: Docs Corpus Ranking and Deduplication — Context & Discussion

## Original Prompt
> Optimize this:
> [https://github.com/SigNoz/signoz-mcp-server/issues/184](https://github.com/SigNoz/signoz-mcp-server/issues/184)

## Reference Links
- [Issue #184 — docs corpus: ranking misses on golden set and on-disk page duplicates](https://github.com/SigNoz/signoz-mcp-server/issues/184)
- [Bleve query documentation](https://blevesearch.com/docs/Query/)
- [Bleve query-string documentation](https://blevesearch.com/docs/Query-String-Query/)

## Key Decisions & Discussion Log
### 2026-07-11 — Baseline and scope
- The current embedded corpus scores recall@3 = 0.889 and precision@1 = 0.711 across 45 golden queries. The restored 0.9 recall floor therefore needs at least one additional hit without dropping precision below 0.7.
- The serialized snapshot contains duplicate URL records while `BuildIndex` merges them at runtime. Deduplicating only exact `(URL, SectionSlug)` pairs would leave cross-section duplicates and would not make `signoz_docs_index_doc_count` equal the unique URL count.
- Preserve every section association while storing one `PageRecord` per canonical URL. This requires moving the existing merge metadata (`SectionSlugs` and `SectionMap`) onto `PageRecord` and bumping the corpus schema version.
- Keep URL merging deterministic and fail-open at index construction so hand-built or stale snapshots cannot lose alternate section filters.
- Ranking changes will use explicit fielded `MatchQuery` clauses. Bleve documents that match queries analyze input with the field analyzer and that disjunction queries score documents matching at least one child clause.

## Open Questions
- [ ] Which breadcrumb, URL-token, title, heading, and body boost mix gives the best full golden-set result rather than only fixing the named examples?
- [ ] Should the embedded corpus be rebuilt from the live sitemap or migrated in place to isolate ranking/schema changes from unrelated docs drift?

### 2026-07-11 — Ranking sweep and corpus migration decisions
- Git and GitHub history confirmed that no later corpus refresh or hidden ranking experiment exists after #182. The May 20 artifact has 777 records, 746 unique canonical URLs, and 31 redundant records across 24 duplicate groups.
- Migrate the existing embedded corpus in place to schema v2. This isolates representation and ranking changes from live documentation drift while preserving the exact golden-set baseline.
- Index docs-relative URL path tokens and all section breadcrumbs with Bleve's English analyzer. Keep the existing standard-analyzed title, headings, and body fields.
- A full boost sweep selected 2.5 for both navigation fields: recall@3 improves from 0.889 to 0.978 and precision@1 from 0.711 to 0.844. Four named regressions return to the top three; `create alerts in signoz` remains sixth.
- Higher or split URL boosts do not fix the remaining alerts landing page and regress the ClickHouse FAQ query. Do not add a hard-coded landing-page prior for one golden example.
- This is an internal corpus/index change. It does not change MCP tools, resources, configuration, or payload contracts, so README, `manifest.json`, user docs, and the companion agent-skills repo need no updates.

## Resolved Open Questions
- [x] Ranking mix — retain title/headings/body/query-string boosts at 5/3/1/0.5 and add English-analyzed breadcrumb/URL-token boosts at 2.5/2.5.
- [x] Corpus source — migrate the May 20 embedded corpus in place instead of fetching a new live snapshot.

### 2026-07-11 — Refresh workflow guard
- Expand the dispatch-only corpus refresh verification to run both manifest integrity and the golden ranking gate. PRs opened with the workflow token do not need a later CI run to catch a ranking regression caused by corpus drift.

### 2026-07-11 — Refresh workflow trigger clarification
- GitHub's current `GITHUB_TOKEN` behavior can create approval-required CI runs for workflow-created pull requests. Keep the ranking check in the refresh job so the generated corpus is validated before PR creation and is not contingent on approving a downstream run. See [GitHub's `GITHUB_TOKEN` documentation](https://docs.github.com/en/actions/concepts/security/github_token).

### 2026-07-11 — Index-boundary invariant correction
- The initial note proposed fail-open duplicate merging inside `BuildIndex`. The implemented schema-v2 boundary instead fails closed on duplicate canonical URLs so a broken producer cannot silently inflate the snapshot/metric again. Both production producers normalize before indexing, and the corpus integrity test detects malformed persisted metadata.
- With one aggregate record per prior URL, runtime refresh no longer needs a second per-section fallback map. It projects the canonical prior payload onto each current sitemap entry before recompacting, which also avoids retaining sections removed from the sitemap.
