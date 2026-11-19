# Plan: Top-operations limit and units on the service tools

Status: Done
Issue: [SigNoz/nerve-pod#655](https://github.com/SigNoz/nerve-pod/issues/655)
PR: [SigNoz/signoz-mcp-server#465](https://github.com/SigNoz/signoz-mcp-server/pull/465)

## Context

`signoz_get_service_top_operations` always asked SigNoz for 5000 operations and returned all of
them, so a service with many operations, or long operation names, produced a result of megabytes
with no way to ask for less. SigNoz's `POST /api/v2/service/top_operations` already takes a
`limit` and applies it after ordering operations by p99 (`buildTopOpsQueryRangeRequest` in
SigNoz's `pkg/modules/services/implservices/module.go`).

Neither service tool stated its units. Both return latencies in nanoseconds, and
`signoz_list_services` returns `callRate` per second and `errorRate` and `fourXXRate` as
percentages. An agent that reads a nanosecond value as milliseconds is off by a factor of a
million. The issue has the production evidence for both problems.

## Approach

Add `limit` to the top-operations tool and send it to SigNoz, so SigNoz returns the slowest
operations and the tool trims nothing itself. Keep the bare-array output. State the units in both
tool descriptions and the README; descriptions reach the model on every client.

## Files to Modify

- `internal/handler/tools/services.go`: `limit` parameter and parsing, the limit-filled note, and
  the units in both descriptions.
- `internal/client/client.go`, `internal/client/interface.go`: pass the limit through.
- `internal/handler/tools/service_top_operations_test.go`: handler tests through the real client.
- `tests/e2e/tests/test_service_listing.py`: units and the limit against the pinned SigNoz.
- `README.md`, `internal/mcp-server/testdata/wire-catalog/tools-list.json`: the two tool entries.

## Key Decisions

### 2026-11-19 — Default 100, maximum 5000, bare array kept

- Decision: `limit` defaults to 100, matching the aggregate tools' default, and values above 5000
  are clamped to 5000, the previous fixed request. The result stays a JSON array of operations.
- Rationale: 100 operations keep a typical service's table complete while bounding the result.
  A `{data, pagination}` shape with `offset` would break callers that read the array, and a
  ranked top-N list rarely needs a second page.
- Alternatives rejected: offset paging in the server (SigNoz has no offset on this route, so each
  page would refetch `limit + offset` operations).

### 2026-11-19 — Say how to see more when the limit is filled

- Decision: when a result fills a limit below 5000, a text note says the service likely has more
  operations and to raise `limit` or rank by calls or errors with `signoz_aggregate_traces`. At
  5000 the existing completeness note stays. The result is text only, so clients that show only
  structured content still pass the note to the model.
- Rationale: the default now cuts results that used to be complete; the note keeps that visible.

### 2026-11-19 — Limit parsing follows the shared rules

- Decision: `limit` uses `intArg`, like every other limit parameter: omitted, empty, or
  non-positive values take the default, and a non-numeric value is `VALIDATION_FAILED` before any
  call to SigNoz.

## Reference Links

- [SigNoz/nerve-pod#655](https://github.com/SigNoz/nerve-pod/issues/655)

## Verification

- `GOTOOLCHAIN=go1.26.11 make ci`: every step passes.
- Handler tests in `service_top_operations_test.go` run through the real client against an
  `httptest` upstream: the limit sent to SigNoz (default 100, string and number forms, clamped to
  5000), the notes when a result fills the limit below and at 5000, and a non-numeric limit
  rejected without reaching SigNoz. They fail against the previous handler.
- E2E against the pinned SigNoz, run locally (4 passed): `test_service_listing.py` checks that
  `list_services` reports `p99` and `avgDuration` in nanoseconds and `errorRate` as a percentage,
  and that top operations come highest p99 first, in nanoseconds, with `limit=1` keeping only
  the slowest and adding the note. The existing top-operations checks in
  `test_enums_and_grammar.py` and `test_param_coercion.py` pass with the new default.

## Outcome

Shipped as planned. Follow-ups outside this change: result notes on structured results do not
reach the model on Claude Code ([SigNoz/nerve-pod#654](https://github.com/SigNoz/nerve-pod/issues/654)).
