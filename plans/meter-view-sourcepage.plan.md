# Plan: Cost Meter saved-view `sourcePage="meter"`

## Status
In Progress

## Context
The SigNoz product models a Cost Meter saved view as a distinct `sourcePage="meter"`
(its own Meter Explorer route), even though the query runs against the metrics signal
with `spec.source="meter"`. The MCP server previously only allowed `sourcePage` ∈
{traces, logs, metrics} and taught LLMs to file Cost Meter views under `metrics` with
`spec.source="meter"`. The Meter Explorer lists views via an **exact-match**
`source_page = 'meter'` query, so MCP-created meter views were mis-filed: invisible in
the Meter Explorer, shown under Metrics, and "open" routed to the wrong page.
(Verified against `SigNoz/signoz` main, 2026-06-15 — see context file.)

## Approach

### 1. `internal/handler/tools/views.go`
- Add `"meter"` to the `validSourcePages` allow-list; update both `validateSourcePage`
  error strings to list it.
- Add `"meter"` to the `signoz_create_view` `sourcePage` enum, the `signoz_update_view`
  body-schema enum (`savedViewSchemaProperties`), and the relevant tool/resource
  descriptions (list/create/update + examples resource).
- Rewrite `validateBuilderSignal` to branch on sourcePage:
  - `sourcePage == "meter"`: each builder_query spec must have `signal == "metrics"`
    **and** `source == "meter"` (otherwise it would silently hit the default metrics store).
  - else (traces/logs/metrics): keep `signal == sourcePage`, **and** reject `source == "meter"`
    (a Cost Meter query belongs on the `meter` page — prevents re-mis-filing).
- Update guard ("cannot change sourcePage on update") is unchanged — meter stays meter
  via the existing string compare.

### 2. `pkg/views/instructions.go`
- sourcePage row now lists `meter`; signal/source rows document the meter rule; add a
  "Cost Meter views are special" bullet to Rules.

### 3. `pkg/views/examples.go`
- Add "Example 4 — Cost Meter usage view" (`sourcePage: meter`, `signal: metrics`,
  `source: meter`, `stepInterval: 3600`); update the Notes.

### 4. `README.md` + `manifest.json`
- Add `meter` to the `signoz_list_views` tool-table entry, the README param reference,
  and the manifest description.

### 5. Tests — `internal/handler/tools/views_test.go`
- `TestHandleListViews_Meter` — meter sourcePage passes through to the client.
- `TestHandleCreateView_AllowsMeterView` — signal=metrics + source=meter accepted; body carries both.
- `TestHandleCreateView_RejectsMeterWithoutSource` — meter without source=meter rejected.
- `TestHandleCreateView_RejectsMeterWrongSignal` — meter with signal!=metrics rejected.
- `TestHandleCreateView_RejectsSourceMeterOnMetricsPage` — source=meter on metrics page rejected.

## Files to Modify
- `internal/handler/tools/views.go` — allow-list, enums, descriptions, validateBuilderSignal
- `internal/handler/tools/views_test.go` — 5 new tests
- `pkg/views/instructions.go` — meter sourcePage + signal/source rules
- `pkg/views/examples.go` — Cost Meter example + notes
- `README.md` — tool table + list_views param
- `manifest.json` — signoz_list_views description

## Verification
```
go build ./...
go test ./internal/handler/tools/ -run View -count=1
go test ./... -count=1
```
All pass. Post-fix review by Codex gpt-5.5/xhigh (fast mode) before PR.
