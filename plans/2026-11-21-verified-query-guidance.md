# Plan: Verified query guidance and measurement notes

Status: Done
Issue: SigNoz/nerve-pod#667, SigNoz/nerve-pod#482
PR: https://github.com/SigNoz/signoz-mcp-server/pull/475

## Context

Models rarely read `signoz://` guides, so a rule taught only there is not taught (SigNoz/nerve-pod#667).
In controlled Claude Code sessions no scored session completed a resource read. The costly rules
fail silently: the query succeeds and returns a plausible wrong number. Nothing checked that the
guidance sent to clients is still true (SigNoz/nerve-pod#482). The goal is that the model gets the
right number, or is told what the number it got measures.

Decisions made with the maintainer before work began:

1. One PR for the audit, the result notes, and the text changes.
2. Result notes for silent traps are shown to models from this release, and each one is counted.
3. Server instructions carry tool routing in their first 512 bytes, and
   `MaxServerInstructionsBytes` goes from 2,048 to 4,096. No other budget changes.
4. Rules move onto the parameter or tool they apply to, each verified against SigNoz first;
   "read signoz://..." imperatives give way to the rule, and a pointer may stay as further reading.
5. Out of scope: resource-URI span attributes, `anthropic/alwaysLoad`, new tools or parameters,
   any guide-read gate (SUR-10), a guide-delivery tool (SigNoz/nerve-pod#157), and skill changes
   (SigNoz/nerve-pod#436).

## Approach

- Audit every claim on the query path against SigNoz v0.151.0 (`9d5c5871cb`) and the current
  handlers, and record what happens when a model breaks it.
- For each silent trap the server can detect from the request or metadata it already fetches, add
  a narrow note in one place, `internal/handler/tools/measurement_notes.go`, counted in
  `mcp.tool.result.measurement_notes` by tool and rule. A note states what was measured, stays true
  when the caller meant the query as written, and names a fix only when an e2e test pins it.
- Put tool routing in the instructions preamble, in users' words, and lead tool descriptions with
  the words a user would say. Rules go on the parameter or tool they apply to.
- Check each behavioural claim with the EVL-1 before/after sessions.

## Audit

Failure modes: **W** wrong result with no error; **EF** an error that states the fix; **EN** an
error that does not; **C** cosmetic. Sources are SigNoz paths at `9d5c5871cb` or this repo's tests.

### Server instructions and routing

| Claim | Status | Checked against | Mode | Was taught | Now |
|---|---|---|---|---|---|
| Which tool answers which question | missing | handlers; eval tool choice | W | nowhere | instructions preamble and routing list |
| A service's requests are its entry spans | missing in instructions | `pkg/modules/services/implservices/module.go` scope `isRoot = true OR isEntryPoint = true`; e2e `test_entry_spans_count_each_request_once` | W | `aggregations` parameter only | instructions, tool descriptions, traces guide, note |
| Negative operators match missing fields | accurate | `querybuildertypesv5/builder_elements.go` `AddDefaultExistsFilter` only for positive operators | W | instructions rule 4 | unchanged (text only, see traps) |
| Operators by type; bool takes = and != | accurate | `pkg/parser/filterquery/sqlcompiler/compiler.go` `BuildBoolComparison` | EF | rule 3 | unchanged |
| On key not found, discover keys | accurate | e2e `test_missing_filter_key_returns_no_rows_with_the_taught_warning` | W (no rows plus warning) | rule 2 | unchanged |
| start/end take RFC 3339; bodies take Unix ms | accurate | SigNoz/signoz-mcp-server#467 tests | EF | rule 5 | unchanged |
| Per-step reduction of percentiles and rates | missing | `pkg/querier/postprocess.go` `applyMetricReduceTo` | W | metrics guide only | rule 8, parameters, note |
| Read result notes before reporting | missing | eval transcripts | W | nowhere | rule 9 |

### Query tools

| Surface | Claim | Status | Checked against | Mode | Now |
|---|---|---|---|---|---|
| `signoz_aggregate_traces` | "Before calling, read signoz://traces/query-builder-guide" | imperative only | decision 4 | W | replaced by the entry-span rule; pointer kept as reference |
| `signoz_aggregate_traces` `aggregations` | entry spans count each request once | accurate | e2e (SigNoz/signoz-mcp-server#474) | W | unchanged |
| `signoz_query_metrics` | routing for a service's request rate | missing | eval of SigNoz/signoz-mcp-server#474 (Sonnet took the rate from `signoz_calls_total`) | W | description names `signoz_list_services` and the entry filter |
| `signoz_query_metrics` `reduceTo` | "avg for a typical value" | misleading for percentiles | `postprocess.go` per-step reduction; new e2e | W | rewritten; same-tool bucket-count route |
| `signoz_query_metrics` `reduceTo` | summing rates is not a count | accurate | new e2e `test_counter_reducers_say_what_they_measured` | W | unchanged, plus note |
| `signoz_query_metrics` `timeAggregation` | running total on cumulative counters | missing | `statement_builder.go` `buildTemporalAggCumulativeOrUnspecified`; e2e `test_counter_max_reads_the_running_total` and new latest/count checks | W | added |
| `signoz_query_metrics` `stepInterval` | "Step in seconds for time_series" | stale (scalar reduces steps too); alignment missing | `pkg/querybuilder/time.go` `AdjustedMetricTimeRange`; new e2e step checks | W | rewritten |
| `signoz_query_metrics` `metricName` | histogram X is X.bucket; span metrics count spans | missing | `metricNotFoundMessage`; new e2e span-metrics test | EF / W | added |
| `signoz_query_metrics` `groupBy` | names need no context prefix | accurate (the SigNoz/nerve-pod#482 example is already fixed) | `metricsGroupByParamDescription` | C | unchanged |
| `signoz_query_metrics` `filter` | REGEXP, not =~ | accurate | e2e `test_promql_matcher_error_names_regexp` | EF | unchanged |
| `signoz_query_metrics` | grouped time-series top-N ranked over the whole window | accurate | `querier` `applySeriesLimit` | W | unchanged (text only) |
| `signoz_execute_builder_query` | guides "required" | imperative only | decision 4 | W | rules inline; pointers kept |
| `signoz_execute_builder_query` | a formula input limit drops series | accurate, previously unverified | new e2e `test_formula_input_limit_drops_series_and_says_so` | W | unchanged, plus note |
| `signoz_list_services` | counts requests at entry spans | missing | `module.go` scope | W (models chose other tools) | added |
| `signoz_list_services` | callRate per second; errorRate in percent | accurate | eval truth (1,200 requests over 1,500 s = 0.8) | C | unchanged |
| `signoz_get_service_top_operations` `tags` | span-attribute tags match | accurate now (SigNoz/nerve-pod#482 cited it as stale) | `implservices/helpers.go` `buildFilterExpression`; e2e `test_service_top_operations_lists_the_seeded_operation` | EN | unchanged |
| `signoz_search_logs`, `signoz_search_traces`, `signoz_aggregate_logs` | "Read the guide before unfamiliar fields" | imperative only | decision 4 | W | key discovery stated; pointer kept |

### Resources and prompts

| Surface | Claim | Status | Checked against | Mode | Now |
|---|---|---|---|---|---|
| `signoz://traces/query-builder-guide` | isRoot and isEntryPoint semantics | accurate | `tracestelemetryschema/span_scope.go` | W | adds the request-scope rule |
| `signoz://traces/query-builder-guide` | example 3, p99 over one service | taught the all-span trap | `span_scope.go`; e2e entry test | W | filter adds the entry spans |
| `signoz://metrics-aggregation-guide` | "Typical value: avg" | misleading | as `reduceTo` | W | "Mean of the steps", window routes |
| `signoz://metrics-aggregation-guide` | window percentile | missing | new e2e (PromQL 68.75 and bucket counts) | W | bucket-count route and PromQL for cumulative |
| `signoz://metrics-aggregation-guide` | histogram example metricName | stale (`X`, not `X.bucket`) | guide's own rule; `metricNotFoundMessage` | EN | fixed |
| `signoz://metrics-aggregation-guide` | step formula | incomplete (rounding, alignment) | `time.go` `RecommendedStepIntervalForMetric` | C / W | fixed |
| `signoz://metrics-aggregation-guide` | span metrics | missing | new e2e | W | added |
| `signoz://metrics-aggregation-guide` | pinned temporality returns no data | not verified in this pass | none | W | unchanged |
| `signoz://promql/instructions` | quoted selector with quoted dotted labels | accurate for the forms used | new e2e PromQL with `"service.name"=...` | EN | unchanged |
| `signoz://logs/query-builder-guide`, `signoz://ai/query-builder-guide` | filter grammar, search(), AI fields | accurate per existing e2e (`test_logs.py`, `test_ai_traces.py`) | e2e | mixed | unchanged |
| `signoz://query-builder/examples` | generated requests run | accurate | e2e `test_query_builder_examples_execute` | EN | unchanged |
| Dashboard, alert, view, and channel resources | payload examples create, replace, delete | accurate for the examples | e2e `test_spec_examples.py` | EF | unchanged; prose not re-verified claim by claim in this pass |
| `debug_service_errors` prompt | `aggregation="count"` | stale (retired parameter) | coded error `legacyAggregationParamsError` | EF | `aggregations=["count()"]` |
| `latency_analysis` prompt | p99 over time for a service | taught the all-span trap | entry test | W | entry filter added |
| `compare_metrics`, `incident_triage` prompts | parameter names | accurate | tool schemas | C | unchanged |
| `manifest.json` | tool descriptions | three led with backend wording | parity test | C | `signoz_list_services`, `signoz_aggregate_traces`, `signoz_aggregate_logs` updated |

### Coded errors

| Error | Claim | Status | Mode |
|---|---|---|---|
| metric not found | search with `signoz_list_metrics`; histograms are `X.bucket` | accurate (e2e `test_metadata_lookup_matches_the_exact_name`) | EF |
| retired `aggregation`/`aggregateOn` | names `aggregations` | accurate | EF |
| key not found on queries | discover keys, `missingKeys` | accurate (e2e `test_trace_fields.py`) | EF |
| alert history not found | names SigNoz v0.118.0 and v0.120.0 | stale against the version policy | C (not changed here) |

Totals over the 45 rows above: 21 accurate, 10 missing, 13 stale or misleading (including the four
"imperative only" guide pointers), and 1 not verified. By failure mode: 27 W, 9 EF, 4 EN, and 6 C;
two rows carry two modes and the logs and AI guides row is mixed.

## Silent traps

| Trap | Status | Reason |
|---|---|---|
| Ungrouped count, rate, or latency over every span of one service | noted (`trace_all_spans`), `signoz_aggregate_traces` and `signoz_execute_builder_query` | the request shows it |
| Scalar percentile reduced with avg, median, or sum | noted (`percentile_of_steps`), both metric paths | the request shows it; bucket-count route and PromQL verified |
| Caller step at least as long as a window that starts mid-step | noted (`step_alignment`), both metric paths | found in the eval; behaviour pinned in e2e |
| Scalar rate reduced with sum | noted (`rate_summed`), both metric paths | the request shows it |
| Cumulative counter read without rate or increase | noted (`counter_running_total`), `signoz_query_metrics`; text only for `signoz_execute_builder_query` | needs monotonicity, which only the metadata lookup has |
| `signoz_calls_total` or `signoz_latency` for one service | noted (`span_metrics_all_spans`), both metric paths | the request shows it |
| Formula input with its own limit | noted (`formula_input_limit`), `signoz_execute_builder_query` | `signoz_query_metrics` never limits inputs |
| Negative filters match missing fields | text only (instructions rule 4) | the request cannot say whether a key is ever missing; a note would fire on most negative filters |
| Grouped top-N ranked over the whole window | text only (limit descriptions, decisions note) | whether a spike group was dropped is not visible in the result |
| rate on a gauge | text only (metrics guide) | a rate of change is a legitimate question; no evidence it is common |
| Pinned temporality that returns no data | text only (metrics guide) | the result is empty, not a plausible number; not re-verified here |
| Histogram X instead of X.bucket | error that states the fix | `metricNotFoundMessage` |
| Absent service means no trace activity | text only (`signoz_list_services`) | accurate as written |
| Default time windows | existing window note on every windowed result | already covered |
| Nanoseconds and percentages in results | text only (parameter and tool descriptions) | units are stated where values appear |
| Units in alert thresholds; panels and alerts that save but never show data or fire | not addressed | needs a query at save time, which is new behaviour |

## Files to Modify

- `internal/handler/tools/measurement_notes.go` — the notes and their counter.
- `internal/handler/tools/{traces,metrics_query,query_builder}.go` — call the notes.
- `pkg/otel/{metrics,attr}.go` — `mcp.tool.result.measurement_notes` and its rule attribute.
- `pkg/querybuilder/bounds.go` — formula-input helpers shared with the notes.
- `pkg/instructions/instructions.go`, `guardrails/policy.go` — routing preamble, 4,096-byte budget.
- Tool and parameter descriptions in `internal/handler/tools`, guides in `pkg/querybuilder` and
  `pkg/metricsrules`, prompts in `pkg/prompts`.
- `internal/mcp-server/contract_budget_test.go` — advertised tool names resolve; the preamble routes.
- `internal/mcp-server/testdata/wire-catalog/` — the changed entries only.
- `README.md`, `manifest.json`, `docs/mcp-best-practices.md` (SUR-11, OUT-7, RES-5).
- e2e: `tests/e2e/tests/test_{trace_fields,metric_aggregations,result_shaping}.py`, `tests/fixtures/telemetry.py`.

## Key Decisions

### 2026-11-21 — Raise the instructions budget to 4,096 bytes

- Decision: `MaxServerInstructionsBytes` 2,048 → 4,096; the 512-byte preamble budget stays and now
  holds the routing.
- Rationale: Claude Code cut instructions at 2,048 characters until v2.1.296 (2026-10-09) and now
  cuts at 4,096; Noz cuts at 4,096; Codex's guidance keeps the first 512 characters self-contained.
  The instructions are 3,912 bytes with a 510-byte preamble.

### 2026-11-21 — Offer only fixes pinned by e2e

- Decision: the percentile note offers the bucket-count route (`timeAggregation: increase`,
  `spaceAggregation: sum`, `reduceTo: sum`, `groupBy: le`), and PromQL `histogram_quantile` over
  `increase(...[<window>s])` for a cumulative histogram, conditionally when temporality is unknown.
  It offers no PromQL for a delta histogram.
- Rationale: on v0.151.0 the PromQL returns the window's p95 (68.75 against 24.69 for the averaged
  steps) for a cumulative histogram and no data for a delta one; summed bucket counts are exact for
  both. A step as long as the window is not a fix: steps start at multiples of the step since the
  epoch, so a cumulative histogram drops the window before the first boundary and a delta one reads
  data from before the window.
- Alternatives rejected: a min/max bound in the note. In round 2 of the eval it led Haiku to report
  the bound as the answer.

### 2026-11-21 — Keep payload-schema pointers on mutation tools

- Decision: dashboard, alert, view, and channel tools keep their "read signoz://..." pointers.
- Rationale: their content is payload schema that cannot fit a 1,024-byte description, and those
  flows read the guides when skills name them (SigNoz/nerve-pod#667).

## Reference Links

- SigNoz/nerve-pod#667, SigNoz/nerve-pod#482, SigNoz/signoz-mcp-server#474

## Verification

- Handler tests in `measurement_notes_test.go`: each note fires and its legitimate neighbour does
  not, and the counter records tool and rule (36 cases).
- e2e against v0.151.0: `test_entry_spans_count_each_request_once` (extended),
  `test_span_metrics_count_every_span_and_say_so`,
  `test_window_percentile_is_not_the_mean_of_step_percentiles`,
  `test_counter_reducers_say_what_they_measured`, and
  `test_formula_input_limit_drops_series_and_says_so` pass; the full suite result is in the PR.
- EVL-1, final round, origin/main against this branch, Claude Code CLI 2.1.330 with tool search on
  (pass counts on the final number; the PR has the setup and every row):
  - Haiku request p95 3/6 → 6/6, request rate 4/6 → 6/6, histogram p95 0/6 → 6/6.
  - Sonnet histogram p95 0/3 → 3/3; its request p95 and rate were 3/3 in both builds.
  - The direct prompt and the negative prompts (span count, worst minute, order rate) passed in
    both builds, and no run narrowed a span count to requests.
  - Order count passed in both builds, so the counter notes show no gain here, only no regression.
  - Two earlier rounds shaped the percentile text: an averaged-percentile note alone left models
    setting a window-long step, and a min/max bound led Haiku to report the bound.
- `GOTOOLCHAIN=go1.26.11 make ci`: pass, with a fresh module cache because a locally modified
  module-cache entry fails `go mod verify`, and with protocol and conformance port overrides because
  another local process holds port 18080.
- Full e2e suite against v0.151.0: 111 passed, 1 skipped (skipped on main as well).

## Outcome

Shipped as planned, plus the `step_alignment` rule, which the eval found. Deferred: the
`signoz_execute_builder_query` ClickHouse `GLOBAL IN` and request-shape rules from
SigNoz/nerve-pod#667 (no room in the description and not verified here); the prose of the
dashboard, alert, view, and channel resources, which this pass did not re-verify claim by claim;
alert-threshold units and saved panels or alerts that can never show data, which need a query at
save time; the stale SigNoz version names in the alert-history recovery text. Skill changes are
tracked in SigNoz/nerve-pod#436.
