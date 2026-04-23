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

### 2026-04-23 — Enrich `signoz://alert/instructions` with user-facing docs coverage
- Prompted by review of https://signoz.io/docs/alerts/ and the referenced openapi.yml. Schema/types/validator/examples are all aligned with upstream; the remaining gap is guidance the LLM needs to *reason* from a user prompt to a valid payload.
- Added to Instructions (no type or validator changes):
  1. **Quick Workflow** — an 8-step decision tree (signal → alertType → ruleType → envelope → aggregation shape → filter → thresholds → evaluation → notification). Placed right after the CRITICAL section so the LLM sees it before diving into per-field tables.
  2. **Filter & Having Expressions** — promoted from a 7-bullet subsection under Aggregation shapes to a top-level H2 with an operator table (EXISTS, NOT EXISTS, =, !=, IN, NOT IN, LIKE, ILIKE, CONTAINS, REGEXP, numeric comparisons) mirroring the signoz MCP server's own instructions. Includes data-type guardrails and composition rules, plus the `EXISTS AND !=` pattern for safe exclusion.
  3. **Evaluation duration format + sizing tips** — explicit Go duration format, acceptance of both `5m` and `5m0s`, defaults (evalWindow=5m, frequency=1m per docs), window-vs-frequency sizing heuristics.
  4. **Threshold targetUnit and recoveryTarget subsections** — when to set targetUnit (unit mismatch between series and threshold), the validator's targetUnit→compositeQuery.unit propagation, how null vs non-null recoveryTarget drives hysteresis.
  5. **Labels & Routing — three subsections**:
     - Label sources merged for policy matching: user labels, platform labels (alertname, threshold.name, ruleSource, ruleId), dynamic labels from groupBy.
     - Routing-policy operators (=, !=, CONTAINS, REGEXP, IN, NOT IN, AND, OR, parens) — narrower than query filters; per docs/routing-policy page.
     - Channel-routing modes table: how usePolicy × thresholds[].channels × preferredChannels combine.
  6. **Anomaly tuning block** — algorithm="standard" is the only value; seasonality hourly/daily/weekly use cases; z_score_threshold tuning table (4.0/3.0/2.5/2.0 with docs-sourced sensitivity labels); scoring formula `|actual − predicted| / stddev(current_season)`; evalWindow should span ≥ one seasonal cycle.
  7. **Further Reading** — canonical SigNoz docs URLs (metrics/logs/traces/exceptions/anomaly alerts, routing policies, planned maintenance, notification setup, alerts history) so the LLM can cite them back to the user.
- **Not changed**: `pkg/types/alertrule.go`, `pkg/alert/validate.go`, `pkg/alert/validate_test.go`, `internal/handler/tools/alerts.go`, `manifest.json`, `README.md`, examples resource. The openapi schema components confirm our types already match upstream.
- **Deferred**: `RuletypesAlertState` openapi enum has 6 values (`inactive | pending | recovering | firing | nodata | disabled`) but our `renotify.alertStates` still enforces only `firing | nodata`. PR #11023 examples only ever use those two, so leaving as-is until upstream widens it.

## Open Questions
- [ ] (deferred) SigNoz anomaly algorithm enumeration — PR #11023 uses `"standard"`; grepping `ee/query-service/rules/anomaly.go` for the full list would let us enumerate in the schema enum. For now, description lists `"standard"` (z-score based) and notes it's anomaly-only.
- [ ] (deferred) Widen `renotify.alertStates` beyond `firing | nodata` — openapi `RuletypesAlertState` enum is broader (`inactive | pending | recovering | firing | nodata | disabled`). Track when upstream docs/examples start using the additional states.
