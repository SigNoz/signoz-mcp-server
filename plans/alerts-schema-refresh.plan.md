# Plan: Refresh alert-rule MCP schema, validator, docs & examples

## Status
Done

## Context
The `signoz_create_alert` input schema (`pkg/types/alertrule.go`), validator (`pkg/alert/validate.go`), and MCP resources (`pkg/alert/resources.go`) were written against an older `PostableRule`. Several divergences from current upstream cause MCP-generated payloads to be silently mishandled or rejected:

- Metric aggregation shape (`expression` vs. `metricName/timeAggregation/spaceAggregation`).
- Anomaly rules must use the v1 schema (top-level `evalWindow/frequency`, condition-level `op/matchType/target/algorithm/seasonality`, no `thresholds`) — MCP unconditionally requires v2 thresholds.
- PromQL/ClickHouse composite queries use envelope `type: "promql"` / `"clickhouse_sql"` — MCP validator only accepts `builder_query`/`builder_formula`.
- Missing fields on the struct (`functions`, `newGroupEvalDelay`); description drift (absentFor unit, operator/match aliases, severity tiers, algorithm value, evalWindow style).
- Tool-level descriptions for `signoz_list_alerts`, `signoz_get_alert`, `signoz_create_alert` misstate behavior (e.g. claim `list_alerts` returns rule definitions).

Refresh everything to match upstream and mirror PR #11023's ten canonical examples.

## Approach

### Types (`pkg/types/alertrule.go`)
- `AlertAggregation` → add `MetricName`, `TimeAggregation`, `SpaceAggregation` (all `omitempty`); keep `Expression`. Description split by signal.
- `AlertQuerySpec` → add `Functions []AlertQueryFunction`.
- New types: `AlertQueryFunction{Name string; Args []AlertQueryFunctionArg}`, `AlertQueryFunctionArg{Name string; Value any}`.
- `NotificationSettings` → add `NewGroupEvalDelay string`. Expand `UsePolicy` description.
- `Renotify.AlertStates` description: clarify only `firing` and `nodata` are valid.
- `BasicThreshold.CompareOp` / `.MatchType` descriptions: list canonical literals; drop numeric-alias hints.
- `BasicThreshold.Name` + `AlertRule.Labels`: document `critical`, `error`, `warning`, `info`; note threshold.name ↔ severity mapping.
- `BasicThreshold.RecoveryTarget` description: explain hysteresis semantics.
- `AlertCondition.Algorithm`: change to `standard` and note anomaly-only.
- `AlertCondition.AbsentFor`: minutes, not milliseconds.
- `AlertEvaluationSpec.EvalWindow`/`Frequency`: canonical `5m`/`1m`/`15m` style.

### Validator (`pkg/alert/validate.go`)
- Broaden `validCompareOps`: `above`, `below`, `equal`, `not_equal`, `above_or_equal`, `below_or_equal`, `outside_bounds`, short (`eq`, `not_eq`, `above_or_eq`, `below_or_eq`), symbolic (`>`, `<`, `=`, `!=`, `>=`, `<=`), numeric (`1`–`7`).
- Broaden `validMatchTypes` with `avg` and `sum` aliases.
- Rename `validBuilderQueryTypes` → `validQueryEnvelopeTypes`; add `promql`, `clickhouse_sql`, `builder_trace_operator`.
- In `validateCondition`: enforce `type` ↔ `queryType` match when `queryType ∈ {promql, clickhouse_sql}`; require `spec.query` for those envelope types.
- Anomaly branch: when `ruleType == "anomaly_rule"`:
  - Skip `thresholds` requirement.
  - Require `condition.op`, `condition.matchType`, `condition.target`, `condition.algorithm`, `condition.seasonality`.
  - Require top-level `evalWindow` and `frequency`.
  - Skip `applyV2Defaults`.
- Validate `renotify.alertStates` ⊂ {`firing`, `nodata`}.

### Resources (`pkg/alert/resources.go`)
- Instructions: update endpoint to `/api/v2/rules`; replace anomaly section with v1-shape template; list broader operator/match-type sets; add severity=`error`; document `newGroupEvalDelay`; document `recoveryTarget`; change `evalWindow`/`frequency` to `5m`/`1m` style; note threshold.name↔severity mapping.
- Instructions enrichment (2026-04-23, follow-up pass aligned with https://signoz.io/docs/alerts/ and `/api/v2/rules` openapi):
  - Quick Workflow decision tree (signal → alertType → ruleType → envelope → aggregation shape → filter → thresholds → evaluation → notification).
  - Filter & Having Expressions promoted to its own H2 with operator table, data-type guardrails, and composition rules (mirrors signoz MCP-server instructions).
  - Evaluation section: Go duration format + defaults + window/frequency sizing tips.
  - Threshold section: `targetUnit` selection subsection + `recoveryTarget` hysteresis subsection.
  - Labels & Routing: label-source taxonomy (user / platform / dynamic), routing-policy matcher operators, channel-routing modes table.
  - Anomaly Alerts: algorithm/seasonality notes + z_score_threshold tuning table + scoring formula + evalWindow sizing.
  - Further Reading: canonical SigNoz docs URLs.
- Examples: replace with the ten PR #11023 examples — `metric_threshold_single`, `metric_threshold_formula`, `metric_promql`, `metric_anomaly`, `logs_threshold`, `logs_error_rate_formula`, `traces_threshold_latency`, `traces_error_rate_formula`, `tiered_thresholds`, `notification_settings`.

### Tool descriptions (`internal/handler/tools/alerts.go`)
- `signoz_list_alerts`: clarify it returns **firing/silenced/inhibited Alertmanager instances**, not rule definitions; point at `signoz_get_alert` for rule definitions.
- `signoz_get_alert`: note response shape is v1 `GettableRule` or v2 `Rule` depending on server version (v2 has canonical `createdAt/createdBy/updatedAt/updatedBy`).
- `signoz_create_alert`: clarify threshold/PromQL use v2alpha1; anomaly uses v1 (top-level `evalWindow/frequency`, condition-level `op/matchType/target`, no `thresholds`).

### Docs sync
- `manifest.json`: tool `description` fields brought in line with tool-string updates.
- `README.md`: alert-section entries mirror the new descriptions.

### Tests
- `internal/handler/tools/alerts_test.go` / `pkg/alert/validate_test.go`:
  - Anomaly rule round-trip accepted (v1 shape, no thresholds).
  - PromQL envelope `type: "promql"` + `queryType: "promql"` accepted.
  - Metric aggregation with `metricName/timeAggregation/spaceAggregation` accepted.
  - `absentFor` passes through unchanged.
  - `renotify.alertStates: ["flapping"]` rejected.
  - `condition.op: "above_or_equal"` and `"outside_bounds"` accepted.
  - `matchType: "avg"` / `"sum"` accepted.

## Files to Modify
- `pkg/types/alertrule.go` — struct + description refresh
- `pkg/alert/validate.go` — enum expansion + anomaly branch + query envelope types
- `pkg/alert/resources.go` — Instructions + Examples replacement
- `internal/handler/tools/alerts.go` — three tool description rewrites
- `manifest.json`, `README.md` — doc sync
- `pkg/alert/validate_test.go` (existing) + `internal/handler/tools/alerts_test.go` — new cases

## Verification
1. `go build ./...`
2. `go test ./...` — all existing tests pass; new tests cover anomaly/promql/enum-expansion/renotify-validation.
3. Spot-check the ten example payloads render verbatim in `Examples` resource output.
