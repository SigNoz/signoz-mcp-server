# Plan: Migrate `signoz_get_alert_history` to the v2 rule-history API

## Status
In Progress

## Context

`signoz_get_alert_history` now calls the v2 timeline API introduced in SigNoz
v0.118.0. The tool remains a raw response passthrough: the server forwards the
upstream cursor and appends the existing cursor-aware completeness note.

The PR review follow-up keeps only the contract fixes needed by agents: a
route-free tool description, canonical `filter` naming, the complete state
enum, explicit rejection of removed `offset`, and correct handling of the
upstream cursor's encoded page size.

## Approach

### Client and request

- Keep `GetAlertHistory` on the shared `doRequest` path and its existing 64 MiB
  response guard.
- Send the v2 GET query parameters through `AlertHistoryRequest.QueryParams()`.
- Forward `data.nextCursor` unchanged. On a cursor follow-up with no explicit
  `limit`, send no limit so SigNoz uses the page size encoded in its cursor.

### Agent-facing contract

- Omit the HTTP method and backend route from the runtime and manifest tool
  descriptions.
- Advertise canonical `filter`; continue accepting backend-shaped
  `filterExpression` as an unadvertised compatibility alias.
- Advertise and validate all six v2 states: `inactive`, `pending`, `recovering`,
  `firing`, `nodata`, and `disabled`.
- Reject any legacy `offset` with guidance to use `data.nextCursor`.
- Tell callers to repeat the original time range, state, filter, and order on
  follow-up calls.

### Completeness and errors

- Keep `alertHistoryCompletenessNote` beside the existing pagination helpers in
  `aggregate_helper.go`.
- Derive `hasMore` from a non-empty upstream `data.nextCursor` and include that
  cursor in the note.
- Preserve the shared coded upstream error path. Add concise recovery guidance
  for HTTP 404 because the rule may be missing or the SigNoz version may predate
  v0.118.0.

### Docs and metadata

- Document the v0.118.0 alert-history requirement separately from the v0.120.0
  alert-rule CRUD requirement.
- Keep README parameters, manifest metadata, and runtime schema aligned.
- No companion `SigNoz/agent-skills` update is needed because shipped skills do
  not teach this read-only tool contract.

## Files to Modify

- `internal/handler/tools/alerts.go` — schema, validation, raw cursor forwarding,
  cursor-limit behavior, and 404 guidance.
- `internal/handler/tools/aggregate_helper.go` — cursor completeness note.
- `pkg/types/alerts.go` — v2 state documentation.
- Focused handler/schema/E2E tests — state, filter, description, cursor, and
  limit coverage.
- `README.md`, `manifest.json` — compatibility and agent-facing metadata.
- `plans/alert-history-v2-migration.context.md` — append-only decision record.

## Verification

1. Run focused alert-history and client tests.
2. Run `go test ./...`, `go vet ./...`, and `go build ./...`.
3. Run `python3 -m json.tool manifest.json` and `git diff --check`.
4. Do not commit, push, resolve review threads, or edit the PR until the user
   confirms.
