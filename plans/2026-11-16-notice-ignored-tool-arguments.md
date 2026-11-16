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
- Registration declares the exceptions next to each handler through `addTool` options:
  `acceptsUndeclaredArguments` for legacy aliases a handler still reads (`ruleId`, `viewId`,
  `filterExpression`, the docs `query`) and for a fetched dashboard's read-only fields, and
  `forwardsArguments` for tools that send the whole argument object to SigNoz as a resource body.
- The notice repeats only short identifier-like key names (at most 10, 64 bytes each); any other
  name is counted, so a client-chosen string can't inject arbitrary text into a server notice.
- Telemetry is unchanged: every undeclared key, alias or not, is still logged and counted.

## Files to Modify

- `internal/handler/tools/schema_compat.go`: tool options, the ignored-arguments notice.
- `internal/handler/tools/alerts.go`, `views.go`, `docs.go`, `dashboards.go`: alias constants shared
  by the handler read and the registration; pass-through and fetched-body options.
- `internal/handler/tools/schema_compat_test.go`: handler tests through the registered tools.
- `internal/handler/tools/normalization_replay_test.go`: checks the schema-mismatch notice only,
  because its stub registration drops the alias policy.
- `README.md`: the input-validation note.

## Key Decisions

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

### 2026-11-16 — Same notice prefix as schema mismatches

- Decision: the new notice starts with `Input validation notice:`.
- Rationale: one handle for every in-band input problem; the README already documents it.

## Reference Links

- [SigNoz/nerve-pod#641](https://github.com/SigNoz/nerve-pod/issues/641)
- [SigNoz/signoz-mcp-server#415: log and count undeclared arguments](https://github.com/SigNoz/signoz-mcp-server/pull/415)
- `docs/mcp-best-practices.md` (SCH-2, CMP-1), `docs/client-visible-writing-style.md`

## Verification

- `go test ./internal/handler/tools -run TestSuccessfulResultNamesTheArgumentsTheToolIgnored`
  covers `searchText` on `signoz_aggregate_logs`, declared-only arguments, `ruleId` on
  `signoz_get_alert`, a fetched dashboard sent to `signoz_update_dashboard`, and a non-identifier
  key that is counted but not repeated. Removing the `ruleId` alias or the dashboard field list
  fails the test.
- `GOTOOLCHAIN=go1.26.0 make ci` in a clean worktree: every step passes except `lint`, which
  fails the same way on `main` locally (staticcheck SA5011 in untouched test files) and is left to
  the CI lint job.
- No e2e test: the notice is decided by this server, not SigNoz.

## Outcome

Shipped as planned. Successful calls name the undeclared arguments the tool ignored; aliases,
fetched dashboard fields, and pass-through tools draw no notice; telemetry is unchanged. No
SigNoz/agent-skills change: the notice is additive and changes no tool, parameter, or payload.
