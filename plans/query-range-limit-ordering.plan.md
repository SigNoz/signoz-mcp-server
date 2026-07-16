# Plan: Explicit QueryRange Limits and Ordering

## Status
Complete — raw and aggregate defaults are 100; server, companion skills, full checks, and fresh live MCP verification are synchronized.

## Context
Issue [SigNoz/nerve-pod #132](https://github.com/SigNoz/nerve-pod/issues/132) requires Query Range payloads authored by the MCP server and companion skills to carry an explicit limit and ordering instead of inheriting backend defaults. The final owner-selected default is 100 for raw rows and aggregate groups. Metric payloads were previously unbounded/unordered, formula bounds were dropped by the typed round trip, and several embedded/companion examples omitted the fields.

The implementation must preserve the semantic distinction between raw row limits and scalar/time-series group limits. It must also surface the known tradeoff: top-N time-series selection ranks groups over the whole window and can hide a locally significant series.

## Contract

| Query Range envelope | Request type | Omitted/zero limit | Default wire `order` | Notes |
|---|---|---:|---|---|
| logs `builder_query` | raw | 100 | `timestamp desc`, then `id desc` | Stable offset pagination |
| traces `builder_query` | raw / trace | 100 | `timestamp desc` | `signoz_get_trace_details` keeps its explicit 1000 limit |
| logs/traces `builder_query` | scalar / time_series | 100 | primary aggregation desc | Positive tool/caller overrides win |
| metrics `builder_query` | scalar / time_series | 100 | `__result desc` | Apply to every generated/query payload member |
| `builder_formula` | scalar / time_series | 100 | `__result desc` | Model and preserve fields in `FormulaSpec` |
| PromQL / ClickHouse SQL | any supported | unchanged | unchanged | These envelopes do not use the builder contract |
| trace operator / join / subquery / future raw envelope | type-specific | unchanged in V1 | unchanged in V1 | Preserve raw JSON; do not inject a generic invalid order |

`orderBy` remains the high-level aggregate-tool parameter; `order` is the v5 wire field; dashboard `orderBy` remains a separate editor-model field.

## Approach

### 1. Centralize typed defaults without clamping authored positive values
- Add named defaults in `pkg/types/querybuilder.go`; both raw rows and scalar/time-series groups default to 100 while remaining separately named for semantic clarity.
- Refactor `QueryPayload.Validate()` into validation/defaulting followed by a second typed bounds pass so composite-query order cannot affect which global `requestType` is used.
- For `builder_query`:
  - reject negative limits;
  - treat omitted, null, and zero limits as requests for the type-aware default;
  - accept integer and numeric-string limits at the raw Query Builder boundary; reject fractional or malformed values with a recovery-oriented error;
  - preserve every positive limit;
  - fill only an empty order with a signal/request-type-safe value;
  - preserve every non-empty authored order.
- Extend `FormulaSpec` with non-omitempty `Limit` and `Order`, preserve authored values during round trip, and apply scalar/time-series omission defaults.
- Make new bounds errors name `spec.limit`, the received value, accepted forms/default behavior, and a corrective example. Reject dashboard/editor `orderBy` inside raw v5 specs with guidance to use wire-level `order` rather than silently dropping it.
- Leave PromQL, ClickHouse SQL, and raw-preserved envelope specs unchanged. Do not mutate untyped maps in the handler.

### 2. Align every MCP-generated Query Range payload
- Keep search-log/search-trace defaults at 100 and pin them to the shared raw constant.
- Add `id desc` after `timestamp desc` for generated raw log searches, matching the official pagination contract.
- Change the aggregate parser and tool schemas from an omitted-input default of 10 groups to 100. Retain caller-provided positive limits/order, the existing 10000 clamp, and clamp notes.
- Put `limit: 100` and `order: __result desc` on every metrics `builder_query` and generated `builder_formula`.
- Remove the unused malformed `BuildMetricsQueryPayload` helper and make `BuildMetricsQueryPayloadJSON` marshal the shared typed `QueryPayload`/`FormulaSpec` path so formulas, source propagation, defaults, and future changes cannot drift.
- Preserve `signoz_get_trace_details` at its explicit 1000 limit; add a regression assertion documenting the exception.

### 3. Update MCP authoring guidance and metadata
- Position `signoz_execute_builder_query` as the raw escape hatch: raw rows use search tools first, grouped/top-N work uses aggregate tools, metrics use `signoz_query_metrics`, and only unsupported multi-query/formula shapes fall back to raw Query Builder.
- Route agents to the signal-specific guide instead of requiring the traces guide for every query: logs, traces, metrics/formulas, and PromQL each point to their own resource.
- In the Query Builder tool description and resources, state that every authored `builder_query`/`builder_formula` must carry a positive limit plus explicit order, with the type-aware defaults above.
- Update raw, scalar, time-series, metric, Cost Meter, and formula examples. Examples that intentionally demonstrate top N may use a smaller positive limit; otherwise use 100.
- Add the whole-range time-series ranking caveat from the official Result Manipulation docs.
- When `signoz_execute_builder_query` inserts a limit/order, append an agent-visible decisions note. Add the same effective bounds to `signoz_query_metrics`' existing decisions note.
- Update `README.md` aggregate defaults and `signoz_execute_builder_query` guidance.
- Update affected `manifest.json` descriptions so published metadata mentions the changed aggregate/metric defaults and raw-builder requirement.
- Review `docs/` for Query Range examples; record “no additional docs changes” in the PR summary if none are found.

### 4. Update the companion `SigNoz/agent-skills` contract
Implement this as a linked companion PR, deliberately stacked on or rebased after the current `codex/align-signoz-mcp-contracts` work.

Update these six workflow files and their six `evals/evals.json` files:
- `plugins/signoz/skills/signoz-creating-alerts/SKILL.md`
- `plugins/signoz/skills/signoz-creating-dashboards/SKILL.md`
- `plugins/signoz/skills/signoz-modifying-dashboards/SKILL.md`
- `plugins/signoz/skills/signoz-generating-queries/SKILL.md`
- `plugins/signoz/skills/signoz-reducing-telemetry-cost/SKILL.md`
- `plugins/signoz/skills/signoz-investigating-alerts/SKILL.md`

Audit `plugins/signoz/skills/signoz-managing-views/SKILL.md` and its evals for the read-to-write contract. It delegates query construction to `signoz-generating-queries`, but copied `compositeQuery.queries` must retain the canonical limit and ordering field for the destination model.

Update the two full-payload references:
- `plugins/signoz/skills/signoz-reducing-telemetry-cost/references/cost-meter-queries.md`
- `plugins/signoz/skills/signoz-investigating-alerts/references/baseline-comparison.md`

Update both byte-identical dashboard execution crosswalks so saved `limit` +
editor `orderBy` translate losslessly to v5 `limit` + `order`, including formula
bounds and deliberate smaller list-page limits:
- `plugins/signoz/skills/signoz-creating-dashboards/references/dashboard-to-query-builder-v5.md`
- `plugins/signoz/skills/signoz-modifying-dashboards/references/dashboard-to-query-builder-v5.md`

Skill/eval invariants:
- dashboard/editor queries use `limit` plus editor-model `orderBy`;
- `signoz_execute_builder_query` calls use `limit` plus v5 `order`;
- raw defaults to 100/timestamp desc;
- scalar/time-series defaults to 100/result-or-primary-aggregation desc;
- every authored `builder_query` and `builder_formula` is covered;
- guidance names the whole-range time-series truncation risk.

No skill-schema change is needed for `signoz_query_metrics`, `signoz_search_logs`, `signoz_search_traces`, or `signoz_aggregate_*`: the MCP server owns those generated payloads.

## Files to Modify

### `signoz-mcp-server`
- `pkg/types/querybuilder.go` — defaults, typed second pass, formula fields, metrics-builder consolidation, log tie-breaker.
- `pkg/types/querybuilder_test.go` — table-driven request-type defaults, preservation, formulas, exclusions, and round-trip coverage.
- `internal/handler/tools/aggregate_helper.go` — aggregate omitted-input default 100.
- `internal/handler/tools/aggregate_helper_test.go` — default, explicit override, and existing clamp behavior.
- `internal/handler/tools/logs.go` / `internal/handler/tools/traces.go` — schema descriptions/defaults.
- `internal/handler/tools/metrics.go` — generated-bounds behavior in the tool/resource description.
- `internal/handler/tools/query_builder.go` — explicit authoring contract in the tool description.
- `internal/handler/tools/param_schema_test.go`, `internal/handler/tools/silent_failures_test.go`, and `internal/handler/tools/metrics_query_test.go` — recovery errors and agent-visible decisions notes.
- `internal/handler/tools/metrics_query_test.go` and targeted search/aggregate handler tests — capture exact outbound JSON.
- `internal/client/client_test.go` — pin the explicit 1000-limit trace-details exception.
- `pkg/types/alertrule.go` — expose alert builder/formula limit and v5 order in the typed MCP schema.
- `pkg/alert/resources.go` / `pkg/alert/resources_test.go` — bounded executable alert examples and a drift guard.
- `pkg/dashboard/widgets_examples.go` / `pkg/dashboard/dashboardbuilder/testdata/full.json` — editor-model limit/orderBy guidance and a full fixture.
- `pkg/views/instructions.go` / `pkg/views/examples.go` — saved-view limits, v5 ordering, and copy-losslessly guidance.
- `internal/handler/tools/read_write_back_contract_test.go` — dashboard/alert/view preservation tests.
- `pkg/querybuilder/logs_guide.go` / `pkg/querybuilder/traces_guide.go` — compliant examples and caveat.
- `pkg/metricsrules/guide.go` — metrics, formula, and Cost Meter examples with bounds/order.
- `README.md` / `manifest.json` — public docs and metadata sync.
- `plans/query-range-limit-ordering.context.md` / `plans/query-range-limit-ordering.plan.md` — planning audit trail.

### `agent-skills`
- The six workflow files, six existing eval files, and two reference files listed above.

## Verification

### MCP server unit and contract tests
- Table-test raw/scalar/time-series omission defaults for logs, traces, and metrics.
- Assert explicit positive limits and non-empty order arrays survive unchanged.
- Assert zero follows the owner-approved policy and negative limits fail locally.
- Assert integer and numeric-string builder/formula limits normalize identically; fractional, malformed, and raw-spec `orderBy` inputs return actionable validation errors.
- Assert every member of a multi-query metrics/formula payload has the correct limit/order.
- Assert `FormulaSpec` preserves caller-authored limit/order through unmarshal, validation, and re-marshal.
- Assert PromQL, ClickHouse SQL, trace operators, joins, and other raw-preserved specs remain byte-preserved and receive no builder-only fields.
- Assert raw logs use timestamp/id desc; raw traces use timestamp desc.
- Capture outbound payloads for search, aggregate, query-metrics, trace-details, and execute-builder handlers.
- Assert aggregate tool schemas and README advertise 100, matching raw search's numeric default while retaining request-type-specific ordering.
- Parse representative raw, aggregate, metric, and formula guide examples through `QueryPayload.Validate()` and assert their serialized bounds/order.
- Extend dashboard/alert/view read-to-write fixtures so query specs retain the destination model's canonical ordering field and limit.

Run:
- `gofmt` on changed Go files.
- `go test ./pkg/types ./internal/handler/tools ./internal/client`
- `go test ./...`
- `go vet ./...`
- `go build ./...`
- `git diff --check`

### Live SigNoz verification
Delegate live verification to a subagent per repository rules. Use read-only fixed-window queries and do not print credentials. Verify:
- raw logs accept timestamp/id ordering and paginate stably;
- raw traces accept timestamp ordering;
- log/trace scalar and time-series queries accept primary-aggregation ordering with limit 100;
- metric scalar/time-series and formula queries accept `__result desc` with limit 100;
- trace request type queries accept the raw trace default and preserve the explicit trace-details exception;
- omitted versus explicit-default requests have the expected bounded shape and compatible results.

No resources should be created. The subagent must still explicitly report that cleanup was unnecessary, which fields were accepted server-side, and any response-shape differences.

### Agent-skills verification
- Validate all edited eval JSON with `python3 -m json.tool`.
- Run `git diff --check`.
- Run `claude plugin validate --strict plugins/signoz`.
- Run the focused evals for the six affected skills and confirm the grader checks both dashboard `orderBy` and execution `order` shapes.

## Observability
- Invalid default order keys fail loudly through the existing coded upstream-error path; caller-authored invalid bounds fail locally with recovery instructions.
- Existing debug payload logging and `mcp.query.payload` trace attributes expose the exact outbound request shape.
- `signoz_execute_builder_query` and `signoz_query_metrics` surface the effective injected bounds in agent-visible decision notes so bounded/truncated behavior is not silent.
- Because omission/default serialization is fully unit-testable, visible to the caller, and backend acceptance is covered live, no new runtime metric is required for V1.

## Out of Scope
- A new hard cap equal to the 100-item default; existing 10000 search/aggregate limits remain.
- New public `limit` or `orderBy` parameters on `signoz_query_metrics`.
- A complete generated nested JSON Schema for the raw `query` object, tolerant normalization of unrelated top-level numeric fields, or new completion tools; track those broader MCP-surface improvements separately.
- Generic mutation of PromQL, ClickHouse SQL, trace-operator, join, subquery, or future raw envelope specs.
- Changing the comprehensive `signoz_get_trace_details` limit from its explicit 1000.
- Mitigating top-N time-series blind spots beyond documenting them and allowing explicit positive overrides in authored builder queries.

## PR Coordination
- Commit this plan pair with the MCP server implementation.
- Open linked MCP server and agent-skills PRs; each PR body must state the other repo's status and link the companion PR when available.
- Preserve the companion checkout's unrelated pre-existing MCP-setup/README edits; this feature modifies only its seven query-authoring workflows, seven eval files, two full-query references, and the two byte-identical dashboard execution crosswalks.
- In the MCP server PR summary, explicitly mention README/resource/manifest updates, the agent-skills contract change, the `get_trace_details` exception, and the live contract verification result.
