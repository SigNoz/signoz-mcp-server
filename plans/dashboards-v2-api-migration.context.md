# Feature: Dashboards v2 (Perses) API migration — Context & Discussion

## Why this change
SigNoz is moving dashboards from the v1 API (`/api/v1/dashboards`, a permissive "accept-all" backend, flat `Dashboard{title, layout[], widgets[]}` at schema `v5`) to a v2 API (`/api/v2/dashboards`, Perses-based schema `v6`, strict server-side validation). This server's dashboard tools were built for v1 and must move to v2 + the new Perses input shape.

Scope: migrate list/get/create/update/delete to v2 and **add a patch tool** (RFC 6902 partial updates — the main token-saving win). Make the server a **pass-through** (v2 validates), supply tool input schemas as embedded JSON (not Go structs), and drop the obsolete v1 validation/types. Import/list-templates are registered as pass-throughs too.

## Reference Links

Upstream SigNoz (source of truth for v2):
- **OpenAPI spec — input schemas are extracted from here:** https://github.com/SigNoz/signoz/blob/main/docs/api/openapi.yml (`DashboardtypesPostableDashboardV2` / `UpdatableDashboardV2` / `PatchableDashboardV2`)
- v2 dashboard routes: https://github.com/SigNoz/signoz/blob/main/pkg/apiserver/signozapiserver/dashboard.go
- v2 Go types (Perses shape): `pkg/types/dashboardtypes/perses_dashboard*.go`

## Key Decisions & Discussion Log

Shorthand — the three layers a request flows through:
- **A** — input *schema* the LLM fills. Was Go structs (`pkg/types/dashboard.go`) reflected to a JSON Schema; now embedded JSON Schemas.
- **B** — server-side *validation/normalization* (`pkg/dashboard/dashboardbuilder` + `panelbuilder`).
- **C** — typed client methods `CreateDashboard(types.Dashboard)`/`UpdateDashboard(...)`, distinct from the `…Raw([]byte)` methods actually called.
- **pass-through** — forward the model's payload to v2 unchanged; let the API validate.

### 2026-06-29 — What v2 changes (vs v1)
Different shape, not a tweak. Endpoints → `/api/v2/dashboards` with path `{id}` and distinct create/get/update(PUT)/patch/delete/lock/unlock routes. Body is Perses-shaped: `{schemaVersion:"v6", name, generateName, tags[], spec}`, `spec = {display, variables[], panels{map}, layouts[], ...}`. Hard differences: `schemaVersion` must be `"v6"`; `name` is an immutable DNS-1123 label, separate from human title `spec.display.name`; tags are key/value objects (max 10); panels are a map keyed by id with a separate `layouts[]`; variables are an array; panel/query plugins are discriminated `oneOf`s keyed by `kind`; unknown fields are rejected (`DisallowUnknownFields`); DELETE returns 204.

### 2026-06-29 — Scope: existing tools + patch; import/templates registered (pass-through)
Migrate list/get/create/update/delete; add **patch** (`PATCH {id}`, RFC 6902) — the model sends only the changed panel/query/variable instead of re-emitting + re-validating the whole dashboard, a big token/error reduction and a cheap correction loop. Out of scope: pin/unpin, personalized list. Import: `signoz_import_dashboard` + `signoz_list_dashboard_templates` are registered, and `handleImportDashboard` is a pass-through (forwards templates verbatim, no v1→v6 converter needed).

### 2026-06-29 — The three layers, and what happens to each
A, B, C were largely independent: A only generated the schema (never touched a live request — handlers work on `map[string]any`), B did the real runtime work, C was dead (handlers used `…Raw`). So the migration is mostly deletion: **A** → embedded JSON Schemas, **B** → pass-through, **C** → deleted.

### 2026-06-29 — Schema is JSON from the OpenAPI doc, not a Go struct
We don't Go-import SigNoz's types or build a struct for `WithInputSchema[T]`: it would pull the whole monorepo (+ perses/k8s deps) pinned to a moving branch; upstream tags lack the model-facing descriptions; and the Perses `oneOf` plugins can't be expressed by struct reflection (collapse to `any`). Instead we feed a pre-built JSON Schema via `WithRawInputSchema` — no struct in the path. Extracted from `docs/api/openapi.yml`, which already encodes the `oneOf`s.

### 2026-06-29 — Why B becomes pure pass-through
B existed only because v1 was accept-all — validation was pushed onto clients (this server, the UI, alerts each grew their own). v2 reclaims validation server-side, so B's reason dissolves. The UI keeps client validation because a human hates a round-trip to find a typo; an LLM just reads a 400 and resubmits in-turn, so that argument doesn't carry. Principle: the tool may *complete* deterministic boilerplate (ids, layout) but must not *re-validate* what the API checks, and anything it fills must show in the response. Decision: B = strip `searchContext`, default `generateName` when no `name`, forward bytes (no `schemaVersion` stamping — the model supplies `"v6"` per the schema). (B's one stricter-than-API check is only worth porting if v2 has a similar gap — it doesn't; see Open Questions.)

### 2026-06-29 — Error handling under pass-through
A v2 `400` flows back as tool-result content (an error *result*, not a Go error), so the model reads it and retries. The correction loop is model-driven; the server retries only transient 5xx/429, never a 400. Works *only if* v2's error messages are precise enough — the load-bearing assumption to verify live.

