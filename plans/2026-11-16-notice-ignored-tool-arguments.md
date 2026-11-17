# Plan: Tell Agents Which Arguments a Tool Ignored

Status: Done
Issue: SigNoz/nerve-pod#641
PR: https://github.com/SigNoz/signoz-mcp-server/pull/450

## Context

SigNoz/signoz-mcp-server#415 (SigNoz/nerve-pod#529) started logging and counting top-level
arguments a tool's input schema does not declare, without telling the caller. Seven days of
`mcp.tool.validation.mismatches{validation.constraint="undeclared_argument"}` show about 36,900
such calls. Most are retired inputs that already fail with a migration error, or harmless keys, but
some change the answer without the agent knowing: `searchText` on `signoz_aggregate_logs` counts all
logs, and `aggregation` or `step` on `signoz_query_metrics` run the defaults.

## Approach

- `validationDecorator` appends an `Input validation notice:` text block to a successful result
  that names the undeclared arguments the handler ignored. Calls are not rejected, so clients that
  send extra keys keep working (CMP-1).
- When a call has undeclared keys, the decorator puts a per-call tracker in the context. The code
  that takes an undeclared key marks it where it does so: `readResourceID` and the docs `query` and
  alert-history `filterExpression` fallbacks mark an alias when they use it, the alert, dashboard,
  and view body builders mark every field they send to SigNoz, and the dashboard update filter marks
  the fetched body's read-only copies. Unmarked keys are reported.
- The notice repeats only short identifier-like key names (at most 10, 64 bytes each); any other
  name is counted, so a client-chosen string can't inject arbitrary text into a server notice.
- Telemetry is unchanged: every undeclared key, alias or not, is still logged and counted.

## Files to Modify

- `internal/handler/tools/argument_use.go`: the per-call tracker and its marking helpers.
- `internal/handler/tools/schema_compat.go`: the tracker in the decorator, the ignored-arguments
  notice.
- `internal/handler/tools/params.go`, `alerts.go`, `views.go`, `docs.go`, `dashboards.go`: mark
  aliases where they are read and bodies where they are serialized; the dashboard update filter.
- `internal/handler/tools/schema_compat_test.go`: handler tests through the registered tools.
- `internal/handler/tools/normalization_replay_test.go`: validates argument forms against the
  advertised schemas directly.
- `README.md`: the input-validation note. `CLAUDE.md`: a one-line rule to mark taken keys.

## Key Decisions

### 2026-11-16 — Handlers mark what they take; registration declares nothing

- Decision: replace the registration-time options (`acceptsUndeclaredArguments`,
  `forwardsArguments`) with marks placed where a handler reads, forwards, or tolerates a key.
- Rationale: suggested in a side-chat review of how the design scales to new tools. Registration
  options were a second copy of handler behavior: a handler that started filtering a forwarded body
  kept its option and dropped fields silently again, and nothing failed. A mark sits on the line
  that does the work, so changing the behavior changes the notice. It is also more precise: an alias
  overridden by `id` is now reported, and `signoz_create_view` reports the server-populated fields
  it strips, which its description says not to send. A forgotten mark still yields a visible false
  notice rather than a silent drop.

### 2026-11-16 — Pass-through tools draw no notice

- Decision: `signoz_create_alert`, `signoz_update_alert`, `signoz_create_dashboard`, and
  `signoz_create_view` get no notice.
- Rationale: their handlers forward undeclared keys to SigNoz, which decides what they mean. This
  server did not ignore them, so a notice saying it did would be false. `signoz_update_alert` also
  takes a fetched rule's audit fields back by design.

### 2026-11-16 — Fetched dashboard fields are accepted, derived from the generated type

- Decision: `signoz_update_dashboard` accepts the top-level fields of
  `apitypes.DashboardtypesGettableDashboardV2` plus `webUrl`.
- Rationale: its description tells agents to send the fetched `data` object back, and the handler
  drops read-only fields by design. Deriving the list from the generated type keeps it current after
  `make gen`; a misspelled top-level field still draws the notice.

### 2026-11-16 — Report `locked: true` on dashboard update

- Decision: the update filter tolerates `locked` only when it is false, so `locked: true` gets the
  standard ignored-arguments notice.
- Rationale: an independent review (GPT-6 Astra) confirmed that exempting the fetched fields also
  hid an attempt to lock a dashboard: the handler strips `locked` before the PUT. SigNoz rejects
  updating a locked dashboard, so after a successful update a copied `locked: false` is a no-op and
  `locked: true` is always a dropped lock request. Routine audit fields stay quiet.

### 2026-11-16 — Same notice prefix as schema mismatches

- Decision: the new notice starts with `Input validation notice:`.
- Rationale: one handle for every in-band input problem; the README already documents it.

## Reference Links

- [SigNoz/nerve-pod#641](https://github.com/SigNoz/nerve-pod/issues/641)
- [SigNoz/signoz-mcp-server#415: log and count undeclared arguments](https://github.com/SigNoz/signoz-mcp-server/pull/415)
- `docs/mcp-best-practices.md` (SCH-2, CMP-1), `docs/client-visible-writing-style.md`

## Verification

- `TestSuccessfulResultNamesTheArgumentsTheToolIgnored` calls the registered tools against an
  `httptest` SigNoz. It checks that the handler's own data stays the first block and that a notice
  is the last block, carries the prefix, and names the ignored arguments, without pinning its
  sentences. Cases: `searchText` on `signoz_aggregate_logs`; a non-identifier key that is not
  repeated; declared arguments only; `ruleId` on `signoz_get_alert` (the request uses it); a non-string
  `filterExpression` that the handler can't use (named); an
  undeclared field on `signoz_create_dashboard` (it reaches the POST, no notice); a fetched
  dashboard sent back to `signoz_update_dashboard` (the edit reaches the PUT, response-only
  fields don't); the same body with `locked: true` (notice); and that body failing with a 403 (the
  coded error and upstream reason survive, with no notices).
- The table passed unchanged when the registration options were replaced by marks, which shows the
  refactor kept behavior. Mutations, each failing the case meant to catch it: drop the alias mark,
  the forwarded-body mark, or the fetched-field mark; tolerate `locked: true`; send read-only fields
  in the PUT; append notices to error results; repeat non-identifier keys.
- `TestAdvertisedSchemasAdmitEveryArgumentFormHandlersRead` now validates each argument form
  against the schema `tools/list` advertises, instead of re-registering a stub (which dropped the
  alias policy) and scanning the result for notice text. Removing `intOrStringType` from the docs
  `limit` fails it.
- `GOTOOLCHAIN=go1.26.0 make ci` in a clean worktree: every step passes except `lint`, which
  fails the same way on `main` locally (staticcheck SA5011 in untouched test files); CI lint passes.
- No e2e test: the notice is decided by this server, not SigNoz.

## Outcome

Shipped with handler-side marks instead of the registration options first planned. Successful
calls name the undeclared arguments the handler did not take; aliases it reads, fields it forwards to
SigNoz, and a fetched dashboard's read-only copies draw no notice, except `locked: true` on dashboard
update. Telemetry is unchanged. No SigNoz/agent-skills change: the notice is additive and changes no
tool, parameter, or payload.
