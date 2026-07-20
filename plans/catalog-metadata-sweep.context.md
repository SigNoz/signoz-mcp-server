# Feature: Catalog Metadata Sweep — Context & Discussion

## Original Prompt
> Now we have merged this PR, can we similarly do a swipe across all tools and resources and compare and test each of them with sonnet 5 like before.
> Do it in commit batches but single PR
> Ensure to follow best practices like in previous PR

## Reference Links
- [MCP tool descriptions and resources — best practices](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5014863452)
- [Merged wire-contract budget PR #247](https://github.com/SigNoz/signoz-mcp-server/pull/247)
- [Wire-contract cleanup task #158](https://github.com/SigNoz/nerve-pod/issues/158)

## Key Decisions & Discussion Log
### 2026-07-19 — Scope and evaluation boundary
- Use merged commit `8b3c298` as the old/baseline catalog and `codex/catalog-metadata-sweep` as the candidate. Keep all changes in one PR but commit them in reviewable tool/resource-family batches.
- Audit the initialized MCP wire catalog rather than only source strings or `manifest.json`. Cover every registered tool, resource, and resource template, including items that need no wording change.
- Apply the issue #34 placement rubric: selection and critical first-call prerequisites in tool metadata; field-local constraints in schemas; shared policy in server instructions; long examples/grammars in resources; immediate repair guidance in execution errors.
- Prefer successful first calls. When a read-only lookup can validate external names, IDs, paths, or other references before a mutation, make that preflight the primary workflow; keep mutation-time validation as a stale/racing-state fallback.
- Evaluate direct, indirect, and negative/confusion prompts with identical old/new catalogs, `claude-sonnet-5`, medium effort, strict MCP configuration, no built-in tools, fresh non-persistent sessions, and structured results. Mutation prompts are routing/argument-only and must not execute.
- Do not use staging credentials or send live workspace data to Anthropic for this sweep. The evaluation concerns repository-derived metadata and model routing only.

## Open Questions
- [ ] Claude Code is installed but currently unauthenticated; complete `claude auth login` before model runs.
- [ ] Final corpus size and repetition allocation depend on the complete tool/resource inventory; preserve at least one direct and one indirect/negative decision for every catalog item, then repeat changed or confused boundaries.
- [ ] Decide which reusable eval artifacts belong in the repository versus an isolated temporary results directory after reviewing the prior harness.

### 2026-07-19 — Initialized catalog and Sonnet policy boundary
- The initialized catalog contains 41 tools, 19 static resources, and 2 resource templates. `manifest.json` mirrors all tools but only 4 static resources, and the existing integration suite checks tool parity but not full resource/template identity.
- Claude Code authentication is now active for the SigNoz organization. The first metadata-only Sonnet 5 pilot was blocked before launch by organization policy because repository-derived descriptions, schemas, resource text, and eval prompts are private data being sent to Anthropic. Nothing was sent. A retry requires explicit user approval after disclosure; the static audit and local harness work can continue independently.
- Keep credentials, live tenant data, and mutating tool execution out of the evaluation. Use placeholder MCP configuration and score selection/argument/resource-reading decisions only.
- Check in the sanitized corpus, runner, coverage validator, and aggregate report so the comparison is reproducible; keep raw model envelopes and session identifiers out of git.

### 2026-07-19 — Static resource audit
- Catalog drift and first-call risks are evidence-backed: incomplete manifest/README resource inventories, duplicate dashboard resource names, misleading template descriptions, fictional notification channels presented as working examples, invalid dashboard query examples, and missing ClickHouse schema provenance.
- Resource templates must distinguish selection boundaries from nearby tools and must not hide global authentication failures. Recoverable partial enrichment should fail open with explicit warnings and freshness/window metadata.
- Add exact initialized-wire catalog guards before changing descriptions so later batches cannot silently add, remove, or mislabel tools/resources/templates.

### 2026-07-19 — Static tool audit
- All 41 registered tools are present in `manifest.json`. The highest-confidence description defects are a false claim that channel listing returns configuration, missing GET-first guidance for full-replacement channel updates, incomplete metric resource-prefix rules, under-specified raw-row versus aggregate boundaries, and unusable service-operation tag format guidance.
- Keep working selection boundaries that already test well, including alert instance/rule/history routing, dashboard template import versus custom creation, and the raw Query Builder escape hatch. Change only descriptions or parameter metadata with a concrete contract/routing reason.
- Do not require `signoz_list_metrics` before `signoz_query_metrics` when an exact metric name is already known; the query tool can fetch missing metric metadata itself. Use list-metrics preflight when the name is unknown.
- Current create/update alert validation mechanically requires at least one existing channel even when policy routing is enabled. Until the upstream contract is changed and verified, metadata must describe the current first-call requirement instead of implying channel-less policy payloads will pass.

### 2026-07-19 — Evaluation corpus frozen
- The checked-in corpus has 151 cases: one direct and one indirect case for each of the 62 initialized surfaces, shared nearest-neighbor negative cases, and one content-use probe for every static resource and template. CI derives the initialized catalog and fails when any required coverage is missing.
- The local runner snapshots 41 tools, 19 resources, 2 templates, and all 21 resource bodies from each binary. Dynamic templates use sanitized GET-only fixtures; any mutation or unknown upstream route fails the run.
- One full paired repetition is 302 isolated Sonnet sessions. Use a $30 global cap, then add three paired repetitions only for failures, A/B deltas, critical workflows, and a stable control sample. The runner reports exact selection, argument/content checks, negative regressions, and aggregate regression without an LLM judge.
- Raw envelopes, binaries, MCP configs, resource snapshots, stderr, and session IDs remain under `/tmp`. The repository keeps only the corpus, deterministic runner, safe fixtures, coverage guard, and a final compact aggregate report.

### 2026-07-19 — Metrics, fields, and services batch
- Reframed metadata around the nearest selection boundaries: metric discovery versus querying, ingestion ranking versus dependency usage versus cardinality, field names versus values, and traced APM services versus arbitrary `service.name` values on logs.
- A known exact metric name can go directly to `signoz_query_metrics`; omitted metric metadata is fetched internally. `signoz_list_metrics` remains the discovery/catalog path and has a limit but no offset pagination.
- Synced all resource-context prefixes with runtime inference and documented the live-tested `TagQueryParam` JSON-string shape for service-operation filters.
- The metrics guide is Markdown with advertised byte size, and its preflight now matches the direct-query behavior. No payload or handler behavior changed.

### 2026-07-19 — Logs, traces, and Query Builder batch
- Made raw-versus-aggregate output the primary boundary: search tools return individual log/span rows, aggregate tools return statistics and grouped/top-N results, and trace details expands one already-known trace ID into its hierarchy.
- `signoz_get_trace_details` now routes unknown IDs through trace search and warns that the time window must contain the trace. Severity metadata names common values as examples rather than inventing a closed backend enum.
- Kept raw Query Builder as the unsupported-shape escape hatch and retained the empirically important formula invariants: input builder queries at 10000, formula result at 100, and non-empty v5 `spec.order`.
- Both signal query guides are Markdown with advertised byte sizes and explicit workspace key/value discovery. No query payload or handler behavior changed.

### 2026-07-19 — Shared mutation preparation policy
- The successful-first-call rule applies to alerts, notification channels, dashboards, and saved views, so the server initialization instructions now require reading full-replacement targets first and resolving referenced names or IDs with read-only calls before mutation.
- Tool descriptions retain only family-specific prerequisites. Mutation-time validation is documented as a fallback for state that changed after the read, not as the primary discovery workflow.

### 2026-07-19 — Dynamic resource commit boundary
- Both resource templates share one registration file and need coordinated wire-inventory tests, so their truthful metadata and alert-history failure handling move into the final cross-catalog batch instead of being split artificially across the alert and dashboard commits.
- Static alert and dashboard authoring resources remain in their respective family batches.

### 2026-07-19 — Alerts and notification channels batch
- Channel listing is the primary read-only preflight for exact-name verification, user choice, duplicate-name prevention, and ID discovery; create/update validation is only a stale/race fallback after that read.
- Channel listing returns summaries, while `signoz_get_notification_channel` returns the embedded receiver configuration. Channel and alert updates are full replacements and therefore require GET-first preservation of unchanged fields; omitting `send_resolved` on a channel update resets it to true.
- Current alert validation mechanically requires at least one existing valid channel reference even when policy routing ignores per-threshold channels. Metadata now states both the routing semantics and the current first-call constraint without changing runtime behavior.
- Alert examples are adaptable canonical shapes rather than directly executable payloads: the illustrative-channel warning now precedes the first example, and static alert resources advertise Markdown MIME and exact byte size.

### 2026-07-19 — Dashboards and saved views batch
- Separated tenant dashboards, curated templates, and saved Explorer views at every list/get/create/update/delete boundary. Dashboard and view updates explicitly start from their full GET result because both are replacement operations.
- Added field-local dashboard schema prose for nested layouts, variables, widgets, query envelopes, aggregations, filters, ordering, and units without changing JSON tags, requiredness, defaults, or runtime normalization. The `list_views.sourcePage` enum now mirrors the existing runtime allow-list exactly.
- All dashboard, PromQL, and saved-view resources now advertise truthful unique names, Markdown MIME, exact byte sizes, and query-engine selection boundaries. ClickHouse schema references identify their bundled-collector-migration provenance and defer to tenant schema errors when versions differ.
- Corrected malformed widget examples (unbalanced aggregations, missing boolean connectors, and quoted booleans) and added tenant-field discovery guidance. Known pre-existing typed-schema gaps for row queries, decimal precision, and normalized optional collections remain out of scope rather than being papered over in metadata.

### 2026-07-19 — Docs, dynamic resources, and catalog invariants
- Docs metadata now separates topical search from fetching a selected page or heading; the sitemap resource is the indexed catalog path, not a live-tenant data source.
- The dashboard template URI returns the complete live definition, so its model-facing name and description no longer promise a smaller summary. The alert template exposes its six-hour window, freshness, history availability, and a corrective warning when optional history enrichment fails; 401/403 history failures propagate instead of being hidden.
- Initialized-wire tests now require unique resource names and URIs, descriptions within the 1,024-byte ceiling, Markdown MIME for static guides, and exact advertised byte sizes. The README inventories all 19 static resources and both templates.
- Shared initialization guidance carries the read-before-replacement and read-only name/ID resolution policy because it applies across alert, channel, dashboard, and saved-view mutations.

### 2026-07-19 — Open-question resolution
- [x] Claude Code authentication is active for the SigNoz organization.
- [x] The frozen corpus contains 151 cases with direct and indirect coverage for all 62 initialized surfaces plus nearest-neighbor negatives and resource-content probes.
- [x] The sanitized corpus, runner, fixtures, coverage guard, and eventual aggregate report belong in the repository; raw model envelopes, binaries, configs, logs, and session IDs remain under `/tmp`.
- [ ] The paid Sonnet phase still requires explicit post-disclosure approval to send unmerged repository metadata and static resource content to Anthropic. No staging credentials or live SigNoz data will be sent.

### 2026-07-19 — Sonnet execution blocked by organization policy
- The user explicitly approved sending the unmerged metadata and static resource content to Anthropic for the requested Sonnet 5 comparison.
- The organization policy still rejected the run before Claude launched because unmerged private repository content cannot be disclosed to Anthropic, even with user approval. No repository data, credentials, or live tenant data was sent.
- Do not circumvent this control. Keep the complete paired corpus and deterministic runner in the PR, publish only local contract verification, and run the model comparison later from an environment whose policy permits the disclosure.

### 2026-07-19 — Final local verification and companion contract check
- The final committed catalog snapshots contain the same 41 tools, 19 static resources, 2 resource templates, and 21 readable resource bodies on both sides of the comparison. The 151-case corpus validates, its runner compiles, and the repository-wide test, vet, build, manifest, and diff checks pass.
- Checked `SigNoz/agent-skills`: no companion update is needed. Tool names, parameter names, payload fields, JSON tags, resource URIs, and template URI patterns are unchanged. The only structural schema delta exposes the existing `signoz_list_views.sourcePage` runtime allow-list, which the saved-view skill already teaches.
- Keep the PR in draft while the Sonnet execution is policy-blocked; do not claim qualitative model improvements until the paired run completes in an allowed environment.

### 2026-07-19 — PR #248 CI lint follow-up
- The first workflow passed tests, formatting, dependency checks, and builds but failed `lint / go` because the new corpus coverage test deferred `f.Close()` without checking its error.
- Handle the close error explicitly in the deferred function. The exact CI linter version and flags (`golangci-lint v2.6.0 run --timeout 10m0s --allow-parallel-runners`) now report zero issues, and `go test -count=1 ./...` passes.

### 2026-07-19 — Sonnet rerun and Claude Code helper-model compatibility
- The user confirmed that repository data can be sent to Claude and that the team subscription does not require a restrictive dollar cap. Keep the fixed case/repetition/worker/180-second boundaries, but make USD-equivalent per-call and total ceilings optional so subscription-backed runs cannot be truncated unnecessarily.
- Claude Code 2.1.214 successfully used `claude-sonnet-5` for the first 15 answers, but also reported `claude-haiku-4-5-20251001` usage for structured-output/tool plumbing. The original exact-single-model assertion falsely marked every call as an error.
- Require the requested Sonnet model to appear, allow only the known Haiku 4.5 helper family alongside it, and continue rejecting missing Sonnet usage or any other fallback/helper model. Stop and discard the partial pass before rerunning the complete corpus.

### 2026-07-19 — Scorer audit and category execution
- The first complete raw pass was diagnostic only: optional discovery, whole-output content matching, and action/resource conflation could overstate correctness. Harden the scorer before using results: observe an actual `ToolSearch` event, separate tool/action selection from prerequisite-resource selection, scope content checks to arguments or facts, require exact URI sets and verbatim `searchContext`, reject malformed/unpaired records, and pin per-case/binary/catalog provenance.
- The user requested category-sized execution. Cover all 151 cases exactly once across telemetry queries (44), alerting (34), dashboards/templates (46), saved views (18), and documentation (9), always pairing baseline and candidate in fresh sessions.
- Run one paired repetition across every case, then three paired repetitions for deltas, critical failures, and stable controls. Repeated gates use a strict majority so a single stochastic miss cannot decide the result.
- USD-equivalent CLI accounting is retained as metadata, not treated as an incremental subscription charge. Dollar guard flags are optional; the selected cases, maximum repetitions, worker count, protocol retries, and per-call timeout bound execution.

### 2026-07-19 — Eval-oracle corrections
- Resource-use cases may coherently return `resource` or `none` after the body has already been supplied, but must still return the exact URI set. Tool cases now score the owning tool separately from prerequisite guides.
- Mutation prompts only expect a mutation when read-only discovery/fetch and confirmation are explicitly complete. Prompts that merely imply an omitted payload were reclassified as routing rather than executable workflow cases.
- Replace the notification-channel placeholder `channel-123` with a valid UUID and assert it on get/update/delete/read cases. Sonnet correctly treated the old placeholder as unresolved, so those old observations are excluded and the 12-case channel family was rerun.
- Strengthen short resource-content oracles (`ns`, `id`, `get`, `put`) into meaningful phrases or tool names so incidental substrings cannot pass.

### 2026-07-19 — Sonnet results and evidence-driven fixes
- The adjusted complete pass selects the exact tool/action in 150/151 candidate cases versus 149/151 baseline cases, ties exact prerequisite-resource selection at 142/151, ties exact arguments at 142/151, and is fully correct in 133/151 versus 131/151. Both variants pass all content and safety checks.
- The saved-view first pass showed candidate guide selection at 2/4 observations versus baseline 4/4. Change `signoz_create_view` to say both view resources must be read before composing any payload; the rebuilt candidate then passes tool, resource, argument, and content checks in 6/6 runs, matching baseline.
- Clarify alert/channel deletion so completed ID resolution and confirmation lead directly to deletion without repeated list/get preflight. On the rebuilt candidate, alert deletion is fully correct in 5/6 targeted runs versus baseline 3/6; the corrected channel family selects the exact tool in 12/12 cases for both variants.
- Dashboard targeted runs favor candidate selection 12/12 versus 10/12; telemetry targeted runs tie tool selection 21/21 while candidate selects prerequisite resources 14/21 versus baseline 10/21 and is fully correct 13/21 versus 9/21.
- Preserve only the compact aggregate report in git. Raw envelopes, sessions, snapshots, and binaries remain in `/tmp`; no staging credential, tenant data, live query, or mutation was used.

### 2026-07-19 — Open-question resolution after approved execution
- [x] The user explicitly approved the repository-metadata disclosure, and the category runs completed successfully with authenticated Claude Code.
- [x] The subscription-backed runs omit the optional dollar guard while retaining deterministic case/repetition/worker/time bounds.
- [x] The compact aggregate report is `evals/mcp-catalog/sonnet-5-results-2026-07-19.md`; raw model artifacts remain outside git.

### 2026-07-19 — Final negative boundary and verification
- Replace the redundant trace-details negative with the missing `signoz_search_logs` negative: grouped ERROR-log totals must route to `signoz_aggregate_logs`, not raw-row search. Baseline and candidate both pass tool, resource, argument, content, and safety scoring in 3/3 fresh Sonnet runs, so the adjusted aggregate totals are unchanged.
- Clarification to earlier artifact notes: the harness persists extracted result records, binaries, and catalog snapshots under `/tmp`; it consumes rather than retains Claude stream envelopes and session identifiers.
- Final local verification passes: 19 Python scorer tests, all 151 corpus cases, the initialized-wire coverage test, repository-wide Go tests, vet, build, `golangci-lint` v2.6.0 with zero issues, manifest parsing, and `git diff --check`.
- The companion `SigNoz/agent-skills` checkout needs no change: no taught tool name, parameter, payload shape, resource URI, or workflow contract changed, and the saved-view skill already requires both authoring resources.

### 2026-07-19 — Strict scorer and final-catalog rerun decision
- A final audit found that the first aggregate scorer treated an omitted resource oracle as unconstrained and allowed `searchContext` prompt text to satisfy payload-content checks. Score every resource set exactly, including empty sets, and exclude `searchContext` from content matching; earlier aggregate numbers are diagnostic and must be replaced by a fresh final-catalog run.
- The grouped-log negative legitimately requires `signoz://logs/query-builder-guide`, matching both aggregate-log positive cases and the selected tool metadata. It now also asserts count, `service.name` grouping, ERROR severity, and scalar output rather than only the request type.
- Strengthen every `searchContext` parameter description to require the entire original request verbatim, including preflight and confirmation clauses. Focused Sonnet reruns cleared the observed channel, dashboard-delete, and raw-builder argument/resource failures, so retain the catalog-wide wording and rerun all 98 tool routing/workflow cases rather than extrapolating.
- Freeze a stronger evaluator before the final category run: 29 unit tests now cover exact scoring, schema-shape validation, selected-case/evaluator/catalog/binary provenance, recorded positive Sonnet usage, successful non-empty ToolSearch result evidence, selected-tool exposure, rejection of real SigNoz tool invocations, retry/cost behavior, and unbuffered MCP response handling. CI runs the Python suite and validates all 151 corpus cases.
- Correction to the preceding verification entry: it described an intermediate 19-test suite. The frozen evaluator has 29 passing Python tests; the plan remains In Progress until the final evidence commit is pushed and CI passes.

### 2026-07-19 — Reproducible candidate and subscription reset
- Freeze the evaluator and production metadata at source commit `78536dc91ec9f7e9fa6727e7b488a9dfddcc3771`. Its clean candidate binary SHA-256 is `4ec36d16169bacfe1219d193c104c1328b0ac1aca654349435c52bd65b6e3d2a` with `vcs.modified=false`.
- The final telemetry and alerting chunks reached the Claude five-hour subscription session limit at 21:02 IST; the CLI reports a 22:30 IST reset. The harness correctly records 429 responses as invalid rather than model failures. Preserve the valid completed pairs and resume only missing cases after reset under the identical evaluator, corpus, baseline, and candidate digests.
- Keep PR #248 in draft and label the checked report as interim until all five strict category chunks and targeted repeats complete.

### 2026-07-19 — Guardrail sync and real-query grounding
- Main added the Tier 1 24 KiB serialized-schema guardrail in PR #249. The PR merge check exposed `signoz_create_dashboard` at 39,944 bytes and `signoz_update_dashboard` at 40,329 bytes because the dashboard batch put too much nested prose on the wire.
- Keep the guardrail unchanged. Compress self-explanatory and repeated dashboard field prose while retaining local meaning, units/defaults/enums, non-persistence warnings, and resource routing. Extended authoring detail remains in the required dashboard resources; JSON tags, types, requiredness, and runtime behavior do not change. The compact local schemas are 22,305 and 22,630 bytes respectively.
- This catalog change supersedes the preceding resume-only decision: partial results from candidate `78536dc` remain diagnostic but cannot be combined with a different candidate catalog hash. Freeze a new clean candidate and rerun every final category from the start after the Claude window resets.
- The user requested that the corpus reflect real requests. Sample bounded MCP tool telemetry through read-only SigNoz MCP calls, analyze it in memory, and add only anonymized intent shapes that expose a genuine coverage gap. Never commit verbatim live queries, tenant identifiers, credentials, or raw query results.

### 2026-07-19 — Provisional 50 KiB schema ceiling
- The user judged the 24 KiB serialized-schema ceiling too restrictive and approved raising it to 50 KiB for now. Restore the detailed dashboard field descriptions; the create/update schemas are 39,944 and 40,329 bytes and remain below the 51,200-byte ceiling.
- Treat 50 KiB as a reviewed ceiling rather than a writing target. Review and lower it once schema generation can deduplicate shared definitions (for example with `$defs`/`$ref`), or before approving growth above the current 40,329-byte high-water mark.
- This intentional guardrail relaxation supersedes the preceding compaction decision. No other wire-contract budget or guardrail changes.

### 2026-07-19 — Anonymized real-query corpus review
- Bounded read-only SigNoz MCP sampling found 43 distinct registered-tool/context pairs in 300 recent seven-day log rows and 40 distinct alert-tool/context pairs in 160 recent thirty-day log rows. No credential, tenant identifier, service name, resource ID, URL, or verbatim request was printed or persisted; the counts describe the bounded sample rather than tenant-wide invocation rates.
- Nine of twelve sanitized intent shapes were already materially covered. Add only three gaps: implicit p99 endpoint aggregation rather than raw trace search, metric discovery before label verification and rate querying, and alert-name resolution before history. The corpus now has 154 cases: telemetry 46, alerting 35, dashboards/templates 46, saved views 18, and docs 9.
- The evaluator enforces exact per-call `searchContext`, but its single-next-action design cannot prove that one original value survives a multi-turn workflow. The new workflow cases test the correct first preflight; a staged transcript evaluator would be required for cross-call continuity.
- Corpus provenance changed, so every earlier partial Sonnet run remains diagnostic. Freeze a new clean candidate and rerun all five categories from the start after the subscription reset.

### 2026-07-19 — Schema budget versus call payload size
- Confirm that `MaxSerializedSchemaBytes` measures the reusable input/output schema advertised for one tool, not the dashboard JSON arguments sent in a call. The former 24 KiB and provisional 50 KiB values therefore protect catalog metadata size rather than limiting dashboard imports.
- Runtime Streamable HTTP calls instead use the configurable `MCP_MAX_REQUEST_BYTES` whole-body limit, which defaults to 4 MiB and includes JSON-RPC envelope overhead; stdio does not use that middleware. A valid 300 KB dashboard can pass both size gates but may still fail client/proxy limits, semantic validation, configuration overrides, or the upstream SigNoz API.

### 2026-07-19 — Real-query oracle review
- Keep the observed “slowest endpoints” phrasing, but accept either `name` or `http.route` as the grouping field and accept `checkout` in either the `service` shortcut or an equivalent filter. This avoids scoring valid tool arguments as failures while preserving the p99, duration, time-window, scalar, resource, and aggregate-not-search requirements.
- The metric-discovery-first and alert-name-resolution-first workflow oracles match the registered prerequisites without relaxation.

### 2026-07-19 — Strict-repeat contract corrections
- Five integrity-valid screening chunks and fresh three-repetition follow-ups exposed one real metadata issue and two false eval assumptions. Treat this pass as diagnostic; its corpus and candidate hashes are superseded by the corrections below.
- `signoz_search_logs` selected the right shortcut arguments but unnecessarily read the logs guide in all three follow-ups. State that `searchText`, service, severity, time, and pagination need no guide; reserve the guide for composing a custom filter with unfamiliar fields.
- Correct the real-query “slowest checkout endpoints by p99” oracle to `signoz_get_service_top_operations`. The SigNoz backend's top-operations contract groups by operation name, calculates p50/p95/p99 plus call/error counts, and orders by p99 descending; generic trace aggregation is needed only for custom grouping, aggregation, time series, cross-service comparison, or filters.
- Correct the rename-only saved-view oracle to require no resource reread when a complete fetched body is already prepared. Keep both view resources required for composing a body or changing `sourcePage`/`compositeQuery`, and make the direct update case explicitly query-changing.
- These corpus and catalog changes require a new clean candidate, new provenance hashes, and a complete restart of all five screening chunks before any final comparison claim.

### 2026-07-19 — Frozen final comparison and stopping rule
- At the user's request, assess both model-quality and performance non-degradation without repeatedly tuning to the corpus. Freeze source `8a987cf439dd05b7acaad4a87b36174121739172`, candidate binary `09e6bdf6a74745687063d6e4647192c600961999c33c4f9d6a6c16eb6b5b59c6`, corpus `db57d7f954b35764d1fa1cd277325695c98d339cda7aae07698e42482524fadd`, and evaluator `a4925365a72abffd4417a1458e4c805a81c9afad7d4d543599348ee621143d04`; do not revise or rerun after the one screen and one three-repetition confirmation set.
- Five integrity-valid category screens cover all 154 cases and improve fully correct outcomes from 135 to 142. Twenty-four repeated delta/failure/control cases improve raw majority-full outcomes from 12 to 20; after excluding one contradictory dashboard oracle, the comparison is 11 to 20 across 23 valid cases.
- Strict case-level non-degradation is not established. `signoz_search_traces` direct and indirect cases retain correct tool/argument/content/safety behavior but unnecessarily read `signoz://traces/query-builder-guide` more often than baseline. Record this residual bug in [PR comment 5016945324](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5016945324) instead of entering another wording/eval loop.
- Record the shared ambiguous alert-delete behavior in [PR comment 5016967157](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5016967157) and the contradictory prepared-dashboard-body oracle in [PR comment 5016973360](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5016973360). Earlier discovered and fixed issues are summarized in [PR comment 5016861127](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5016861127).
- Performance is mixed: the full screen improves candidate mean/median/p95/p99 elapsed time and ToolSearch protocol retries fall 56.5%, while Sonnet output tokens rise 2.1%, cache-creation input rises 24.5%, and Claude's subscription-equivalent usage estimate rises 18.3%. Failure-enriched repeats improve aggregate mean/median but worsen p95 by 0.6%, p99 by 18.8%, maximum by 3.5%, and telemetry mean by 6.5%, so strict every-slice latency non-degradation is not established. Tool descriptions grow 9.9%, the compact tool catalog 24.7%, summed advertised schemas 28.0%, the largest schema 73.1%, and resource bodies 1.5%. PR-specific production changes do not alter SigNoz tool-handler execution logic, but the candidate binary also contains `main`'s separately merged serialized-result-size measurement, so baseline-to-candidate handler runtime is confounded and was not benchmarked.
- The complete catalog comparison snapshot SHA-256 is `5ac1b0427cfbccd7ad87a67ccbaaa2b4aa3490697596f11e0073083e19bd41d0`. Individual accepted records pin selection-specific baseline/candidate catalog hashes, which vary with the included resource subset and must not be presented as universal run hashes.
- Two artifacts were discarded in full and rerun after one non-allow-listed-tool invocation in each. The accepted 452 records are fully paired and contain no SigNoz invocation, 429, or non-zero exit.
- The companion `SigNoz/agent-skills` checkout needs no update: it already teaches conditional replacement-resource reads and the corrected logs/top-operations boundaries, and no tool name, parameter, payload, or URI contract changed.
- Keep PR #248 draft. The compact final report is `evals/mcp-catalog/sonnet-5-results-2026-07-19.md`; it explicitly avoids a blanket no-degradation claim.

### 2026-07-20 — Isolated first-invocation follow-up
- The initial Haiku pilot exposed a harness limitation: after real `ToolSearch`, Haiku naturally invoked a discovered SigNoz tool, while evaluator v2 required it to stop and self-report a planned call. All 48 pilot records are diagnostic only and provide no old/new comparison.
- The user explicitly approved SigNoz tool invocation. Keep the evaluation isolated from the Noz system prompt and skills, and score the first observed SigNoz tool call and its arguments rather than the model's reported plan.
- Preserve safety and reproducibility by snapshotting each real initialized catalog into a static local MCP fixture. Evaluated tool calls return a deterministic success without starting the SigNoz binary or forwarding any request; the separate upstream HTTP fixture remains only for snapshotting sanitized dynamic resources. No staging or production tenant is reachable.
- Run one bounded 24-case Haiku old/new pilot using the previously selected case IDs. If record integrity is valid, use the result only to decide whether a full isolated Haiku comparison is warranted; do not tune catalog wording from the pilot or enter another rerun loop.

### 2026-07-20 — Haiku stopping decision
- Evaluator v3 snapshots each frozen initialized catalog and serves it through a static local MCP no-op. A one-case paired smoke passed integrity and proved the observed call/arguments path without forwarding to SigNoz.
- The fixed 24-case pilot completed all 48 records but failed integrity: 13 missing SigNoz calls (baseline 4, candidate 9) and one candidate double-call. Five missing-call records reported a selected tool and arguments without executing it; eight instead chose clarification.
- Treat every pilot quality total as non-comparable because invalid records score all dimensions false and the missingness is asymmetric. Do not run the full Haiku suite and do not tune metadata from this result.
- Record the one concrete candidate workflow bug in [PR comment 5017376479](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5017376479): after the prompt stated that alert-rule resolution was already complete and deletion confirmed, Haiku still called `signoz_list_alert_rules` before `signoz_delete_alert`.

### 2026-07-20 — Tool-by-tool optimization strategy
- The owner is not satisfied with the catalog-wide aggregate improvement and explicitly chose tool-by-tool optimization for faster, attributable gains.
- Use `claude-sonnet-5` at medium effort as the primary optimizer/evaluator. Evaluate one registered tool together with its closest competing tools, development prompts, and untouched holdout prompts; keep a change only when the tool improves without a neighbor, critical, safety, resource, or execution regression.
- Add `gpt-5.6-luna` at medium reasoning as a secondary client gate after a tool has a clear Sonnet improvement. Use Codex CLI's real MCP client with user config ignored, read-only sandboxing, and only the static local no-op MCP fixture enabled. Fast mode remains off.
- Replace the forced exactly-one-call protocol with natural first-action measurement. Missing calls, clarification, planned-only output, and extra preflights are behavioral outcomes rather than invalid records; malformed event/provenance/model/tool-discovery evidence remains invalid.
- Start with the two evidenced weak spots: `signoz_search_traces` (unnecessary query-guide reads for shortcut searches) and `signoz_delete_alert` (redundant rule discovery after an already-resolved, confirmed deletion). Then proceed by observed usage and category in logical commit batches.
- Avoid corpus overfitting: use development cases while editing, open holdout cases only for the keep/revert gate, do not rewrite a holdout oracle after seeing a miss, and do not rerun a failed holdout with prompt wording tweaks.

### 2026-07-20 — Multi-model automatic optimization research
- The owner asked to explore a bounded automatic research/optimization loop and then made `gpt-5.6-luna` medium co-primary with Sonnet 5 medium rather than a secondary transfer check.
- Evaluate every metadata candidate independently through each client's native discovery path: Claude ToolSearch for Sonnet and Codex code-mode MCP discovery for Luna. Normalize only downstream behavior (ordered SigNoz calls, first-call arguments, resources, clarification, extra preflights, safety, latency, and context size); do not require the two clients to expose identical discovery events.
- Candidate selection is multi-objective. Hard safety, negative-neighbor, execution, resource, and call-discipline gates must not regress for either model. Among survivors, prefer Pareto improvements across both models and catalog size/latency; do not trade a clear regression in one model for an aggregate gain in the other.
- Bound each tool's research loop: sealed development/regression sets, at most three generated wording candidates per generation, successive-halving evaluation, at most two generations, and one sealed holdout opening for the final incumbent/challenger decision. Never let the optimizer modify cases, graders, schemas, annotations, runtime behavior, or tool names.

### 2026-07-20 — Production-shaped prompt sampling
- A single read-only, 20-row sample from the `signoz-ai-assistant` log stream confirmed that realistic requests frequently arrive as terse follow-ups with active dashboard context, completed discovery, prior tool results, or mutation-error recovery. These interaction shapes should complement direct/indirect/negative one-shot prompts in the evaluator.
- Retain only anonymized intent patterns. Do not copy prompt text, customer/service names, object IDs, telemetry values, or conversation content into the repository or external eval corpus.
- The bounded request still scanned roughly 3.1 GB because the prompt-bearing log attribute is not a selective indexed filter. Do not repeat broad prompt scans for this optimization; the marginal eval benefit does not justify the scan cost.

### 2026-07-20 — Holdout calibration
- The historical `holdout` partition has already been exercised during the catalog-wide comparison, so it is now a regression partition rather than evidence of unseen generalization.
- Cases written directly from the observed trace-resource and alert-preflight failures belong in `development`, even when phrased as follow-ups. Freeze candidate wording first, then author and hash a fresh final prompt set and open it once; if that gate fails, reject the candidate instead of tuning against it.

### 2026-07-20 — Parallel Luna/Sonnet optimization contract
- The owner explicitly confirmed that `gpt-5.6-luna` medium must be optimized in parallel with `claude-sonnet-5` medium, not used as a secondary transfer check. Run each surviving candidate through both native clients at the same evaluation stage and reject a gain that causes a case-level regression in either client.
- Prevent aggregate wins from hiding local damage: gate every selected case on selection, execution, minimal call path, resource use, and safety, plus arguments/content when the case declares those oracles. Accept equivalent valid argument shapes with recursive `any_of` assertions rather than optimizing for one client's preferred JSON spelling.
- Treat performance dimensions according to their evidence quality. Exact catalog bytes and discrete extra calls are deterministic; tokens and latency are paired within a client, and a one-pass or statistically inconclusive result must not be called either improvement or non-degradation. Cross-client token and latency values are not directly comparable.
- A clean identical-binary audit found that the dynamic alert-summary resource differed only in generated `asOf`/`historyWindow` timestamps. Normalize those fixture-only volatile fields and start snapshot binaries from an allowlisted environment before spending further model runs, so an A/B difference cannot come from clock skew or ambient SigNoz/OTel/proxy settings.

### 2026-07-20 — Natural dual-client smoke and first failure card
- Harden both adapters before optimization: require exact client/discovery identity, deterministic within-case A/B ordering, strict per-case quality gates, actual ordered MCP resource reads, isolated subprocess environments, deterministic dynamic resource bodies, and paired performance reporting. A final-confirmation run requires adequate repeated evidence; a one-pass screen remains explicitly inconclusive for latency and tokens.
- The first plumbing smoke was invalid as model-quality evidence. Claude's schema-backed terminal `StructuredOutput` ended both turns after selection but before the reported SigNoz call, while Codex failed before inference because its strict report schema contained an open nested object; the shared Codex model-cache warning was separately evidenced as nonfatal. Remove the competing terminal tool from Sonnet action cases, use an action-after-call JSON report, give Luna a fully closed report schema, isolate an auth-only temporary `CODEX_HOME`, and preserve sanitized client errors.
- The corrected one-case smoke reached the local fixture on both clients and both catalogs. Sonnet made one correct `signoz_search_traces` call with exact arguments and no resource read for both variants. Luna made the same correct call but both variants read `signoz://traces/query-builder-guide` unnecessarily before it. This is the first valid `signoz_search_traces` development failure card; it shows no current Luna improvement and no Sonnet regression. One-pass performance values remain descriptive only.
- No live SigNoz request was possible in either smoke. Only repository metadata, sanitized static fixtures, and synthetic/anonymized eval prompts were disclosed; model/session artifacts remain outside git.

### 2026-07-20 — Client-event compatibility fixes
- Candidate screening exposed two client-version identities that the evaluator had incorrectly rejected. Claude Code accepts `ReadMcpResource` on its CLI but reports the actual invocation as `ReadMcpResourceTool`; accept both names only as aliases of the same read-only control tool. Codex reports its read-only catalog helper as `mcp_tool_call(server="codex", tool="list_mcp_resources")`; allow only the three known read-only MCP catalog/resource helpers and continue rejecting every unknown Codex server tool.
- Preserve bounded, argument-free Codex tool-event summaries so future protocol failures identify item type, server, tool, and status without retaining prompts or inputs. Preserve at most 2,000 characters of Claude's failed final report text; malformed final reports remain integrity failures rather than being repaired or scored optimistically.
- The evaluator now passes 129 unit tests and validates 161 corpus cases. These compatibility fixes change record validity only; the static fixture journal remains authoritative for SigNoz calls and ordered resource reads.

### 2026-07-20 — Trace oracle correction
- The critical custom-filter prompt said “checkout spans” while its oracle required `service=checkout` or an equivalent `service.name` filter. The server contract also supports the defensible interpretation `operation=checkout`, and Luna chose that same shape for both incumbent and candidate. Treat this as an evaluator bug, not a metadata failure.
- Make all service-intended raw-trace prompts explicit (“checkout service” / “payment-api service”) and add a separate critical `POST /checkout` operation-shortcut case. The corrected matrix now tests both real code paths: `service` adds `service.name = '<value>'`, while `operation` adds `name = '<value>'`.
- Oracle corrections are evaluator-owner changes applied equally to both variants. They require restarting the affected development screen and do not authorize candidate generation to rewrite cases or a final holdout after seeing a miss.

### 2026-07-20 — Bounded `signoz_search_traces` optimization result
- Generation 1 screened three description-only candidates on Sonnet 5 medium and Luna 5.6 medium. The strongest explicit shortcut exception made the corrected target matrix perfect on both clients: Sonnet improved from 2/3 to 3/3 and Luna from 1/3 to 3/3, with exact guide reads and calls. One pass remained insufficient for token/latency claims.
- Nearest-neighbor screening rejected that candidate. In three-repeat evidence, Sonnet missed the required aggregate-traces guide and stopped after planning the built-in top-operations call; Luna chose generic aggregation instead of the built-in top-operations tool in 2/3 candidate runs and showed a clear material token regression on that case. These failures violate the per-client, per-case no-regression gate even though the target tool improved.
- Generation 2 tested two causal alternatives. Localizing the rule to the `search_traces.filter` parameter did not stop Luna's shortcut guide reads and caused a new unnecessary guide read on the top-operations case. Retaining the successful search wording while strengthening aggregate ownership still failed Luna's top-operations execution gate. The catalog deltas were +38 and +31 bytes respectively; neither reached final confirmation.
- Stop after the predeclared two generations. Keep the production `signoz_search_traces` and `signoz_aggregate_traces` metadata unchanged, do not open a fresh holdout, and retain the existing residual-bug comment rather than overfit another wording variant. Continue tool-by-tool optimization with `signoz_delete_alert`.

### 2026-07-20 — Bounded `signoz_delete_alert` optimization result
- The failure card separates target identity from deletion authorization. A known alert ID does not authorize deletion; when the latest instruction withholds confirmation, the correct behavior is to ask and make no SigNoz call. Conversely, an already resolved and confirmed target should be deleted directly without list/get preflight.
- Generation 1 tested the delete description, shared mutation instructions, and the neighboring `signoz_get_alert` negative boundary independently. Only the get boundary prevented the redundant Sonnet read, but Sonnet still reported `get_alert` as a planned action; Luna passed all four target cases. That candidate therefore failed the absolute Sonnet selection gate.
- Generation 2 tested a combined boundary and a smaller delete-only boundary. Both made the four-case development screen 4/4 on Sonnet and Luna; the smaller survivor added 87 tool-list bytes instead of 317. Neighbor adjudication rejected it: Luna made the indirect confirmed delete directly in 3/3 runs, while Sonnet did so in only 1/3 and continued to repeat or plan rule discovery. The final combined state-machine/get-boundary candidate was 8/8 on Luna but again missed Sonnet's indirect delete and one missing-ID workflow.
- Stop at the predeclared two-generation limit. Keep the production `signoz_delete_alert` and `signoz_get_alert` metadata unchanged, open no holdout, and make no token, latency, performance, or non-degradation claim. One-pass numbers were inconsistent and repeated behavior runs used parallel workers, so latency was deliberately ungateable.
- The neighbor screen separately exposed that `signoz_update_alert` metadata unconditionally asks for get/channel preflights even when the prompt states those steps are complete. Both clients repeatedly re-read, clarified, or stopped before the update. Track that as its own tool optimization rather than changing it inside the delete experiment.
- Sonnet fenced some otherwise valid reports or omitted observable ToolSearch evidence; Luna also produced timeouts and Codex-JSONL/fixture-journal disagreement. The evaluator rejected those records rather than repairing or treating them as model-quality evidence. The findings and no-change decision are recorded in [PR comment 5018201207](https://github.com/SigNoz/signoz-mcp-server/pull/248#issuecomment-5018201207).

### 2026-07-20 — Frozen `signoz_search_logs` boundary matrix
- Verify the current search/log-guide wording against baseline commit `8b3c298` before generating candidates. Treat Sonnet 5 medium and Luna 5.6 medium as equal first-stage gates.
- Add two development cases before opening model results: a production-shaped shortcut follow-up using only service, severity, body text, and time must not read `signoz://logs/query-builder-guide`; a custom workspace-field negative filter must read the guide and use the documented `EXISTS` plus `!=`/`NOT IN` semantics.
- Screen these with the existing raw-vs-aggregate neighbors and logs-guide discovery/content cases. Do not edit the corpus, assertions, or grader in response to the resulting model failures unless an independently evidenced contract/oracle bug applies equally to both catalogs.

### 2026-07-20 — `signoz_search_logs` guide-oracle correction
- The first dual-client screen independently disproved the custom-filter resource oracle before any candidate model call. The prompt says field discovery already confirmed `deployment.environment`; current metadata limits guide reads to unfamiliar fields; and shared server instructions—not the logs guide—state the `EXISTS` plus negative-operator rule. Requiring the guide would reward an unnecessary read and invite metadata overfitting.
- Correct the known-field custom-filter case to require no guide for both catalogs. Add the complementary missing-field workflow: when `customer.tier` has not been checked in this workspace, the next action is `signoz_get_field_keys`, not `signoz_search_logs` or a guide read. Restart every affected comparison from fresh sessions.
- Discard the unexecuted candidate that would have mandated a guide for every non-empty filter. It was built locally but never sent to either model and makes no production or result claim.

### 2026-07-20 — `signoz_search_logs` corrected screen and bounded candidate hypothesis
- The corrected 13-case screen was integrity-valid on Luna but not Sonnet. Luna improved from 7/13 fully correct on baseline to 11/13 on current metadata; Sonnet's valid records improved from 7 to 8, but three malformed or discovery-free records invalidate its aggregate comparison. Neither run supports a performance claim.
- Three-repeat confirmation was also client-noisy: Sonnet produced eight invalid records and Luna produced eight timeout/reconnect records. Valid traces nevertheless reproduce one causal issue: Luna read `signoz://logs/query-builder-guide` for the known-field custom filter in every current run, while the tool description says that guide is only for unfamiliar fields. The shared filter parameter still says `see signoz://logs/query-builder-guide` without that qualifier; test this conflict with at most two search-only parameter variants and do not change the shared aggregate-log contract.
- Trace inspection found an independent direct-case ambiguity: `checkout ERROR log rows` allowed `checkout` to be interpreted as body text. Make the prompt explicit about the checkout service and assert either the `service` shortcut or an equivalent `service.name` filter for both catalogs. Restart that affected case before accepting any candidate.
- Current metadata remains the incumbent. Sonnet's first unknown-field duplicate did not reproduce in three later current runs, while Luna's known-field guide overread did; do not optimize the former or treat protocol-invalid records as behavioral failures.

### 2026-07-20 — Bounded `signoz_search_logs` candidate result
- Candidate C1 qualified the search-only filter pointer: known fields may be composed directly, while unfamiliar fields still point to the guide after discovery. It changed exactly one schema description and reduced the initialized tool-list payload by 168 bytes. Sonnet's six search/discovery target cases passed, but Luna was exactly unchanged from the incumbent: both variants still read the guide for the known-field filter and over-continued the unknown-field workflow.
- Candidate C2 removed the search-filter parameter's guide pointer while retaining the tool-level unfamiliar-field rule. It changed exactly one schema description and reduced the tool-list payload by 199 bytes. It did not stop Luna's known-field guide read, regressed Luna's unknown-field minimality/safety, and produced an invalid Sonnet custom-filter report plus a planned-only unknown-field turn.
- Stop after the two predeclared variants. Keep `signoz_search_logs` production metadata unchanged, open no holdout, and make no token, latency, performance, or non-degradation claim. C1's Sonnet run had one malformed incumbent record; C2's Sonnet run had one malformed candidate record; both Luna screens were integrity-valid.
- Treat the persistent Luna guide read as a residual client/metadata interaction, not a reason to broaden the resource requirement. Treat repeated Sonnet aggregate-log guide omissions as the next tool's failure card; aggregate behavior was unchanged by both search-only candidates.

### 2026-07-20 — `signoz_aggregate_logs` guide-oracle correction
- Handler and resource audits supersede the 2026-07-19 conclusion that every grouped-log aggregate requires `signoz://logs/query-builder-guide`. The high-level tool validates its typed aggregation/grouping/filter/time arguments and authors the full Query Builder envelope; the resource metadata scopes the 10,263-byte guide to raw Query Builder JSON or unfamiliar log fields. The three existing aggregate prompts use only the high-level contract.
- Native traces confirm the mismatch rather than a selection problem. Across the corrected and C1/C2 screens, Sonnet selected/called aggregate logs in every valid aggregate record and skipped the guide; Luna selected/called it and read the guide every time. Rewarding Luna's deterministic extra read would violate the metadata-placement and non-degradation objectives.
- Correct the three known-field aggregate cases to require no resource, accept either the severity shortcut or an equivalent `severity_text` filter, and strengthen their aggregation/grouping/time assertions. Add a field-free scalar-count control and an unknown custom-resource-field workflow whose next action is `signoz_get_field_keys`, not a guide read or aggregate call.
- Strengthen the existing unknown log-field workflow with `fieldContext=resource`; its prompt already requires that context. This late oracle fix only strengthens the rejection of search-log C2 and does not reopen the stopped search loop.
- Accept both `24h` and `1d` for the explicit one-day window. The scorer compares relative strings literally even though both represent 86,400,000 ms; saved Sonnet calls use `1d` while Luna uses `24h`.
- Before opening aggregate results, add one confirmed numeric-field p95 case covering the required `aggregateOn` branch, scalar grouping, descending order/default, and a six-hour window. Also assert `count() desc` or the documented absent/default order on the ranked count case. The prior matrix covered only count/rate.

### 2026-07-20 — `signoz_aggregate_logs` baseline adjudication
- The first corrected current-versus-original screen remains diagnostic only: Sonnet emitted three fenced final reports and Luna had one reconnect timeout, so neither aggregate comparison is integrity-valid. Valid traces still reproduce the causal target: Luna reads the 10,263-byte logs guide before every known-field aggregate, while Sonnet calls the high-level tool without that resource.
- Independently audit the baseline before sending a candidate. Strengthen the field-free scalar oracle to forbid grouping, require the ERROR constraint and every explicitly excluded trace/builder neighbor on the raw-log negative, and make the raw-builder formula prompt executable by specifying its one-hour window, scalar result, and A/B meanings. These are contract corrections applied equally to incumbent and challenger, not model-driven candidate edits.
- Preserve genuine failures instead of weakening the tests: Sonnet shortened `searchContext` in the confirmed-field percentile case; Luna omitted the requested `searchText` during unknown-field discovery and continued into aggregation without a confirming result. The aggregate candidate must pass those critical workflows as written on both co-primary clients.
- Screen a description-only first candidate that replaces the unconditional guide instruction with direct/no-resource behavior for schema-documented or confirmed fields and discovery-first behavior for unfamiliar filter, `groupBy`, or `aggregateOn` keys. Keep the candidate binary outside git until both clients pass the frozen screen.

### 2026-07-20 — Bounded `signoz_aggregate_logs` candidate result
- Candidate C1 replaced the universal guide instruction with direct calls for documented/confirmed fields and conditional key discovery. It added 92 tool-list bytes. Luna's exact resource behavior improved from 3/9 to 7/9, but it retained two unnecessary guide reads, preflighted a normal grouped count with `signoz_get_field_keys`, and omitted the exact key in the unknown-field lookup. Sonnet planned rather than invoked the unknown-field lookup and used the wrong field context. Reject C1 on both clients' critical gates.
- Candidate C2 made shortcut/grouping behavior and exact-key discovery operational. It added 385 tool-list bytes. Luna was 9/9 fully correct versus the incumbent's 3/9, with all six unnecessary guide reads removed and no neighbor regression. Sonnet's observable MCP calls and reads were correct on all nine cases, but the candidate emitted a fenced final report for the raw-builder control in the full screen and again in an isolated retry; the strict evaluator rejected both records. Do not waive the twice-reproduced downstream-format failure.
- Candidate C3 compressed C2 to a 239-byte increase. Sonnet made an extra aggregate call on the critical field-free case and again produced malformed reports, so stop immediately under successive halving. Its Luna run was intentionally interrupted after 11/18 records because the candidate could no longer pass the co-primary gate; the interrupted runner also surfaced a temporary-directory cleanup error.
- Stop after three bounded description-only candidates. Retain the production `signoz_aggregate_logs` metadata, open no holdout, and make no token, latency, performance, or non-degradation claim. All screens used one repetition, so their paired performance summaries are inconclusive even where C2's Luna behavior and resource use improved sharply.

### 2026-07-20 — Luna 5.6 remains a co-primary optimizer
- Optimize for `gpt-5.6-luna` medium in parallel with `claude-sonnet-5` medium at every candidate stage, rather than using Luna only as a secondary confirmation client.
- Run both clients over the same frozen case definitions, binaries, repetitions, and stage. A candidate is acceptable only when it is Pareto-safe: no case-level hard-gate regression on either client. A gain on one client cannot compensate for a regression on the other.
- The external screens use the static no-op MCP fixture so client behavior is attributable to catalog metadata. Staging availability does not relax wrong-tool or destructive-boundary checks, but it removes live-environment risk as a blocker for later delegated verification.

### 2026-07-20 — Frozen executable `signoz_update_alert` matrix
- Replace the two prior update cases before candidate generation. They claimed a complete replacement had been prepared without supplying it and asserted only the ID, which could reward a fabricated destructive replacement.
- Freeze seven critical states: complete direct replacement, complete one-field-preserving edit, structural PromQL rewrite, missing ID, missing current definition, unverified new channel, and a corrected retry after channel validation returned choices. Ready-to-update prompts now contain a complete writable rule and deeply assert preserved fields; prerequisite prompts require exactly one next action.
- Correct the structural PromQL oracle before external runs. Histogram p99 uses the documented `http.server.duration.bucket` selector, not the base metric. The prompt now states metric/data, `le`, label, and unit preflights are complete; the scorer checks the p99/5m selector, checkout filtering and grouping, millisecond unit, full threshold block, preserved alert fields, exact resources, and forbidden repeated discovery.
- The corpus now validates all 172 cases; 129 Python evaluator tests and the initialized-catalog coverage test pass. First run the unchanged incumbent on this frozen matrix, then test at most two description-only candidates under the existing two-generation stopping rule.

### 2026-07-20 — Reduce the committed eval surface to deterministic contracts
- The user reviewed the PR-level evaluation footprint and concluded that it is too large and that many cases do not add decision value. The committed suite had grown to roughly 10,000 Python/JSON/Markdown lines, 172 prompts, and 129 harness tests; one old/new pass across both clients implied 688 external model sessions before repetitions.
- Remove the entire committed model-evaluation package, generated comparison report, evaluator fixtures, corpus-coverage coupling test, and evaluator-only CI job. Preserve the historical experiment findings in PR discussion rather than as production-repository machinery.
- Remove per-family literal-description inventories and other prose assertions that duplicate review or merely freeze wording. Keep only compact deterministic checks for wire/schema budgets, manifest parity, resource-pointer resolution and readability, schema compatibility, and resource-template runtime behavior changed by this PR.
- Update the PR summary so it no longer claims that the repository ships or validates the external-model harness. External Sonnet/Luna observations remain qualitative design evidence and do not support a deterministic performance or non-degradation claim.

### 2026-07-20 — Minimal eval cleanup completed
- Removed 11,392 lines from the branch tip, including all 10,029 lines under `evals/mcp-catalog`, five prose-inventory/coverage test files, overlapping assertions, and the evaluator-only CI job.
- Kept the catalog-wide static-resource contract and four changed resource-template behavior tests. Repository-wide tests, vet, build, manifest parsing, and diff checks pass.

### 2026-07-20 — Remove the serialized-schema byte ceiling
- The user asked to remove the schema-size guardrail rather than retain the provisional 50 KiB ceiling. Remove `MaxSerializedSchemaBytes` and its enforcement while keeping description, name, property-inventory, and nesting constraints.
- Serialized MCP schemas and dashboard JSON passed as runtime tool arguments are separate contracts. This change does not alter the configurable streamable-HTTP request-body limit (`MCP_MAX_REQUEST_BYTES`, 4 MiB by default) or stdio behavior.

### 2026-07-20 — Consolidate PR experiment comments
- Replace the eleven superseded top-level model-evaluation and performance updates on PR #248 with one concise current-state note. Preserve reviewer threads and unrelated discussion.
- The consolidated note records that the experiments were diagnostic, unsuccessful candidates were not applied, model/performance totals are not decision-grade, the committed evaluator was removed, and deterministic CI is green.

### 2026-07-20 — Restore concise improvement evidence in the consolidated comment
- The user noted that the first consolidated note removed all numbers and concrete improvements. Revise that same comment to include the 41-tool/19-resource/2-template production scope and the earlier frozen 154-case Sonnet deltas.
- Label those model numbers as historical diagnostic evidence because later branch changes were not re-benchmarked, retain the catalog-growth and tail-latency caveats, and avoid a blanket quality or performance claim.

### 2026-07-20 — Final single-comment record
- The user asked to delete the remaining superseded top-level comment and replace it with one final comment that also records all material issues and other conclusions.
- Keep the final record concise but complete: shipped improvements, historical diagnostic numbers, fixed oracle/metadata defects, unresolved model/client behaviors, evaluator limitations, removed eval footprint, final evidence boundary, and current CI state. Reviewer threads remain untouched.

### 2026-07-20 — Plain-language metadata cleanup
- The user approved simplifying the alert-examples resource description and asked for the same cleanup across similar model-facing text.
- Replace abstract phrases such as "adaptable canonical payload shapes" with direct terms such as "alert payload examples." Remove obvious statements such as "must be 0 or greater" when the field meaning already makes the constraint clear; keep non-obvious units, defaults, maximums, and special zero semantics.

### 2026-07-20 — Plain-language cleanup completed locally
- Simplified model-facing descriptions and resource introductions across alerts, dashboards, Query Builder, logs, metrics, notification channels, services, and saved views; synchronized `README.md` and `manifest.json`.
- Removed redundant sign guidance from pagination, layout, histogram bucket, and page-size fields. Kept positive-limit guidance where zero deliberately selects server defaults, as well as units, caps, formula limits, ordering, and other behavioral caveats.
- Focused tests, contract guardrails, the full Go test suite, `go vet`, JSON validation, formatting, and diff checks pass. `actionlint` was unavailable in the local environment. No commit or push was made pending user approval.

### 2026-07-20 — Field-discovery review feedback
- The user selected three unresolved review threads: remove traced-service inventory caveats from the generic field-key/value tool descriptions, and clarify how to discover log severity values.
- Keep the trace-activity versus log-value boundary on `signoz_list_services`, where it is relevant. For log severity, `signoz_get_field_keys` can confirm the field name, while `signoz_get_field_values(signal="logs", name="severity_text", fieldContext="log")` returns observed values.

### 2026-07-20 — Field-discovery feedback addressed locally
- Removed the service-inventory caveat from both generic field tool descriptions and the matching README section; the boundary remains documented on `signoz_list_services`.
- Updated both log severity parameters and README entries to route observed-value discovery through `signoz_get_field_values` with the built-in log field context.
- Focused tests, contract guardrails, the full Go suite, `go vet`, JSON validation, formatting, and diff checks pass. Review threads were not replied to or resolved, and no commit or push was made.

### 2026-07-20 — Live severity-field verification
- A read-only call to `signoz_get_field_keys(signal="logs", searchText="severity")` returned `severity_text` with `fieldContext="log"` and `fieldDataType="string"`.
- `signoz_get_field_values(signal="logs", name="severity_text", fieldContext="log")` returned the observed values DEBUG, ERROR, FATAL, INFO, Normal, WARN, and Warning. The control call with `fieldContext="attribute"` returned no values. No resources were created and no credentials were printed or persisted.

### 2026-07-20 — Single-commit PR history requested
- The user explicitly approved committing the local changes and asked to squash every PR commit into one commit, then force-push the branch.
- Preserve a local backup ref before rewriting and use `--force-with-lease` for the push.
- All local metadata, review-feedback, documentation, plan, and live-verification updates passed the focused, guardrail, full-suite, vet, JSON, formatting, and diff checks before the history rewrite.

### 2026-07-20 — Live severity-value verification
- A read-only staging MCP check confirmed that `signoz_get_field_keys(signal="logs", searchText="severity")` returns `severity_text` with `fieldContext="log"` and `fieldDataType="string"`.
- `signoz_get_field_values(signal="logs", name="severity_text", fieldContext="log")` returned the observed values `DEBUG`, `ERROR`, `FATAL`, `INFO`, `Normal`, `WARN`, and `Warning`. A control call with `fieldContext="attribute"` returned no values.
- No resources were created or modified, and no credentials were printed or persisted.
