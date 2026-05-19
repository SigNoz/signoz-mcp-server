# Plan: PromQL & ClickHouse SQL support in signoz_execute_builder_query

## Status
Done

## Context
The `signoz_execute_builder_query` tool description advertises PromQL support and points callers at `signoz://promql/instructions`, but the typed `QueryPayload` in `pkg/types/querybuilder.go` can only model builder queries. The handler in `internal/handler/tools/query_builder.go` unmarshals the incoming JSON into that typed payload, validates it, and re-marshals — silently dropping any PromQL `query` string and the (misleading) `compositeQuery.queryType` field. Callers following the documented shape get empty results with no error.

Beyond fixing the data loss, the tool description itself is wrong: the SigNoz core v5 `CompositeQuery` strictly rejects unknown fields and does not accept `compositeQuery.queryType`. The query type discriminator lives on each `QueryEnvelope.type` (e.g. `"promql"`, `"clickhouse_sql"`, `"builder_query"`).

## Approach

### 1. Discriminated `Query.Spec` decoding (pkg/types/querybuilder.go)

Add a typed PromQL/CH SQL spec and decode `Spec` based on `Query.Type` via a custom `UnmarshalJSON` on `Query`:

```go
type Query struct {
    Type string `json:"type"`
    Spec any    `json:"spec"` // QuerySpec | PromQLSpec | ClickHouseSQLSpec
}

type PromQLSpec struct {
    Name     string `json:"name"`
    Query    string `json:"query"`
    Disabled bool   `json:"disabled,omitempty"`
    Step     any    `json:"step,omitempty"`   // duration string or seconds (matches core's Step OneOf)
    Stats    bool   `json:"stats,omitempty"`
    Legend   string `json:"legend,omitempty"`
}

type ClickHouseSQLSpec struct {
    Name     string `json:"name"`
    Query    string `json:"query"`
    Disabled bool   `json:"disabled,omitempty"`
    Legend   string `json:"legend,omitempty"`
}
```

`Query.UnmarshalJSON` switches on `type`:
- `"promql"` → decode into `PromQLSpec`
- `"clickhouse_sql"` → decode into `ClickHouseSQLSpec`
- otherwise → decode into the existing `QuerySpec` (builder_query, builder_formula, builder_trace_operator, builder_sub_query, builder_join). Formula and trace-operator specs already round-trip through `QuerySpec`'s JSON because their fields (`expression`, `legend`) don't collide with anything; preserving zero-value fields on output is acceptable since the backend ignores empties.

This requires changing `Spec`'s static type from `QuerySpec` to `any`. All in-package construction sites (`BuildLogsQueryPayload`, `BuildAggregateQueryPayload`, `BuildMetricsQueryPayload`, `BuildTracesQueryPayload`) keep working unchanged because Go lets us put a concrete `QuerySpec{...}` into an `any` field. Callers in `internal/handler/tools/*_helper.go` etc. that read `q.Spec.X` need type assertions — we audit those.

### 2. Validate() update

- Keep the existing `query.Type != "builder_query" { continue }` short-circuit semantics for builder validation.
- Add new branches:
  - `promql`: type-assert `*PromQLSpec`/`PromQLSpec`; require non-empty `query`; default `RequestType` to `time_series` if empty.
  - `clickhouse_sql`: type-assert; require non-empty `query`; leave `RequestType` to the caller.
- Builder branches: change the `query.Spec` access to a `query.Spec.(QuerySpec)` type assertion, with a clean error if a builder envelope has a non-builder spec (shouldn't happen post-unmarshal but defensive against in-Go constructions).

### 3. Tool description fix (internal/handler/tools/query_builder.go)

- Drop the `compositeQuery.queryType="promql"` phrasing.
- Replace with: "For PromQL, set the per-query `type: \"promql\"` and put the expression in `spec.query`. ALSO read signoz://promql/instructions — …"
- Same swap for ClickHouse SQL: mention `type: \"clickhouse_sql\"` with `spec.query`.

### 4. Audit Spec consumers

After `Query.Spec` becomes `any`, audit anything outside `pkg/types/querybuilder.go` that touches `q.Spec.<Field>`:
- `pkg/types/querybuilder.go` — Validate() (we already update this).
- `internal/handler/tools/*` — grep for `Spec.` patterns on `types.Query` values; type-assert as needed. Helpers that construct payloads from scratch are fine since they pass through `BuildLogsQueryPayload` etc.
- Tests — `pkg/types/querybuilder_test.go` already does `q.CompositeQuery.Queries[0].Spec.StepInterval` — update to type-assert.

### 5. Tests (pkg/types/querybuilder_test.go)

Add cases:
- **PromQL round-trip**: unmarshal `{"compositeQuery":{"queries":[{"type":"promql","spec":{"name":"A","query":"{\"http.server.duration_count\"}"}}]}, …}`, `Validate()` succeeds, re-marshal, assert resulting JSON contains the original query string and `"type":"promql"`.
- **PromQL missing query** → `Validate()` errors.
- **ClickHouse SQL round-trip**: similar.
- **Mixed envelopes**: `builder_query` + `promql` in one composite — both survive round-trip.
- Existing builder tests updated for the `Spec any` change.

### 6. Docs / metadata sync (per signoz-mcp-server CLAUDE.md)

- `README.md` §`signoz_execute_builder_query` — add a sentence about PromQL/ClickHouse SQL envelope shapes.
- `manifest.json` — current description "Execute a raw SigNoz Query Builder v5 query" is generic enough; no change needed.

### Skip

- Adding a `QueryType` to `CompositeQuery`. The backend rejects unknown top-level fields on the composite; the discriminator is per-envelope and that's what the core requires.
- Modeling `builder_join`, `builder_sub_query` discriminated specs — neither is exposed by current MCP tooling and they'd round-trip cleanly through `QuerySpec` if used today.

## Files to Modify
- `pkg/types/querybuilder.go` — `Query.Spec` becomes `any`; add `PromQLSpec`, `ClickHouseSQLSpec`; add `Query.UnmarshalJSON`; update `Validate()`.
- `pkg/types/querybuilder_test.go` — update existing tests for the new `Spec` type, add PromQL/CH SQL round-trip & validation cases.
- `internal/handler/tools/query_builder.go` — fix the tool description to point at `query.type` instead of `compositeQuery.queryType`.
- `internal/handler/tools/<other>.go` — only if grep reveals direct `Spec.X` access on a `types.Query` value (audit step).
- `README.md` — add PromQL/CH SQL note under `signoz_execute_builder_query`.
- `plans/promql-execute-builder-query.context.md` — append dated decisions as implementation lands.

## Verification
- `go build ./...` and `go test -count=1 ./...` from `signoz-mcp-server/`.
- Manual repro from the issue: send the PromQL payload through the tool and confirm the outgoing JSON to the backend retains `type:"promql"` and the `query` string. (Without a live backend, the unit test asserts the marshaled JSON shape — sufficient evidence of the fix.)
- Confirm an existing builder_query roundtrip (e.g. one of the existing tests) still passes — no regression to logs/traces/metrics.
- Grep `rg -n "compositeQuery.queryType" internal/ pkg/ README.md` — zero hits.
