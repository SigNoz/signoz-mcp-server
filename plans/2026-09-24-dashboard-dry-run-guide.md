# Plan: Dashboard Panel Query Dry-Run Guide

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/323

## Context

SigNoz/agent-skills carried a `dashboard-to-query-builder-v5.md` reference in both dashboard skills
to explain how a saved Perses panel query becomes a `signoz_execute_builder_query` request. That is
schema mapping, which the agent-skills contributing guide assigns to MCP resources, and no MCP
resource taught it. The skills reference is being retired in favor of this resource section.

## Approach

Add a "Dry-run a panel query" section to `signoz://dashboard/widgets-instructions`: the envelope per
query plugin, composite expansion, the tool argument shape, request types per panel, PromQL and ClickHouse
specs, variable substitution, bounds, retries, and save discipline. Guard the mapping with a test driven by the embedded create schema, and prove the core
claim (saved specs execute unchanged) with an e2e test against GET output.

## Files to Modify

- `pkg/dashboard/widgets.go` — dry-run section.
- `internal/handler/tools/dashboard_schema_test.go` — every schema query plugin kind maps to a valid Query Builder v5 query type.
- `internal/mcp-server/testdata/wire-catalog/` — widgets-instructions size and hash only.
- `tests/e2e/tests/test_dashboards.py` — saved direct and composite panel queries dry-run unchanged.

## Key Decisions

### 2026-09-24 — Extend widgets-instructions instead of adding a resource

- Decision: add a section to the existing widget guide.
- Rationale: both dashboard skills already read it before authoring panels, and a new resource would add catalog surface for guidance that belongs next to the panel rules (SUR-3).

## Reference Links

- [SigNoz/agent-skills#98: removes the duplicated reference](https://github.com/SigNoz/agent-skills/pull/98)

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci` passes, including `TestWidgetsDryRunGuideCoversEverySchemaQueryPlugin`.
- `e2e/tests/test_dashboards.py` against SigNoz v0.143.0: `test_saved_panel_queries_dry_run_unchanged_through_execute_builder_query` passes for saved BuilderQuery, CompositeQuery (two inputs plus a formula), PromQLQuery, and ClickHouseSQL panels read back through GET, and confirms the `missing start or end timestamp` error.
- Rule-by-rule review against the 105-line reference on agent-skills main: every rule is in this section or in `widgets-examples`, `dashboard/examples`, or the `signoz_execute_builder_query` description, except two contradictory lines about `functions` and a saved-only `legend`, which were dropped.

## Outcome

Agents learn panel query dry-runs from MCP, and SigNoz/agent-skills#98 removes its duplicated reference files.
