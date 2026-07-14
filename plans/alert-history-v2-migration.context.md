# Feature: Migrate `signoz_get_alert_history` to the v2 rule-history API — Context & Discussion

## Original Prompt
> Create a plan to migrate the v1 alert history endpoint to v2. Refer to ~/Stash/signoz for the openapi endpoint to see how v2 works. Also look at how skaff works and if we can reuse skaff for generating the client stubs.

## Reference Links
- SigNoz backend OpenAPI spec (generated from Go): `~/Stash/signoz/docs/api/openapi.yml`
- v2 route registration: `~/Stash/signoz/pkg/apiserver/signozapiserver/rulestatehistory.go`
- v2 handler impl: `~/Stash/signoz/pkg/modules/rulestatehistory/implrulestatehistory/handler.go`
- v2 request query structs: `~/Stash/signoz/pkg/types/rulestatehistorytypes/http.go`
- v2 response structs: `~/Stash/signoz/pkg/types/rulestatehistorytypes/response.go`
- v1 route (still live): `~/Stash/signoz/pkg/query-service/app/http_handler.go:494-497`
- v1 request/response model: `~/Stash/signoz/pkg/query-service/model/alerting.go`
- skaff generator: `~/Stash/skaff` (module `github.com/SigNoz/skaff`)
- skaff's real consumer: `~/Stash/terraform-provider-signoz` (`skaff.yml` + `internal/{customtypes,schemas,apitypes,apiclients,convertors,services}`)
- Prior related plan (explicitly deferred history migration): `plans/v2-convention-migration.plan.md`

## Key Decisions & Discussion Log

### 2026-07-14 — Research & scoping
- **Why now:** the earlier `v2-convention-migration` work (2026-04-22) moved rule CRUD to `/api/v2/rules/*` but *deferred* history, noting "`history/timeline` is not migrated upstream (only `history/filter_keys` is on v2)." That is no longer true — upstream now serves the full v2 history surface via `signozapiserver`. This plan closes that gap.
- **Current MCP surface (v1):** `POST /api/v1/rules/{id}/history/timeline` with a JSON body `types.AlertHistoryRequest{start,end,state,offset,limit,order,filters:{items,op}}`. Client method `SigNoz.GetAlertHistory` (`internal/client/client.go:632`). Handler `handleGetAlertHistory` (`internal/handler/tools/alerts.go:343`). Response is a **raw JSON passthrough** — there is intentionally no typed response struct; only `countAlertHistoryRows` (`aggregate_helper.go:591`) peeks at `data[]`/`data.items[]` for a completeness note. Second caller: `handleAlertSummaryResource` (`resource_templates.go:61`) for the `signoz://alert/{id}/summary` resource.
- **Target v2 endpoint:** `GET /api/v2/rules/{id}/history/timeline`. Pure GET with query params — no request body.
  - Query params (`PostableRuleStateHistoryTimelineQuery`): `start` int64 **required**, `end` int64 **required**, `state` (`ruletypes.AlertState`, optional), `filterExpression` string (optional; v5 QB expression), `limit` int64 (optional, server default 50), `order` (asc/desc, server default **desc**), `cursor` string (optional, opaque base64 of `{offset,limit}`).
  - Response envelope: `{"status":"success","data":{"items":[...],"total":N,"nextCursor":"<base64>"}}` (via `render.Success`). Errors: `{"status":...,"error":{...}}` on 400/401/403/500 (via `render.Error`).
  - Item shape (`GettableRuleStateHistory`): `ruleId`, `ruleName`, `overallState`, `overallStateChanged`, `state`, `stateChanged`, `unixMilli`, `labels` (structured `[{key,value}]` array), `fingerprint`, `value`.
- **v1 → v2 diffs that matter to the MCP client:**
  1. Method `POST`+body → `GET`+query string.
  2. `filters` (structured `{items,op}` FilterSet) → `filterExpression` (single v5 QB string). The MCP tool always sent an *empty* filter set, so default behavior is preserved by simply omitting `filterExpression`.
  3. Raw `offset` → opaque `cursor` (base64 `{offset,limit}`, encoded/decoded server-side). Raw offset is no longer part of the wire contract.
  4. Response gains `nextCursor` and drops the v1 `data.labels` map (labels now live on dedicated `filter_keys`/`filter_values` endpoints). Item key `ruleID` → `ruleId`; `labels` string → structured array.
- **v1 is NOT deprecated/removed upstream** — the `POST /api/v1/.../history/timeline` route is still registered (unlike `/api/v1/services`, which carries an explicit "Deprecated" comment). So this is a forward-alignment migration for consistency with the already-v2 rule CRUD, not a forced break. We accept a hard cutover to v2 on the MCP side (same stance the `v2-convention-migration` plan took for rule CRUD).
- **v2 `state` enum values:** `inactive`, `pending`, `firing`, `nodata`, `disabled` (`~/Stash/signoz/pkg/types/ruletypes/alert_state.go`). Note the no-data spelling is `nodata` (no hyphen). The MCP tool today only exposes `firing`/`inactive` — keep that minimal set for now (recommendation); expansion is a separate, additive decision.

