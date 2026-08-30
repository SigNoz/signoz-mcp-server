# Feature: Saved-views v2 API migration — Context & Discussion

## Original Prompt
> Raise a PR for https://github.com/SigNoz/nerve-pod/issues/100.
> The branch name should be nerve-pod/issues/100.
> The ultimate goal is to migrate the saved view tools to the v2/saved_view apis introduced in SigNoz.
> Start with the migration to v2/saved_views. The migration should fix nerve-pod/issues/100.

## Reference Links
- [nerve-pod issue #100](https://github.com/SigNoz/nerve-pod/issues/100)
- Upstream `SigNoz/signoz` (cloned to /tmp): `pkg/apiserver/signozapiserver/savedview.go`, `pkg/types/savedviewtypes/*`, `pkg/modules/savedview/implsavedview/{handler,handler_v2,store,module}.go`

## Issue #100 Summary
`signoz_update_view` against a deleted saved view returns `success` with sentinel zero-value metadata; `signoz_get_view` on the deleted id returns HTTP 500 instead of 404. Root cause is upstream v1 `/api/v1/explorer/views/*`; the v2 `/api/v2/saved_views` store checks `RowsAffected()==0` on update/delete and returns `ErrCodeSavedViewNotFound` (rendered as 404). Migrating the MCP view tools to v2 fixes the fail-silent behavior.

## Key Decisions & Discussion Log
### 2026-08-29 — v1→v2 shape mapping
- **List**: v1 `sourcePage`+`name`/`category` → v2 `source`+`name` only. v2 has NO `category` filter (and name/name LIKE only). Decision: drop the `category` tool param on list; `sourcePage` param renamed/constrained to the v2 `Source` enum (`traces`, `logs`, `metrics`, `meter`, `ai_observability`).
- **v2 SavedView shape** (read): `{id, name, source, schemaVersion:"v2", spec{displayName, panelType, requestType, queries[], selectedFields[], display{maxLines,fontSize,format,color}}}` + audit fields (`createdAt/By`,`updatedAt/By`). No more v1 `compositeQuery` wrapper — queries live directly in `spec.queries`. The v1 `name`/`category`/`tags`/`extraData` fields are gone from writes; `selectedFields` and `display` carry what `extraData` used to.
- **Create (Postable)**: `{name, generateName, source, schemaVersion:"v2", spec}` → 201 `types.Identifiable{id}`. `generateName` supported.
- **Update (Updatable)**: `{source, schemaVersion:"v2", spec}` → 204 No Content. `name` is immutable server-side (update body has no name). Full-replacement semantics preserved.
- **Delete**: shared v1/v2 handler → 204.
- **Response envelope**: render.Envelope `{status, data}` on success; `{status, error:{...}}` on error with coded errors (e.g. `saved_view_not_found`).
- MCP handlers become mostly **pass-through** (like the dashboards-v2 migration), reading/writing the v2 typed shape directly.

### 2026-08-30 — PR #296 review decisions
- @makeavish: drop `ai_observability` from the `source` enum until the UI ships it (enum is `traces|logs|metrics|meter`). Applied.
- @makeavish: `schemaVersion` is not a supported input; hard-code `v2` in the create/update handlers so a caller value never reaches upstream. Applied (param removed, value forced).
- @makeavish: the companion `SigNoz/agent-skills` update is a pre-*release* gate, NOT a pre-*merge* blocker — this PR may merge first; the skill is updated before the next MCP version release. Recorded; PR body keeps the CMP-3 flag.
- Codex bot (accepted): `name` param description now states it is a DNS-1123 label (lowercase/digits/hyphens) and `spec.displayName` is the display label.
- Codex bot: proposed preserving a legacy `sourcePage` alias — **rejected** after discussion (option b): legacy `sourcePage` is intentionally rejected to avoid a partial compat shim on a hard rename. Wire-catalog goldens updated through the guardrail path for the changed entries only.

## Open Questions
- [x] The old MCP tools carried `category`, `tags`, `extraData` free-text params. v2 has no category/tags. Resolved by upstream reviewer + @makeavish: category/tags were removed in the v2 contract; rendering prefs map to `spec.selectedFields`/`spec.display`. **Decision (PR review, @makeavish, 2026-08-29): the agent-skills update is a pre-*release* gate, not a merge blocker; `ai_observability` is dropped from the source enum until the UI ships it; `schemaVersion` is hard-coded to `v2`; `name` is documented as a DNS-1123 label; legacy `sourcePage` is intentionally rejected (no alias).**

## Agent-skills impact (CMP-3)
The `signoz_*_view` tool parameter contracts change (list loses `category`; create/update take the v2 typed shape instead of `compositeQuery`). This is a breaking contract change — agent-skills needs a companion update; flag in the PR.
