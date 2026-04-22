# Feature: Refresh alert-rule MCP schema, validator, docs & examples — Context & Discussion

## Original Prompt
> Review the gleaming-hearth plan (~/.claude/plans/based-on-the-latest-gleaming-hearth.md) and execute items 1–20 + 22.

## Reference Links
- Plan file: `/Users/ankitnayan/.claude/plans/based-on-the-latest-gleaming-hearth.md`
- SigNoz PR #11023 — `chore: add request examples for alert rules`: https://github.com/SigNoz/signoz/pull/11023
- Upstream source of truth (downloaded to /tmp at planning time):
  - `pkg/apiserver/signozapiserver/ruler_examples.go` (ten example payloads)
  - `pkg/types/ruletypes/compare.go` (valid CompareOperator aliases)
  - `pkg/types/ruletypes/match.go` (valid MatchType aliases)

## Key Decisions & Discussion Log

### 2026-04-22 — Execute gleaming-hearth items 1–20 + 22 (not 21)
- Item 21 (v2 endpoint migration) was already shipped in the earlier "glistening lemur" work — `GetAlertByRuleID`, `CreateAlertRule`, and new `UpdateAlertRule`/`DeleteAlertRule` already hit `/api/v2/rules`. The user previously chose **v2-only (no v1 fallback)**, so `SupportsRulesV2()` probe is **not** being added.
- Everything else in the plan is outstanding: schema fields, validator rules, descriptions, examples, tool-level copy, docs sync. Execute the whole set in one pass.
- `builder_trace_operator` added alongside `promql` and `clickhouse_sql` as an accepted composite query envelope type (upstream `qbtypes.QueryEnvelope` includes it).
- Examples: replace the seven hand-written examples in `pkg/alert/resources.go` with the ten canonical examples from PR #11023 verbatim (structural fidelity — the payloads are known-good against current upstream).
- Anomaly rules: upstream PR #11023's `metric_anomaly` example confirms the v1 shape is the only path today. MCP validator branches on `ruleType == "anomaly_rule"` to:
  1. skip the `condition.thresholds` requirement
  2. require `condition.{op, matchType, target, algorithm, seasonality}` + top-level `evalWindow` + `frequency`
  3. skip `applyV2Defaults` (no `schemaVersion=v2alpha1`, no evaluation block default).
- `absentFor` unit: upstream `base_rule.go` multiplies by `time.Minute`. Description corrected from "milliseconds" to "minutes (≈ consecutive evaluation cycles when frequency is 1m)".
- Severity: add `error` to canonical set (upstream accepts `critical`, `error`, `warning`, `info`). Threshold `.name` tier accepts the same four.
- CompareOp aliases (from upstream `compare.go`): literal (`above`, `below`, `equal`, `not_equal`, `above_or_equal`, `below_or_equal`, `outside_bounds`), short (`eq`, `not_eq`, `above_or_eq`, `below_or_eq`), symbolic (`>`, `<`, `=`, `!=`, `>=`, `<=`), numeric (`1`..`7`). All accepted; descriptions highlight the canonical literals.
- MatchType aliases (from upstream `match.go`): add `avg` (alias for `on_average`) and `sum` (alias for `in_total`).

## Open Questions
- [ ] (deferred) SigNoz anomaly algorithm enumeration — PR #11023 uses `"standard"`; grepping `ee/query-service/rules/anomaly.go` for the full list would let us enumerate in the schema enum. For now, description lists `"standard"` (z-score based) and notes it's anomaly-only.
