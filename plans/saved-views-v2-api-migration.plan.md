# Plan: Saved-views v2 (typed-spec) API migration

## Status
Done

Client repointed to `/api/v2/saved_views`; handlers accept the v2 typed shape (`source`, `spec{...}`, `generateName`, `schemaVersion`); update drops immutable `name` and handles 204; resources/manifest/README rewritten; wire-catalog goldens updated through the guardrail path. `go build`, `gofmt`, `go test ./...`, and the focused `TestGuardrail_*` suite all green. Live e2e verification on SigNoz ≥ v0.137.0 and the agent-skills companion PR remain as follow-ups (flagged in the PR body).

## Context
Move the `signoz_*_view` tools from v1 `/api/v1/explorer/views/*` (legacy free-form shape) to v2 `/api/v2/saved_views/*` (typed `spec` shape, `schemaVersion:"v2"`). This fixes nerve-pod #100 because the upstream v2 store returns `not-found` (404) on update/get/delete of a nonexistent id, instead of v1's upsert-like success + zero-value metadata. Mirrors the dashboards-v2 migration convention.

## Approach

### Client (`internal/client/{client,interface,mock}.go`)
Repoint the five view methods to v2 and saturate pass-through helpers:
- `ListViews(ctx, source, name)` → GET `/api/v2/saved_views?source=..&name=..`
- `GetView(ctx, id)` → GET `/api/v2/saved_views/{id}`
- `CreateView(ctx, body)` → POST `/api/v2/saved_views`
- `UpdateView(ctx, id, body)` → PUT `/api/v2/saved_views/{id}`
- `DeleteView(ctx, id)` → DELETE `/api/v2/saved_views/{id}`
Update `interface.go` + `mock.go` signatures (`sourcePage` → `source`).

### Handler (`internal/handler/tools/views.go`)
Rewrite `RegisterViewHandlers` + the five handlers against the v2 shape:
- **list**: param `source` (enum `traces|logs|metrics|meter|ai_observability`) + `name` (both optional upstream, but the MCP tool keeps `sourcePage`→`source` required for discoverability); drop `category` param. Pass-through render envelope → `paginate.Wrap` into our pagination.
- **get**: `readResourceID(args,"viewId")` → v2 Get; return `{status,data:{view typed shape}}` preserved envelope via `structuredResult`.
- **create**: accept v2 `PostableSavedView` shape (`name`, `generateName`, `source`, `schemaVersion`, `spec{...}`); strip nothing more than `searchContext`; POST pass-through. Validate `source` against the v2 enum (incl. `ai_observability`). The Cost Meter mapping check moves to `spec.queries[].type=="builder_query"` spec `signal`/`source`.
- **update**: accept v2 `UpdatableSavedView` (`source`, `schemaVersion`, `spec`); `id` path param; PUT pass-through; reject cross-source change? v2 has no `name` in update (`displayName` lives in `spec`).
- **delete**: pass-through; treat 204-empty as success.

### Resources (`pkg/views/{instructions,examples}.go`)
Rewrite `Instructions` and `Examples` against the v2 typed shape (`spec.queries`, `panelType`, `requestType`, `selectedFields`, `display`, `schemaVersion:"v2"`). Tool descriptions stay linked to them.

### Docs/metadata
- `manifest.json` — refresh the five `signoz_*_view` tool entries (param changes).
- `README.md` — view tool table updated; note minimum SigNoz version.
- `AGENTS.md` — unchanged conventions.

### Tests
- Rework `views_test.go` fixtures to the v2 shape (list envelope, postable/updatable bodies, 204 empty update, 404 not-found mapping).
- Keep coded-error coverage (`views_codes_test.go`).
- Update `guardrails/tests.txt` entry if the file list changes.

## Files to Modify
- `internal/client/{client.go,interface.go,mock.go}` — v2 URLs + signatures
- `internal/handler/tools/views.go` — handlers + registration
- `pkg/views/{instructions.go,examples.go}` — resource content rewritten to v2
- `pkg/types/view.go` / `view_test.go` — may drop the legacy `SavedView` struct if unused
- `internal/handler/tools/views_test.go`, `views_codes_test.go` — fixtures
- `manifest.json`, `README.md`

## Verification
1. `go build ./cmd/server`
2. `go test ./...`
3. Live smoke (delegated to subagent): list/create/get/update/delete on v2; update a nonexistent id → expect coded not-found (404) rather than success.
