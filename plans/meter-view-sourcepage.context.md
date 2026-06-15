# Feature: Cost Meter saved-view `sourcePage="meter"` — Context & Discussion

## Original Prompt
> I want to work on this: https://github.com/SigNoz/signoz-ai-assistant/issues/325
> Ask codex 5.5 xhigh to validate if issue is valid
> Let's fix it, post fix get review from codex 5.5 xhigh fast and then create PR

## Reference Links
- [Issue SigNoz/signoz-ai-assistant#325](https://github.com/SigNoz/signoz-ai-assistant/issues/325)
- AI-assistant consumer: SigNoz/signoz-ai-assistant#238 (saved-view "open" deep-link)
- Prior cost-meter work: `plans/cost-meter-source.context.md`, issue SigNoz/signoz-mcp-server#176

## Key Decisions & Discussion Log

### 2026-06-15 — Issue validated (Codex gpt-5.5/xhigh + product-repo verification)
- Codex verified all 6 MCP-side claims accurate (allow-list `{traces,logs,metrics}`, strict `signal==sourcePage`, instructions/examples/context wording, update guard). Verdict: VALID-BUT-NEEDS-VERIFICATION because the product-side premise lives in another repo.
- Verified the product premise directly against `SigNoz/signoz` `main` (HEAD `4f3b7647d3`, 2026-06-15):
  - `frontend/src/pages/SaveView/constants.ts` → `SOURCEPAGE_VS_ROUTES` maps `meter: ROUTES.METER_EXPLORER`; `[ROUTES.METER_EXPLORER_VIEWS]: 'meter'`.
  - `frontend/src/api/saveView/getAllViews.ts` → param typed `DataSource | 'meter'` (meter outside the DataSource enum).
  - `frontend/src/container/ExplorerOptions/ExplorerOptions.tsx` → persists `sourcePage: isMeterExplorer ? 'meter' : sourcepage` on save AND update; lists via `useGetAllViews(isMeterExplorer ? 'meter' : sourcepage)`; opens via `history.replace(ROUTES.METER_EXPLORER)`.
  - Backend `pkg/sqlmigration/004_add_saved_views.go` → `source_page` is free-text (`type:text,notnull`, no enum).
  - Backend `pkg/modules/savedview/implsavedview/module.go` `GetViewsForFilters` → `WHERE org_id = ? AND source_page = ? AND name LIKE ?` (exact match).
- **Mechanical proof of the bug:** a view saved with `source_page="metrics"` can never satisfy the Meter Explorer's exact-match `source_page = 'meter'` query → invisible there, mis-filed under Metrics. Issue is **unambiguously VALID**.

### 2026-06-15 — Fix scope decision
- **Tighter meter validation than the issue's literal fix point #2.** Codex flagged that merely "accept signal=metrics when sourcePage=meter" leaves a loophole: a meter-page view with no `source=meter` would query the default metrics store. Adopted the tighter rule: for `sourcePage="meter"`, require `signal="metrics"` AND `source="meter"`.
- **Symmetric guard against re-mis-filing.** Also reject `source="meter"` on a non-meter sourcePage (e.g. `metrics`), pointing the caller to `sourcePage="meter"`. The existing update guard (cannot change sourcePage) already prevents migrating old mis-filed views via update — so blocking new mis-filed creates is safe and the only way to "fix" an old one is delete + re-create, which is the documented cross-Explorer flow anyway.
- **Surface area confined** to `internal/handler/tools/views.go` + `pkg/views/{instructions,examples}.go` + `README.md` + `manifest.json`. Swept the repo: no other sourcePage allow-list/enum sites. The dashboard panel validator and alert validator handle `source="meter"` for their own signals (panel/rule level) and are correct as-is, out of scope.

### 2026-06-15 — Live E2E verification (subagent, demo.in.signoz.cloud)
Verified directly against the SigNoz API (`/api/v1/explorer/views`, the exact paths the MCP client uses), not via the deployed MCP plugin (which runs old code and would reject `meter`). Subagent created → round-tripped → confirmed list filing → deleted → confirmed gone; no credentials printed/persisted:
- **Create** `sourcePage="meter"` + builder spec `signal="metrics"` + `source="meter"` → HTTP 200, accepted.
- **Round-trip faithful:** sourcePage="meter", signal="metrics", source="meter", queryType/panelType/stepInterval=3600/filter/having all preserved. Only server-added defaults: empty `temporality:""` and `reduceTo:""` on the aggregation. Nothing dropped.
- **Discriminating result:** view appears in `GET ?sourcePage=meter` (1 view) and is **absent** from `GET ?sourcePage=metrics` (7 views, none matching) — proving the old `metrics` encoding mis-filed it.
- **Delete** → 200; re-list `?sourcePage=meter` empty (`data:null`); re-GET by id 500 (gone, per the known deleted-view-returns-500 behavior).
- Created-view id returned as the `.data` string directly (not `.data.id`).

## Open Questions
- [x] Does the SigNoz product actually treat `meter` as a distinct first-class sourcePage with its own route? **Yes** — verified against `SigNoz/signoz` main (2026-06-15) and confirmed live via the API (2026-06-15).
- [x] Should we also reject `sourcePage="metrics" + source="meter"` going forward? **Yes** — symmetric guard prevents perpetuating the mis-filing; delete+re-create is the path for legacy mis-filed views.
