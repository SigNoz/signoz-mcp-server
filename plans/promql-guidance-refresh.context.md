# Feature: PromQL guidance refresh — Context & Discussion

## Original Prompt
> Update the promql usage throughout the tools to use the below instructions. Eg querying metrics, using promql to set alerts. Find all types and references where promql is supported. [Attached: Prometheus 3.x UTF-8 quoted selector notes + "forms that do NOT work" table + histogram quantile example with dotted labels + pre-flight checklist.]

## Reference Links
- Existing guide: `pkg/dashboard/query.go::PromqlQuery` (served as `signoz://dashboard/promql-example`)
- PromQL alert example: `pkg/alert/resources.go` → example #3 `metric_promql`
- Prometheus 3.x UTF-8 metric/label selector: https://prometheus.io/blog/2024/09/11/utf-8-support-fully-released/
- SigNoz PromQL format guide: https://signoz.io/docs/userguide/write-a-prom-query-with-new-format/

## Key Decisions & Discussion Log

### 2026-04-23 — Scope and placement
- The project already ships a PromQL guide under `signoz://dashboard/promql-example` via the `PromqlQuery` constant in `pkg/dashboard/query.go`. It covers the curly-brace + quoted-dotted-name syntax.
- Prompt adds: explicit naming of the Prometheus 3.x UTF-8 quoted selector form, an anti-pattern table (what does NOT resolve in SigNoz), a combined dotted-label histogram_quantile example, and a PromQL-alert pre-flight checklist (signoz_list_metrics → signoz_query_metrics → signoz_get_field_keys for `le` → signoz_get_alert state check).
- Placement decision: **generalize the existing resource** rather than add a duplicate.
  - Rename URI from `signoz://dashboard/promql-example` to `signoz://promql/instructions` (pre-release project, no external consumers).
  - Keep the constant in `pkg/dashboard/query.go` for now (the file is already the home of all generated-query guides). Move later if a dedicated `pkg/promql/` package is wanted.
  - Cross-reference from alert tools, alert instructions resource, and types so LLMs generating `promql_rule` alerts or PromQL dashboard widgets land on the same guidance.
- Content philosophy: the existing guide is correct and comprehensive; append the new material at the end (after the SUMMARY section) so existing readers don't lose their place. Don't reorder.

### 2026-04-23 — Surfaces covered
- PromQL is a first-class path in:
  1. Alerts — `ruleType=promql_rule` with `compositeQuery.queryType=promql` and envelope `type=promql`. Query string lives in `AlertQuerySpec.Query` (pkg/types/alertrule.go:107).
  2. Dashboard widgets — `PromQL` struct (pkg/types/dashboard.go:123-127), widget validators, defaults, examples.
  3. Saved views — explicitly *not* supported (`pkg/views/instructions.go:38` documents "PromQL and raw ClickHouse query types … are not supported"); no update needed there.
- Tools touched: `signoz_create_alert`, `signoz_update_alert`, `signoz_create_dashboard`, `signoz_update_dashboard`, `signoz_query_metrics` (reference only — does not accept raw PromQL).

### 2026-04-23 — Implementation landed
- Enriched `pkg/dashboard/query.go::PromqlQuery` with three appended sections (Prometheus 3.x UTF-8 quoted selector + anti-pattern table, combined dotted-label histogram_quantile example, PromQL alert pre-flight checklist).
- Renamed registered MCP resource URI `signoz://dashboard/promql-example` → `signoz://promql/instructions` in `internal/handler/tools/dashboards.go` (single registration point; both dashboards.go in-description references updated).
- Cross-referenced from alert surfaces:
  - `signoz_create_alert` description — added as mandatory resource #3 when ruleType=promql_rule.
  - `signoz_update_alert` description — same pointer inline.
  - `pkg/alert/resources.go` Instructions — added "PromQL for OTel dotted metric names" subsection next to the existing PromQL / ClickHouse query spec, with the anti-pattern callouts.
