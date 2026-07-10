# Plan: Trace Span Links

## Status

Done

## Context

`signoz_get_trace_details` promises a full trace but its fixed v5 query-range projection omits the canonical `links` column. For asynchronous traces, this removes the only producer-to-consumer correlation edge.

## Approach

1. Add a client-level regression around `GetTraceDetails` using a query-aware HTTP fixture.
2. Give the fixture a realistic stored OpenTelemetry link (`traceID`, `spanID`, `refType`) and return it only when the request selects canonical `links`.
3. Prove the test fails at the pinned commit.
4. Add canonical `links` to `BuildTracesQueryPayload` as a span-context string field. Since both trace search and detail use the helper, both gain the field without a second code path.
5. Update focused assertions and user-facing trace parameter documentation if the output contract needs clarification.
6. Run formatting, focused tests, then the full Go test suite.

## Files to Modify

- `internal/client/client_test.go` — end-to-end client/request-projection regression fixture.
- `pkg/types/querybuilder.go` — select canonical `links`.
- `pkg/types/querybuilder_test.go` — pin canonical field metadata and reject deprecated `references`.
- `README.md` — document the additive linked-span field if required by repository contract rules.
- `PROGRESS.md` — exact commands, evidence, outcomes, and limitations.

## Verification

- Show the new regression failing before implementation.
- `gofmt` on changed Go files.
- `go test ./pkg/types ./internal/client ./internal/handler/tools`.
- `go test ./...`.
- `go vet ./...` if time permits.
- No credentialed/live SigNoz test unless an already-configured, read-only environment exists and can be used without exposing secrets.
