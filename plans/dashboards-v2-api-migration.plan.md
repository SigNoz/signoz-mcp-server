# Plan: Dashboards v2 (Perses) API migration

## Status
In Progress

- **Done:** schema extraction; client → v2 + `PatchDashboardRaw`; pass-through handlers on the shared helpers (canonical `id` + `uuid` alias, structured output); embedded JSON Schemas; `name` auto-generation; old v1 types deleted; `handleImportDashboard` made pass-through and import/list-templates tools registered; all three v6 resources rewritten in place + registered (`instructions` = basics/layout/variables, `widgets-instructions` = concepts, `widgets-examples` = worked round-tripped panels) and linked from create/update; tests + docs synced. `go build`, `gofmt -l`, `go test ./...` green.
- **Remaining:** migrate the `SigNoz/dashboards` repo + regen script to v6 (so import actually works);

## Context (brief)
v1 (`/api/v1/dashboards`, accept-all, flat `Dashboard{title,layout[],widgets[]}` at `v5`) → v2 (`/api/v2/dashboards`, Perses `{schemaVersion:"v6",name,generateName,tags[],spec}`, strict server-side validation). Because v2 validates server-side, the migration is mostly *removal*: input schemas become embedded JSON, the old `dashboardbuilder`/`panelbuilder` validation drops to pass-through, and the dead typed client methods are deleted (the layered A/B/C breakdown and rationale are in the context file). Handlers follow the package conventions (shared helpers, id-aliasing, structured output). Scope: list/get/create/update/delete + a **patch** tool, plus import/list-templates (registered as pass-throughs); out of scope: pin/unpin, personalized list.

## Implementation

**Input schemas.** The three write tools get pre-built JSON Schemas via a local `rawInputSchema()` option, `//go:embed`-ed from `schemas/dashboard_{create,update,patch}.json` and produced by the committed regenerator `schemas/extract_schemas.py` (re-runnable, not a one-off; extraction mechanics in the context file). Each adds top-level `searchContext` (never `required`); update/patch add both `id` and `uuid` (neither `required`; `TestUpdateStructs_IDNotSchemaRequired`). The `schema_compat.go` normalizer handles `$defs`/`oneOf` for clients, unchanged.

**Handlers (pass-through) — `dashboards.go`.** All use the shared helpers (`requireArgsMap`/`notAConfigObjectError`, `readResourceID(args,"uuid")`, `errorWithCode`, `upstreamError`).
- **Create** — strip `searchContext`; default `generateName:true` when no `name`; marshal; `CreateDashboardRaw`. No local validation. `structuredResult`.
- **Update** — read id; strip `id`/`uuid`/`searchContext`; PUT the rest. `name` immutable. `structuredResult`.
- **Patch** — forward the RFC 6902 op array to `PATCH …/{id}`. `structuredResult`.
- **Get/Delete** — id via `readResourceID`; get injects `webUrl` + `structuredResult`; delete is 204 → confirmation text.
- **List** — `paginate.ParseParams` → `ListDashboards(ctx,limit,offset)` (server-side pagination, no client-side clamp); inject `webUrl` per `dashboards[]` entry; `structuredResult`.

**Client — `{client,interface,mock}.go`.** `CreateDashboardRaw`→POST; `UpdateDashboardRaw(ctx,id,[]byte)`→PUT (returns body); `GetDashboard(ctx,id)`/`ListDashboards(ctx,limit,offset)`→v2 GET; `DeleteDashboard(ctx,id)`→DELETE (204); new `PatchDashboardRaw(ctx,id,[]byte)`→PATCH. Typed `CreateDashboard`/`UpdateDashboard` deleted (last `types.Dashboard` users).

**Old types.** `pkg/types/dashboard.go` deleted; `QueryType` (used by `alertrule.go`) preserved in new `pkg/types/querytype.go`. `pkg/dashboard` resources are still used (schemas, the v6-adapted `Basics` basics guide + `WidgetsInstructions` concepts guide); the `dashboardbuilder`/`panelbuilder` pipeline (`dashboard.ValidateFromMap`) is now **unused** (import became pass-through too) but kept for now. B's `validateFilterExpressionConsistency` not ported (v1-only two-filter hazard; v2 has a single `filter.expression`).

**Import/templates.** `handleImportDashboard` is now a pure pass-through — fetch the template, default `generateName` when it has no `name`, POST to v2 verbatim (no local validation; this removed the last caller of `dashboard.ValidateFromMap`), and return the created dashboard via `structuredResult` like create. Both `signoz_import_dashboard` + `signoz_list_dashboard_templates` are **registered** (and in `manifest.json` + README), so they're discoverable — but imports **fail validation** until the (owned) `SigNoz/dashboards` repo is migrated to v6 and `dashboard_templates.json` is regenerated (the regen-script change is in Remaining work). (`list_dashboard_templates` itself is content-agnostic and works now; its listed paths only resolve once the repo is migrated.)

**Docs/metadata.** `manifest.json` + `README.md` → v2 tool set (added patch; import/templates kept and updated; `id`-canonical with `uuid` alias).

## Files changed
- `internal/handler/tools/dashboards.go` — handlers, patch tool + registration, `rawInputSchema`, embedded-schema wiring, `handleImportDashboard` made pass-through (import/templates stay registered); the v6 `instructions` (basics) + `widgets-instructions` (concepts) resources registered + linked from create/update (examples resource deferred).
- `pkg/dashboard/basics.go` — `Basics` surgically adapted in place to v6 (display/layout/variables), keeping its original Basics/Layout/Variables framework (`signoz://dashboard/instructions`).
- `pkg/dashboard/widgets.go` — `WidgetsInstructions` surgically rewritten to structure-agnostic v6 concepts (panel/query selection, legend/layout conventions); both guides re-registered.
- `internal/handler/tools/schemas/dashboard_{create,update,patch}.json` — new embedded schemas, plus `extract_schemas.py`, the committed regenerator that produces them.
- `internal/client/{client,interface,mock}.go` — v2 endpoints, `PatchDashboardRaw`, typed methods removed.
- `pkg/types/dashboard.go` deleted; `pkg/types/querytype.go` added.
- `manifest.json`, `README.md` — v2 tool set.
- `.github/scripts/regenerate_dashboard_templates.py` — catalog regenerator made v6-aware (`spec.display.*` + KV tags), v1-tolerant.
- Tests: `client_test.go`, `tools/{dashboards,schema_compat,id_alias,silent_failures,structured_content,upstream_error}_test.go`, `internal/mcp-server/integration_test.go` (asserts `tools/list` ↔ `manifest.json` parity).

## Remaining work
**Make import functional** — migrate the `SigNoz/dashboards` repo to v6, then rerun `.github/scripts/regenerate_dashboard_templates.py` to refresh `dashboard_templates.json`. The script is already v6-aware (reads `spec.display.*` + KV tags for the keyword index, v1-tolerant), so this is now blocked only on the repo migration — which only the repo owner can do. Until then `signoz_import_dashboard` is registered but every import 400s.