### 2026-06-29 — Dashboard resources: basics guide + concepts guide + examples
The three v5 envelope resources are rewritten **in place** to their v6 counterparts — no new file or URI, each keeps its existing `signoz://dashboard/*` slot **and its original document framework**, with only the v5-specific fields swapped for v6:
- `Basics` → `signoz://dashboard/instructions` — dashboard **basics**: display (title/tags/description), the grid layout (positions + `content.$ref` panel linkage), and variable configuration (incl. the always-ask-which-panels rule). Kept its Basics/Layout/Variables/Links framework; swapped `X/Y/W/H/I` → `x/y/width/height` + `content.$ref`, dropped grid fields v6 doesn't have (`MinW/MaxH/static/isDraggable`), and remapped the variable fields to the v6 `ListVariable`/`DynamicVariable` plugin shape.
- `WidgetsInstructions` → `signoz://dashboard/widgets-instructions` — **which** query/panel type to choose, plus legend/layout conventions; rewritten to be **structure-agnostic** (no JSON field names), Layout block + `thresholdFormat` field-claims removed, query-type/panel "when to use" guidance kept.
- `WidgetExamples` → `signoz://dashboard/widgets-examples` — worked payloads, authored from real round-tripped panels and registered.

The exact envelope (panels map, plugin `kind` discriminators, query structure) is the JSON Schema's job — the create/update tool descriptions point at it and name the key facts (`schemaVersion "v6"`, `spec` = display/variables/panels(map)/layouts, plugin `kind`). The two prose guides keep the same scopes they had in v5 (basics = display/layout/variables; concepts = panels/queries/legends), so there's no overlap to dedupe.

**Done:** all three resources adapted to v6 and registered, linked from the create/update tool descriptions (numbered list = `instructions` + `widgets-instructions` + `widgets-examples`). The v6 field changes (layout `x/y/width/height` + `content.$ref`; `ListVariable`/`DynamicVariable`/`CustomVariable`/`QueryVariable` plugin specs) were taken from the embedded create schema — verified, not guessed. Query-authoring resources (promql, clickhouse, query-builder) unaffected. **Pending:** only additive example gaps (bar, histogram, ClickHouse SQL, PromQL, a metrics panel).

### 2026-06-29 — Implementation notes (facts not covered above)
- **Schema extraction (committed regenerator, not a one-off):** `internal/handler/tools/schemas/extract_schemas.py` takes the transitive `$ref` closure of the three V2 root schemas in `openapi.yml` (OAS 3.0.3), rewrites `#/components/schemas/X` → `#/$defs/X`, converts `nullable:true` → `["T","null"]`, injects top-level `searchContext`, and applies the K5 `id`/`uuid` contract on update/patch (the alias + neither-required step, originally a manual JSON edit, now folded into the script). It emits the three self-contained schemas (120 `$defs` for create/update; patch has 2). Re-run it (fetch `openapi.yml` → run → copy outputs) to regenerate when upstream drifts. Plugin discriminators: 7 panel, 6 query, 3 variable, 1 datasource.
- **`rawInputSchema()` (load-bearing):** `mcp.WithRawInputSchema` doesn't clear the default object `InputSchema` that `mcp.NewTool` seeds, and `Tool.MarshalJSON` errors if both are set — would break `tools/list`. The helper zeroes the default first. Used for create/update/patch.
- **`QueryType` rescue:** deleting `pkg/types/dashboard.go` also removed `QueryType`, used intra-package by `alertrule.go` (unqualified, so a cross-package grep missed it). Preserved in a new minimal `pkg/types/querytype.go`.
- **Embedded create/update schemas duplicate ~110K** of near-identical `$defs`; fine for now.

## Open Questions

Resolved:
- [x] **Schema source / extraction?** → `docs/api/openapi.yml`; `$ref`-closure script → self-contained JSON Schemas. The script is committed and reusable at `internal/handler/tools/schemas/extract_schemas.py` (re-run to regenerate).
- [x] **List shape?** → `ListDashboards(ctx, limit, offset)` forwards to `GET /api/v2/dashboards` (server-side pagination), returns `ListableDashboardV2` verbatim; client-side `paginate` wrapping dropped. Uses `ParseParams` (not `ParseParamsClamped`) — v2 bounds `limit` server-side, so no client-side clamp is needed.
- [x] **Should the model author `name`?** → No. It sets `spec.display.name`; create defaults `generateName:true` when no name, so the API derives a DNS-1123 name. A name is sent only if explicitly provided.
- [x] **Port B's filter-consistency check?** → No. It guarded a v1 two-filter-forms hazard; v2 builder queries have a single `filter.expression`, so the inconsistency can't arise.

Open:
- [ ] The schema is generated from `openapi.yml` by the committed `extract_schemas.py` regenerator (re-runnable). Long-term sourcing options:
    - **Keep current** — the script is already committed; still want a `go:generate` hook + CI drift-check (regen actually surfaced live drift, so the risk is real, not hypothetical). Lowest effort.
    - **Shared owned module** that publishes the generated JSON Schema (or exposes the Perses types' schema-gen methods). Single source of truth, `oneOf` intact, no monorepo dep — best long-term.
    - **Author model-facing descriptions upstream** so any consumer gets an LLM-ready schema.
    - Rejected: importing the upstream Go types (monorepo + perses/k8s deps, and reflection can't emit `oneOf`); replicating structs locally (max maintenance, still no `oneOf`).
    - Lean: keep current now; move to the shared module (+ upstream descriptions) once ownership is agreed. No handler/client/test rework — only the schema source.