- Type-level description updates:
  - `pkg/types/alertrule.go::AlertQuerySpec.Query` — adds UTF-8 quoted-selector rule + resource pointer.
  - `pkg/types/dashboard.go::PromQL.Query` — same.
- Verified `go build`, `go vet`, `go test -count=1 ./...` all green.
- `rg -n 'signoz://dashboard/promql-example'` returns zero hits — rename complete.

### 2026-04-23 — Align with upstream PR #11023 PromQL refs + SigNoz docs
Pulled upstream references into the guide:
- **SigNoz PR #11023** `metric_promql` example uses `by(topic, partition, "deployment.environment")` and `on(topic, partition, "deployment.environment") group_right` — demonstrating that non-dotted labels stay bare while dotted labels are quoted in the *same* `by()`/`on()` call. Upstream English description also frames the PromQL ruleType use case as "queries that combine series with group_right or other Prom operators."
- **SigNoz docs** (https://signoz.io/docs/userguide/write-a-prom-query-with-new-format/) confirms: `sum by (le) (rate({"request.duration.bucket",...}[5m]))` — mixing a bare `le` with a quoted dotted metric selector. Backward compatibility is explicitly guaranteed for pre-existing non-dotted queries.

Changes applied:
- `pkg/dashboard/query.go::PromqlQuery` — new section "MIXING QUOTED AND UNQUOTED LABELS IN VECTOR MATCHING" that calls out `by()` / `without()` / `on()` / `ignoring()` mixing, `group_left` / `group_right` semantics, empty-tuple forms (`group_left ()`), and the backward-compat guarantee. Uses the PR #11023 consumer-group-lag query as the worked example.
- `pkg/alert/resources.go::Examples` — example #3 (`metric_promql`) description now aligns with PR #11023's "Useful for queries that combine series with group_right or other Prom operators" phrasing while keeping the MCP-specific "envelope type is 'promql'" and `signoz://promql/instructions` pointers.

### 2026-04-23 — Audit follow-ups
- Codex PR review (#142 comment r3131348029) flagged `http.server.duration_bucket` as inconsistent with the guide's own histogram-suffix rule (base has dots ⇒ suffix must be `.bucket`, not `_bucket`). Fix: example corrected to `http.server.duration.bucket` during the package move.
- Approved judgement calls from chat review:
  - Package home: moved `PromqlQuery` out of `pkg/dashboard/query.go` into a new `pkg/promql` package as `promql.Instructions`. Only consumer (`internal/handler/tools/dashboards.go`) switched to the new import. Other dashboard ClickHouse constants (`ClickhouseSqlQueryForMetrics`, `ClickhouseSqlQueryForLogs`, `ClickhouseSqlQueryForTraces`, `Querybuilder`) stay in `pkg/dashboard/query.go` — they are dashboard-specific.
  - Guide length: keeping redundancy between KEY EXAMPLES / COMMON PATTERNS / new PROMETHEUS 3.X sections — the duplication reinforces the rule for pattern-matching LLMs.
  - Registration smoke test: added `TestIntegration_PromqlInstructionsResourceRegistered` in `internal/mcp-server/integration_test.go` — reads `signoz://promql/instructions` and asserts the body carries three load-bearing substrings ("Prometheus 3.x UTF-8 quoted selector", "payment_latency_ms.bucket", "group_right").
  - PR strategy: held — do not open the PR yet; stacking more work on this branch.
- Also filled small cross-link gaps spotted during the audit (commit `b7c5d22`):
  - `signoz_execute_builder_query` now points at `signoz://promql/instructions` when `queryType=promql`.
  - `pkg/dashboard/widgets.go` PromQL entry has a Reference: line.
  - README's alert-tool tip names the PromQL resource.

## Open Questions
- [ ] Should `signoz_query_metrics` get a direct PromQL passthrough? Deferred: today that tool maps to the v5 builder; PromQL only surfaces via alerts and dashboards. Users who want ad-hoc PromQL can use `signoz_execute_builder_query` with `queryType=promql`.
