# Plan: Area Chart Panel

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/320

## Context

SigNoz v0.143.0 (released 2026-09-23, source `7ce73f3470371daa7b245716a1b1a3fd6a2daee4`) adds the
`signoz/AreaChartPanel` dashboard panel: backend schema in SigNoz#12635 and a dashboard UI renderer
(`frontend/src/pages/DashboardPage/DashboardContainer/Panels/kinds/AreaChartPanel`). The MCP
dashboard input schemas were generated from the v0.142.0 OpenAPI spec, so clients were never offered
the new kind, and the widget guides did not describe it.

Regenerating the schemas from the v0.142.0 spec reproduces the committed files byte for byte. The
v0.143.0 spec differs only by adding the area panel (`DashboardtypesAreaChartPanelSpec`,
`AreaChartVisualization.stack` = `none|normal|percent`, `AreaFillMode` = `solid|gradient`,
`FillOpacity` 0 to 1) and its `DashboardtypesPanelPlugin` branch. The patch schema is unchanged.

## Approach

Move the schema pin to v0.143.0 and regenerate, teach the panel in the widget guide and examples,
and add an e2e round trip. The base PR moves the e2e SigNoz image to v0.143.0. The change is additive: no input
is removed or renamed.

## Files to Modify

- `internal/handler/tools/schemas/extract_schemas.py`, `dashboard_create.json`, `dashboard_update.json` — pin and regenerate from v0.143.0.
- `pkg/dashboard/widgets.go` — panel list, legend rule, and an Area chart section.
- `pkg/dashboard/widgets_examples.go` — stacked area example (validated against the create schema by `TestWidgetExamplesValidateAgainstCreateSchema`).
- `internal/handler/tools/dashboards.go`, `manifest.json` — widgets-examples description lists area.
- `internal/mcp-server/testdata/wire-catalog/` — only the create/update input schemas and the two widget resource entries.
- `README.md` — area chart note under `signoz_create_dashboard`.
- e2e against SigNoz v0.143.0 comes from the base PR (Slack channel message fields), which this PR stacks on.
- `tests/e2e/tests/test_dashboards.py` — area chart create, get, patch, and `fillMode: none` rejection.
- SigNoz/agent-skills#98 — dashboard skills mention the area panel (companion, additive).

## Key Decisions

### 2026-09-24 — Stacking guidance

- Decision: teach stacking for additive quantities only and route latency, percentiles, and ratios to Timeseries.
- Rationale: a stacked total of non-additive values is misleading, and the panel defaults to `stack: none`.

## Reference Links

- [SigNoz#12635: area chart panel schema](https://github.com/SigNoz/signoz/pull/12635)
- [SigNoz v0.143.0 release](https://github.com/SigNoz/signoz/releases/tag/v0.143.0)

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci` passes. `TestWidgetExamplesValidateAgainstCreateSchema` accepts the new area example against the regenerated schema and rejects it against the v0.142.0 schema.
- `make test-e2e` against a clean SigNoz v0.143.0 cast: 59 passed, 0 failed, including `test_area_chart_panel_round_trips_stack_and_fill_and_rejects_fill_none` (create, get, patch `stack`, and upstream rejection of `fillMode: none`).

## Outcome

Agents are offered `signoz/AreaChartPanel` in the create and update schemas and taught when to stack it. SigNoz/agent-skills#98 carries the matching skill guidance and eval.
