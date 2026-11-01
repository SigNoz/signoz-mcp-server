# Plan: Stop Limiting Formula Inputs

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/405

## Context

`ApplyBounds` filled an omitted limit on every builder query a formula references with 10000, and
the server taught agents to set that limit themselves: the `signoz_execute_builder_query` and
`signoz_query_metrics` descriptions, the `query` schema description, `signoz://metrics-aggregation-guide`,
`signoz://dashboard/widgets-examples`, and the README. SigNoz applies a builder query's limit before it
evaluates the formulas that reference it, so any input limit is a top-N cut ahead of the formula. Each
input is ranked by its own value, so `A / B` can keep different groups from A and B, and the formula
silently loses or misstates series. This is the defect #345 removed from alert payloads; the query tools
and the dashboard teaching kept it. SigNoz applies no limit of its own to an aggregate query without one.

## Approach

- `ApplyBounds` leaves a builder query a formula references untouched: no inserted limit or order. A
  limit the caller set is still forwarded. Formula results keep the 100 default.
- Teaching says to omit limit and order on formula inputs, everywhere it said 10000, and the dashboard
  and metrics examples drop them.
- Inputs still come back in `signoz_query_metrics` results because they are not disabled; that is a
  separate follow-up.

## Files to Modify

- `pkg/querybuilder/bounds.go`, `pkg/querybuilder/request.go`: skip formula inputs; drop
  `DefaultFormulaInputQueryLimit` and `AppliedBounds.FormulaInput`.
- `internal/handler/tools/query_builder.go`, `internal/handler/tools/metrics_query.go`: decision notes
  and the tool description.
- `internal/handler/tools/metrics.go`, `pkg/types/querybuilder.go`, `pkg/metricsrules/guide.go`,
  `pkg/dashboard/widgets_examples.go`, `README.md`: teaching and examples.
- `internal/mcp-server/testdata/wire-catalog/`: the three tool texts and the two resources this changes.
- `pkg/querybuilder/bounds_test.go`, `tests/e2e/tests/test_dashboards.py`.

## Key Decisions

### 2026-10-28 — no limit and no order on formula inputs

- Decision: insert neither on a builder query a formula references, including inputs of a disabled
  intermediate formula.
- Rationale: an input limit drops series before the formula runs, and an order without a limit selects
  nothing. Response size stays bounded by the formula-result limit and by disabling inputs.
- Alternatives rejected: keeping a wider default (10000) still cuts high-cardinality inputs silently.

## Reference Links

- [fix(alerts)!: stop injecting limit/order into alert evaluation queries (#345)](https://github.com/SigNoz/signoz-mcp-server/pull/345)

## Verification

- `TestApplyBounds_LeavesFormulaInputsUnboundedAndBoundsFormulaResults` asserts formula inputs reach
  SigNoz without a limit or order, an authored input limit is kept, and formula results get 100.
- e2e: `test_saved_panel_queries_dry_run_unchanged_through_execute_builder_query` saves a formula panel
  whose inputs carry no limit or order and runs it unchanged against the pinned SigNoz release.
- Wire catalog: only the `signoz_execute_builder_query` and `signoz_query_metrics` texts and the
  `signoz://metrics-aggregation-guide` and `signoz://dashboard/widgets-examples` sizes and hashes changed.

## Outcome

Formula inputs are no longer limited by the server or by its teaching. The SigNoz/agent-skills
companion removes the same 10000 guidance from the skills.
