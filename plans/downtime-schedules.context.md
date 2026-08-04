# Feature: Downtime (Planned Maintenance) Schedule Tools — Context & Discussion

## Original Prompt
> Implement four MCP tools for SigNoz planned-maintenance (downtime) schedules in this Go repo (signoz-mcp-server).
>
> Background: SigNoz has NO Alertmanager silences API (verified: `pkg/alertmanager/handler.go`'s Handler interface has no
> silence method; Alertmanager is embedded as a library so its `/api/v2/silences` is never mounted). The actual muting
> mechanism is **Planned Maintenance / Downtime Schedules** (`pkg/alertmanager/alertmanagerserver/maintenance_muter.go`).
> This is what makes `signoz_list_alerts` report an alert as `silenced`.
>
> Routes (`pkg/apiserver/signozapiserver/ruler.go:119-188`, impl `pkg/ruler/signozruler/handler.go:188+`):
> List `GET /api/v1/downtime_schedules?active=<bool>&recurring=<bool>`; Get `GET /api/v1/downtime_schedules/{id}` (UUIDv7);
> Create `POST /api/v1/downtime_schedules` → 201 with body; Update `PUT .../{id}` → 204 no body; Delete `DELETE .../{id}` → 204.
> Responses are wrapped `{"status":"success","data":...}` via `pkg/http/render`.
>
> Create body = `PostablePlannedMaintenance`. Required: `name`, `schedule`; `schedule.startTime` must precede
> `schedule.endTime`; if `recurrence` present, `repeatType` (`daily|weekly|monthly`) and `duration` are required.
> `scope` is an **expr-lang boolean expression** (NOT Alertmanager matchers), compiled server-side with `expr.AsBool()`;
> invalid scope → 400. Empty/omitted `alertIds` means the schedule applies to ALL alert rules. `status`
> (`active|upcoming|expired`) and `kind` (`fixed|recurring`) are server-derived and NOT settable. `Schedule.MarshalJSON`
> re-renders times in the schedule's timezone, so do NOT assume UTC-Z on read.
>
> Tools: `signoz_list_downtime_schedules` (read-only, tri-state `active`/`recurring`, paginated),
> `signoz_get_downtime_schedule` (read-only, required `id`), `signoz_create_downtime_schedule` (create),
> `signoz_delete_downtime_schedule` (delete). Do NOT build an update tool (out of scope; PUT returns 204 and the issue
> explicitly defers it).
>
> Requirements included: handler file `internal/handler/tools/downtime_schedules.go` registered via `h.addTool` only, one
> annotation composite per tool, mandatory `searchContext` (typed field on the create input type, stripped from the
> forwarded body), handlers never returning a non-nil Go error, coded error helpers only, UUIDv7 id guard, delete result
> envelope mirroring `handleDeleteAlert`, `paginate` pipeline on list, `boolOrStringType()` + `parseTriStateBool` for
> tri-state booleans, no output schemas, `map[string]any` passthrough; four client methods (mutating POST via the plain
> `doRequest` path, never `doReplaySafePost`) plus interface and mock updates; a defensive envelope parser that accepts
> both the wrapped and legacy bare shapes with a WARN on drift; no client-side expr-lang validation; registry updates
> (annotations inventory, nil-arguments test, `manifest.json`, `README.md`); handler and client tests; and this plan pair.

## Reference Links
- [Issue #268 — silence/downtime tools](https://github.com/SigNoz/signoz-mcp-server/issues/268)
- SigNoz backend routes: `pkg/apiserver/signozapiserver/ruler.go` (lines ~119-188)
- SigNoz backend handler impl: `pkg/ruler/signozruler/handler.go` (from ~line 188)
- Muting mechanism: `pkg/alertmanager/alertmanagerserver/maintenance_muter.go`
- Envelope: `pkg/http/render`
- Absence of a silences API: `pkg/alertmanager/handler.go` (Handler interface has no silence method)

## Key Decisions & Discussion Log

### 2026-08-03 — Pivotal finding: there is no silences API, downtime schedules are the mechanism
- Issue #268 originally asked for Alertmanager **silence** tools (create/list/expire a silence).
- Verification of the SigNoz backend shows there is **no silences API at all**: the alertmanager `Handler` interface in
  `pkg/alertmanager/handler.go` exposes no silence method, and because Alertmanager is embedded as a library rather than
  run as a server, its native `/api/v2/silences` routes are never mounted.
- The real, supported muting surface is **Planned Maintenance / Downtime Schedules**
  (`pkg/alertmanager/alertmanagerserver/maintenance_muter.go`). That muter is also what makes `signoz_list_alerts`
  report an alert as `silenced`, so the concept users mean by "silence" maps onto these schedules.
- Decision: implement the feature against `/api/v1/downtime_schedules` and describe the tools in silencing terms so an
  agent reaching for "silence this alert" finds them.

### 2026-08-03 — Design decisions
- **No update tool.** PUT returns 204 with no body and the issue explicitly defers update; shipping list/get/create/delete
  keeps the surface small. Revisit only if users ask to extend a live window.
- **`scope` validation is left to the backend.** `scope` is an expr-lang boolean expression compiled with `expr.AsBool()`.
  Reimplementing that grammar client-side would drift from the compiler and produce worse messages, so the handler forwards
  `scope` verbatim and lets the backend's 400 propagate through `upstreamError`, where the real compiler message reaches
  the agent. Noted in a code comment so it is not "fixed" later.
- **Defensive envelope parsing.** These endpoints are undocumented, and older SigNoz builds served the same paths from the
  legacy `ee/query-service` router **without** the `{"status":"success","data":...}` render envelope. `decodeDowntimeScheduleList`
  therefore accepts both a wrapped `data` array and a bare array. Any other shape fails open to an empty list but always
  emits a WARN, so a contract change is detectable and an empty workspace (which logs nothing) stays distinguishable from
  a shape change. Per CLAUDE.md: fail open, never fail silent.
- **`status`/`kind` stripped as server-derived.** Both are computed by SigNoz from the window, so they are absent from the
  typed create input and deleted from any raw payload an agent supplies, along with `id` and the created/updated audit
  fields. Read paths keep them by passing the upstream object through untyped.
- **Times are not assumed UTC.** `Schedule.MarshalJSON` re-renders times in the schedule's own timezone; reads pass values
  through unmodified and the README says so.
- **Empty `alertIds` is dangerous by default.** Omitting it mutes ALL alert rules, so both the tool description and the
  README state this explicitly and steer the agent to confirm rules via `signoz_list_alert_rules` first.
- **Create POST is single-attempt.** `CreateDowntimeSchedule` uses the plain `doRequest` mutating path, not
  `doReplaySafePost`: a retried POST would create a duplicate window (also enforced by
  `TestGuardrail_MutatingPOSTNotRetried`).
- **`id` has no legacy alias.** These tools are new, so only the canonical `id` param is accepted — no `scheduleId` alias
  is invented.

## Open Questions
- [x] Should an update tool ship in this PR? — No; deferred by the issue, and PUT's 204 gives nothing to echo back.
- [x] Should `scope` be validated client-side? — No; the backend expr-lang compiler owns it and its 400 propagates.
- [x] Which envelope does the list route return? — Both shapes are tolerated; current builds wrap, legacy builds do not.
- [ ] Does `Recurrence` accept its own `startTime`/`endTime` bounds in addition to `repeatType`/`repeatOn`/`duration`? Only
      the three documented fields are modelled; extend the typed input if a live instance shows more.