### 2026-07-14 — skaff evaluation (can we reuse it for client stubs?)
- **What skaff is:** a standalone SigNoz Go CLI (`github.com/SigNoz/skaff`, checkout at `~/Stash/skaff`) that generates a **layered Terraform plugin-framework provider** from an OpenAPI spec + a `skaff.yml`. It orchestrates HashiCorp's `tfplugingen-openapi`/`tfplugingen-framework` and `oapi-codegen`, then adds custom types, TF schemas, wire DTOs, a typed HTTP client, and expand/flatten convertors. Its only real consumer is `terraform-provider-signoz`.
- **Verdict: do NOT use skaff for this migration.** Reasons:
  1. **Wrong target shape.** skaff emits Terraform-framework code (custom types, TF schemas, expand/flatten convertors, `resource.Resource`/`datasource.DataSource` shells) — none of which the MCP server has or wants.
  2. **Wrong client architecture.** Even skaff's `apitypes`/`client` subcommands produce an `oapi-codegen` typed client with generated DTOs. The MCP server deliberately uses a hand-written `SigNoz` client + `doRequest` with bespoke retry, response-size caps, tenant context, OTel spans, auth-header injection, and `upstreamError` status mapping, and returns **raw `json.RawMessage` passthroughs** (no typed response structs). Adopting a generated typed client would be a repo-wide rewrite, not a one-endpoint stub.
  3. **Not wired here, and overkill.** skaff is absent from this repo (no `skaff.yml`, no `zz_generated_*`), and the change is ~a dozen lines of hand-written client + handler edits. Pulling in a codegen toolchain for one GET endpoint is disproportionate.
  - **If** spec-driven codegen is ever wanted in this repo, the sanctioned direction is the repo's own `auto-gen-tools` design (custom `kin-openapi` generator emitting MCP-shaped tools; `plans/auto-gen-tools.plan.md`), which explicitly chose *not* to use oapi-codegen — not skaff. That is out of scope here.
- **We DO reuse the spec, not the tool.** `~/Stash/signoz/docs/api/openapi.yml` is the source of truth for the exact v2 param/response shapes; we transcribe from it by hand (as every other endpoint in this client is written).

## Key Decisions & Discussion Log (cont.)

### 2026-07-14 — Recommended answers locked in
User directed: "lock in the recommended answers." The four questions with a
recommendation are resolved as follows; the plan is finalized to match. The
min-version item stays open (awaits a released version number, no recommendation
to lock). Implementation has not started — plan stays `Planning` until coding begins.
- **Pagination → opaque `cursor` (option a).** Drop the `offset` param; add a `cursor`
  string param and surface the response's `data.nextCursor` in the completeness note.
- **`filterExpression` → add now** as an optional passthrough string (v5 QB expression).
- **`state` enum → keep minimal** (`firing`/`inactive`); expansion is a separate additive change.
- **Scope → `timeline` only.** The sibling v2 history endpoints (`stats`,
  `top_contributors`, `overall_status`, `filter_keys`, `filter_values`) are new tools
  tracked separately, not part of this migration.

### 2026-07-14 — Implementation complete (pending PR)
- Client: `GetAlertHistory` rewritten to `GET /api/v2/rules/{id}/history/timeline` with a query string; interface/mock signatures unchanged.
- Types: `AlertHistoryRequest` reshaped (dropped `Offset`/`Filters`, added `FilterExpression`/`Cursor`) with a new `QueryParams() url.Values`; removed `AlertHistoryFilters`.
- Handler: dropped the `offset` param, added `cursor` + `filterExpression`, refreshed the tool description; a passed `offset` is now silently ignored.
- Completeness: new `alertHistoryCompletenessNote` reads `data.nextCursor` as the authoritative hasMore signal (falls back to the row-count heuristic only when the cursor is absent and rows are uncountable). `countAlertHistoryRows` unchanged (still counts `data.items[]`/`data[]`).
- Resource template: dropped the removed `Offset:0` field.
- Tests: rewrote `client_test.go` `TestGetAlertHistory` for GET/query/v2-envelope; repointed `TestHandleGetAlertHistoryFamilyA_TopLevelDataArrayCompletenessNote` to v2 cursor semantics (no cursor → hasMore=false); added `TestHandleGetAlertHistory_NextCursorHasMore` (cursor+filter forwarded; nextCursor → hasMore=true). Full `go build`/`go vet`/`go test ./...` green.
- Docs: `manifest.json` + `README.md` updated (v2 path, `cursor`, `filterExpression`, response shape, required-version note).
- **agent-skills check outcome:** no change needed — no shipped skill teaches the alert-history parameter surface (history is read-only; the create/update skills cover different contracts). To be re-stated in the PR summary.
- Not yet done: commit/PR; fill the concrete minimum SigNoz version into README once known.

## Open Questions
- [x] **Pagination surface** → RESOLVED (2026-07-14): opaque `cursor` (option a). Drop raw `offset`; surface `nextCursor`.
- [x] **Add `filterExpression` param now, or defer?** → RESOLVED (2026-07-14): add now as an optional passthrough string.
- [x] **Expand the `state` enum?** → RESOLVED (2026-07-14): keep `firing`/`inactive`; expansion deferred.
- [x] **Migrate only `timeline`, or also the sibling v2 history endpoints?** → RESOLVED (2026-07-14): `timeline` only.
- [ ] **Minimum SigNoz version** that ships the v2 `signozapiserver` rule-history routes — fill into README once confirmed (same open item as `v2-convention-migration`).
