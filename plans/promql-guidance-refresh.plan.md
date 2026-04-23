# Plan: PromQL guidance refresh

## Status
Done

## Context
The project already ships a PromQL guide under `signoz://dashboard/promql-example`, but (a) the URI implies it's dashboard-specific, and (b) the content is missing the Prometheus 3.x UTF-8 quoted-selector framing, the anti-pattern table, a combined dotted-label histogram_quantile example, and the PromQL-alert pre-flight checklist that the user provided. LLMs generating `promql_rule` alerts or PromQL dashboard widgets don't have a single canonical reference.

## Approach

### Content enrichment (`pkg/dashboard/query.go::PromqlQuery`)
Append three sections to the existing constant:
1. **Prometheus 3.x UTF-8 quoted selector** — explicit framing of `{"metric.name.with.dots"}` as the canonical form; anti-pattern table (underscored conversion / `__name__` selector with dots / bare dotted name — all return no data).
2. **Histogram quantile with dotted labels** — combined example showing dotted metric name + dotted resource attribute in `by()` and label matchers.
3. **PromQL alert pre-flight checklist** — step-by-step: `signoz_list_metrics` → `signoz_query_metrics` → `signoz_get_field_keys` to verify `le` label → after create, `signoz_get_alert` and check state (inactive on a known-breached metric ⇒ query isn't resolving).

### Resource URI generalization
Rename the registered MCP resource URI from `signoz://dashboard/promql-example` to `signoz://promql/instructions` in `internal/handler/tools/dashboards.go`. Update the two existing references to the old URI (lines 53, 79) to the new one. Keep the constant location.

### Cross-referencing PromQL surfaces

- `internal/handler/tools/alerts.go`:
  - `signoz_create_alert` description — add a bullet: "PromQL rules (`ruleType=promql_rule`): MUST read `signoz://promql/instructions` before writing the query. SigNoz needs the Prometheus 3.x UTF-8 quoted selector form (`{"metric.name.with.dots"}`) for OTel metrics."
  - `signoz_update_alert` — same note.

- `pkg/alert/resources.go` Instructions — extend the existing "PromQL / ClickHouse query spec" section with a "PromQL for OTel metrics" callout pointing at `signoz://promql/instructions`.

- `pkg/types/alertrule.go::AlertQuerySpec.Query` description — mention the dotted-name rule and point at `signoz://promql/instructions`.

- `pkg/types/dashboard.go::PromQL.Query` description — same rule + resource reference.

### Skip

- No changes to `pkg/views/*` — saved views explicitly don't support PromQL.
- No changes to `signoz_query_metrics` / `signoz_execute_builder_query` — they don't accept raw PromQL as a separate mode; they go through the v5 builder. If the user wants ad-hoc PromQL, they use `signoz_execute_builder_query` with `queryType=promql` inside the payload, and the payload docs already come from `signoz://promql/instructions` (after the rename).
- No new MCP resource registration — just a rename.

## Files to Modify
- `pkg/dashboard/query.go` — append sections to `PromqlQuery` constant.
- `internal/handler/tools/dashboards.go` — rename resource URI, update two in-description references, update resource description to reflect generic scope.
- `internal/handler/tools/alerts.go` — add PromQL-rule pointer in create/update descriptions.
- `pkg/alert/resources.go` — add PromQL for OTel metrics callout in the Instructions constant.
- `pkg/types/alertrule.go` — enrich `AlertQuerySpec.Query` description.
- `pkg/types/dashboard.go` — enrich `PromQL.Query` description.
- `plans/promql-guidance-refresh.context.md` — append dated discussion log entries as decisions land.

## Verification
- `go build ./...` and `go test -count=1 ./...` (no behaviour changes expected; only string-constant and description edits).
- Load `signoz://promql/instructions` via the MCP inspector and verify the new sections render cleanly.
- Spot-check: give a fresh Claude session a prompt like "alert me when p99 of payment.latency.ms is above 500ms" and confirm it reads `signoz://promql/instructions` and produces a query using `{"payment.latency.ms.bucket"}`.
- Grep: `rg -n 'signoz://dashboard/promql-example' internal/ pkg/ README.md manifest.json` — should return zero hits after the rename.
