# Plan: Verified query guidance and measurement notes

Status: Done
Issue: SigNoz/nerve-pod#667, SigNoz/nerve-pod#482
PR: https://github.com/SigNoz/signoz-mcp-server/pull/475

## Context

Models rarely read `signoz://` guides, so a rule taught only there is not taught (SigNoz/nerve-pod#667).
The costly rules fail silently: the query succeeds and returns a plausible wrong number. Nothing
checked that the guidance sent to clients is still true (SigNoz/nerve-pod#482). The goal is that
the model gets the right number, or is told what the number it got measures.

Decisions made with the maintainer before work began:

1. One PR for the audit, the result notes, and the text changes.
2. Result notes for silent traps are shown to models from this release, and each one is counted.
3. Server instructions carry tool routing in their first 512 bytes, and
   `MaxServerInstructionsBytes` goes from 2,048 to 4,096. No other budget changes.
4. Rules move onto the parameter or tool they apply to, each verified against SigNoz first;
   "read signoz://..." imperatives give way to the rule, and a pointer may stay as further reading.
5. Out of scope: client-specific always-load hints, new tools or parameters,
   any guide-read gate (SUR-10), a guide-delivery tool (SigNoz/nerve-pod#157), and skill changes
   (SigNoz/nerve-pod#436). Resource-URI span attributes were out of scope at first; the
   maintainer moved them in on 2026-11-22 (see Key Decisions).

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
| `signoz_list_services` | callRate per second; errorRate in percent | incomplete: callRate is numCalls over the whole requested window | eval truth (1,200 requests over 1,500 s = 0.8); `implservices/module.go` `periodSeconds`; e2e `test_services_sorted_by_name_and_paged_without_gaps` | C | adds that callRate divides numCalls by the whole window |
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
| `signoz_get_service_map` | which side an edge's latency and errorRate are measured on | missing | the collector's service-calls view takes them from the callee's span; e2e `test_service_map.py` | W (found in the eval) | description and units note name the callee side; the note says the client-span route pools destinations and how to keep one |
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

## Audit, third pass: coverage by surface

The first two passes recorded claims; this pass records coverage. Every client-visible text was
read in full: the server instructions, all 48 tool descriptions with every parameter description,
all 25 `signoz://` resources, the 4 prompts, every `manifest.json` description, and the recovery
text of the coded errors. Generated text was read and checked for contradictions but not edited:
the dashboard input schemas, the nested alert, view, channel, and query_range fields from the SigNoz
OpenAPI spec, the example payload resources (`pkg/*/examples.generated.md`), the ClickHouse schema
resources, and the docs sitemap. Claims were checked against SigNoz at `9d5c5871cb`, the handlers,
and, where the source left a question, a live probe on the pinned cast. Same modes as above; the
e2e column names a test or says why there is none.

### Coverage

| Surface | Read | Claim rows, passes 1 / 2 / 3 | Pass 3 status | Listed in |
|---|---|---|---|---|
| server instructions | in full | 8 / 0 / 5 | 5 accurate | first pass, third pass |
| `signoz_aggregate_logs` | in full | 2 / 0 / 18 | 17 accurate, 1 missing | first pass, third pass |
| `signoz_aggregate_traces` | in full | 5 / 0 / 12 | 11 accurate, 1 missing | first pass, third pass |
| `signoz_check_metric_cardinality` | in full | 0 / 1 / 0 | none new | second pass |
| `signoz_check_metric_usage` | in full | 0 / 1 / 0 | none new | second pass |
| `signoz_create_alert` | in full; nested schema text generated | 0 / 0 + 17* / 2 | 1 accurate, 1 open | second pass*, third pass |
| `signoz_create_dashboard` | in full; nested schema text generated | 0 / 0 + 18* / 1 | 1 accurate | second pass*, third pass |
| `signoz_create_notification_channel` | in full; nested schema text generated | 0 / 0 + 10* / 1 | 1 accurate | second pass*, third pass |
| `signoz_create_view` | in full; nested schema text generated | 0 / 1 + 10* / 1 | 1 accurate | second pass, third pass |
| `signoz_delete_alert` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_delete_dashboard` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_delete_notification_channel` | in full | 0 / 1 + 10* / 1 | 1 accurate | second pass, third pass |
| `signoz_delete_view` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_execute_builder_query` | in full; nested schema text generated | 3 / 2 / 2 | 2 accurate | first pass, second pass, third pass |
| `signoz_fetch_doc` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_get_ai_pricing` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_get_alert` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_get_alert_history` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_get_dashboard` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_get_field_keys` | in full | 0 / 1 / 4 | 3 accurate, 1 stale or misleading | second pass, third pass |
| `signoz_get_field_values` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_get_notification_channel` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_get_org_overview` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_get_service_map` | in full | 0 / 2 / 1 | 1 accurate | second pass, third pass |
| `signoz_get_service_top_operations` | in full | 1 / 0 / 2 | 2 accurate | first pass, third pass |
| `signoz_get_top_metrics` | in full | 0 / 1 / 0 | none new | second pass |
| `signoz_get_trace_details` | in full | 0 / 1 / 2 | 1 stale or misleading, 1 accurate | second pass, third pass |
| `signoz_get_view` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_import_dashboard` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_list_ai_attribute_mappings` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_list_alert_rules` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_list_alerts` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_list_dashboard_templates` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_list_dashboards` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_list_metrics` | in full | 1 / 1 / 2 | 2 accurate | first pass, second pass, third pass |
| `signoz_list_notification_channels` | in full | 0 / 0 / 2 | 2 accurate | third pass |
| `signoz_list_services` | in full | 4 / 0 / 2 | 2 accurate | first pass, third pass |
| `signoz_list_views` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_patch_dashboard` | in full | 0 / 1 + 18* / 1 | 1 accurate | second pass, third pass |
| `signoz_query_metrics` | in full | 9 / 1 / 8 | 7 accurate, 1 stale or misleading | first pass, second pass, third pass |
| `signoz_search_docs` | in full | 0 / 1 / 1 | 1 accurate | second pass, third pass |
| `signoz_search_logs` | in full | 1 / 0 / 16 | 16 accurate | first pass, third pass |
| `signoz_search_traces` | in full | 1 / 0 / 10 | 10 accurate | first pass, third pass |
| `signoz_submit_feedback` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_test_ai_attribute_mapping` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz_update_alert` | in full; nested schema text generated | 0 / 0 + 17* / 1 | 1 accurate | second pass*, third pass |
| `signoz_update_dashboard` | in full; nested schema text generated | 0 / 0 + 18* / 1 | 1 accurate | second pass*, third pass |
| `signoz_update_notification_channel` | in full | 0 / 0 + 10* / 1 | 1 accurate | second pass*, third pass |
| `signoz_update_view` | in full; nested schema text generated | 0 / 0 + 10* / 1 | 1 accurate | second pass*, third pass |
| `signoz://ai/query-builder-guide` | in full | 1 / 0 / 1 | 1 accurate | first pass, third pass |
| `signoz://alert/examples` | in full; generated | 0 / 0 / 2 | 2 accurate | third pass |
| `signoz://alert/instructions` | in full | 0 / 0 + 17* / 23 | 21 accurate, 2 stale or misleading | second pass*, third pass |
| `signoz://dashboard/clickhouse-logs-example` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/clickhouse-metrics-example` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/clickhouse-schema-for-logs` | in full; generated | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/clickhouse-schema-for-metrics` | in full; generated | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/clickhouse-schema-for-traces` | in full; generated | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/clickhouse-traces-example` | in full | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://dashboard/examples` | in full; payloads generated | 0 / 0 / 3 | 3 accurate | third pass |
| `signoz://dashboard/instructions` | in full | 0 / 0 / 8 | 5 accurate, 1 stale or misleading, 1 guidance, 1 style | third pass |
| `signoz://dashboard/list-filter-guide` | in full | 0 / 0 + 18* / 6 | 6 accurate | second pass*, third pass |
| `signoz://dashboard/patch-instructions` | in full | 0 / 0 + 18* / 6 | 5 accurate, 1 style | second pass*, third pass |
| `signoz://dashboard/query-builder-example` | in full | 0 / 0 + 18* / 11 | 7 accurate, 3 stale or misleading, 1 style | second pass*, third pass |
| `signoz://dashboard/widgets-examples` | in full | 0 / 0 / 3 | 3 accurate | third pass |
| `signoz://dashboard/widgets-instructions` | in full | 0 / 0 + 18* / 7 | 7 accurate | second pass*, third pass |
| `signoz://docs/sitemap` | in full; generated | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://logs/query-builder-guide` | in full | 1 / 0 / 5 | 5 accurate | first pass, third pass |
| `signoz://metrics-aggregation-guide` | in full | 6 / 0 / 1 | 1 accurate | first pass, third pass |
| `signoz://notification-channel/examples` | in full; generated | 0 / 0 / 2 | 2 accurate | third pass |
| `signoz://promql/instructions` | in full | 1 / 0 / 2 | 1 accurate, 1 stale or misleading | first pass, third pass |
| `signoz://query-builder/examples` | in full; generated | 1 / 0 / 1 | 1 accurate | first pass, third pass |
| `signoz://traces/query-builder-guide` | in full | 3 / 0 / 5 | 4 accurate, 1 stale or misleading | first pass, third pass |
| `signoz://view/examples` | in full; generated | 0 / 0 / 1 | 1 accurate | third pass |
| `signoz://view/instructions` | in full | 0 / 0 + 10* / 5 | 5 accurate | second pass*, third pass |
| `compare_metrics` prompt | in full | 1 / 1 / 1 | 1 accurate | first pass, second pass, third pass |
| `debug_service_errors` prompt | in full | 1 / 1 / 1 | 1 accurate | first pass, second pass, third pass |
| `incident_triage` prompt | in full | 1 / 1 / 1 | 1 accurate | first pass, second pass, third pass |
| `latency_analysis` prompt | in full | 1 / 1 / 2 | 1 accurate, 1 stale or misleading | first pass, second pass, third pass |
| `manifest.json` | in full | 1 / 3 / 2 | 1 stale or misleading, 1 accurate | first pass, second pass, third pass |
| `coded errors` | in full | 4 / 6 / 3 | 3 accurate | first pass, second pass, third pass |

\* Rows of the second pass's Alerts, Dashboards, or Views and notification channels table, shared by
the tools and guides of that subject.

### Claims

| Surface | Claim | Status | Checked against | Mode | e2e |
|---|---|---|---|---|---|
| instructions | AI telemetry questions mean the user's app in SigNoz | accurate | routing; ai tools | W | test_ai_traces.py (tool routes) |
| instructions | usage and billing metrics: signoz_query_metrics source=meter | accurate | metrics_helper.go source param; SigNoz meter source | W | none: no meter data on the e2e cast |
| instructions | rule 2 calls signoz_get_field_keys with fieldContext=resource | accurate | get_field_keys fieldContext param | EF | test_field_keys_context_filter_is_honored |
| instructions | rule 6 resource results include webUrl | accurate | alerts.go, dashboards.go, traces.go, services.go | C | test_dashboard_web_urls_follow..., test_trace_deep_links... |
| instructions | rule 8 count = increase with reduceTo sum | accurate with the counter default spaceAggregation sum | pkg/metricsrules/rules.go:104 | W | test_counter_reducers_say_what_they_measured |
| logs tools | defaults 1h window; end defaults to now; start forms | accurate | timeutil.ResolveWindow; handler tests | EF | test_services_ns_and_ms_windows |
| logs tools | rows are complete records (attributes_string, resources_string) | accurate | SigNoz raw log rows | C | test_search_logs_select_fields_and_order |
| logs tools | selectFields adds timestamp and id, at most 50, prefixes pick context | accurate | logs_helper.go; SigNoz select | EF | test_search_logs_select_fields_and_order |
| logs tools | body.<path> selectable/groupable only when listed | accurate | SigNoz error text | EF | test_unlisted_body_field_cannot_be_grouped |
| logs tools | limit default 20 (search) / 100 (aggregate), max 10000 clamped | accurate | handler constants; querybuildertypesv5 MaxQueryLimit | C | test_aggregate_logs_several_aggregations_and_having |
| logs tools | orderBy default timestamp desc, id tie-break; aggregate orderBy key rule | accurate | logs_helper.go; test_order_keys_the_guides_teach | EF | test_search_logs_select_fields_and_order, test_order_keys_the_guides_teach |
| logs tools | searchScope/searchText semantics; ignored with a note | accurate | logs_helper.go | W | test_explicit_search_scopes..., test_search_scope_without_search_text_is_ignored_with_a_note |
| logs tools | severity matches any case as a prefix | accurate | logs_helper.go | W | test_severity_shortcut_matches_any_case_as_a_prefix |
| logs tools | service shortcut adds resource.service.name; missing key gives no rows plus warning | accurate | logs_helper.go:225 | W | test_missing_filter_key_returns_no_rows_with_the_taught_warning |
| logs tools | ambiguous keys default to resource | accurate | pkg/querybuilder/key_resolution.go ResolveLogicalFields | W | none: SigNoz says so in the result's warning (live probe); not pinned |
| logs tools | no key:value, STARTS WITH, IS NULL in the grammar | accurate | grammar/FilterQuery.g4 | EF | none: SigNoz rejects them with a syntax error |
| logs tools | search() over every field or scopes, slow, may be rejected | accurate | key_resolution.go search docs; SigNoz search | EF | test_explicit_search_scopes_quoting_and_warning_round_trip |
| logs tools | aggregations, __result_N order, having by expression | accurate | SigNoz validation | EF | test_aggregate_logs_several_aggregations_and_having |
| logs tools | functions: timeShift semantics; scalar applies only timeShift | accurate | SigNoz postprocess | W | test_aggregate_logs_time_shift_reads_an_earlier_window, test_aggregate_traces_functions_change_time_series_and_not_scalars |
| logs tools | an unknown groupBy key can come back as one empty group | accurate | SigNoz group by missing key | W | none: the result carries a key-not-found warning (live probe); not pinned |
| logs tools | time_series top groups ranked over the whole range | accurate | querier applySeriesLimit (plan row 70) | W | test_query_metrics_limit_order_having_and_typed_formula_queries |
| trace tools | rows are complete span records with events and links; selectFields adds timestamp, trace_id, span_id | accurate | SigNoz raw trace rows | C | test_search_traces_complete_rows_and_select_fields |
| trace tools | search() is logs-only | accurate | where_clause_visitor.go VisitSearchCall RejectsBodyFunction | EF | test_trace_fields.py |
| trace tools | keys in several contexts default to resource | accurate | key_resolution.go ResolveLogicalFields | W | none: as the logs row |
| trace tools | min/maxDuration units; a bare number is nanoseconds | accurate | traces handler duration parsing | W | handler tests (traces_test.go) |
| trace tools | operation, error, service shortcuts compose with AND | accurate | traces handler | W | test_shortcut_filters_narrow_an_or_filter |
| trace tools | builder_query trace rows come back slowest first; orderBy ignored | accurate | SigNoz trace request | C | test_plain_trace_rows_come_back_slowest_first |
| trace tools | builder_ai_query scope, trace-level fields, ordering | accurate | SigNoz AI query | W | test_ai_traces.py (17 tests) |
| trace tools | one call cannot mix trace-level and span-level aggregations | accurate | SigNoz validation | EF | test_ai_aggregate_rejects_mixing_trace_and_span_aggregations |
| trace tools | grouping by timestamp fails | accurate | querybuildertypesv5/validation.go "group by on timestamp is not allowed" | EF | none: SigNoz's error names the field |
| trace tools | aggregate orderBy must be an aggregation or groupBy key | accurate | SigNoz order validation | EF | test_order_keys_the_guides_teach |
| `signoz_query_metrics` | standalone and formula results capped at 100; formula inputs not limited | accurate | metrics_query.go bounds | W | test_query_metrics_limit_order_having_and_typed_formula_queries |
| `signoz_query_metrics` | having filters query A before a formula; SigNoz ignores having on the formula result | accurate | SigNoz formula evaluation | W | test_query_metrics_limit_order_having_and_typed_formula_queries |
| `signoz_query_metrics` | defaults: requestType time_series; orderBy __result desc; limit 100, max 10000 | accurate | metrics_query.go | C | handler tests (metrics_query_test.go) |
| `signoz_query_metrics` | type defaults: avg for gauges and non-monotonic sums, rate and sum for counters, p99 for histograms; summary has none | accurate | pkg/metricsrules/rules.go | EF | test_scalar_requires_an_explicit_reduce_to; handler tests |
| `signoz_query_metrics` | automatic step: about 300 points, at least 60 s | accurate | pkg/querybuilder/time.go RecommendedNumberOfPoints, RecommendedStepIntervalForMetric | W | test_window_percentile_is_not_the_mean_of_step_percentiles |
| `signoz_query_metrics` | formulaQueries need the primary's groupBy to match series | accurate | SigNoz formula join on labels | W | test_query_metrics_limit_order_having_and_typed_formula_queries |
| `signoz_query_metrics` | source=meter reads Cost Meter data | accurate | metrics_helper.go; SigNoz meter source | W | none: the e2e cast has no meter data |
| `signoz_execute_builder_query` | start and end required; missing or zero standalone and formula-result limits normalize to 100; formula inputs get none | accurate | query_builder.go precondition; pkg/querybuilder/bounds.go ApplyBounds | EF / W | test_execute_builder_query_bounds_builder_ai_query_without_an_order, test_formula_input_limit_drops_series_and_says_so |
| `signoz_execute_builder_query` | requestType has no default | accurate | querybuildertypesv5 request validation | EF | test_execute_builder_query (query_response_paths) |
| `signoz_list_services` | sorted by name, paginated, limit 50 max 1000; follow nextOffset | accurate | services.go | C | test_services_sorted_by_name_and_paged_without_gaps |
| `signoz_list_services` | absence means no trace activity in the window | accurate | SigNoz services module scope | W | test_services_ns_and_ms_windows |
| `signoz_get_service_top_operations` | highest p99 first, limit 100 max 5000; p50/p95/p99 ns, numCalls, errorCount | accurate | services.go; SigNoz top operations | C | test_top_operations_keep_the_slowest_within_limit |
| `signoz_get_service_top_operations` | tags is a JSON-encoded string | accurate | services.go tags parsing | EF | test_service_top_operations_lists_the_seeded_operation |
| `signoz_get_service_map` | environment, cluster, namespace narrow the graph; direction default both; limit 50 max 1000 | accurate | services.go serviceMapTags | W | test_service_map_reports_one_edge_from_both_ends |
| `signoz_list_metrics` | lists metrics active in the window; default limit 50; no offset pagination | accurate | metrics.go; plan pass 2 | C | test_list_metrics_path |
| `signoz_list_metrics` | source=meter lists the usage metrics SigNoz meters (ingestion volume) | accurate | SigNoz meter source | C | none: the e2e cast has no meter data |
| `signoz_get_field_keys` | data.complete=false when cut; intrinsic and materialized fields on top of the limit | accurate | SigNoz fields API | W | test_field_lookups_honor_limit_and_say_when_cut |
| `signoz_get_field_keys` | fieldDataType: an array form such as []string reads as its element type | accurate | telemetrytypes/field_datatype.go mapping | C | none: SigNoz's own mapping, no silent result |
| `signoz_get_field_keys` | fieldContext 'tag' is ignored when listing keys and returns no values | stale | field_context.go maps tag to attribute only in JSON bodies, not on the fields endpoints; what comes back for 'tag' differs by signal, so the text now says only that it is not read as 'attribute' there | W | none: the probe showed empty keys and values; text fixed |
| `signoz_get_field_keys` | queryType builder_ai_query lists trace-level and gen_ai fields | accurate | SigNoz AI fields | W | test_ai_field_keys_list_per_trace_columns, test_ai_field_keys_filter_to_trace_context |
| `signoz_get_field_values` | values come from all retained metadata; no time range | accurate | SigNoz fields API | W | test_seeded_resource_attribute_values_discoverable |
| alert tools | create/update route by exact displayName; ask before creating a channel | accurate | ruletypes threshold channel validation | EF | test_create_alert_unknown_channel_rejected |
| `signoz_list_alerts` | active, silenced, inhibited default true; limit 50 max 1000 | accurate | alerts.go; Alertmanager API defaults | C | none: handler defaults, handler tests |
| `signoz_list_alert_rules` | filter keys, labels, bare words; at most 1024 characters; sort updated_at desc; states | accurate | ruletypes/list.go:54; alerts.go | EF | test_list_alert_rules_filters_in_signoz |
| `signoz_get_alert_history` | 6h default, limit 20 max 10000, order asc, cursor repeats filters | accurate | alerts.go:367-391 | C | test_alert_history_envelope |
| signoz_get_alert, signoz_delete_alert | rule IDs from signoz_list_alert_rules; UUIDv7 | accurate | ruletypes rule IDs | EF | test_alert_crud_round_trip |
| dashboard tools | create/update read the guides; dry-run panel queries; update is a full replacement | accurate | plan pass 2 dashboards | EF / W | test_text_panel_create_get_update_patch..., test_panels_save_without_data_or_a_grid_item |
| `signoz_patch_dashboard` | remove on a missing path is a no-op, add creates parents, result validated, locked rejected | accurate | dashboardtypes/perses_dashboard_patch.go:32 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| `signoz_list_dashboards` | limit default 50, clamped at 200; source user or integration; not a filter column | accurate | dashboardtypes/list_v2.go:15 MaxListLimit | EF | test_system_dashboard_is_hidden_but_gettable_and_source_is_not_a_list_filter |
| signoz_get_dashboard, signoz_delete_dashboard | only source=user dashboards can be written | accurate | plan pass 2 | EF | test_system_dashboard_is_hidden_but_gettable... |
| signoz_import_dashboard, signoz_list_dashboard_templates | relative template path; every word must match; at most 2048 characters | accurate | dashboard_templates.go | EF | test_template_catalog_import_lifecycle |
| view tools | full replacement, name immutable, schemaVersion v2 filled in, list limit 50 max 1000, source validated | accurate | views.go; plan pass 2 | EF | test_view_crud_round_trip, test_list_views_without_source_spans_explorers, test_update_view_source_change_moves_view |
| channel tools | config {kind, spec}; unknown kind or spec field rejected with the requirement; name immutable DNS-1123; routing uses displayName; test defaults false | accurate | plan pass 2 channels; alertmanagertypes | EF | test_every_provider_config_round_trips..., test_local_webhook_test_is_opt_in... |
| `signoz_list_notification_channels` | page size 20 when omitted, capped at 200 | accurate | alertmanagertypes/channellist.go:12-13 | C | none: SigNoz's own cap, reported in pagination.limit |
| `signoz_get_trace_details` | includeSpans: include detailed span information (default true) | stale | internal/client/client.go:725 GetTraceDetails never reads it; every call returns up to 1,000 spans | W | none: the parameter has no effect (behaviour item filed separately) |
| `signoz_get_trace_details` | complete span records; found by ID alone; an unknown ID returns no spans | accurate | SigNoz trace v3 waterfall | C | test_trace_details_finds_a_trace_by_id_alone |
| `signoz_get_org_overview` | missing fields are unreported, not zero | accurate | plan pass 2 | W | test_org_overview |
| AI pricing and mapping tools | views, last-hour unpriced models, mapping semantics, 1 to 100 sample spans, nothing saved | accurate | plan pass 2; SigNoz llm pricing and mapping APIs | C | test_ai_pricing_and_mappings.py |
| docs tools | ranking, limit 10 max 25, section_slug exact, fetch by URL or path and heading | accurate | internal/docs golden tests | C | none: no SigNoz dependency (embedded corpus) |
| `signoz_submit_feedback` | categories; message at most 2000 characters | accurate | feedback.go | C | none: no SigNoz dependency |
| alert/instructions | dry-run shape: query_range without queryType, panelType, or unit | accurate | querybuildertypesv5 unknown-field rejection (plan pass 2) | EF | test_older_request_shape_error_names_the_queries_list |
| alert/instructions | channel list defaults to 20 rows, at most 200 per page | accurate | alertmanagertypes/channellist.go:12-13 | C | none: SigNoz's own cap |
| alert/instructions | alertType maps to the signal; signal must match alertType | misleading | query-service/rules/threshold_rule.go:295-313: evaluation runs the query whatever alertType says; alertType picks the related traces or logs link in a fired alert's annotations | W | none: the link appears only in a fired alert's notification (UNVERIFIED-12) |
| alert/instructions | envelope types per compositeQuery.queryType | accurate | ruletypes compositeQuery validation | EF | test_alert_examples_create_and_delete |
| alert/instructions | source meter works with rolling and cumulative evaluation | accurate | ruletypes evaluation kinds | W | test_alert_examples_create_and_delete (Cost Meter budget example) |
| alert/instructions | order and limit on a grouped query cap evaluation to the top N | accurate | logsstatementbuilder/statement_builder.go:577-582 __limit_cte; querier applySeriesLimit | W | none: needs an alert to fire on a series outside the top N (UNVERIFIED-2) |
| alert/instructions | formula functions abs, sqrt, log, exp; selectedQueryName picks the alerted query | accurate | SigNoz formula evaluator; ruletypes selectedQueryName | W | test_alert_examples_create_and_delete |
| alert/instructions | metric timeAggregation default by type: gauge avg, counters rate | stale | metricsstatementbuilder rewrites only histograms (statement_builder.go:258-270); other types get "invalid time aggregation" (metricstelemetryschema/const.go:353-357) | W | test_rule_on_a_missing_metric_saves (saves; dry-run fails) |
| alert/instructions | logs/traces having must use an aggregation; alerts evaluate the first aggregation | accurate | query-service/rules/threshold_rule.go:177-183 Aggregations[0] | W | none: needs an alert to fire on a two-aggregation query (UNVERIFIED-4) |
| alert/instructions | operator reference and data type guardrails | accurate | filterquery sqlcompiler (plan pass 1 row 50) | EF | test_negative_filters_also_match_rows_missing_the_field |
| alert/instructions | common compositeQuery.unit values | accurate | pkg/units converters | C | test_threshold_unit_converts_only_with_a_query_unit |
| alert/instructions | tier name sets threshold.name and the lower-case severity | accurate | ruletypes/threshold.go:346-347 | C | none: visible only on a fired alert |
| alert/instructions | matchType canonical values and aliases avg, sum | accurate | ruletypes/match.go | EF | test_alert_examples_create_and_delete |
| alert/instructions | op canonical, short, and symbolic forms | accurate | ruletypes/compare.go | EF | test_alert_examples_create_and_delete |
| alert/instructions | recoveryTarget null uses the target | accurate | ruletypes/threshold.go recoveryTarget | W | none: needs a firing and recovering alert |
| alert/instructions | evaluation windows are Go durations; largest unit h | accurate | ruletypes/evaluation.go:38-39 valuer.TextDuration | EF | test_create_alert_unknown_channel_rejected (1d rejected) |
| alert/instructions | cumulative schedule types and fields; SigNoz rejects a schedule missing one | accurate | ruletypes/evaluation.go cumulative schedule validation | EF | test_alert_examples_create_and_delete (budget example) |
| alert/instructions | usePolicy defaults false; renotify fields | accurate | ruletypes/api_params.go NotificationSettings | C | test_alert_crud_round_trip |
| alert/instructions | labels.severity does not set a fired alert's severity; reserved labels dropped; list reads the label | accurate | ruletypes/labels.go:309-317 reservedLabelNames; listable_rule.go:159 | C | none: visible only on a fired alert (UNVERIFIED-3) |
| alert/instructions | platform labels alertname, threshold.name, severity, ruleSource, ruleId | accurate | query-service/rules/threshold_rule.go:280-284 | C | none: visible only on a fired alert |
| alert/instructions | routing-policy matcher operators | accurate | ruletypes routing policy expression | EF | none: routing policies are not created in e2e |
| alert/instructions | anomaly rule fields, seasonality values, signed score, outside_bounds absolute | accurate | ruletypes anomaly threshold; anomaly provider | W | test_alert_examples_create_and_delete (metric_anomaly example) |
| alert/instructions | SigNoz fills only usePolicy=false; annotations get no defaults; unknown keys rejected | accurate | ruletypes/api_params.go (plan pass 2 row 125) | EF | test_alert_crud_round_trip |
| dashboard/list-filter-guide | grammar: AND, OR, NOT, implicit AND, bare-word free text, quoted phrases | accurate | dashboardtypes list filter (SigNoz filter grammar) | EF | test_system_dashboard_is_hidden_but_gettable_and_source_is_not_a_list_filter |
| dashboard/list-filter-guide | reserved keys and their operators; tag keys accept EXISTS | accurate | dashboardtypes/list_filter.go operator sets; the compiler rejects REGEXP (live: "REGEXP filtering on name is not supported") | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/list-filter-guide | REGEXP and NOT REGEXP are rejected | accurate | list_filter.go lists REGEXP, but the live compiler rejects it | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/list-filter-guide | EXISTS and NOT EXISTS apply only to tag keys | accurate | list_filter.go: stringSearchOps has no EXISTS | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/list-filter-guide | filter capped at 1024 characters; reservedKeywords echoed | accurate | dashboardtypes/list_v2.go:16,190 | EF | none: SigNoz's error names the cap |
| dashboard/list-filter-guide | a misspelled tag key matches nothing rather than erroring | accurate | TagKeyOps: any non-reserved key is a tag | W | test_system_dashboard_is_hidden_but_gettable_and_source_is_not_a_list_filter (source is a tag key) |
| dashboard/patch-instructions | panels map, layouts array of Grid, items 0-based | accurate | dashboardtypes perses replicas | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/patch-instructions | a grid item that references a removed panel is rejected (references unknown panel) | accurate | dashboardtypes/perses_dashboard_data.go:330 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/patch-instructions | overlapping items are rejected (items overlap) | accurate | dashboardtypes/perses_replicas.go:408 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/patch-instructions | query-backed panels hold exactly one query | accurate | dashboardtypes/perses_dashboard_data.go:153 | EF | none: needs a query panel patched to two queries; the panel round trips save one query each |
| dashboard/patch-instructions | name immutable; rename via /spec/display/name; TextPanel queries [] | accurate | plan pass 2 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/patch-instructions | prose em dashes (5) | style | docs/client-visible-writing-style.md | C | fixed |
| dashboard/instructions | schemaVersion v6; top-level name generated on create, required on full update | accurate | dashboardtypes/perses_dashboard.go:23; dashboards.go generateName | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/instructions | tags at most 10 | accurate | dashboardtypes/perses_dashboard.go:24,354 | EF | none: SigNoz's error names the cap |
| dashboard/instructions | grid: 12 columns, x 0-11, width 1-12, items must not overlap | accurate | dashboardtypes/perses_replicas.go:332,408 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup (overlap) |
| dashboard/instructions | height "minimum 2" | misleading | perses_replicas.go:332: height at least 1; 2 or more is layout advice | C | fixed |
| dashboard/instructions | source is server-owned and not a list-filter key | accurate | plan pass 2 | EF | test_system_dashboard_is_hidden_but_gettable... |
| dashboard/instructions | DynamicVariable signal and attribute; ALL in a dynamic variable means no filter; $var in builder, ClickHouse, PromQL | accurate | querybuildertypesv5 variables (generated spec text) | W | test_dashboard_examples_create_replace_patch_and_delete |
| dashboard/instructions | ask before wiring a variable into panels | guidance | none | C | none: no SigNoz claim |
| dashboard/instructions | prose em dash in a label | style | docs/client-visible-writing-style.md | C | fixed |
| dashboard/widgets-instructions | query count per panel: exactly one; TextPanel queries []; null rejected | accurate | dashboardtypes/perses_dashboard_data.go:153; plan pass 2 | EF | test_text_panel_create_get_update_patch_defaults_layout_and_cleanup |
| dashboard/widgets-instructions | dry-run envelope per query plugin; requestType per panel kind; executor does not expand variables | accurate | plan pass 2 (saved panel queries dry-run unchanged) | EN | test_saved_panel_queries_dry_run_unchanged_through_execute_builder_query |
| dashboard/widgets-instructions | missing start or end fails with "missing start or end timestamp" | accurate | query_builder.go precondition and SigNoz validateTimeRange | EF | none: the handler's own precondition, handler tests |
| dashboard/widgets-instructions | legend rules per panel type | accurate | SigNoz panel rendering (UI) | C | none: UI rendering |
| dashboard/widgets-instructions | area, top list, scatter, geo map rules | accurate | plan pass 2 | EF | test_area_chart_panel_round_trips..., test_top_list_scatter_plot_and_geo_map_panels_round_trip |
| dashboard/widgets-instructions | heatmap: rejects AI builder and trace operators; one enabled query; no functions or having; bucketOptions rejected on histograms; exponential histograms unsupported; y scales | accurate | dashboardtypes/perses_replicas.go:101; querybuildertypesv5/validation.go:968-972,1049; heatmap.go:157,172; perses_signoz_plugins.go:1674 | EF | test_histogram_buckets_read_with_rate_and_sum (functions) |
| dashboard/widgets-instructions | thresholds color labels or backgrounds, no line marker; histogram, list, pie, table, value panel behaviour | accurate | SigNoz panel rendering (UI) | C | none: UI rendering |
| view/instructions | schemaVersion v2 filled in; server-populated fields stripped; name dropped on update | accurate | views.go | EF | test_view_crud_round_trip |
| view/instructions | SigNoz accepts a signal that differs from source | accurate | plan pass 2 | W | test_view_with_a_mismatched_signal_saves |
| view/instructions | the Explorer renders only matching views | accurate (frontend filters views by source) | SigNoz frontend; not in the backend | W | none: UI rendering (UNVERIFIED-10) |
| view/instructions | changing source moves the view; Cost Meter views are source meter with signal metrics | accurate | plan pass 2 | W | test_update_view_source_change_moves_view, test_view_examples_create_replace_and_delete |
| view/instructions | time-series limit selects groups over the whole range | accurate | querier applySeriesLimit (plan row 70) | W | test_query_metrics_limit_order_having_and_typed_formula_queries |
| example resources | request payloads (alert, dashboard, view, channel, query_range) are generated from the SigNoz spec | accurate (generated) | pkg/*/examples.generated.md; make gen | EF | test_spec_examples.py (all five run against SigNoz) |
| notification-channel/examples | config.spec fields by kind, required and secret flags | accurate (generated) | SigNoz spec (alertmanagertypes) | EF | test_every_provider_config_round_trips_and_update_preserves_it |
| dashboard/widgets-examples | name a column with the aggregation's alias field, not "as" in the expression | accurate | querybuildertypesv5/validation.go:344-348 "aliasing is not allowed in expression. Use alias field instead" | EF | test_aggregate_logs_several_aggregations_and_having |
| dashboard/widgets-examples | variables as $name; without $ the literal string matches | accurate | querybuildertypesv5 variables (generated spec text) | W | test_dashboard_examples_create_replace_patch_and_delete |
| dashboard/widgets-examples | every payload is a server-verified panel | accurate | dashboard_schema_test.go; e2e round trips | EF | test_area_chart_panel..., test_top_list_scatter_plot_and_geo_map_panels_round_trip |
| dashboard/examples | metrics order key is spaceAggregation(timeAggregation(metric)) or __result; the bare metric name is rejected | accurate | querybuildertypesv5/validation.go:544-553 | EF | none: SigNoz's error lists the valid keys (EF) |
| dashboard/examples, metrics guide | a pinned temporality overrides metadata; a wrong value returns no data | accurate | querier/querier.go:419-420; metricsstatementbuilder/statement_builder.go:565 | W | test_counter_reducers_say_what_they_measured |
| alert/examples | Key Notes: aggregation shapes, selectedQueryName, routing, required blocks, signed anomaly targets | accurate | ruletypes (as alert/instructions) | EF | test_alert_examples_create_and_delete |
| traces/query-builder-guide | operator set; no STARTS WITH, IS NULL, =~; no full-text search on traces | accurate | grammar/FilterQuery.g4; where_clause_visitor.go RejectsBodyFunction | EF | test_promql_matcher_error_names_regexp; test_trace_fields.py |
| traces/query-builder-guide | isRoot/isEntryPoint semantics, only "= true"; entry spans are requests | accurate | plan pass 1 rows 82-83 | W | test_is_root_selects_root_spans, test_entry_spans_count_each_request_once |
| traces/query-builder-guide | span columns, kind_string values, response_status_code is a string | accurate | telemetrytraces field definitions | W | test_group_by_resolves_custom_and_intrinsic_trace_fields |
| traces/query-builder-guide, logs guide | start/end in ms; the timestamp column is nanoseconds; an inline ms value silently matches everything | accurate | telemetry timestamp columns (DateTime64(9)) | W | none: arithmetic on the column type; not pinned |
| traces/query-builder-guide | SigNoz ignores "tag" when listing keys and returns no values | stale | as the get_field_keys row | W | none: text fixed |
| logs/query-builder-guide | bare word or quoted phrase is full-text search; search() scopes | accurate | grammar fullText; where_clause_visitor.go VisitSearchCall | W | test_explicit_search_scopes_quoting_and_warning_round_trip |
| logs/query-builder-guide | resource plus another context defaults to resource with a warning; other multi-context matches are ORed | accurate | querybuilder/key_resolution.go ResolveLogicalFields | W | none: as the logs row |
| logs/query-builder-guide | body paths filter everywhere, group and select only when listed | accurate | SigNoz body JSON fields | EF | test_unlisted_body_field_cannot_be_grouped |
| logs/query-builder-guide | severity_text = is case-sensitive; the shortcut matches any case | accurate | logs_helper.go; SigNoz string equality | W | test_severity_shortcut_matches_any_case_as_a_prefix |
| metrics-aggregation-guide | defaults table, reduceTo rules, bucket recipe, step alignment | accurate | plan pass 1 rows 84-88; rules.go | W | test_window_percentile_is_not_the_mean_of_step_percentiles, test_counter_reducers_say_what_they_measured |
| metrics-aggregation-guide | the window's percentile lies between what reduceTo min and max return | accurate | a mixture's quantile lies between its components' quantiles | W | test_window_percentile_is_not_the_mean_of_step_percentiles (min 16.667, max 96.875) |
| metrics-aggregation-guide | SigNoz rejects a percentile on a non-histogram and a non-delta exponential histogram | accurate | querybuildertypesv5/validation.go:389,400 | EF | none: SigNoz's error names the rule |
| metrics-aggregation-guide | formula functions: exp, log, ln, sqrt, sin, cos, tan, abs, ceil, floor | stale | querybuildertypesv5/formula.go:757-763 GetSupportedFunctions has no ceil or floor | EN | none: SigNoz's error names the undefined function (live: Undefined function ceil) |
| metrics-aggregation-guide | summaries: query .quantile with both aggregations | accurate | rules.go:50-52 | EF | none: the handler's own error names the fix |
| metrics-aggregation-guide | groupBy and filter keys need no context prefix; REGEXP for =~ | accurate | plan pass 1 rows 68-69 | EF | test_promql_matcher_error_names_regexp |
| ai/query-builder-guide | two-pass trace rows, window-only aggregations, token and cost definitions, per-trace keys, level rules, ordering, grouping | accurate | SigNoz AI statement builder | W | test_ai_traces.py (17 tests), test_ai_pricing_and_mappings.py |
| promql/instructions | quoted selector, dotted labels, forms that return no data | accurate | plan pass 1 row 90 | EN | test_window_percentile_is_not_the_mean_of_step_percentiles (PromQL), test_promql_matcher_error_names_regexp |
| dashboard/query-builder-example | histogram and summary suffixes are dotted; span metrics count spans | accurate | plan pass 2 rows 155-157 | EF | test_metadata_lookup_matches_the_exact_name, test_span_metrics_count_every_span_and_say_so |
| dashboard/query-builder-example | aggregation set including rate_sum and rate_avg | accurate | SigNoz aggregation registry | EF | test_aggregation_set_matches_backend |
| dashboard/query-builder-example | auto step: logs/traces window/300 floored to 5s, metrics at least 60s, at most 1,500 points | accurate | querybuilder/time.go:16-17 RecommendedStepInterval | W | test_window_percentile_is_not_the_mean_of_step_percentiles |
| dashboard/query-builder-example | 24 hours -> 5min steps (300 points) | stale | time.go RecommendedStepInterval: 86400/300 = 288, floored to 285 s | C | fixed |
| dashboard/query-builder-example | trace operator example joins two filter conditions with => | stale | querybuildertypesv5/trace_operator.go:43: the expression combines builder query names (A => B && NOT C) | EF | none: SigNoz's error names the unknown query |
| dashboard/query-builder-example | pitfall: "status = 'active' not status = active" | misleading | grammar/FilterQuery.g4: a bare word is a value | C | fixed |
| dashboard/query-builder-example | negative operators skip the existence check; positive ones add it | accurate | querybuildertypesv5/builder_elements.go AddDefaultExistsFilter | W | test_negative_filters_also_match_rows_missing_the_field |
| dashboard/query-builder-example | escape only \' and \; RE2 regex; precedence NOT > AND > OR | accurate | grammar QUOTED_TEXT; ClickHouse match() | EF | test_body_regexp_with_doubled_backslashes_matches_the_message |
| dashboard/query-builder-example | has/hasToken body JSON functions and their errors | accurate | querybuilder/key_resolution.go function doc URLs | EF | none: SigNoz's errors name the rule |
| dashboard/query-builder-example | meter data is bucketed hourly | accurate | querybuilder/time.go RecommendedStepIntervalForMeter (min 3600) | C | none: the e2e cast has no meter data |
| dashboard/query-builder-example | banned word "Leverage" | style | docs/client-visible-writing-style.md | C | fixed |
| ClickHouse schema resources (3) | tables, columns, types, indexes | accurate (generated) | generated from the collector migrations bundled with the server | EN | none: generated; SigNoz's own error names a wrong column (the descriptions say to follow it) |
| ClickHouse example resources (3) | resource CTE, ts_bucket_start window with a 1800 s lead, attribute access, rate with lagInFrame, GLOBAL IN on distributed tables | accurate | logsstatementbuilder/statement_builder.go (SigNoz builds the same CTE and GLOBAL IN); SigNoz docs pages cited in each resource | EN | none: the e2e cast is single-shard, and these SQL examples are not executed (spec ClickHouse requests run in test_query_builder_examples_execute) |
| docs/sitemap | the indexed docs catalog | accurate (generated) | internal/docs corpus from signoz.io/docs/manifest.json | C | internal/docs golden tests |
| `signoz_query_metrics` | decisions note: "stepInterval: Ns (caller-provided)" after SigNoz raised the step | stale (result text) | metrics_query.go:106-118, 237-242; SigNoz raises a step below 60 s (data.meta.stepIntervals) | W | test_counter_reducers_say_what_they_measured (10 s requested, 60 s used) |
| signoz_aggregate_logs, signoz_aggregate_traces | an alias in the expression (count() as total) | missing | querybuildertypesv5/validation.go:344-348: "aliasing is not allowed in expression. Use alias field instead", a field these tools do not have | EN | test_aggregate_logs_several_aggregations_and_having |
| signoz_aggregate_traces, signoz_aggregate_logs | an orderBy that names the query (A) fails | accurate | querybuildertypesv5/validation.go:556-590: the error lists the valid keys | EF | test_order_keys_the_guides_teach |
| promql/instructions | single quotes are wrong in a quoted selector | stale | live SigNoz returns the same series for {'X'} and {"X"}; SigNoz's own spec text uses single quotes | C | test_window_percentile_is_not_the_mean_of_step_percentiles |
| generated condition.compositeQuery.queries | envelope types per queryType (narrower than the alert guide's table) | consistency, not established | ruletypes/alerting.go:122 states the rule; no check enforces either list at save | EF | none: which combinations evaluate needs firing rules |
| prompts (4) | tool names, parameters, and steps match the tools | accurate | tool schemas; plan pass 2 | C | test_logs.py, test_trace_fields.py |
| latency_analysis prompt | description "Query latency metrics" | misleading | pkg/prompts/prompts.go: the steps use traces only | C | fixed |
| manifest.json | signoz_get_trace_details "return every span's complete record" | stale | internal/client/client.go:727: at most 1,000 spans | C | fixed |
| manifest.json | the other 47 tool entries | accurate | tool descriptions | C | none: no SigNoz claim beyond the tools' |
| coded errors | parameter validation messages name the field and the tool to call | accurate | handler code (78 call sites) | EF | tool_error_codes_test.go |
| coded errors | scoped-role telemetry denial: resource.service.name, resource.deployment.environment.name, signoz.workspace.key.id; PromQL and ClickHouse not key-scoped | accurate | telemetrytypes/selector.go telemetryGrantKeys, telemetryGrantQueryTypes | EF | none: needs a scoped role (enterprise authorization) the e2e cast does not configure |
| coded errors | 401 and 403 recovery: re-authenticate or request access | accurate | errs.go upstreamError | EF | test_invalid_api_key_surfaces_coded_error, test_unknown_rule_id_is_coded_not_found |

### Formerly unverified claims

| Claim (where it was marked not verified) | Verdict | Checked against | e2e |
|---|---|---|---|
| A pinned temporality returns no data (metrics guide, first pass) | accurate | `querier/querier.go:419-420` fills an unknown temporality from metadata; `metricsstatementbuilder/statement_builder.go:565` filters samples by a pinned one | `test_counter_reducers_say_what_they_measured` (delta on a cumulative counter returns nothing) |
| Order and limit on a grouped alert query cap evaluation to the top N | accurate | `logsstatementbuilder/statement_builder.go:577-582` builds `__limit_cte`; the querier's `applySeriesLimit` for metrics; rules run the same builders | none: needs an alert to fire, or not, on a series outside the top N |
| labels.severity does not set a fired alert's severity | accurate | `ruletypes/threshold.go:346-347` stamps the tier name as `threshold.name` and, lower-cased, as `severity`; `ruletypes/labels.go:309-317` skips rule labels with reserved names; `listable_rule.go:159` reads the rule's own label | none: severity is visible only on a fired alert |
| Alerts evaluate the first aggregation | accurate | `query-service/rules/threshold_rule.go:177-183` reads `Aggregations[0]` | none: needs a two-aggregation rule to fire |
| Metric timeAggregation defaults by type for alerts | stale | `metricsstatementbuilder/statement_builder.go:258-270` rewrites only histograms; `metricstelemetryschema/const.go:353-357` returns "invalid time aggregation"; live: such a rule saves | `test_rule_on_a_missing_metric_saves` (the rule saves; its dry-run fails) |
| Evaluation windows are Go durations, largest unit h | accurate | `ruletypes/evaluation.go:38-39` (`valuer.TextDuration`); live: `time: unknown unit "d"` | `test_create_alert_unknown_channel_rejected` |
| Removing a panel without its grid item, overlapping items, and a second query are rejected | accurate | `dashboardtypes/perses_dashboard_data.go:330` (unknown panel), `perses_replicas.go:408` (overlap), `perses_dashboard_data.go:153` (one query) | `test_text_panel_create_get_update_patch_defaults_layout_and_cleanup` (the first two; a second query needs a query panel) |
| Heatmap panel rules | accurate | `dashboardtypes/perses_replicas.go:101`; `querybuildertypesv5/validation.go:968-972,1049`; `heatmap.go:157,172` | `test_histogram_buckets_read_with_rate_and_sum` (functions rejected) |
| List filter: REGEXP rejected, EXISTS only on tag keys | accurate | `dashboardtypes/list_filter.go` lists REGEXP in its operator sets, but the live compiler rejects it ("REGEXP filtering on name is not supported"); EXISTS is only in `TagKeyOps` | `test_text_panel_create_get_update_patch_defaults_layout_and_cleanup` |
| The explorer shows only views whose signal matches | not verifiable here | the frontend lists views by `sourcePage`; rendering a mismatched view needs the UI | none: UI |
| signoz_list_alerts takes Alertmanager label matchers =, !=, =~, !~ | accurate | `alertmanagertypes/alert.go:270-280` parses each filter with Alertmanager's `compat.Matcher` | none: matching needs a firing alert; live, a regex matcher is accepted |
| ClickHouse examples use GLOBAL IN | accurate | SigNoz's own statement builders join distributed tables with `GLOBAL IN` (`logsstatementbuilder/statement_builder.go:582`) | none: the e2e cast is single-shard, where a plain IN also works |
| An alertType that does not match the query signal (second pass: effect unknown) | established | `query-service/rules/threshold_rule.go:295-313`: evaluation runs the query whatever alertType says; alertType picks the related traces or logs link in a fired alert's annotations, and the rule list's `alert_type` | none: the link is only in a fired alert's notification; the alert guide now says this |

### Order keys and aliases (the third SigNoz/nerve-pod#482 example)

- Taught today: the traces and logs guides say an aggregate orders by an aggregation expression, an
  index, or a groupBy key, and that the query name (A), `__result`, and `timestamp` are not order keys
  there; the aggregate tools' `orderBy` parameters say the same; the dashboard examples resource gives
  the metrics order key (`spaceAggregation(timeAggregation(metric))` or `__result`); the widgets
  examples resource says to name a column with the aggregation's `alias` field, not `as` inside the
  expression.
- SigNoz's errors: an order key that is not valid now reads `invalid order by key 'A' for order by
  clause #1 in query 'A'`, followed by the valid keys and suggestions
  (`querybuildertypesv5/validation.go:556-590`), and the client sees all of it. That error states
  its fix, so no text was added; `test_order_keys_the_guides_teach` now pins it.
- `aliasing is not allowed in expression. Use alias field instead`
  (`querybuildertypesv5/validation.go:344-348`) names a field that `signoz_aggregate_logs` and
  `signoz_aggregate_traces` do not have. Their `aggregations` descriptions now say to write no
  alias; `test_aggregate_logs_several_aggregations_and_having` pins the rejection.

### Totals

- Surfaces: 80, each read in full: the instructions, 48 tools, 25 resources, 4 prompts, the
  manifest, and the coded errors.
- Claim rows: 287 across the three passes (45, 65, and 177).
- Status after this pass: 230 accurate, 36 stale or misleading, 15 missing, 3 style violations, 1
  not verifiable here (the explorer's rendering of a mismatched view), 1 open consistency item (the
  envelope types an alert query accepts), and 1 guidance row with no SigNoz claim. Every stale,
  misleading, missing, and style row is fixed except the open item.
- The 12 claims the earlier passes marked not verified: 10 accurate, 1 stale and fixed, 1 not
  verifiable here. Six of them now have an e2e assertion.

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
- Bug fixes: `internal/handler/tools/{metrics_query,metrics_helper,services}.go`; e2e in
  `test_{service_map,service_listing,spec_examples}.py`.
- Second pass: alert, dashboard, view, and channel descriptions; `internal/handler/tools/errs.go`
  and `notification_channels.go` recovery text; `pkg/alert/resources.go`, `pkg/dashboard/query.go`;
  e2e in `test_{alerts,dashboards,logs,notification_channels,saved_views,query_response_paths}.py`.

## Key Decisions

### 2026-11-21 — Raise the instructions budget to 4,096 bytes

- Decision: `MaxServerInstructionsBytes` 2,048 → 4,096; the 512-byte preamble budget stays and now
  holds the routing.
- Rationale: the clients this server targets keep 4,096 characters of server instructions, and
  client guidance asks that the first 512 characters stand alone. The instructions are 3,912 bytes
  with a 510-byte preamble.

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

A review of the measurement notes only raised M1 to M16 against
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

### 2026-11-22 — Seventh review: a confirming round

A round at the same settings, limited to `e102b2d`, confirmed N1, N3, N4, N6's edge sources, N9,
and N10, and raised P1 to P5.

- P1: with `le` among several group keys, the span-metric note said to group "by the same keys".
  It now names the keys it keeps.
- P2: predicate keys are now read as keys: a word followed by a comparison or an operator keyword,
  lower-cased, without the field-context prefix SigNoz drops (`span.isRoot`,
  `attribute.operation`). An unquoted value that reads like a key no longer silences a note, and
  `kind = 2`, the server kind's number, counts as a server filter.
- P3: the formula-input note is not emitted for a metric input SigNoz skipped.
- P4: the service map units note says to use the same window, because `signoz_aggregate_traces`
  defaults to one hour and the map to six, and describes the population as the caller's client
  spans to every destination.
- P5: the service map e2e asserts "come from the callee's spans", since "callee" alone also
  matched another clause; the percentile test asserts the reducer, percentile, and step. The
  step-decoder test gains a malformed entry beside a valid one.

These fixes have not been reviewed again.

### 2026-11-22 — Bugs next to this PR's texts

Four bugs this PR's texts sit on, fixed here because each is small and the texts would otherwise
describe them.

- Whether the caller sent isMonotonic is the field itself: a `*bool`, nil when it was not sent.
  It first shipped as a second `IsMonotonicSet` flag kept in step by hand; a request built without
  the parser would have left the flag false and let the lookup override the caller's value. The
  published schema is unchanged.
- `metricType` alone skipped the metadata lookup (SigNoz/nerve-pod#674 item 6), so `metricType:
  sum` defaulted to avg and the counter notes stayed off. With `metricType` set, the tool now
  fetches whichever of temporality and isMonotonic the caller left out, for the primary query and
  formula sub-queries; a value the caller sent wins. A gauge needs neither, and only a sum needs
  monotonicity. A failed lookup or a metric missing from the metadata keeps the query running on
  the caller's values, WARN-logged, and the decisions note says the value is unknown; a 401 or 403
  is returned as a coded error. The fetched temporality feeds the defaults and the notes and is not
  sent to SigNoz for these callers or for sub-queries, so their requests are what they were (see
  the eighth review below). The metrics guide's mistake
  about `metricType` alone is removed, and the isMonotonic and temporality descriptions say a sent
  value overrides the metadata.
- The `routing_channels` e2e fixture read the channel id from `data.id`, which the create result
  does not carry, so it deleted no channel (SigNoz/nerve-pod#674 item 13). It uses `dig_id` and fails
  on a missing id.
- The service map units note sent caller-latency questions to client spans without saying they
  pool every destination, and client spans to different services can share a span name. The note
  and the traces `groupBy` description now say to filter or group by a destination attribute such
  as `server.address`, as an example, since instrumentation decides which attributes exist.
- `signoz_list_services` now says callRate is numCalls divided by the whole requested window, so a
  service active for part of it reads lower than its rate while active.

### 2026-11-22 — `resources/read` spans carry the resource URI

- Decision: a `resources/read` span records `mcp.resource.uri`. A registered resource records its
  URI; any other URI records `unknown`, because a client can ask for anything. The URI is not a
  metric attribute.
- Rationale: SigNoz/nerve-pod#667's done bar asks for it. Without it no OpenTelemetry signal says
  which guide was read: the span carried only the method name, and the analytics event that holds
  the URI is off by default. This was out of scope at first (decision 5); the maintainer moved it
  in so this PR can close the issue.
- Check: `TestResourceReadSpansNameOnlyRegisteredResources` reads a guide and a made-up URI through
  a real session and checks the span attribute and the method metric.

### 2026-11-22 — Eighth review: the commits pushed without one

Three behaviour commits (the seventh review's fixes, the third audit pass, and the four bug fixes)
were pushed before any review. A review of everything since the last reviewed commit, including
the `mcp.resource.uri` attribute and the `IsMonotonic` refactor, raised D1 to D10.

- D1: with `metricType` omitted, the metadata overwrote a caller's `isMonotonic` and `temporality`,
  though the descriptions now say a sent value wins. A sent value wins on that path too, for the
  primary query and formula sub-queries.
- D2: the fix for `metricType` alone had begun sending the fetched temporality to SigNoz for those
  callers and for sub-queries. SigNoz's catalog holds one temporality with no time window, while
  the query path resolves an omitted one over the requested window, so pinning it can drop series
  for a metric whose temporality changed. The fetched value now feeds only the defaults and notes
  on those paths. A primary query without `metricType` still sends the fetched temporality, as it
  did before this PR; that is listed as a follow-up.
- D3: a 401 or 403 from the metadata lookup is returned as a coded error, not swallowed by the
  fallback, as the repo's rule for upstream auth failures requires.
- D4: the filter reader had dropped every field context, so `scope.name` read as the span's `name`
  and silenced the all-spans note. A key now keeps its context and matches bare or under the one
  context SigNoz accepts for it (`span.` for span fields, `resource.` for the service).
- D5: "SigNoz returns no keys and no values for `tag`" was too strong; the text now says SigNoz
  does not read `tag` as `attribute` there.
- D6: the alert guide said to set `timeAggregation` on every query but a histogram percentile. A
  histogram bucket-count query needs none either, so it now says every gauge and sum query. The
  e2e reads the saved rule back and checks SigNoz stored no default.
- D7 to D10: the singular `CONTAIN` and `EXIST` operators; `erf`, `erfc`, `lgamma`, and `tgamma`
  in the metrics guide's function list; the step examples in the dashboard query example labelled
  as traces and logs; and the README table's "all spans" for trace details.

A second round resolved D1 to D10, accepted the part of D2 that was declined, and raised three more:

- D11: metric filters read every field context from one label set, so on a span metric
  `attribute.service.name` is the service and `resource.operation` picks one operation. The metric
  rules now match a label bare or under any of those contexts; trace filters still keep contexts
  apart.
- D12: the alert guide's sentence left out summaries, whose quantile series also need a
  `timeAggregation`.
- D13: SigNoz reads `spanfield.` as `span.`, so `spanfield.isRoot` counts as a root filter.

A third round resolved D11 to D13 and approved. It left one nit, declined as too rare: D14, a key
written with a data-type suffix such as `resource.operation:string` is not recognised.

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
- Third audit pass: the handler tests cover the step the decisions note now names (requested
  10 s, used 60 s; and a matching step). e2e assertions added to existing tests:
  `test_counter_reducers_say_what_they_measured` (a wrong pinned temporality, the raised step),
  `test_create_alert_unknown_channel_rejected` (a 1d window), `test_rule_on_a_missing_metric_saves`
  (a rule without timeAggregation saves and its dry-run fails),
  `test_text_panel_create_get_update_patch_defaults_layout_and_cleanup` (patch and list-filter
  rejections), `test_histogram_buckets_read_with_rate_and_sum` (heatmap functions),
  `test_aggregate_logs_several_aggregations_and_having` (alias), `test_order_keys_the_guides_teach`
  (order key), and `test_window_percentile_is_not_the_mean_of_step_percentiles` (single-quoted
  PromQL). The full e2e suite passes on a fresh v0.151.0 cast (118 passed, 1 skipped as on main),
  and `make ci` passes.
- Bugs next to this PR's texts: handler tests cover `metricType` alone on a cumulative counter
  (rate by default; `counter_running_total` on latest), the caller's values winning, a failed
  lookup, a metric missing from the metadata, and a formula sub-query. e2e assertions:
  `test_counter_reducers_say_what_they_measured` (`metricType` alone fetches both values),
  `test_service_map_reports_one_edge_from_both_ends` (two callees: grouping client spans by name
  pools them, by `server.address` keeps each latency),
  `test_services_sorted_by_name_and_paged_without_gaps` (callRate is numCalls over 3,600 s), and
  `test_spec_examples.py` run twice on one cast with no channel left behind (the create result is
  `{channel, testNotification}`, so `data.id` was always empty). The touched files pass on a fresh
  v0.151.0 cast (22 of 22), the full suite passes (118 passed, 1 skipped as on main), and
  `make ci` passes.
- `GOTOOLCHAIN=go1.26.11 make ci` with a temporary module cache (a locally modified cache entry
  fails `go mod verify`) and protocol and conformance port overrides (another local process holds
  port 18080). Full e2e suite against v0.151.0.

## Outcome

Shipped as planned, plus the `step_alignment` rule the eval found, the fixes from four
review rounds (C1 to C28, less the declined C13, C14 and C26), and the second and third audit
passes with their fixes. The third pass covers all 80 client-visible surfaces and resolves the 12
claims the earlier passes left unverified (see "Formerly unverified claims"). Left open:

- Explorer rendering of a view whose signal does not match its source: needs the UI.
- The envelope types an alert query accepts: SigNoz's generated field text and the alert guide
  name different sets, and SigNoz enforces neither at save; which combinations evaluate needs
  firing rules.
- SigNoz-dependent claims whose e2e column says why there is no test: most need an alert to fire,
  a UI to render, a scoped role, meter data, or a multi-shard cluster.
- Review findings C13, C14, and C26 (a sentence that predates this PR; a limit-plus-offset formula
  input; an offset carried into the retried bucket query).
- Measurement-note cases left as too rare in the fifth review (M8, M12, M13, and parts of M1, M3,
  M5, M6 and M11), and M2 and N12, declined.
- Skill changes are tracked in SigNoz/nerve-pod#436; behaviour bugs are in SigNoz/nerve-pod#674,
  of which items 6 and 13 are fixed here (see "Bugs next to this PR's texts").
