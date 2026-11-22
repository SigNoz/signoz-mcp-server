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
| A service's requests are its entry spans | missing in instructions | `pkg/modules/services/implservices/module.go` scope `isRoot = true OR isEntryPoint = true`; e2e `test_entry_spans_count_each_request_once` | W | `aggregations` parameter only | instructions, the `signoz_aggregate_traces` and `signoz_execute_builder_query` descriptions, traces guide, note |
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
| `signoz_aggregate_traces` `aggregations` | entry spans count each request once | accurate | e2e (SigNoz/signoz-mcp-server#474) | W | sentence removed; the tool description carries it |
| `signoz_query_metrics` | routing for a service's request rate | missing | eval of SigNoz/signoz-mcp-server#474 (Sonnet took the rate from `signoz_calls_total`) | W | description names `signoz_list_services` and `signoz_aggregate_traces` |
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
| `signoz_list_services` | counts requests at entry spans | missing | `module.go` scope | W (models chose other tools) | added, without the filter expression |
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

## Audit, second pass: mutation tools, remaining tools, prompts, errors, manifest

Same columns and failure modes. Many nested alert, view, and dashboard parameter descriptions are
generated from the pinned SigNoz OpenAPI spec (`internal/apiclient/apitypes`), so they are SigNoz's
own text; they were spot-checked, not edited.

### Alerts

| Claim | Status | Checked against | Mode | Was taught | Now |
|---|---|---|---|---|---|
| schemaVersion v2alpha1 required | accurate | `ruletypes/api_params.go` `validateSchemaVersion` | EF | guide, schema | unchanged |
| thresholds, evaluation, notificationSettings required for v2alpha1 | accurate | `api_params.go` | EF | guide | unchanged |
| panelType must be value, table, or graph | accurate | `ruletypes/alerting.go` `PanelType.Validate` | EF | guide, schema | unchanged |
| unknown keys rejected at the top level and in condition | accurate | `api_params.go` `WithDisallowUnknownFields` | EF | guide | unchanged |
| a tier with no channels or an unknown channel is rejected | accurate | e2e `test_create_alert_unknown_channel_rejected` | EF | descriptions, guide | unchanged |
| targetUnit converts to compositeQuery.unit; with no unit the target is compared unconverted | accurate | `ruletypes/threshold.go` `convertToRuleUnit`, `units/converter_time.go`; new e2e `test_threshold_unit_converts_only_with_a_query_unit` | W | guide only | create and update descriptions, guide |
| SigNoz saves a rule whose query returns no data | missing | new e2e `test_rule_on_a_missing_metric_saves` | W | nowhere | create description, guide step |
| negative operators "only match rows where the field is present" | stale | `querybuildertypesv5/builder_elements.go` `AddDefaultExistsFilter`; new e2e `test_negative_filters_also_match_rows_missing_the_field` | W | guide | fixed |
| order and limit on a grouped alert query cap evaluation to the top N | not verified in this pass | none | W | guide | unchanged |
| labels.severity does not set a fired alert's severity | not verified in this pass | none | C | guide | unchanged |
| alerts evaluate the first aggregation | not verified in this pass | none | W | guide | unchanged |
| metric timeAggregation defaults by type for alerts | not verified in this pass | none | W | guide | unchanged |
| evaluation windows are Go durations, largest unit h | not verified in this pass | none | EF | guide | unchanged |
| PromQL uses the quoted selector for dotted names | accurate | e2e PromQL with `{"X.bucket", "service.name"=...}` | EN (no data) | guide, PromQL guide | unchanged |
| an alertType that does not match the query signal | missing | probe: SigNoz saves it; its effect on evaluation not verified | unknown | nowhere | not addressed |
| generated condition and notificationSettings descriptions | accurate where spot-checked | `compositeQuery.unit` against `threshold.go` | mixed | schema (generated) | unchanged |
| alert-history 404 recovery names SigNoz v0.118.0 and v0.120.0 | stale against the version policy | `CLAUDE.md` | C | coded error | fixed |

### Dashboards

| Claim | Status | Checked against | Mode | Was taught | Now |
|---|---|---|---|---|---|
| the server derives name from spec.display.name on create | accurate | `dashboards.go` sets `generateName` | C | guide, schema | unchanged |
| only source=user dashboards can be written | accurate | e2e `test_system_dashboard_is_hidden_but_gettable...` | EF | descriptions | unchanged |
| update is a full replacement; the {status, data} envelope is rejected | accurate | e2e `test_text_panel_create_get_update_patch...` | EF | description | unchanged |
| a panel with no grid item is accepted but never renders | accepted: accurate; renders: UI, not verified | new e2e `test_panels_save_without_data_or_a_grid_item` | W | patch guide, a parenthesis on the tool | rule on `signoz_patch_dashboard` |
| a panel whose query returns no data saves | missing on the tools | new e2e (same test) | W | widgets guide dry-run rule | create and update descriptions |
| removing a panel without its grid item is rejected; overlapping items are rejected; one query per panel | not verified in this pass | none | EF | patch guide | unchanged |
| TextPanel queries must be [], not null | accurate | e2e text-panel lifecycle | EF | widgets guide | unchanged |
| a saved panel query dry-runs unchanged through signoz_execute_builder_query | accurate | e2e `test_saved_panel_queries_dry_run_unchanged...` | EN | widgets guide | unchanged |
| area, top list, scatter, and geo map panel rules | accurate | e2e panel round-trip tests | EF | widgets guide | unchanged |
| heatmap panel rules | not verified in this pass | none | EF | widgets guide | unchanged |
| query-builder guide: negative operators match missing fields | accurate | new e2e | W | guide | unchanged |
| query-builder guide: signoz_latency.count is the number of requests | stale | e2e span metrics (one sample per span) | W | guide | fixed: spans, not requests |
| query-builder guide: process_cpu_seconds_total listed as a gauge | stale | counter by name | C | guide | replaced |
| histogram suffixes are dotted (.bucket, .count, .sum) | accurate | e2e histogram tests | EF | guide | unchanged |
| ClickHouse examples use GLOBAL IN | not verifiable here | the pinned single-shard cast answers a plain IN subquery without error | EN on multi-shard | ClickHouse guides | unchanged (see Outcome) |
| list-filter guide: REGEXP rejected; EXISTS only on tag keys | not verified in this pass | none | EF | guide | unchanged |
| dashboard examples create, replace, and patch | accurate | e2e `test_dashboard_examples_create_replace_patch_and_delete` | EF | examples | unchanged |
| widget examples match the create schema | accurate | `dashboard_schema_test.go` | EF | examples | unchanged |

### Views and notification channels

| Claim | Status | Checked against | Mode | Was taught | Now |
|---|---|---|---|---|---|
| create_view: "you must read both" guides | imperative only | decision 4, SUR-10 wording | W | description | rule and pointer |
| a view whose builder query signal differs from source still saves | accurate | new e2e `test_view_with_a_mismatched_signal_saves` | W | view guide | create description |
| the explorer shows only views whose signal matches | not verified (UI) | none | W | view guide | unchanged |
| view name is immutable; update is a full replacement | accurate | e2e `test_saved_views.py` | EF | descriptions, schema | unchanged |
| view examples | accurate | e2e `test_view_examples_create_replace_and_delete` | EF | examples | unchanged |
| delete channel: "does not check alert-rule references" | stale | `signozalertmanager/provider.go` `DeleteChannelByID`; new e2e `test_channel_an_alert_routes_to_cannot_be_deleted` | EN (rule IDs labelled routing policies) | description | rule in description, recovery note on the error |
| channel name is an immutable DNS-1123 label; routing uses displayName | accurate | e2e channel lifecycle | EF | descriptions | unchanged |
| test defaults to false; the channel stays if the test fails | accurate | e2e `test_local_webhook_test_is_opt_in...` | C | descriptions | unchanged |
| omit a template field to unset it; an empty string is rejected | accurate | e2e `test_update_unsets_template_field_by_omission...` | EF | description | unchanged |
| channel examples | accurate | e2e `test_channel_examples_create_replace_and_delete` | EF | examples | unchanged |

### Remaining tools, prompts, errors, and manifest

| Surface | Claim | Status | Checked against | Mode | Now |
|---|---|---|---|---|---|
| `signoz_list_metrics` | active in the window; no offset pagination | accurate | e2e `test_list_metrics_path` | C | unchanged |
| `signoz_get_top_metrics` | a fixed top 100 | accurate | `metrics_top.go` `topMetricsLimit` | C | unchanged |
| `signoz_check_metric_usage` | up to 50 names per call | accurate | `client.MaxMetricUsageNames` | EF | unchanged |
| `signoz_check_metric_cardinality` | keys sorted by cardinality with samples | accurate | `client/metric_cardinality.go`; e2e `test_metric_cardinality_resolves_seeded_metric` | C | unchanged |
| `signoz_get_field_keys`, `signoz_get_field_values` | no time range; data.complete=false when cut | accurate | e2e `test_field_lookups_honor_limit_and_say_when_cut` | W | unchanged |
| `signoz_get_trace_details` | found by ID alone | accurate | e2e `test_trace_details_finds_a_trace_by_id_alone` | C | unchanged |
| `signoz_get_service_map` | paired calls only; a window under a minute can be empty | accurate | e2e `test_service_map.py` | W | unchanged |
| `signoz_get_service_map` | which side an edge's latency and errorRate are measured on | missing | the collector's service-calls view takes them from the callee's span; e2e `test_service_map.py` | W (found in the eval) | description and units note name the callee side |
| `signoz_get_org_overview` | missing fields are unreported, not zero | accurate | e2e `test_org_overview` | W | unchanged |
| AI pricing and mapping tools | views, unpriced models in the last hour, mapping groups | accurate | e2e `test_ai_pricing_and_mappings.py` | C | unchanged |
| `signoz_search_docs`, `signoz_fetch_doc` | ranking, sections, headings | accurate | `internal/docs` golden tests | C | unchanged |
| `signoz_list_alerts` | Alertmanager label matchers =, !=, =~, !~ | not verified in this pass | none | EF | unchanged |
| `signoz_list_alert_rules` | filter keys and label matching | accurate | e2e `test_list_alert_rules_filters_in_signoz` | EF | unchanged |
| `signoz_get_alert_history` | cursor paging and envelope | accurate | e2e `test_alert_history_envelope` | C | unchanged |
| prompts `debug_service_errors` steps 1 and 3, `latency_analysis` steps 1, 3, 4, `compare_metrics`, `incident_triage` | tool names, parameters, and steps | accurate | tool schemas; e2e log and trace tests | C | unchanged |
| `signoz_execute_builder_query` error | the older builderQueries shape gets "atleast one query is required", which does not name the shape | missing | probe; new e2e `test_older_request_shape_error_names_the_queries_list` | EN | recovery text added |
| `signoz_execute_builder_query` error | an unknown top-level field such as queryType or panelType names the valid fields | accurate | probe | EF | unchanged |
| `signoz_query_metrics` error | a scalar query without reduceTo is rejected | accurate | e2e `test_scalar_requires_an_explicit_reduce_to` | EF | unchanged |
| `manifest.json` | `signoz_create_view` "after reading both view authoring resources" | stale against its description | descriptions | C | updated |
| `manifest.json` | `signoz_delete_notification_channel` routing-policy wording | incomplete | e2e | C | updated |
| `manifest.json` | the other 46 entries | accurate against their tools | descriptions | C | unchanged |

Totals over the 65 rows above: 41 accurate, 1 accurate in part (the patched panel saves; the "never
renders" half is UI), 4 missing, 8 stale or misleading, and 11 not verified in this pass. By failure
mode: 16 W, 26 EF, 5 EN, and 16 C; one row is mixed and one unknown. Every stale or missing claim
above is fixed except the alertType mismatch, whose effect on evaluation is not established. The
not-verified rows keep their text; checking them needs an alert to fire, a UI to render, or a
multi-shard cluster.

## Silent traps

| Trap | Status | Reason |
|---|---|---|
| Ungrouped count, rate, or latency over every span of one service | noted (`trace_all_spans`), `signoz_aggregate_traces` and `signoz_execute_builder_query` | the request shows it |
| Scalar percentile reduced with avg, median, or sum | noted (`percentile_of_steps`), both metric paths | the request shows it; the bucket-count route is offered only for explicit-bucket histograms without having, keeps the caller's groups, and asks for a limit that returns every bucket |
| Caller step at least as long as a window that starts mid-step | noted (`step_alignment`), both metric paths | found in the eval; behaviour pinned in e2e |
| Scalar rate reduced with sum | noted (`rate_summed`), both metric paths | the request shows it |
| Cumulative counter read without rate or increase | noted (`counter_running_total`), `signoz_query_metrics`; text only for `signoz_execute_builder_query` | needs monotonicity, which only the metadata lookup has |
| `signoz_calls_total` or `signoz_latency` for one service | noted (`span_metrics_all_spans`), both metric paths | the request shows it |
| Formula input with its own limit | noted (`formula_input_limit`), `signoz_execute_builder_query` | `signoz_query_metrics` never limits inputs |
| Negative filters match missing fields | text only (instructions rule 4, guides; the alert guide's opposite claim fixed) | the request cannot say whether a key is ever missing; a note would fire on most negative filters; pinned in e2e |
| Grouped top-N ranked over the whole window | text only (limit descriptions, decisions note) | whether a spike group was dropped is not visible in the result |
| rate on a gauge | text only (metrics guide) | a rate of change is a legitimate question; no evidence it is common |
| Pinned temporality that returns no data | text only (metrics guide) | the result is empty, not a plausible number; not re-verified here |
| Histogram X instead of X.bucket | error that states the fix | `metricNotFoundMessage` |
| Absent service means no trace activity | text only (`signoz_list_services`) | accurate as written |
| Default time windows | existing window note on every windowed result | already covered |
| Nanoseconds and percentages in results | text only (parameter and tool descriptions) | units are stated where values appear |
| A threshold targetUnit with no query unit | text on `signoz_create_alert`, `signoz_update_alert`, and the alert guide | SigNoz compares the target unconverted; pinned in e2e by a rule that fires and one that does not |
| An alert or panel saved on a query with no data | text on the alert and dashboard tools and the alert guide (dry-run first) | SigNoz saves both; detecting it would need a query at save time |
| A patched panel without its grid item | text on `signoz_patch_dashboard` | SigNoz saves it outside the layout; pinned in e2e |
| A view whose query signal differs from its source | text on `signoz_create_view` | SigNoz saves it; pinned in e2e; the explorer side is not verified |
| A caller-side latency read from a service map edge | text on `signoz_get_service_map` and its units note on every graph result | an edge between two services reports the callee's spans; found in the eval; pinned in e2e |

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
- Second pass: alert, dashboard, view, and channel descriptions; `internal/handler/tools/errs.go`
  and `notification_channels.go` recovery text; `pkg/alert/resources.go`, `pkg/dashboard/query.go`;
  e2e in `test_{alerts,dashboards,logs,notification_channels,saved_views,query_response_paths}.py`.

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

### 2026-11-21 — Narrow the notes after review

An independent review of the first pass raised fourteen findings; each was checked against the code
and SigNoz source.

- C1: the bucket-count route covers the same aligned steps as the result, not the exact window;
  the note said "over the same steps" (superseded in the second review round below).
- C2: a grouped percentile keeps its groups; the route adds le to the caller's groupBy keys and
  interpolates per group.
- C3: the route is offered only for an explicit-bucket histogram (type histogram, or type unknown
  and a `.bucket` name); an exponential histogram gets only the sentence saying what was measured.
- C4: the note no longer offers PromQL. No final-round session used it, and the bucket route passed
  every session; the guide keeps its PromQL paragraph, pinned in e2e by a literal query.
- C5: a filter with OR names no service, so it gets no service-scoped note.
- C6: the trace note says to add `AND (isEntryPoint = true OR isRoot = true)`; unparenthesised,
  the OR admits root spans of every service. The span-metric notes parenthesise the filter too.
- C7: notes use the step SigNoz reports, which can differ from a caller's (SigNoz raises a step
  below its 60 s minimum).
- C8: `rate_summed` needs spaceAggregation sum (or unset on the builder path); with count the steps
  are series counts.
- C9: a query with post-query functions gets no metric notes, on both tool paths.
- C10: the step-alignment note says a cumulative histogram's first step is left out only when
  timeAggregation is empty; with an explicit rate SigNoz reads the preceding step.
- C11: the latency text covers only `signoz_latency.bucket` and keeps the caller's percentile;
  `signoz_latency.count` gets the calls text; other `signoz_latency.*` names get no note.
- C12: `count_distinct` no longer counts as a sample count.
- C13 and C14 (a sentence that predates this PR; a limit-plus-offset formula input) are declined
  as nits and left as follow-ups.

### 2026-11-21 — Second review round

The second round left C1 and C10 open and raised C15 to C24; each was checked against the code and
SigNoz source.

- C1: "the same steps" is not always true either (a cumulative percentile with no timeAggregation
  can lose its first step, while the increase retry reads a preceding step and keeps it). The
  recipe now offers a single percentile from each bucket's count and promises no steps or window,
  in the note, the reduceTo description, and the guide.
- C10: "is left out" became "can be left out" in the stepInterval description, guide, and README.
- C15: SigNoz applies the limit after reduceTo, and with le in groupBy each bucket is a series, so
  the recipe asks for a limit that returns every bucket of every group.
- C16: SigNoz rewrites a histogram query that is not a percentile to increase with sum
  (`rewriteQueryForHistogramCTE`), so rate with sum returns counts. rate_summed skips histograms
  on both paths. e2e now pins the absolute counts (+Inf = 6 for six observations), and the guide's
  "the le="+Inf" series is the total observation rate" now says count.
- C17: a filter with NOT names no service, as with OR.
- C18: with only the service argument the trace note says to set filter to the entry-span condition
  and keep the service argument; with a filter it says to add it with AND. The span-metric notes
  name the service in the trace call they suggest.
- C19: a query with having gets the measurement sentence but no bucket recipe.
- C20: SigNoz names the routing policies that use a channel; an alert rule's own policy is named
  with the rule's ID. The delete description and recovery text say a UUID name is an alert rule and
  any other name a routing policy in SigNoz settings.
- C21: the alert guide scopes "can fire only a nodata alert" to an evaluation that returns no
  series, and says an empty dry-run can be expected for a rare event.
- C22: signoz_patch_dashboard, signoz_update_alert, and signoz_update_view carry the dry-run or
  signal rule for changed queries or source; signoz_update_alert is at 1,024 bytes after rewording.
- C23: the alert-unit e2e test gives the converted rule a below comparison and waits for both rules
  to fire, with cleanup registered after each creation.
- C24: declined as a nit; "also match" is the instructions' long-standing wording.
- On a fresh cast the negative-filter e2e test failed: a key SigNoz has not indexed yet also matches
  as a body JSON path, which the e2e ClickHouse rejects. The test now uses a per-run key scoped by
  the service argument and waits until SigNoz answers, so every run takes that path.
- The C16 count assertion failed once with 1, 2, 3: its two histogram points, 10 seconds apart and
  not aged, could fall on either side of the last step boundary, so only one was read. The test now
  ages them as the counter test does, so both sit in completed steps.

### 2026-11-21 — Third review round

The third round resolved C1, C10, C15, C17 to C19 and C21 to C23, kept C16, C20 and C24 open, and
raised C25 to C27. Each was checked against the code and the SigNoz source.

- C16: the metric rules compare the type in lower case, as the aggregation defaults do, so a
  caller-typed "Histogram" no longer gets the summed-rate note.
- C24: accepted. The alert guide says negative operators "can also match" rows without the field;
  SigNoz reads a missing numeric trace attribute as zero, so `field != 0` leaves that row out.
- C25: the guide's bucket-read recipe names timeAggregation increase, as the note and the reduceTo
  description do. `AdjustedMetricTimeRange` fetches the step before the window only for an explicit
  rate or increase on a cumulative metric.
- C27: the builder having test asserts the bucket recipe is absent.
- C20 was declined here as a rare case and fixed in the fourth round (below).
- C26 is declined as a nit: an offset carried into the retried bucket query.

### 2026-11-21 — Fourth review round

A fourth round, limited to the third round's fixes and the two disputed findings, resolved C16, C24,
C25 and C27, accepted C26 as declined, and kept C20 open.

- C20: a user-created routing policy can share an alert rule's ID as its name, so finding the rule
  with signoz_get_alert does not show the policy is that rule's own. The delete description and the
  recovery text now say to check whether the rule routes to the channel, and to treat the name as a
  routing policy in SigNoz settings when it does not.
- C28: this plan's deferred list no longer names C24.

### 2026-11-22 — The entry-span filter in three always-loaded texts

- Decision: the filter expression stays in the server instructions, the `signoz_aggregate_traces`
  and `signoz_execute_builder_query` descriptions, and the result notes. It leaves the
  `aggregations` parameter of `signoz_aggregate_traces`, which loads with that tool's description,
  and the `signoz_list_services` and `signoz_query_metrics` descriptions, which take no trace
  filter and now name the tool to use. The Go copies read `querybuilder.EntrySpanFilter`; the
  metrics guide repeats it by hand because the query builder tests import that guide.
- Rationale: the maintainer asked whether six always-loaded copies overfit the eval. No round had
  compared fewer copies, and the only gain from the rule was Haiku on request p95.
- Check (EVL-1 round 6, three builds: main, the branch before the trim, the branch after it): the
  four trace prompts gave the same pass counts before and after the trim. A new negative, the p95
  latency of checkout-web's outgoing calls to inventory-api, never drew the entry filter on any
  build.

### 2026-11-22 — Service map edges say which side they measure

- Finding: on that new negative both branch builds did worse than main with Haiku (3 of 6 and 4 of
  6 against 6 of 6). The branch's instructions route dependency questions to
  `signoz_get_service_map`, and every miss reported the edge's p95, the callee's latency, for a
  caller-side question. The tool said only "latency in nanoseconds".
- Source: the collector's `dependency_graph_minutes_service_calls_mv_v2` view joins a parent span
  and a child span in another service and takes its quantiles and error count from the child. The
  database and messaging views take theirs from the caller's own span.
- Decision: the description says the latency is as the callee service recorded it, and the units
  note says where the caller's side is: `signoz_aggregate_traces` on the caller with
  `kind_string = 'Client'`, which covers every outgoing call the caller makes. The routing line
  stays.
- Check (round 7, the same negative on main, the trimmed branch, and the fixed branch): Haiku
  6 of 6, 4 of 6, 6 of 6; Sonnet 3 of 3 on each. No fixed-branch session answered from the edge.

### 2026-11-22 — Fifth review: the measurement notes

A review of the measurement notes only (GPT-6.2 Astra, max effort) raised M1 to M16 against
`11b04f4`. Each was checked against the code and SigNoz at `9d5c5871cb`. The maintainer's rule for
this pass: ignore the rarest cases and implement the rest properly.

Fixed:

- M1: on a scalar request the counter note says increase with reduceTo sum.
- M3: the trace and span-metric rules read filter keys from a small tokenizer, so
  `k8s.service.name`, `peer.service.name`, and `service.name` inside a quoted value no longer name
  a service. A span-kind predicate other than `kind = 'Server'` counts as deliberate.
- M4: the span-metric notes name `count()` or `rate()` and say to carry over the query's other
  filter conditions and grouping.
- M6: with no known type, a name ending in `_bucket` counts as a histogram, as `.bucket` does.
- M7: a rank in the `+Inf` bucket takes the highest finite bound, as SigNoz's `bucketQuantile` does.
- M9: the step-alignment note no longer promises the exact window, and says the result can be
  empty when the next step boundary is at or after the window's end.
- M10: builder `start` and `end` are read as SigNoz's `ToMilliSecs` reads them.
- M11: the builder trace rule runs only for scalar and time-series requests.
- M14: one decoder reads the named query's step for both metric tools and WARN-logs metadata it
  cannot decode. With no reported step the notes name none.
- M15: each per-query builder note names its query.
- M16, and the tests under M5: the formula-input e2e ranks B against A and checks the surviving
  service and its value; the counter e2e pins the summed rates and the sample count against the
  window's increase.

Left as too rare: M8 (groups times buckets over the 10,000-row limit cap), M12 (several
aggregations in one metric builder query), M13 (a query named like a function), and parts of M1 and
M5 (`having`; a window that is exactly one step), M3 (a dashboard variable as the service), M6 (an
exponential histogram named `*.bucket`), and M11 (trace-operator queries).

Declined: M2. A nested span that shares an entry span's name also counts as an entry point. That
is SigNoz's definition, the Services page counts the same way, and the notes already say "as
signoz_list_services does".

### 2026-11-22 — Sixth review: the fifth review's fixes

A second review at the same settings, of `11b04f4..1543d64`, confirmed M6, M7, M11, M15, M16, the
M5 test changes, and the trim, and raised N1 to N12. N1 to N11 are fixed in `e102b2d`.

- N1: the counter note names the whole retry (increase, sum, sum for a scalar count; rate, sum, avg
  for a scalar rate), because the caller's spaceAggregation also decides the result.
- N2: span-metric labels differ from span fields in name and value (`operation`, `span.kind`,
  `status.code` against `name`, `kind_string`, `status_code_string`), so the span-metric notes say
  to rewrite other conditions and group keys as span fields, and leave `le` out.
- N3: for a time-series metric request the suggested trace call names requestType time_series, the
  same window, and the step. `signoz_list_services` is offered only for a scalar over the whole
  service.
- N4: the step-alignment note compares the next boundary with the end as SigNoz floors it.
- N5: the filter reader accepts the grammar's spellings: `=` and `==`, `IN (..)`, `IN [..]`,
  `IN value`, and unquoted values. `kind_string = Server` and `kind_string IN ('Server')` fire
  again, and `service.name IN ['frontend']` names its service.
- N6: the service map description said the callee recorded every edge's latency. A database or
  messaging edge is measured on the caller's spans; the description and units note now say so, and
  the note names an aggregation in the caller-side call.
- N7: on the builder path, a metrics query SigNoz reported no step for was skipped, so it gets no
  notes.
- N8: the last two substring checks read keys from the tokens, so a service named
  `operations-api` no longer silences a note. The `signoz_execute_builder_query` description says
  "every matching span".
- N9: a step entry that is null, negative, or fractional is WARN-logged and dropped.
- N10: the formula-input e2e ages both gauges and waits for the unlimited sums before limiting.
- N11: the percentile test and the service map e2e assert key terms, not whole sentences.

Declined: N12, a nanosecond timestamp that loses a millisecond to float precision.

### 2026-11-21 — Mutation-side rules on the tools, schema pointers kept

- Decision: rules that prevent a wrong result with no error go on the tool: the alert unit rule,
  dry-running alert and panel queries, the patched panel's grid item, and the view signal. Payload
  schema stays in the guides, with pointers.
- Rationale: each rule is pinned in e2e; the guides hold schema that cannot fit a description.

### 2026-11-21 — GLOBAL IN not taught

- Decision: no `GLOBAL IN` rule or recovery text.
- Rationale: the pinned e2e cast is single-shard and answers a plain `IN` subquery over the
  distributed tables without error, so neither the failure nor its error text can be verified here.

## Reference Links

- SigNoz/nerve-pod#667, SigNoz/nerve-pod#482, SigNoz/signoz-mcp-server#474

## Verification

- Handler tests: `measurement_notes_test.go` (52 cases: each note fires and its legitimate
  neighbour does not, and the counter records tool and rule), the channel-in-use and older
  request-shape recovery texts.
- e2e against v0.151.0, first pass: `test_entry_spans_count_each_request_once` (extended),
  `test_span_metrics_count_every_span_and_say_so`,
  `test_window_percentile_is_not_the_mean_of_step_percentiles` (now with the grouped bucket route
  and the guide's literal PromQL), `test_counter_reducers_say_what_they_measured`,
  `test_formula_input_limit_drops_series_and_says_so`.
- e2e, second pass: `test_threshold_unit_converts_only_with_a_query_unit`,
  `test_rule_on_a_missing_metric_saves`, `test_panels_save_without_data_or_a_grid_item`,
  `test_negative_filters_also_match_rows_missing_the_field`,
  `test_channel_an_alert_routes_to_cannot_be_deleted`, `test_view_with_a_mismatched_signal_saves`,
  `test_older_request_shape_error_names_the_queries_list`.
- EVL-1, origin/main against this branch, Claude Code CLI 2.1.330 with tool search on; the PR has
  the setup and every row. Third-pass round on the final text: Haiku request p95 3/6 → 6/6,
  histogram p95 0/6 → 6/6; Sonnet histogram p95 0/3 → 3/3; request rate passed in both builds this
  round (the second-pass round had Haiku 3/6 → 6/6); the span-count and worst-minute negatives held.
  The alert prompt passed in both builds; dry-running the query before creating the rule went from
  1 of 9 to 7 of 9, at about one more turn per session.
- EVL-1 round 6 (111 sessions; main, before the entry-span trim, after it): request p95 Haiku
  5/6, 6/6, 6/6; request rate Haiku 4/6, 6/6, 6/6; entry-span p95 and the span-count negative
  passed in every build; Sonnet passed every prompt in every build. Outgoing-call p95 (new
  negative): Haiku 6/6, 3/6, 4/6, every miss the service map edge's p95.
- EVL-1 round 7 (27 sessions; main, trimmed, service map text fixed), outgoing-call p95: Haiku
  6/6, 4/6, 6/6; Sonnet 3/3 in each.
- Fifth review fixes (`1543d64`): `make ci` passes and the full e2e suite passes on a fresh
  v0.151.0 cast (118 passed, 1 skipped as on main). The handler tests gain a firing case and a
  quiet neighbour for each changed rule.
- Sixth review fixes (`e102b2d`): `make ci` passes; `test_result_shaping.py`,
  `test_service_map.py`, `test_metric_aggregations.py`, and `test_trace_fields.py` pass on a fresh
  v0.151.0 cast (23 of 23), and the formula-input test passed 10 of 10 runs.
- e2e, 2026-11-22: `test_service_map_reports_one_edge_from_both_ends` now also asserts the units
  note names the callee side and that the caller's client spans give a different p99 (210 ms
  against the edge's 200 ms). It and `test_trace_fields.py` pass on a fresh v0.151.0 cast.
- `GOTOOLCHAIN=go1.26.11 make ci` with a temporary module cache (a locally modified cache entry
  fails `go mod verify`) and protocol and conformance port overrides (another local process holds
  port 18080). Full e2e suite against v0.151.0.

## Outcome

Shipped as planned, plus the `step_alignment` rule the eval found, the fixes from four
review rounds (C1 to C28, less the declined C13, C14 and C26), and the second audit pass with its
fixes. Deferred:

- `GLOBAL IN` for ClickHouse SQL: not verifiable on the single-shard e2e cast.
- The other 10 claims marked not verified in this pass (alert top-N, severity labels, first aggregation,
  metric defaults for alerts, evaluation formats, patch rejections, heatmap rules, list-filter
  rules, Alertmanager matchers, explorer rendering of mismatched views), and an alertType that does
  not match its query signal.
- Review findings C13, C14, and C26 (a sentence that predates this PR; a limit-plus-offset formula
  input; an offset carried into the retried bucket query).
- Measurement-note cases left as too rare in the fifth review (M8, M12, M13, and parts of M1, M3,
  M5, M6 and M11), and M2 and N12, declined.
- Skill changes are tracked in SigNoz/nerve-pod#436; behaviour bugs are in SigNoz/nerve-pod#674.
