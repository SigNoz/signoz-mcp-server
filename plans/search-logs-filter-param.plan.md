# Plan: search_logs `filter` Param Alias

## Status
Planning

## Context
`signoz_search_logs` exposes its free-form filter argument as `query`, while its sibling
`signoz_aggregate_logs` calls the same thing `filter`. Models (and humans) naturally reach for
`filter` on `search_logs`. Because `parseSearchLogsArgs` only reads `args["query"]`
(`logs_helper.go:29`) and MCP silently ignores unknown properties, a `filter` argument is
dropped with no error: the search runs with an empty filter (time-window only) and returns
arbitrary logs. This produced a real failed session where an agent burned multiple turns
debugging "filter syntax" that was actually being discarded wholesale.

## Approach
Make `signoz_search_logs` accept `filter` as an alias for `query`, and align the schema/docs
with `signoz_aggregate_logs` so the two log tools share one vocabulary. No behavioral change
for existing `query` callers.

1. **Parser** (`logs_helper.go`): in `parseSearchLogsArgs`, read `filter` and fall back to it
   when `query` is empty (e.g. `query := firstNonEmpty(args["query"], args["filter"])`). Keep
   `query` working.
2. **Schema** (`logs.go`): add a `filter` string param to `signoz_search_logs` whose
   description matches the `aggregate_logs` `filter` wording, and note in the `query`
   description that `filter` is an accepted alias (or vice-versa). Keep `searchContext` as a
   top-level param per CLAUDE.md.
3. **Audit sibling**: confirm whether `signoz_search_traces` has the same `query`/`filter`
   divergence; if so, apply the same alias there (track as a follow-up if it widens scope).
4. **Docs sync** (CLAUDE.md checklist — this IS a schema change):
   - `README.md` — update the `signoz_search_logs` parameter table to list `filter`/`query`.
   - `manifest.json` — update the `signoz_search_logs` tool metadata/param descriptions.
   - Grep `docs/` for stale `signoz_search_logs` references.
   - Call out the doc updates explicitly in the PR summary.

## Files to Modify
- `internal/handler/tools/logs_helper.go` — `parseSearchLogsArgs`: accept `filter` as alias for `query`
- `internal/handler/tools/logs.go` — add/align `filter` param on `signoz_search_logs` schema
- `internal/handler/tools/logs_test.go` — regression test: `filter` arg produces the same
  filter expression as `query`; both-empty and both-set precedence covered
- `README.md` — `signoz_search_logs` parameter table
- `manifest.json` — `signoz_search_logs` tool metadata
- (conditional) `internal/handler/tools/traces.go` — same alias if `search_traces` diverges

## Verification
- `go build ./...`, `gofmt -l`, `go vet ./internal/handler/tools/` clean.
- `go test ./internal/handler/tools/` passes, including a new test asserting
  `parseSearchLogsArgs({"filter": "host LIKE 'storm%'"})` yields filter expression
  `host LIKE 'storm%'` (and that `query` still works and takes precedence when both set).
- Manual: invoke `signoz_search_logs` with `filter` against a live instance and confirm the
  filter is actually applied (reproduces the screenshot scenario, now returning scoped logs).
- README.md / manifest.json reflect the new `filter` param.
