# Plan: Docs Search Ranking and Query Guidance

Status: Done
Issue:
PR:

## Context

`signoz_search_docs` is a Bleve full-text index over the embedded SigNoz docs corpus
(746 whole pages, 5.4 MB of Markdown, median page 5 KB, 33 pages over 20 KB), refreshed
every 6 hours. Per-field `MatchQuery` clauses (title 5, headings 3, body 1, breadcrumb 2.5,
URL tokens 2.5, query-string 0.5) are combined in one disjunction. Title, headings, and body
use Bleve's `standard` analyzer (no stemming); breadcrumb and URL tokens use `en` (porter
stemming). Scoring is Bleve's default TF-IDF.

The architecture matches the "full-text plus query rewriting" recipe from
[RAG Is Simpler Than You Think](https://www.lighthousenewsletter.com/p/rag-is-simpler-than-you-think):
small stable corpus, keyword-heavy product vocabulary, freshness matters, no chunking, no
embeddings. That article's main lever is query rewriting. In MCP the calling LLM is the
rewriter, and today nothing tells it how to query well.

Evidence gathered 2026-09-16 against the embedded corpus:

- Golden set (45 keyword-style queries): recall@3 0.978, precision@1 0.844. The set contains
  no sentence-style or abbreviation queries, so it does not measure how LLM clients actually
  query.
- Sentence queries rank poorly because every clause is OR-only, so a page sharing one word
  competes with a page matching all words. "How do I send logs from a Kubernetes cluster to
  SigNoz Cloud?" ranks a Kubernetes dashboard template first (total 745 of 746 pages match).
- No stemming on content fields: "alerting on error rate", "instrumenting a python fastapi
  app", "nodejs express instrument" miss pages titled "Alerts", "Instrumentation".
- No synonym handling: "k8s otel collector helm" ranks an AWS Lambda page first; spelling the
  terms out returns the collector pages.
- The `headings` field is indexed as raw JSON, so `id`, `text`, and `level` are tokens in
  every document.
- The tool's `searchText` description says "Natural-language or keyword query", and the
  companion agent-skills `signoz-searching-docs` skill says to pass the user's natural-language
  query and "trust the ranking".
- Metering records result-count buckets and duration only. There is no signal for weak
  results or for whether a returned page was subsequently fetched.

Experiment results (temporary env-switched variants, reverted):

| Variant | recall@3 | precision@1 | Note |
|---|---|---|---|
| Current | 0.978 | 0.844 | one miss: "create alerts in signoz" |
| BM25 scoring | 0.978 | 0.844 | no rank changes |
| all-terms conjunction clause (title 6, body 3) | 0.978 | 0.867 | +1, no regressions |
| `en` analyzer on title/headings/body | 0.956 | 0.867 | fixes sentence phrasings, loses "clickhouse query performance dashboard panels" |

Out of scope, with reasons recorded so they are not re-litigated: embeddings, vector store,
hybrid reranking (corpus too small and stable to justify chunking and model-deprecation
cost); BM25 (no measured rank change); section-level chunking with heading anchors (deferred;
only 33 pages exceed 20 KB and `signoz_fetch_doc` already takes a `heading`).

## Approach

Work in the order below. Establish the expanded evaluation before any tuning so every ranking
change is measured against the same baseline. Each ranking change follows the aggregate and
per-style gates below; memory guardrails still pass without relaxation.

1. **Golden set that reflects real clients, first.** Add sentence-style and abbreviation
   queries with a `style` tag per entry (`keyword`, `sentence`, `abbreviation`,
   `production-raw`, `production-keyword`, `production-real`). Because the
   new client guidance tells the agent to split multi-intent questions, do not add
   multi-intent single queries; add the split sub-queries instead, tagged by their style.
   Keep `expected_top_pages` as an "any of these is acceptable" list; it does not test
   coverage of several intents in one query. Mark roughly a fifth of the new entries
   `holdout: true`; holdout entries are reported and gated but must not be consulted while
   sweeping boosts, so the sweep is checked for generalization. Raise the test's upper bound
   on query count. Record the pre-change baseline per style and overall in this plan.
   Over the full golden set, neither overall recall@3 nor precision@1 may decrease
   versus the pre-change baseline, and at least one must increase. Keep keyword floors
   recall@3 >= 0.9 and precision@1 >= 0.7; no style may fall below its own pre-change
   recall@3 or precision@1 by more than 0.05. Holdouts participate in all final gates.
   Log every per-query change of rank between baseline and result. Include 18 production
   intents as raw/keyword pairs, plus38 sampled actual client queries (133 total queries);
   validate expected URLs against the corpus before running searches.
2. **Index headings as text, not JSON.** Feed the headings field the space-joined heading
   texts; keep `available_headings` as the stored JSON for `signoz_fetch_doc`.
3. **All-terms conjunction clauses.** Add `MatchQuery` clauses with the AND operator on title
   and body to the existing disjunction so documents matching every analyzed query term in
   a field are favored over partial matches. This is a ranking preference, not a guarantee:
   the outer disjunction still sums clause scores, and the AND applies per field after
   analysis. Sweep the two boosts around the measured 6/3 on the non-holdout set and keep
   the best result that passes the gates. For4–64 distinct standard-analyzed query terms,
   also reward ceil(0.6*n) matches per title/body field at boost2 each, measured separately.
4. **Tool description as the query rewriter.** Rewrite the `signoz_search_docs` tool and
   `searchText` descriptions to instruct the client: send 2 to 6 keywords, keep product and
   technology terms exactly as the user wrote them, split multi-intent questions into separate
   calls, pass `section_slug` when the area is known, and retry with different terms when the
   top result is off-topic. Stay within the 1024-byte tool and parameter description budgets
   and follow `docs/client-visible-writing-style.md`. Mirror the exact text into the
   wire-catalog `tools-list.json` fixture, `manifest.json`, and the README parameter table.
5. **Query-side glossary expansion, measured.** Keep a small reviewed alias table in the docs
   package. Expansion never rewrites the raw query: the existing clauses, including the
   validated query-string clause, receive the original text unchanged. Expansion adds
   separate, lower-boost `MatchQuery` clauses (or `MatchPhraseQuery` for multiword aliases
   such as "google cloud") for each alias hit. Rules: whole-token, case-insensitive matching
   on the analyzed terms; non-recursive (aliases of aliases are not followed); deduplicate
   against terms already present; cap the number of added clauses (for example eight) so
   attacker-controlled input cannot inflate the query. Exclude ambiguous aliases: `node` is
   not an alias for Node.js (Kubernetes nodes), and `gcp` maps to the phrase "google cloud",
   never to the bare term "cloud". Unit tests cover each table entry, ambiguous queries
   ("kubernetes node memory"), quoted and fielded query-string input, the cap, and no-op
   input. Land only if the abbreviation style improves and the aggregate/per-style gates pass. Bleve
   2.5 native synonym sources were considered and rejected because they change the index
   build and memory profile.
6. **Stemming on content fields with a boost re-sweep, measured.** Switch title and headings
   to the `en` analyzer. For body, measure two options: (a) `en` only, (b) keep `standard`
   and add a stemmed duplicate field at a lower boost. Option (b) preserves exact matches for
   identifiers and env vars but grows the index; it is acceptable only if
   `TestGuardrail_DocsIndexBuildPeakHeap` and the resident budget in `guardrails/policy.go`
   still pass without relaxation. If (b) wins, the search request must also highlight the
   stemmed field and `chooseSnippet` must accept fragments from it, with tests for
   inflection-only matches, glossary-only matches, and the snippet rune limit, so a page
   matched only through the stemmed field still shows a relevant snippet. Re-sweep boosts
   after the analyzer change rather than reusing the current mix. Record the chosen option
   and numbers. Land only if the sentence style improves and the aggregate/per-style gates pass.
   Final measured option (c) keeps en on title/headings and standard body only; it supersedes
   (b) after repeated whole-package heap-budget failures. Keep 64-page build batches for headroom.
7. **Score telemetry, not relevance claims.** Add an `outcome` attribute (`ok`, `error`) to
   the docs search counter so failed searches stop being counted as zero-result searches, and
   record the top hit's raw Bleve score in a new histogram documented as uncalibrated score
   telemetry, only for successful searches with at least one result (a zero-result search has
   no top score). Do not add bucketed "relevance" thresholds; the same change alters boosts,
   analyzers, and clause counts, so any fixed thresholds would be wrong on arrival. Follow-up
   fetch attribution (whether a returned page was later fetched) is deferred to a separate
   issue: the HTTP transport is stateless and shared across tenants, so a process-local URL
   set keyed only by URL would attribute unrelated fetches to searches and miss cross-replica
   traffic. Any future version needs a defined client or request correlation mechanism.

Companion change (CMP-3): the agent-skills `signoz-searching-docs` skill currently tells the
agent to pass the natural-language query verbatim and trust the ranking. It must be updated to
the same guidance as the new tool description in a linked PR in SigNoz/agent-skills. This is a
documented-behavior change, so the companion PR is required, not optional.

## Files to Modify (approved candidate scope; retained changes listed in Outcome)

- `internal/docs/index.go` — conjunction clauses, glossary expansion hook, analyzer and field
  mapping changes, headings text indexing, boost constants.
- `internal/docs/glossary.go` (new) — alias table and expansion clause builder.
- `internal/docs/glossary_test.go` (new) — per-entry, ambiguity, query-syntax, cap, and no-op tests.
- `internal/docs/index_test.go` — ranking regressions for the probe queries in Context.
- `internal/docs/golden_test.go` — style and holdout tags, per-style metrics and gates, rank
  change log, updated count bounds.
- `internal/docs/testdata/golden_queries.json` — new sentence and abbreviation queries (including
  split sub-queries of multi-intent questions) with expected pages.
- `internal/docs/guardrail_test.go` — re-verify heap budgets after mapping changes (no
  relaxation).
- `internal/handler/tools/docs.go` — tool and parameter description text, `outcome`
  attribute, top-score recording.
- `internal/handler/tools/docs_test.go` (new or existing) — attribute and outcome tests.
- `pkg/otel/metrics.go` and `metrics_test.go` — top-score histogram instrument.
- `internal/mcp-server/testdata/wire-catalog/tools-list.json` — mirror the description text.
- `manifest.json` — description parity for `signoz_search_docs`.
- `README.md` — `signoz_search_docs` parameter table and any metric attribute docs.
- `.github/workflows/docs-index-refresh.yml` — keep the golden gate command valid if the test
  name or flags change.

## Key Decisions

### 2026-09-16 — Stay on full-text; improve ranking and client guidance instead of adding embeddings

- Decision: no embeddings, vector store, or hybrid reranking in this change.
- Rationale: 746 stable pages refreshed every 6 hours, product-term-heavy queries, and a
  measured recall@3 of 0.978 on keyword queries. The failures found are ranking and
  query-formulation failures that cheaper changes fix. Embeddings would reintroduce chunking
  and model-deprecation cost for no demonstrated gain.
- Alternatives rejected: BM25 scoring (no rank change on the golden set); section-level
  chunking (deferred, see Context).

### 2026-09-16 — Conjunction clause first, stemming only with a re-sweep

- Decision: land the all-terms clause as the first ranking change; land stemming only after a
  boost re-sweep shows no keyword-subset regression.
- Rationale: the conjunction clause measured strictly positive. A blind analyzer flip traded
  one golden query for several sentence-query wins, which is a boost-balance problem, not a
  reason to skip stemming.

### 2026-09-16 — Glossary is query-side Go code that adds clauses, never rewrites the query

- Decision: expand abbreviations in Go as separate lower-boost clauses; leave the original
  clauses and the query-string clause untouched; exclude ambiguous aliases such as `node`.
- Rationale: deterministic, unit-testable, no index rebuild or memory change, and it mirrors
  the article's "glossary in the system prompt" without depending on the client. Adding
  clauses instead of rewriting keeps quoted and fielded query syntax intact and bounds the
  blast radius of a bad alias.

### 2026-09-16 — The client LLM is the query rewriter

- Decision: put query-formulation guidance in the tool and parameter descriptions and in the
  companion skill instead of adding a server-side LLM rewrite step.
- Rationale: the server has no model access and adding one would add latency, cost, and a new
  failure mode. Every MCP client already has a capable model in the loop.

### 2026-09-16 — Evaluation before tuning; gate per style with a holdout

- Decision: expand the golden set and record a baseline before any ranking change; gate each
  query style separately and hold out part of the new queries from boost sweeps.
- Rationale: Codex review (round 1) noted that aggregate gates let one style regress while
  another improves, and that sweeping boosts on the full set overfits. Multi-intent queries
  are represented as their split sub-queries because the client guidance asks for the split.

### 2026-09-16 — Defer follow-up fetch attribution; record uncalibrated score telemetry

- Decision: no recent-search URL set and no bucketed relevance thresholds in this change.
- Rationale: Codex review (round 1) showed the stateless, shared transport makes URL-only
  correlation attribute unrelated fetches to searches, and that fixed score buckets cannot be
  calibrated in the same change that alters the scorer inputs. Raw top-score telemetry and
  an `outcome` attribute still give a baseline for a later, calibrated signal.

### 2026-09-16 — Codex plan review outcome

- Reviewed with Codex (gpt-6-astra, high effort) in two rounds. Round 1 returned REVISE with the
  five points recorded in the two decisions above plus wording and ordering changes. Round 2
  returned APPROVED with two implementation notes: record top-score telemetry only for
  successful non-empty results, and keep holdout queries out of parameter selection.

### 2026-09-16 — Measured heading and conjunction decisions

- Text-only headings were reverted: keyword recall fell from 44/45 to 43/45.
- Conjunction title/body boosts: 6/5. Swept title {3,6,9,12}, body {1,3,5,7}
  using non-holdout queries only. Body 5 maximized precision without recall loss;
  title settings tied, so retained the originally measured title boost 6. Body 7
  lost keyword recall and was rejected. No landing-page prior was added.

### 2026-09-16 — Glossary accepted

- Additive title/body clauses at boost 0.5, max eight clauses. Reviewed aliases:
  k8s → kubernetes, otel → opentelemetry, gcp → phrase google cloud,
  aws → phrase amazon web services, js → javascript. No node alias or recursive expansion.
- Abbreviation precision increased from 5/7 to 6/7 with unchanged recall and other styles.

### 2026-09-16 — Stemming rejected after both re-sweeps

- Retain standard title/headings/body and existing en navigation fields. Both (a) en-only
  body and (b) standard body plus a lower-boost stemmed duplicate lose keyword recall
  (43/45 versus 44/45). Swept conjunction title {3,6,9,12} and body {1,3,5,7};
  option (b) additionally swept stemmed-body {0.1,0.25,0.5,0.75}. All excluded holdouts.
- Neither stemming option met the no-style-regression acceptance rule, despite improved
  sentence results and passing memory checks. Neither mapping nor duplicate-field code remains.
- Glossary snippet tests exposed an existing 203-rune result for a 200-rune limit.
  Trim the ellipsis inside the budget, including fallback snippets; no budget relaxation.

### 2026-09-16 — User-selected aggregate improvement policy supersedes strict per-style non-regression

- The user chose overall evaluation improvement instead of strict per-style non-regression.
  Neither whole-set recall@3 nor precision@1 may decrease versus the original baseline;
  at least one must increase. A recall tie with higher precision qualifies.
- Keep keyword floors 0.9 / 0.7, and permit no style to lose more than 0.05 in either
  metric versus its own original baseline. Golden tests derive baselines from pinned ranks.
  This intentionally replaces the earlier new-style post-change floors and acceptance rule,
  as requested; no guardrail or memory budget changes.
- Re-land option (b) exactly at recorded boosts: en title/headings; standard body plus
  en body_stemmed at OR 0.5; conjunction title/body 6/5, all other boosts unchanged.
  Body and body_stemmed are highlighted. chooseSnippet prefers marked fragments before
  Bleve's unmarked fallbacks, with exact-body marked fragments preferred when both match.
- Re-test heading text on top of (b). Counts tie (b) alone, so leave heading text out
  because the user required overall improvement versus (b) to retain it.

### 2026-09-16 — Reduce full-build batch size to retain dual-field stemming within heap budget

- Coordinator approved reducing indexBatchSize from 100 to 64 after cold dual-field builds
  exceeded the unchanged 224 MiB peak budget (observed 230 and 244 MiB). Both body fields
  already share indexDocument and the same BuildIndex batch; no separate unbatched build exists.
- The duplicate stemmed body roughly doubles resident index memory: 28 MiB before to
  about 50 MiB (measured 45–57 MiB) afterward, under the unchanged 64 MiB resident budget.
- Five isolated Go1.26 CI-shaped builds at 64 pages peak at 193 MiB max (31 MiB headroom),
  with resident max 57 MiB below 64 MiB. Retain option (b) and batch 64; no need to try 32.
- Guardrail wall times including corpus load/heap settling/build: batch100 0.58/0.57/0.55/0.54/0.55s
  (mean0.558s); batch64 0.57/0.55/0.55/0.56/0.58s (mean0.562s). Negligible observed cost;
  these are whole-test times, not isolated BuildIndex CPU time.
- Delta refresh does not use indexBatchSize: refresh.go calls ApplyDelta, which retains one
  atomic batch under the existing 25% changed-page threshold. Both fields still use the same
  indexDocument mapping. Splitting delta commits would change publication/failure semantics;
  not changed here. This is a known follow-up for delta-memory measurement and bounding.
  Existing delta/swap/refresh tests pass; no delta memory budget is claimed.

### 2026-09-16 — Add paired production evaluation without query-specific tuning

- Add the 18 supplied credential-free production intents twice: production-raw and
  production-keyword. Verify every supplied expected page exists in the immutable manifest.
  Keep typo, multilingual, conversational and syntax-bearing raw phrasings verbatim.
- Temporarily stash tracked implementation and the two new glossary files using explicit
  paths; leave the plan, report and .codex untouched. Evaluate the enlarged corpus against
  HEAD's pre-change index with an evaluation-only golden test, then restore the stash.
  Pin all 95 measured baseline ranks. Stash restored successfully; branch unchanged.
- Same gate for all five styles: no metric more than0.05 below its original baseline;
  keyword floors0.9/0.7; whole-set metrics cannot decrease and at least one improves.
  Both new production styles are evaluation-only, not used for boost selection.
  Fluent Bit raw and keyword remain open misses present before this change, not regressions.

### 2026-09-16 — Record executed search text and fail open on query-string syntax

- Production traces carry mcp.search_context (the whole user request), not the keyword
  searchText actually sent. Add span-only mcp.docs.search_text (256-rune prefix),
  mcp.docs.section_slug, mcp.docs.result_count and successful non-empty mcp.docs.top_score.
  Query text is not a metric dimension; absent top hits do not emit a synthetic score.
- Raw requests contain quotes, colons, equals signs and other query syntax. Rejecting an
  invalid optional Bleve query-string clause hid otherwise usable matches from users.
  Drop only that clause on validation failure; keep MatchQuery/conjunction/glossary clauses,
  and emit mcp.docs.query_string_dropped=true on the current span. Do not change valid syntax.
- Keep ErrInvalidSearchQuery for empty/whitespace-only search text and handler compatibility.
  Tests now assert malformed non-empty input searches successfully. No wire fixture pinned
  the old invalid-query error; existing description parity remains unchanged.

### 2026-09-16 — Stemming narrowed to title and headings after heap flakiness

- Coordinator measurements found option (b) flaky at batch64: whole-package peaks136,
  154,162,174,189,203,204 MiB and one full-suite run above the unchanged224 MiB budget.
  Batch32 was rejected because resident heap ranged63–75 MiB against the64 MiB budget.
- Coordinator quality comparison over95 queries: (b) recall0.863 /precision0.811;
  (c), en title/headings and standard body only,0.863 /0.789;
  no stemming0.842 /0.747. Option(c) keeps all recall gain and four of six precision
  hits gained by(b) over no stemming, at roughly the original memory footprint.
- Adopt(c): remove duplicate body mapping, document field, query clause, highlight field
  and fragment merging. Keep title/headings en, body standard, AND boosts6/5, glossary0.5.
  Batch64 is retained for headroom, not required by duplicate-body indexing anymore.
- Keep marked-fragment preference for plain-body fragments and glossary highlighting;
  replace duplicate-body-only tests with title/headings stemming and body non-stemming tests.
  No requirement remains to provide an inflection-matched body snippet when only title or
  headings matched; existing bounded body fallback remains the behavior.
- Five fresh whole-package Go1.26 runs: peak97–124 MiB, resident28–34 MiB;
  unchanged peak/resident budgets224/64 MiB. Heap-test wall time0.34–0.37s.
  This supersedes earlier decisions retaining(b) and its optimistic memory measurements.

### 2026-09-16 — Label actual production searchText before evaluating it (Task D)

- Source:208 distinct strings, first occurrence/file order, from supplied production logs.
  Select1-based positions5,10,...205. Privacy-review before labeling: exclude customer or
  person identifiers, hosts/URLs/emails and credential values; keep public technology/product
  terms such as AWS,Azure,SigNoz,Slack. None of the selected41 required privacy replacement.
  Generic SIGNOZ-API-KEY identifiers describe a header, not credential values.
- Label using manifest titles/URLs only, before any index search; up to3 acceptable pages.
  Drop3 intents with no plausible manifest page, producing38 production-real entries.
  Mark every5th retained entry holdout (7 new;10 total). Do not tune on holdouts.
- Content gaps (not ranking misses or guessed labels):
  - `CLI command line access SigNoz query API`: no CLI documentation in manifest.
  - `MTTR MTTA incident acknowledge alert lifecycle SLO reliability metrics`: no incident
    acknowledgement/MTTR/MTTA/SLO lifecycle page in manifest.
  - `upload source maps browser javascript symbolication minified stack trace`: no
    source-map upload/symbolication page in manifest; frontend overview is not evidence.
- Repeat explicit-path stash baseline capture on pre-change HEAD, keeping plan/.codex
  untouched; restore stash successfully. Record all133 original ranks and use the same
  per-style0.05 tolerance and aggregate gates. No hard-coded page priors or corpus edits.

### 2026-09-16 — Retain minimum-should-match preference at title/body boost2/2 (Task E)

- Add independent per-field disjunctions for4–64 distinct standard-analyzed terms;
  each requires ceil(0.6*n) matching per-term MatchQueries. Dedup repeated terms and drop
  standard stopwords; field analyzers still govern each MatchQuery. Queries outside the
  range retain original clauses without expansion. At most128 new per-term clauses.
- Sweep title/body boosts independently over{2,3,4}, excluding all10 holdouts. All9
  combinations tie on non-holdout quality; select the smallest boosts2/2.
- Compared with option(c) immediately before MSM, full133 improves recall108→109 and
  precision90→91. production-real recall stays26/38, precision15→16;6 top-three expected
  ranks change. Production-raw precision loses1 hit (9→8) but remains above original6/18.
  All six style and overall gates pass. No index mapping or memory-budget change.

### 2026-09-16 — Description tightened after PR review pass

The tool, `searchText`, and `section_slug` descriptions were reviewed against `docs/mcp-best-practices.md` DSC-1 to DSC-3 after PR #309 opened.

- The tool description and `searchText` repeated the same four rules almost verbatim. The rules that change a first call (2 to 6 keywords, names as the user wrote them, split multi-intent questions, retry) stay on the tool description as the most universally delivered surface; `searchText` now carries only the field-local format, the example, and an explicit `Required.` marker, matching `signoz_get_view.id`, because the schema cannot mark it required while the legacy `query` alias stays valid.
- "Retry with different terms" became "retry with fewer or different terms": the production evaluation showed shorter keyword rewrites outranking long strings, and fewer terms is the retry that most often helps.
- `section_slug` now states that an unknown slug returns zero results and that a slug should be reused from an earlier result. A probe confirmed `section_slug: "kubernetes"` silently returns zero hits, and the sitemap resource lists page URLs, not slugs, so it cannot serve as the discovery pointer.
- `manifest.json` uses a one-sentence summary like every other tool entry; the parity test compares names only, and the earlier full-length copy was inconsistent with the rest of the file.
- Two `errcheck` lint failures on unchecked `idx.Close()` in tests were fixed with the package's existing `defer func() { _ = idx.Close() }()` form.

### 2026-09-16 — Glossary widened into bidirectional synonym groups

Review of the shipped five-entry glossary against 385 production search strings showed the table was too narrow and one-directional. Most out-of-vocabulary query terms are non-English words, IDs, ClickHouse function names, and typos that no alias fixes, but a handful of real gaps had docs-side spellings: `prebuilt` (4 queries, 0 docs pages) against `pre-built`; `mute` (3 queries) against `silence` and the `Planned Maintenance` page; `postgres` against `postgresql`; `msteams`, `dotnet`, `nodejs`, and `mongo` against `microsoft teams`, `.net`, `node.js`, and `mongodb`.

- Entries became synonym groups compiled once at init; when a triggering member is present in the analyzed query, the absent members are added as clauses. Multiword members match only as a contiguous token sequence, so `planned alert maintenance` does not trigger the maintenance group.
- `k8s` and `infra` are query-only: they trigger expansion but are never added, because docs use both mostly for the `k8s-infra` chart. Adding them from the long forms regressed `install signoz kubernetes helm` (1 to 2) and `monitor aws infrastructure` (1 to 2).
- `.net` is expansion-only: it is added for `dotnet` but never triggers, because the standard analyzer reduces it to the token `net`, which also appears in `net/http`.
- Boost is per group and defaults to 0.5. Only the `mute` / `silence` / `planned maintenance` group carries 2.0, because 0.5 could not lift `Planned Maintenance/Downtime` above pages whose titles contain `alert`; 1.5, 2.0, and 3.0 gave identical ranks. An earlier draft applied 2.0 to every multiword expansion; independent review showed that made `dashboard templates` a wrong first result for `default prebuilt out of the box alert rules created on install templates`, so `dashboard templates` was dropped from the `prebuilt` group and the global phrase boost was replaced by the per-group value.
- `pat` was dropped: no docs page describes personal access tokens, so it is a content gap. `go`, `node`, and `es` stay out as ambiguous.
- `maxGlossaryClauses` rose from 8 to 12.
- Ten `abbreviation`-style golden queries were added for the new groups, none as holdouts. Their `baseline_rank` is the rank under the #309 ranking before this change, while the 133 earlier entries keep the pre-#309 baseline. The aggregate gate therefore measures the branch, not this diff, and the old glossary already cleared it; the old-versus-new rank diff in Verification is the evidence for this change.

### 2026-09-16 — searchText bounded at 2048 characters after security review

Codex security review noted that a caller could send a multi-megabyte `searchText` and every analyzer pass (base clauses, glossary, minimum-should-match) would run over the whole value before the 64-term check returned. `boostedDocsQuery` now rejects values over 2048 characters with the existing validation-coded error, and `minimumMatchClauses` stops collecting once it has seen 64 distinct terms instead of tokenizing into full-size slices first. The longest production search string observed is 395 characters and the longest golden query 139, so the cap does not touch real traffic. The parameter description does not mention the limit; the error message carries it.

## Reference Links

- [RAG Is Simpler Than You Think](https://www.lighthousenewsletter.com/p/rag-is-simpler-than-you-think)
- [Bleve match query operator](https://blevesearch.com/docs/Query/)
- [Bleve analyzers](https://blevesearch.com/docs/Analyzers/)
- Prior work: `plans/docs-corpus-ranking-dedup.plan.md`

## Verification

### 2026-09-16 — Step 1 baseline (before ranking changes)

- Added 14 queries (7 sentence, 7 abbreviation), with 3 holdouts (21% of additions).
  Expected URLs verified against the immutable corpus manifest. 59 total queries.
- `go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`: PASS.
- Baseline keyword recall@3 / precision@1: 0.978 / 0.844 (45 queries).
- Baseline sentence: 0.714 / 0.429 (7 queries); abbreviation: 0.857 / 0.571 (7).
- Baseline ranks are pinned per entry; the test logs every changed top-three rank.
  New-style floors are finalized after the measured implementation; keyword floors remain 0.9 / 0.7.

- `go test ./internal/docs -run 'TestGoldenSet|TestCorpusManifestMatches|TestGuardrail_DocsIndexBuildPeakHeap' -count=1 -v`
  and record per-style recall@3 and precision@1 before and after each ranking step.
- `go test ./...`, `make fmt goimports`, `go build ./cmd/server`.
- Focused guardrail suite and workflow lint per `guardrails/README.md`; wire-catalog golden
  test passes with the mirrored description.
- Probe queries from Context added as regression tests and passing.
- Local CI via the `local-ci` workflow before handoff.
- No live SigNoz instance is required; the docs index is embedded.

### Step 2: headings text experiment (not retained)

- `go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`: PASS existing floors,
  but keyword recall regressed: keyword 0.956 / 0.844, sentence 0.714 / 0.571,
  abbreviation 0.857 / 0.571. Reverted the headings-only change before step 3,
  per the stricter no-style-regression acceptance rule. Stored headings remain unchanged.

### Step 3: conjunction accepted

- `go test ./internal/docs -run '^TestRankingSweep$' -count=1 -v`: PASS, temporary
  sweep excludes holdouts before searching. At 6/5, non-holdout counts (n/recall/precision):
  keyword 45/44/40, sentence 5/4/2, abbreviation 6/5/4.
- `go test ./internal/docs -run 'TestGoldenSet|TestGuardrail_DocsIndexBuildPeakHeap' -count=1 -v`:
  PASS. Full-set keyword 0.978 / 0.889, sentence 0.714 / 0.429, abbreviation 0.857 / 0.714.
  Peak 118 MiB, resident 28 MiB (unchanged 224/64 MiB budgets).

### Step 4: query guidance

- Handler, wire fixture (text edits only), manifest and README synchronized.
- `go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`: PASS.
- `go test -count=1 -run '^TestGuardrail_' ./...`: PASS.
- Ranking metrics unchanged. Tool / parameter descriptions are 525 / 277 bytes.

### Step 5: glossary accepted

- `go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`: PASS.
  Keyword 0.978 / 0.889, sentence 0.714 / 0.429, abbreviation 0.857 / 0.857.
- `go test ./internal/docs -run 'TestGlossary|TestGuardrail_DocsIndexBuildPeakHeap' -count=1 -v`:
  entry, case, whole-token, no-op, ambiguity, cap, query syntax and snippet checks.

### Step 6: stemming experiments (not retained)

- `go test ./internal/docs -run '^TestRankingSweep$' -count=1 -v`: PASS for each
  temporary sweep; all evaluated boost mixes lost keyword recall (43/45).
- `go test ./internal/docs -run 'TestGoldenSet|TestGuardrail_DocsIndexBuildPeakHeap' -count=1 -v`:
  PASS existing gates for both candidates, but rejected by stricter no-regression rule.
  Option (a), conjunction 6/5: keyword 0.956 / 0.889, sentence 0.857 / 0.571,
  abbreviation 0.857 / 0.857; peak 142 MiB, resident 26 MiB.
  Option (b), conjunction 6/5, stemmed-body 0.5: keyword 0.956 / 0.911,
  sentence 0.857 / 0.571, abbreviation 0.857 / 0.857; peak 162 MiB, resident 41 MiB.
- Reverted stemming. `go test ./internal/docs -run 'TestGlossary|TestGoldenSet' -count=1 -v`:
  PASS. Restored keyword 0.978 / 0.889, sentence 0.714 / 0.429, abbreviation 0.857 / 0.857.
- Final new-style floors: sentence 0.67 / 0.38, abbreviation 0.81 / 0.81;
  each is less than 0.05 below shipping quality. Keyword remains 0.9 / 0.7.
- Initial glossary tests failed with exact assertions `"203" is not less than or equal to "200"`
  and `should have 10 item(s), but has 8`. Fixed snippet trimming; corrected the test's
  mistaken assumption that the standard analyzer splits `title:otel` into two terms.
  Query-string field syntax is preserved, not rewritten or parsed as an alias token.

### Exact rejected-candidate reproduction and lost ranks

- Headings step: replace only indexed `headings` with space-joined Heading.Text values;
  leave stored `available_headings` JSON. Original OR boosts and no glossary/conjunction.
  Keyword 43/45 (0.955556) recall, 38/45 (0.844444) precision; sentence 5/7
  (0.714286), 4/7 (0.571429); abbreviation 6/7 (0.857143), 4/7 (0.571429).
  `setup alerts notification channel` fell from rank 2 to **5**.
- Best option (a): `en` title/headings/body, original OR boosts 5/3/1,
  breadcrumb/URL 2.5 each, query-string 0.5, conjunction title/body 6/5,
  glossary title/body 0.5. Keyword 43/45 (0.955556), 40/45 (0.888889);
  sentence 6/7 (0.857143), 4/7 (0.571429); abbreviation 6/7 (0.857143),
  6/7 (0.857143). `clickhouse query performance dashboard panels` fell from rank 3 to **6**.
- Best option (b): same as (a), except standard `body` plus `body_stemmed` (`en`)
  containing identical Markdown; add OR MatchQuery on body_stemmed at 0.5.
  Conjunction body still targets standard body. Keyword 43/45 (0.955556),
  41/45 (0.911111); sentence 6/7 (0.857143), 4/7 (0.571429); abbreviation
  6/7 (0.857143), 6/7 (0.857143). Same ClickHouse query fell to **4**.
  Stemmed boosts 0.1/0.25/0.5/0.75 tied on counts at 6/5; 0.5 is the measured candidate.
  Re-applying (b) also requires highlighting body_stemmed and accepting those fragments
  after body fragments in chooseSnippet, plus inflection-only snippet coverage.
- Exact ranks measured using temporary `go test ./internal/docs -run '^TestRankingLostRanks$' -count=1 -v`
  with a 1000-hit direct Bleve request. All three runs PASS; temporary probes removed.
- `nodejs express instrument` remains outside top three with retained analyzers, covered
  as a golden miss rather than claimed fixed. `kubernetes node memory` is guarded against
  JavaScript instrumentation matches; its expected Kubernetes page is still outside top three.

### Step 7: score telemetry

- `go test ./pkg/otel ./internal/handler/tools -run 'TestDocs' -count=1`: PASS.
  Covers successful hit, zero results, malformed query, cancellation, raw-score fidelity,
  outcome attributes, client source, and no error result-count bucket.
- Counter outcome describes executed index searches; pre-index input/readiness failures
  retain their existing tool-error telemetry. README documents the raw uncalibrated histogram.

### Initial implementation verification (2026-09-16; before revised gate and re-landed stemming)

- `make fmt goimports`: PASS.
- `go build ./cmd/server`: PASS (native Go 1.27.1).
- `go test ./...`: FAIL on an unchanged test under native Go 1.27.1:

  ```text
  --- FAIL: TestSchemaConversionFailuresIncludeDirectionAndTool (0.00s)
      --- FAIL: TestSchemaConversionFailuresIncludeDirectionAndTool/decode_input_schema_object (0.00s)
          mcp_test.go:46: panic = "decode input schema for tool \"input_probe\" (type jsontext.Value) as object: json: cannot unmarshal array into Go value of type map[string]interface {}", want context "decode input schema for tool \"input_probe\" (type json.RawMessage) as object"
  ```

  Go 1.27 reports the alias target `jsontext.Value`; the untouched test pins the old
  `json.RawMessage` type name. No unrelated test or guardrail was weakened.
- `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...`: PASS, including the above test,
  on the repository's Go 1.26 CI toolchain. Re-run after final formatting: PASS.
- `actionlint .github/workflows/guardrails.yaml`: PASS.
- `go test -count=1 -run '^TestGuardrail_' ./...`: PASS.
- `go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`: PASS.
- `go test ./internal/docs -run 'TestGoldenSet|TestCorpusManifestMatches|TestGuardrail_DocsIndexBuildPeakHeap|TestGlossary|TestSnippetRuneBudget|TestEmbeddedSearchProbeRegressions' -count=1 -v`: PASS.
  Final keyword 44/45 recall (0.977778), 40/45 precision (0.888889);
  sentence 5/7 (0.714286), 3/7 (0.428571); abbreviation 6/7 (0.857143), 6/7 (0.857143).
  Final peak/resident: 115/28 MiB, below unchanged 224/64 MiB budgets.
- `GOTOOLCHAIN=go1.26.0 go vet ./...`: PASS.
- `GOTOOLCHAIN=go1.26.0 go test -race ./internal/docs ./internal/handler/tools ./pkg/otel`: PASS.
- `git diff --check`: PASS. Corpus binary and corpus manifest untouched.
- Exact description-parity check across handler/wire fixture/manifest/README: PASS;
  525-byte tool and 277-byte parameter descriptions. No schema or wire result-shape change.
- `npx run-local-ci run --quiet --all --pause-on-failure`: earlier run interrupted at session end.
  Not restarted per coordinator instruction; coordinator owns remaining CI.
- Companion audit: `/Users/makeavish/signoz/agent-skills/skills/signoz-searching-docs/SKILL.md`
  lines 23 and 26 still say BM25, natural-language query, trust ranking, and user's phrase.
  A companion PR must replace these with keyword/split/retry guidance and avoid the false
  BM25 claim. No companion edits or PR were created, as requested.
- Direct guidance review: product/setup discovery routes to search, exact URLs route to
  fetch, tenant telemetry is excluded; examples preserve product terms and searchContext
  still holds the original request. No model-based client behavior evaluation was run.

### Revised-gate evaluation (2026-09-16)

| Configuration | Keyword recall / precision | Sentence recall / precision | Abbreviation recall / precision | Overall recall / precision |
|---|---|---|---|---|
| Original baseline | 44/45 / 38/45 | 5/7 / 3/7 | 6/7 / 4/7 | 55/59 / 45/59 |
| Re-landed option (b) | 43/45 / 41/45 | 6/7 / 4/7 | 6/7 / 6/7 | 55/59 / 51/59 |
| Option (b) + heading text (not retained) | 43/45 / 41/45 | 6/7 / 4/7 | 6/7 / 6/7 | 55/59 / 51/59 |

- Overall baseline 0.932203 / 0.762712; final 0.932203 / 0.864407.
  Keyword final 0.955556 / 0.911111; sentence 0.857143 / 0.571429;
  abbreviation 0.857143 / 0.857143. Keyword recall loss is 1/45 (0.022222),
  within the user-selected 0.05 tolerance. Other styles improve or tie.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run 'TestGoldenSet|TestGuardrail_DocsIndexBuildPeakHeap' -count=1 -v`:
  (b) initially failed a literal strict-increase-on-both gate: `"0.9322033898305084" is not greater than "0.9322033898305084"`.
  Coordinator clarified no metric decreases and at least one increases; test now implements that exact rule.
  Option (b) heap 146/40 MiB. Combined heading-text experiment PASS, heap 190/49 MiB,
  but no quality increase versus (b), so heading text reverted.
- Initial inflection-only snippet test failed because the body's unmarked fallback
  fragment preceded the correctly highlighted stemmed fragment. Fixed preference ordering;
  end-to-end test now verifies a term absent from standard body analysis still returns
  a marked, relevant stemmed-body snippet within 200 runes.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run 'TestGoldenSet|TestGlossary|TestSnippetRuneBudget|TestSearchStemmedOnlySnippet|TestChooseSnippetPrefersMatchedStemmedFragment|TestEmbeddedSearchProbeRegressions' -count=1 -v`: PASS.
  Includes now-passing Node.js instrumentation probe, glossary-only snippets, Unicode rune
  limits, and exact-body versus stemmed-body highlighted fragment preference.
- Final full verification before memory follow-up: Go1.26 formatting, build, full test
  suite, vet, race, and standalone wire-catalog checks PASS. Focused golden/probe/heap run
  PASS with peak/resident 154/41 MiB.
- A separate `GOTOOLCHAIN=go1.26.0 go test -count=1 -run '^TestGuardrail_' ./...`
  FAILS intermittently: peak 230 MiB, resident 45 MiB; exact assertion
  `"241827840" is not less than or equal to "234881024"`.
- Isolated repeat `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestGuardrail_DocsIndexBuildPeakHeap$' -count=5 -v`
  confirms cold-build failure: peak 244 MiB, resident 47 MiB, exact assertion
  `"255959040" is not less than or equal to "234881024"`; subsequent four builds
  peak 164–194 MiB and resident 43–50 MiB. No budget or test changes made.
  Coordinator approved smaller full-build batches; measurements and outcome follow.

### Batch64 verification (2026-09-16)

- Workflow inspected: `.github/workflows/guardrails.yaml` pins Go1.26 and runs
  `go test -count=1 -run '^TestGuardrail_' ./...`; inventory contains the heap test.
  Memory sampler does not build another index; no `t.Parallel` is used by the heap test.
  Cold failures reproduce in isolation, so they are not caused by another test's concurrent build.
- Before, `GOTOOLCHAIN=go1.26.0 go test -count=5 -run '^TestGuardrail_DocsIndexBuildPeakHeap$' ./internal/docs -v`:
  PASS on this repetition, peaks203/183/173/174/188 MiB, residents49/41/45/43/47 MiB;
  earlier cold failures244/230 MiB remain the reason for reducing batches.
- After, identical command at batch64: PASS, peaks193/158/193/191/162 MiB,
  residents45/47/52/57/55 MiB; max193/57 MiB. Test duration mean0.562s versus0.558s before.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -count=1 -v`: PASS before (175/43 MiB,
  heap test0.66s) and after (193/50 MiB,0.60s). Full-set ranking unchanged by batch size.
- Final verification commands after batch change, all PASS:
  - `GOTOOLCHAIN=go1.26.0 make fmt goimports`
  - `GOTOOLCHAIN=go1.26.0 go build ./cmd/server`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...`
  - `actionlint .github/workflows/guardrails.yaml`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 -run '^TestGuardrail_' ./...`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`
  - `GOTOOLCHAIN=go1.26.0 go vet ./...`
  - `GOTOOLCHAIN=go1.26.0 go test -race ./internal/docs ./internal/handler/tools ./pkg/otel`
  - `git diff --check`
- README Memory footprint updated to measured resident45–57 MiB / peak193 MiB; removed
  stale whole-process and refresh-peak claims not re-measured with the stemmed field.
- Local CI not restarted, as requested. Coordinator owns remaining CI.

### Production-pair baseline, span and fallback verification (2026-09-16)

- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`
  on stashed/pre-change implementation: PASS after treating malformed raw query syntax
  as a retrieval miss during baseline capture only. First capture stopped on `syntax error`
  for the dashboard-variable raw query. Two raw requests were parser-invalid in the old index.
  Final test has no error exception: all95 searches must succeed and return results.
- All expected URLs exist in corpus.manifest.json; no credentials or hard-coded page priors added.

| Style | Pre-change recall@3 / precision@1 | Before syntax fallback | Final |
|---|---|---|---|
| keyword (45) | 44/45 / 38/45 =0.977778 /0.844444 |43/45 /41/45 |0.955556 /0.911111 |
| sentence (7) |5/7 /3/7 =0.714286 /0.428571 |6/7 /4/7 |0.857143 /0.571429 |
| abbreviation (7) |6/7 /4/7 =0.857143 /0.571429 |6/7 /6/7 |0.857143 /0.857143 |
| production-raw (18) |9/18 /6/18 =0.500000 /0.333333 |9/18 /8/18 |11/18 /10/18 =0.611111 /0.555556 |
| production-keyword (18) |14/18 /13/18 =0.777778 /0.722222 |16/18 /16/18 |0.888889 /0.888889 |
| overall (95) |78/95 /64/95 =0.821053 /0.673684 |80/95 /75/95 =0.842105 /0.789474 |82/95 /77/95 =0.863158 /0.810526 |

- Dashboard-variable raw query now ranks an expected page **1st**, rather than failing parser
  validation. The raw self-host migration request also becomes rank1. Fluent Bit raw and
  keyword remain outside top3 before and after; do not claim those fixed.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/handler/tools -run 'TestDocsSearchSpanAttributes|TestDocsSearchScoreTelemetry' -count=1 -v`: PASS.
  Recording-tracer cases cover results, zero results, whitespace validation, syntax fallback,
  Unicode-safe256-rune truncation, section, result count, top score and fallback signal.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs ./internal/handler/tools -run 'TestGoldenSet|TestGlossary|TestIndexSearchFetchAndSwap|TestDocs' -count=1 -v`: PASS after fallback.
- Final verification after production/span/fallback changes, all PASS:
  - `GOTOOLCHAIN=go1.26.0 make fmt goimports`
  - `GOTOOLCHAIN=go1.26.0 go build ./cmd/server`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -count=1 -v`
  - `GOTOOLCHAIN=go1.26.0 go test -count=5 -run '^TestGuardrail_DocsIndexBuildPeakHeap$' ./internal/docs -v`
  - `actionlint .github/workflows/guardrails.yaml`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 -run '^TestGuardrail_' ./...`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`
  - `GOTOOLCHAIN=go1.26.0 go vet ./...`
  - `GOTOOLCHAIN=go1.26.0 go test -race ./internal/docs ./internal/handler/tools ./pkg/otel`
  - `git diff --check`
- Latest isolated5 heap peaks194/138/170/166/181 MiB, residents48/50/50/49/50 MiB:
  max194/50 MiB. Full docs package measured208/48 MiB, leaving16 MiB peak headroom.
  Guardrail budgets remain224/64 MiB; batch size remains64. Earlier193/57 figures are
  historical measurements, not a hard upper bound; README now states the observed range.
- Corpus assets, guardrail policy/inventory unchanged. No local-CI restart. The existing
  production report and .codex directory were not edited. No stash remains from baseline capture.

### Final option (c) verification (2026-09-16; supersedes option b)

| Style | Original baseline recall@3 / precision@1 | Final option(c) |
|---|---|---|
| keyword (45) |44/45 /38/45 =0.977778 /0.844444 |43/45 /40/45 =0.955556 /0.888889 |
| sentence (7) |5/7 /3/7 =0.714286 /0.428571 |6/7 /4/7 =0.857143 /0.571429 |
| abbreviation (7) |6/7 /4/7 =0.857143 /0.571429 |6/7 /6/7 =0.857143 /0.857143 |
| production-raw (18) |9/18 /6/18 =0.500000 /0.333333 |11/18 /9/18 =0.611111 /0.500000 |
| production-keyword (18) |14/18 /13/18 =0.777778 /0.722222 |16/18 /16/18 =0.888889 /0.888889 |
| overall (95) |78/95 /64/95 =0.821053 /0.673684 |82/95 /75/95 =0.863158 /0.789474 |

- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run 'TestGoldenSet|TestGlossary|TestSearchStemsTitleAndHeadingsOnly|TestChooseSnippetPrefersMatchedBodyFragment|TestEmbeddedSearchProbeRegressions' -count=1 -v`: PASS.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -count=1 -v -run '.'` repeated in five
  separate processes: all PASS. Peak MiB103/124/97/103/120; resident32/33/28/34/32.
  Min/max peak97/124 MiB; min/max resident28/34 MiB. Heap test0.36/0.34/0.37/0.35/0.35s.
- README memory section now describes option(c)'s measured ranges, not the removed body field.
- Final verification command list, all PASS under the repository's Go1.26 toolchain:
  - `GOTOOLCHAIN=go1.26.0 make fmt goimports`
  - `GOTOOLCHAIN=go1.26.0 go build ./cmd/server`
  - `GOTOOLCHAIN=go1.26.0 go vet ./...`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...`
  - `actionlint .github/workflows/guardrails.yaml`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 -run '^TestGuardrail_' ./...`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`
  - `GOTOOLCHAIN=go1.26.0 go test -race ./internal/docs ./internal/handler/tools ./pkg/otel`
  - `git diff --check`
- No body_stemmed references remain in docs implementation/tests. Corpus assets and all
  guardrail policy/inventory files remain unchanged. Build artifact removed; no commits.
  Local-CI runner not restarted; coordinator owns remaining CI.

### Actual production searchText and MSM verification (2026-09-16)

- Manifest-only labels frozen before search in38 production-real entries;133 total.
- Stashed baseline `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`:
  PASS (evaluation-only parser errors counted as misses). Pre-change overall101/133 recall,
  79/133 precision. production-real23/38 recall,15/38 precision. Stash restored afterward.
- Before MSM same command: PASS, overall108/133 recall,90/133 precision;
  production-real26/38 recall,15/38 precision.
- Temporary `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestMinimumMatchSweep$' -count=1 -v`:
  PASS.123 non-holdouts: baseline103 recall/85 precision, each tested{2,3,4}×{2,3,4}
  candidate103 recall/86 precision. Non-holdout production-real23/31 recall,13/31 precision
  versus23/31 and12/31 before. Holdouts excluded before searching. Temporary sweep removed.

| Style | Original baseline recall@3 / precision@1 | Before MSM | Final with MSM2/2 |
|---|---|---|---|
| keyword45 |0.977778 /0.844444 |0.955556 /0.888889 |0.955556 /0.911111 |
| sentence7 |0.714286 /0.428571 |0.857143 /0.571429 |1.000000 /0.571429 |
| abbreviation7 |0.857143 /0.571429 |0.857143 /0.857143 |0.857143 /0.857143 |
| production-raw18 |0.500000 /0.333333 |0.611111 /0.500000 |0.611111 /0.444444 |
| production-keyword18 |0.777778 /0.722222 |0.888889 /0.888889 |0.888889 /0.888889 |
| production-real38 |0.605263 /0.394737 |0.684211 /0.394737 |0.684211 /0.421053 |
| overall133 |0.759398 /0.593985 |0.812030 /0.676692 |0.819549 /0.684211 |

- Final counts:109 recall /91 precision out of133. All gates PASS.
- Six production-real expected-page rank changes versus pre-MSM (0=outside top3):
  - `AWS integration CloudWatch metric streams configure which metrics namespaces filter`:0→2.
  - `service map dependency graph dashboard host metrics infrastructure`:2→3.
  - `Azure monitoring integration send Azure metrics logs to SigNoz`:1→3.
  - `logs explorer list view add columns to log table`:3→0.
  - `query_range v5 API request variables field format`:3→1.
  - `dashboard traces query builder stacked bar chart stacking toggle step interval legend group by filter expression EXISTS OR HTTP status`:2→1.
- `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run 'TestMinimumMatch|TestGlossary|TestGoldenSet' -count=1 -v`: PASS.
  Unit checks cover standard stopwords/case, dedup,4-term minimum,ceil threshold,
  64-term cap, per-field boosts and actual minimum-match filtering.
- Three separate final whole-package runs of
  `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -count=1 -v -run '.'`: all PASS.
  Peak MiB134/102/115, resident36/29/32; min/max peak102/134, resident29/36.
  Heap-test durations0.43/0.50/0.39s. No mapping change from option(c); batches64,
  reviewed budgets224/64 unchanged. README extends observed ranges across all8 option(c) runs.
- Final verification, all PASS:
  - `GOTOOLCHAIN=go1.26.0 make fmt goimports`
  - `GOTOOLCHAIN=go1.26.0 go build ./cmd/server`
  - `GOTOOLCHAIN=go1.26.0 go vet ./...`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...`
  - `actionlint .github/workflows/guardrails.yaml`
  - `GOTOOLCHAIN=go1.26.0 go test -count=1 -run '^TestGuardrail_' ./...`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/mcp-server -run '^TestGuardrail_WireCatalogGoldens$' -count=1`
  - `GOTOOLCHAIN=go1.26.0 go test ./internal/docs -run '^TestGoldenSet$' -count=1 -v`
  - `GOTOOLCHAIN=go1.26.0 go test -race ./internal/docs ./internal/handler/tools ./pkg/otel`
  - `git diff --check`
- No commits/pushes, corpus changes, guardrail relaxation or local-CI restart. Build artifact
  removed. The temporary baseline stash was restored; source production log file untouched.

### Glossary synonym-group verification (2026-09-16)

Old glossary versus new on the same 143-query set. Only these five queries changed rank; the 133 earlier queries and all 10 holdouts kept identical ranks.

| Query | Before | After |
|---|---|---|
| prebuilt dashboards | 0 | 2 |
| mute alert | 0 | 1 |
| silence alerts | 0 | 1 |
| mongo monitoring | 0 | 1 |
| dotnet auto instrumentation | 2 | 1 |

Overall recall@3 118/143 (0.825), precision@1 98/143 (0.685); old glossary on the same set 0.797/0.657. `postgres monitoring` stays at 2 and `infra monitoring` at 0: the `k8s-infra` titles match `infra` directly and a 0.5 clause on `infrastructure` does not outweigh that. `prebuilt dashboards` ranks the Dashboards overview first and Dashboard Templates second, which is acceptable.

## Outcome

Done. Shipped on `feat/docs-search-ranking-and-query-guidance`:

- Golden evaluation grown from 45 keyword queries to 133 queries across six styles (keyword,
  sentence, abbreviation, production-raw, production-keyword, production-real), ten holdouts
  excluded from every boost sweep, per-style and aggregate gates. Overall recall@3 0.759 to
  0.820 and precision@1 0.594 to 0.684 versus the pre-change configuration; the 38 real
  production search strings moved from 0.605 / 0.395 to 0.684 / 0.421.
- Ranking: all-terms conjunction clauses (title 6, body 5), minimum-should-match clauses for
  queries of four or more terms (title 2, body 2), query-side glossary at boost 0.5, `en`
  analyzer on title and headings with the body left on `standard`. TF-IDF unchanged.
- Robustness: invalid Bleve query-string syntax drops only that clause and sets
  `mcp.docs.query_string_dropped` instead of failing the search.
- Client guidance: `signoz_search_docs` tool and `searchText` descriptions rewritten as query
  guidance, mirrored into the wire-catalog fixture, `manifest.json`, and README.
- Telemetry: `outcome` attribute on the search counter, uncalibrated top-score histogram, and
  span attributes for the executed search text (256 runes), section filter, result count, and
  top score.
- Memory: build peak 97 to 134 MiB and resident 28 to 36 MiB across eight whole-package runs,
  budgets unchanged at 224 / 64 MiB; index batch size 64 retained for headroom.

Rejected with numbers recorded above: headings indexed as text, stemmed duplicate body field
(heap guardrail flaky), batch size 32 (resident over budget), BM25 scoring, embeddings.

Deferred: follow-up fetch attribution (needs a client or request correlation mechanism);
`ApplyDelta` still applies one atomic batch; content gaps for CLI access to the query API,
MTTR/MTTA incident metrics, and browser source-map upload; scrubbing of verbatim user prompts
stored in `mcp.search_context`. Companion SigNoz/agent-skills change to the
`signoz-searching-docs` skill is required and tracked in the PR description.
