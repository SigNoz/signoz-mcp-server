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

### 2026-07-15 — Live smoke verification (staging) + follow-up test
- Ran a read-only live smoke against `app.us.staging.signoz.cloud` (delegated to a subagent; API key via env, never persisted). Exercised the real `GET /api/v2/rules/{id}/history/timeline` via curl AND through the actual `signozclient.NewClient(..., "SIGNOZ-API-KEY", ...)` → `handleGetAlertHistory` path.
- **Result: PASS.** 200 OK; envelope `{"status":"success","data":{items,total,nextCursor}}`; item fields `ruleId/ruleName/overallState/overallStateChanged/state/stateChanged/unixMilli/labels/fingerprint/value` all present. Completeness note correctly derived `hasMore=true` from `data.nextCursor`; cursor pagination advanced (cursor decodes to `{offset,limit}`); `state:"firing"` accepted; `filterExpression` forwarded faithfully (a bad label yielded a clean upstream 400 via the `upstreamError` path, reproduced by raw curl — not an encoding bug).
- Added `TestHandleGetAlertHistory_ItemsExactFillNoCursor` — pins that a `data.items[]` page filling the limit with no `nextCursor` reports `hasMore=false` (the exact case the old row-count heuristic got wrong).
- **Notes for any FUTURE typed consumer** (do not affect this raw-passthrough tool): v2 `labels` is an array of `{key:{name,signal,fieldContext,fieldDataType}, value}` objects (not a flat `map[string]string`); `fingerprint` is a `uint64` that can exceed int64 max (don't unmarshal into `int64`).

## Open Questions
- [x] **Pagination surface** → RESOLVED (2026-07-14): opaque `cursor` (option a). Drop raw `offset`; surface `nextCursor`.
- [x] **Add `filterExpression` param now, or defer?** → RESOLVED (2026-07-14): add now as an optional passthrough string.
- [x] **Expand the `state` enum?** → RESOLVED (2026-07-14): keep `firing`/`inactive`; expansion deferred.
- [x] **Migrate only `timeline`, or also the sibling v2 history endpoints?** → RESOLVED (2026-07-14): `timeline` only.
- [x] **Minimum SigNoz version** → RESOLVED (2026-07-15): **v0.118.0** (2026-04-08). The v2 routes were added and wired in commit `bb4e7df` (PR #10488, 2026-03-30); `git tag --contains` shows `v0.118.0` is the earliest stable release containing it (`v0.117.x` does not). Documented in README.

### 2026-07-15 — PR review follow-up and user direction
- User asked to address every valid review issue locally and explicitly directed that the agent-facing tool description must not mention `GET /api/v2/rules/{id}/history/timeline`. No commit, push, review-thread resolution, or PR-body edit is authorized until the user confirms.
- Live review-thread refresh confirmed two open findings: custom page limits were not preserved on cursor-only follow-ups, and the v2 state enum was incomplete. Independent runtime/docs/MCP-practice passes also confirmed silent legacy `offset`, canonical-filter, pagination-drift, version-recovery, and compatibility-documentation gaps.
- **Pagination decision superseded:** raw upstream cursors encode only offset/limit, so `id+cursor` could silently recompute a moving default time window and drop state/filter/order. The tool now returns a versioned MCP cursor containing the full absolute scope plus the inner upstream cursor. Follow-ups pass only `id`, `cursor`, and `searchContext`; unsafe or mismatched tokens fail before the upstream call.
- **Filter decision superseded:** the MCP schema now advertises canonical `filter`, while the just-introduced backend-shaped `filterExpression` name remains an accepted alias. Conflicting aliases and wrong types fail loudly to prevent accidentally broad results.
- **State decision superseded:** the tool now exposes all authoritative v2 values: `inactive`, `pending`, `recovering`, `firing`, `nodata`, and `disabled`.
- **Offset compatibility:** the removed field cannot safely map to the opaque v2 cursor. Legacy `offset=0` is accepted only with a deprecation note; nonzero or malformed offset values return actionable `VALIDATION_FAILED` guidance.
- **Cross-boundary drift:** malformed/inconsistent `items`, `total`, or `nextCursor` now emits a WARN and a fail-open “more results may exist” note. Cursor rewriting uses `json.RawMessage` maps so uint64 fingerprints remain exact.
- **Docs/metadata:** runtime and manifest descriptions omit the backend route, the runtime description includes the v0.118.0 requirement, README compatibility is split from the v0.120.0 alert-rule CRUD requirement, and no companion `SigNoz/agent-skills` change is needed because shipped skills do not teach this contract.

### 2026-07-15 — Independent-review hardening
- A final independent runtime/MCP/docs pass found additional valid edge cases, all addressed locally before handoff.
- Filter guidance no longer promises that unknown label keys hard-error; it advises unfiltered key discovery and retry because unknown labels generally return no matches. Fractional offsets are rejected before the shared loose integer parser can truncate them to zero.
- Repeated cursors, cursor-without-row-progress pages, cursors after the reported total, and pages exceeding the clamped limit now withhold `nextCursor`, emit a WARN, and return fail-open recovery guidance. Cursor encoding and decoding enforce the same 64 KiB token bound.
- Cursor rewriting can hold multiple JSON representations, so alert history now uses a 16 MiB response cap rather than the generic 64 MiB cap. Large uint64 item fields remain byte-exact.
- The alert-summary resource now wraps `recentHistory.data.nextCursor` with the same absolute scope; a resource-to-tool continuation test pins the second call path. Credential-gated E2E coverage follows a live cursor when available and exercises every v2 state.
- HTTP 404 recovery now preserves the shared upstream code/message/type/status fields, directs v0.118–v0.119 users to verify the rule in the UI, and names `signoz_list_alert_rules` only for v0.120.0+.

### 2026-07-15 — Resource-facing pagination recovery
- The independent re-review confirmed that logging alert-summary pagination drift was insufficient for resource consumers. The resource template now documents the history-tool handoff, and every summary includes machine-readable recent-history pagination or recovery metadata.
- When recent-history pagination cannot be verified, all usable response data is retained but any parseable raw `nextCursor` is removed. The consumer is told that more results may exist and to retry `signoz_get_alert_history` without a cursor and with a narrower time range or filter.
- A best-effort history-fetch failure is also visible in the summary as generic recovery metadata instead of being server-log-only; upstream error details are not leaked in the resource body.
- Malformed history bytes returned with HTTP 200 are omitted from the best-effort summary so they cannot invalidate the surrounding JSON; the valid alert and generic history-recovery metadata remain available.

### 2026-07-15 — Resource output simplification
- User direction: do not overengineer the resource response. The nested pagination/recovery metadata described above is superseded by one concise `recentHistoryNote`; safe continuation remains in `recentHistory.data.nextCursor`, and malformed HTTP-200 history bodies are still omitted so the alert summary remains usable.

### 2026-07-15 — Owner scope correction
- Owner rejected the alert-history-specific 16 MiB response cap and the MCP-owned cursor wrapper as overengineering. Those changes, their resource wrapping, and their extensive edge-case test surface were removed.
- The tool now forwards SigNoz's `data.nextCursor` unchanged and keeps `alertHistoryCompletenessNote` in `aggregate_helper.go`; deeper cursor refactoring remains in the separately tracked issue.
- The narrow limit fix remains: when a cursor follow-up omits `limit`, the MCP request also omits it so SigNoz uses the page size encoded in the upstream cursor. The runtime description tells callers to repeat the original time range, state, filter, and order.
- Filter compatibility is canonical-first (`filter`, then `filterExpression`) without a bespoke alias-conflict subsystem. Any supplied legacy `offset`, including zero, is rejected with direct cursor guidance.
