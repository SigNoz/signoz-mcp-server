# Plan: Downtime (Planned Maintenance) Schedule Tools

## Status
Done

## Context
Users ask agents to "silence" SigNoz alerts during maintenance. SigNoz exposes no Alertmanager silences API — Alertmanager
is embedded as a library, so its `/api/v2/silences` routes are never mounted, and the alertmanager `Handler` interface has
no silence method. The supported muting surface is Planned Maintenance / Downtime Schedules
(`/api/v1/downtime_schedules`), whose muter is also what makes `signoz_list_alerts` report an alert as `silenced`. The MCP
server had no tools for that surface, so an agent could observe a silenced alert but never create or inspect the window
causing it.

## Approach
Four tools, registered from a new handler group appended to `RegisterAllToolHandlers`:

- `signoz_list_downtime_schedules` — read-only. Tri-state `active` / `recurring` (via `boolOrStringType()` +
  `parseTriStateBool`) forwarded only when supplied, so the backend default applies when absent; a garbage value is a
  validation error. Locally paginated with `paginate.ParseParamsClamped` → `Array` → `Wrap` → `listResult`.
- `signoz_get_downtime_schedule` — read-only. Required `id`, guarded with `util.IsUUIDv7` before any upstream call;
  upstream object returned via `structuredResult` so unknown fields survive.
- `signoz_create_downtime_schedule` — create. Typed input schema `types.CreateDowntimeScheduleInput` (so `SearchContext`
  is a field on the type, as typed schemas replace option-built properties). `searchContext` and the server-derived
  fields (`id`, `status`, `kind`, created/updated audit fields) are deleted from the payload before it is forwarded.
  Local validation is limited to `name` non-empty and `schedule` being an object; window ordering, recurrence
  completeness, and the expr-lang `scope` are the backend's to reject, and its 400 propagates via `upstreamError` so the
  real message reaches the agent.
- `signoz_delete_downtime_schedule` — delete. UUIDv7 guard, then the `{"status":"success","id":...}` envelope mirroring
  `handleDeleteAlert`.

Client layer gains four methods. `ListDowntimeSchedules` takes `active, recurring *bool` and sets each query param only
when non-nil. `CreateDowntimeSchedule` uses the plain mutating `doRequest` POST path with the write timeout — never
`doReplaySafePost`, since a replay would duplicate the window.

Defensive upstream parsing: `decodeDowntimeScheduleList` accepts both the current render envelope
(`{"status":"success","data":[...]}`) and the bare array older `ee/query-service` builds returned for the same path.
Unrecognized shapes fail open to an empty list and always emit a WARN, keeping drift detectable and distinguishable from
a genuinely empty workspace.

No output schemas (that would require editing the pinned `expectedOutputSchemaTools` list); schedules pass through as
`map[string]any`/`[]any`. No update tool.

## Files to Modify
- `pkg/types/downtime.go` (new) — `CreateDowntimeScheduleInput`, `DowntimeScheduleWindow`, `DowntimeRecurrence` with
  `jsonschema` prose descriptions and the typed `SearchContext` field.
- `internal/handler/tools/downtime_schedules.go` (new) — tool registration, four handlers, defensive envelope decoder.
- `internal/handler/tools/register.go` — call `RegisterDowntimeScheduleHandlers` last.
- `internal/client/client.go` — `ListDowntimeSchedules`, `GetDowntimeSchedule`, `CreateDowntimeSchedule`,
  `DeleteDowntimeSchedule`.
- `internal/client/interface.go` — the four methods on `Client`.
- `internal/client/mock.go` — `...Fn` fields plus methods with nil-Fn defaults.
- `internal/handler/tools/annotations_inventory_test.go` — pin read/read/create/delete triples.
- `internal/handler/tools/nil_arguments_test.go` — add the three tools requiring ≥1 arg.
- `manifest.json` — four tool entries beside the alert tools.
- `README.md` — four summary-table rows and four per-tool detail sections (expr-lang `scope`, all-rules `alertIds`,
  server-derived `status`/`kind`).
- `internal/handler/tools/downtime_schedules_test.go` (new) — handler tests.
- `internal/client/client_test.go` — client tests for all four methods.
- `plans/downtime-schedules.{context,plan}.md` (new).

## Verification
- `go build ./...`
- `go test ./...`
- `go test -count=1 -run '^TestGuardrail_' ./...`
- `gofmt -l .`

Handler tests cover: list success and pagination; tri-state forwarding (true / false / absent → nil pointer) asserted on
the captured client arguments; invalid tri-state → validation error with no upstream call; legacy unwrapped envelope still
parses; unknown shape fails open to an empty list; unparseable body → error result; get success and missing id;
non-UUIDv7 id rejected for both get and delete with no upstream call; create strips `searchContext` and the server-derived
fields from the captured payload while forwarding `name`, `scope`, and `schedule`; create rejects empty/non-object payloads
and missing `name`/`schedule`; delete returns the success envelope; upstream failures surface the `SigNoz API error:` prefix.

Client tests assert exact method and path for all four routes, that the list omits absent query params and sets present
ones, that create sends `application/json` with the exact body and is attempted exactly once, and that 500 / 401 (and a
400 standing in for an invalid expr-lang `scope`) propagate as errors.

End-to-end verification against a live SigNoz instance (create a window, confirm it round-trips and mutes, then delete it
and confirm removal) is not covered by these unit tests and should be delegated to a subagent per CLAUDE.md.
