# Feature: PromQL & ClickHouse SQL support in signoz_execute_builder_query — Context & Discussion

## Original Prompt
> using superpower skill work on https://github.com/SigNoz/signoz-mcp-server/issues/179

GitHub issue:
> `signoz_execute_builder_query` silently drops PromQL queries despite advertising support.
>
> The tool description claims support for PromQL via `compositeQuery.queryType="promql"` and references `signoz://promql/instructions`, but the typed `QueryPayload` has no field for a PromQL expression. The unmarshal → validate → re-marshal round trip silently drops the PromQL string before the request reaches the backend.

## Reference Links
- Issue: https://github.com/SigNoz/signoz-mcp-server/issues/179
- Tool description: `internal/handler/tools/query_builder.go:24-32`
- Typed payload: `pkg/types/querybuilder.go:9-58`
- SigNoz core v5 envelope: `signoz/pkg/types/querybuildertypes/querybuildertypesv5/req.go:15-205`
- SigNoz core PromQuery: `signoz/pkg/types/querybuildertypes/querybuildertypesv5/prom_query.go`
- SigNoz core ClickHouseQuery: `signoz/pkg/types/querybuildertypes/querybuildertypesv5/clickhouse_query.go`

## Key Decisions & Discussion Log

### 2026-05-19 — Bug confirmed end-to-end
- `CompositeQuery` in `pkg/types/querybuilder.go` has only `Queries []Query`; the typed payload cannot carry `queryType` on the composite. Re-marshaling drops anything unknown.
- `QuerySpec` has no `query string` field. A `promql` envelope's `spec.query` is lost on the same round trip.
- `Validate` skips non-builder query types with `continue`, but more importantly there is nowhere for their data to live.

### 2026-05-19 — Backend payload reality
- The actual SigNoz core type `querybuildertypesv5.CompositeQuery` has a strict unknown-field check (req.go:215-260) and accepts only `queries`. **It does NOT accept `compositeQuery.queryType`** — the tool's own description was wrong on that point.
- `QueryEnvelope` (one per query in `queries`) carries the discriminator via `type` ("builder_query" | "builder_formula" | "promql" | "clickhouse_sql" | "builder_trace_operator"). Core decodes `spec` differently based on `type`.
- For `type=promql` the spec is `PromQuery { name, query, disabled, step, stats, legend }`. For `type=clickhouse_sql` the spec is `ClickHouseQuery { name, query, disabled, legend }`.
- Decision: fix in MCP server by mirroring the core's discriminated-spec decoding rather than inventing a `queryType` field on the composite that the backend would reject anyway.

### 2026-05-19 — Approach selection
- Issue's suggested fix (1) is the right one: add PromQL support to the typed payload. We extend it to ClickHouse SQL too because the existing `Validate()` already mentions both as out-of-scope, and the round-trip drop affects both.
- We do NOT add a `QueryType` field to `CompositeQuery`. Instead, we make `Query.Spec` a discriminated union decoded by `Query.Type`. This matches the backend exactly and is the minimum change that unblocks PromQL.
- We also correct the tool description to remove the misleading "`compositeQuery.queryType=\"promql\"`" claim and point at `query.type` instead.

### 2026-05-19 — Test surface
- Round-trip tests in `pkg/types/querybuilder_test.go`: unmarshal a promql JSON payload, run `Validate`, re-marshal, then assert the `query` string survives.
- Validation tests: builder_query without aggregations on time_series still errors; promql with empty `query` errors; missing `name` defaults like builder.

### 2026-05-19 — Implementation landed; code review applied
- Implemented per the plan. `go build ./...` clean, `go test ./... -count=1` all packages `ok`.
- Code review (general-purpose subagent) raised three Important items:
  1. Stale `manifest.json` description for `signoz_execute_builder_query` — fixed; description now enumerates the four envelope types (builder_query / builder_formula / promql / clickhouse_sql).
  2. Round-trip tests asserted on substring `Contains`, which is fragile — replaced with re-unmarshal-and-compare-typed-values for both PromQL and ClickHouse SQL round-trip tests.
  3. No tests for malformed promql/clickhouse_sql specs — added `TestQueryUnmarshalJSON_MalformedPromQLSpec` covering three malformed inputs (string-for-object, number-for-string, array-for-object).
- Also applied two minor suggestions: added a step-as-string round-trip test (`TestQueryPayloadRoundTrip_PreservesPromQLWithStepString`) confirming the `Step any` choice round-trips both number-seconds and Go-duration-string forms; added `builder_trace_operator` to the tool description's enumeration of envelope types.
- Pushed back on minors 4 and 5 (the reviewer itself flagged them as no-action-required).

## Open Questions
- [x] Should we also model `builder_formula`/`builder_trace_operator` discriminated specs? — Resolved: out of scope for this issue. `BuildMetricsQueryPayloadJSON` already side-steps the typed `QuerySpec` for formulas via its own raw type, and formula spec keys (`expression`, `legend`) happen to round-trip through the existing `QuerySpec` JSON without data loss (no field collision); trace operator can be added later if it comes up. We will only carve out promql + clickhouse_sql now.
- [x] Should `Validate` default `RequestType` for promql/clickhouse_sql? — Resolved: default to `time_series` for promql (mirrors metrics builder default) and leave `clickhouse_sql` to caller; backend accepts `scalar`/`time_series`/`raw` for CH SQL depending on the query.
