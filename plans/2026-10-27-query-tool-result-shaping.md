# Plan: Result Shaping on the Query Tools

Status: Done
Issue: [SigNoz/nerve-pod#518](https://github.com/SigNoz/nerve-pod/issues/518),
[SigNoz/nerve-pod#519](https://github.com/SigNoz/nerve-pod/issues/519),
[SigNoz/nerve-pod#520](https://github.com/SigNoz/nerve-pod/issues/520),
[SigNoz/nerve-pod#524](https://github.com/SigNoz/nerve-pod/issues/524)
PR: https://github.com/SigNoz/signoz-mcp-server/pull/380

## Context

The 2026-10-26 tool-surface review of production usage put the five query tools at most of
the call volume, with `signoz_execute_builder_query` among the busiest. The escape hatch is where
agents land when a shortcut tool cannot express the request, and the four tickets name the same
gaps:

- `signoz_aggregate_logs` and `signoz_aggregate_traces` take one aggregation and cannot set
  `having`, `functions`, or `limitBy` (#518).
- `signoz_query_metrics` fixes the result bounds at 100 groups, `__result desc`, and cannot set
  `having`, `functions`, or `limitBy` (#519).

Not every accepted field runs: the pinned spec marks the builder query's `limitBy` and the
formula's `having` as "accepted but currently ignored", and `pkg/querier/postprocess.go`
applies `functions` to time-series results only.
- `signoz_search_logs` has no `selectFields` while `signoz_search_traces` does; neither can set
  the row order; both page by offset only although SigNoz returns a `nextCursor` for raw
  requests (#520).
- `formulaQueries` advertised `items: {type: object}` with the field list in prose (#524).

Every field is already in the pinned SigNoz spec (`internal/apiclient/apitypes`), so the change
forwards them verbatim and SigNoz stays the only validator.

## Approach

- `pkg/querybuilder`: `Aggregate` takes an `AggregateSpec` (several aggregations, having,
  functions); `LogsSearch` and `TracesSearch` take `RawSearchOptions` (selectFields, order);
  `MetricsQuery` carries limit, order, having, and functions. Limit and order go to the formula
  when one exists and otherwise to the primary query; having always goes to the primary query;
  functions go to the primary query. `ApplyBounds` still fills only what the caller left unset.
- Handlers: one required `aggregations` parameter takes an expression string, an array of
  expression strings, or a JSON-encoded array, forwarded verbatim as `aggregations[].expression`;
  the retired `aggregation` and `aggregateOn` inputs return a coded error naming it (CMP-2), and
  the local function-name allowlist is gone with them. `functions` is typed
  from the generated `Function` through a new `mcpcontract.SchemaFor[T]`, and also accepts a
  JSON-encoded string because some clients stringify nested values. `formulaQueries` items are
  typed from the `formulaSubQuery` struct's jsonschema tags, with `groupBy` and `isMonotonic`
  keeping their string forms. When `functions` arrive with `requestType: scalar`, the result
  carries a note that SigNoz did not apply them.
- Raw searches: `orderBy` on both tools (`timestamp` keeps `id` as the tie-breaker on logs) and
  `selectFields` on logs with the same contract as traces: a default core set (`timestamp`, `id`,
  `body`, `severity_text`, `trace_id`, `span_id`) with extras added on top. `groupBy` on both aggregate tools accepts an array or a comma-separated
  string (nerve-pod#509). Cursor paging stays out; see the decision below.
- Guardrails: `signoz_aggregate_logs` lands at 15 properties, inside the cap, so it stays off
  the grandfathered list; `signoz_aggregate_traces` (18) and `signoz_query_metrics` (22) grow
  their pinned inventories. Intentional relaxation: the alternative, nesting the new fields under one
  object, mirrors the backend instead of the task and hides them from clients that read
  top-level properties.

## Files to Modify

- `pkg/querybuilder/request.go` — `AggregateSpec`, `RawSearchOptions`, `MetricsQuery` fields
- `internal/mcpcontract/mcp.go` — `SchemaFor`, `Items`, `FromSchema`
- `internal/handler/tools/query_options.go` — shared parsing and schema helpers
- `internal/handler/tools/{aggregate_helper,logs_helper,traces_helper,metrics_helper}.go` — parsing
- `internal/handler/tools/{logs,traces,metrics,metrics_query}.go` — schemas and wiring
- `internal/client/client.go` — `TracesSearch` call site
- `guardrails/policy.go` — wide-schema inventories
- `internal/mcp-server/testdata/wire-catalog/tools-list.json` — the five touched entries only
- `README.md`, `tests/e2e/tests/test_result_shaping.py`

## Key Decisions

### 2026-10-27 — `aggregations` takes full expressions; `aggregation` and `aggregateOn` retire

- Decision: one required `aggregations` parameter carrying expressions as the querier takes
  them (`count()`, `p99(duration_nano)`), one or many. The old pair returns a coded
  `VALIDATION_FAILED` that names the replacement with an example.
- Rationale: the first draft kept `aggregation` as a function name or expression next to
  `aggregateOn`, which left two ways to say the same thing and a local allowlist of function
  names that SigNoz, the only validator, already checks. Mirroring the querier's own shape
  teaches one grammar for the shortcut tools and `signoz_execute_builder_query`. The break is
  deliberate and follows the log `query` alias precedent: a coded error, never a silent change.
- Alternatives rejected: keeping `aggregation` as a silent alias (two contracts to maintain and
  to teach); an `alias` parameter (results are addressable by position as `__result_N`).

### 2026-10-27 — limit and orderBy shape the result the caller reads; having stays on query A

- Decision: on `signoz_query_metrics`, `limit` and `orderBy` go on the formula when one exists
  and on the primary query otherwise; `having` and `functions` always go on the primary query.
- Rationale: the caller sees the formula result, and bounding the inputs instead would
  drop groups before the formula runs, so formula inputs carry no limit. The formula's
  own `having` is "accepted but currently ignored" in the pinned spec, so sending it there
  would claim a filter that never runs; filtering query A before the formula is the operation
  SigNoz does implement, and the description and decisions note say so.

### 2026-10-27 — no `limitBy`, and `functions` teach their time-series-only effect

- Decision: review of the first draft found that the pinned spec marks `limitBy` "accepted but
  currently ignored" on every builder query, and that `pkg/querier/postprocess.go` returns
  scalar results untouched by `functions`. `limitBy` is dropped from all three tools; `functions`
  stays, with the description stating the restriction and a result note when the caller sends
  functions with `requestType: scalar`.
- Rationale: an accepted field is not an implemented one. Advertising `limitBy` would teach
  per-group limits SigNoz never applies. Re-add it when SigNoz implements it.

### 2026-10-27 — no `cursor` parameter until SigNoz's raw cursor is usable

- Decision: #520 asked for cursor paging on both raw searches. The parameter was built, then
  removed before review.
- Rationale: probed on the pinned release (v0.145.0 cast) with three logs 1 ms apart and
  `limit: 2`. SigNoz returned `nextCursor` (base64 of the last row's millisecond timestamp),
  but a second request carrying that cursor returned zero rows under the tools' default order
  `timestamp desc, id desc`, and returned two rows (one already on the first page) under a
  timestamp-only order, because the cursor has millisecond granularity. Teaching that would
  trade offset paging that works for cursor paging that skips or repeats rows. Filed as a
  SigNoz-side issue; re-add the parameter once the cursor honors the secondary key and is
  exact.
- Alternatives rejected: forcing a timestamp-only order whenever `cursor` is set (still repeats
  boundary rows); sending the cursor through and documenting the caveats (fails the "SigNoz is
  the only validator" bar in the other direction, since the server would teach a contract it
  knows is wrong).

### 2026-10-27 — logs rows default to a core set; `selectFields` is additive on both searches

- Decision: the first draft gave `signoz_search_logs` a `selectFields` that replaced the full
  record while `signoz_search_traces` adds to a core set. Same name, opposite meaning, on sibling
  tools, and nobody depended on the log form yet. Logs now default to the intrinsic columns and
  `selectFields` adds to them. No wildcard: an explicit selection is SigNoz's own semantics, and
  a caller that needs more fields names them.
- Rationale: one contract for both raw searches, and a smaller default log row, which is the
  lever behind the large-search OOM. The core set holds no resource attribute because logs carry
  none by contract; a default naming `service.name` would warn on every call in a workspace
  whose log pipeline does not set it. The default row shape changes for every caller, so the PR
  carries it in the migration note.
- Alternatives rejected: keeping the replace semantics on logs (a CMP-1 break later, once
  taught); including `service.name` in the core set (per-call warnings where it is absent).

### 2026-10-28 — review fixes: no silent drops or reorderings

- Decision: a selected field whose name another context already holds (`attribute.body` beside
  the core `body`) is a validation error on both searches, not a silent drop; an unprefixed name
  still defers to the held field. A `formulaQueries` item's string `isMonotonic` rejects values
  other than true or false, like the top-level parameter. A custom log `orderBy` keeps `id` as
  the tie-breaker, as the default order does.
- Rationale: SigNoz keys each raw row by field name alone, so it can return only one of two
  same-named fields; dropping the caller's choice returned the built-in value instead. A typo
  read as false turned a counter's rate into an average. Without a unique tie-breaker, offset
  pages over a low-cardinality key such as `severity_number` repeat or skip rows.
- Alternatives rejected: letting the caller's field replace the core one (rows lose a column the
  contract promises); leaving trace `selectFields` as before (the two searches share one
  contract).

## Reference Links

- [Tool-surface review issues](https://github.com/SigNoz/nerve-pod/issues/518)

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci` on 2026-10-27: every step passed for the branch. The final
  `check-repo-docs` step reported only the untracked `plans/2026-09-25-*` files in the local
  working tree, which are not part of this change.
- Unit: `TestQueryTools_SendResultShapingUpstream` runs `signoz_aggregate_logs` and
  `signoz_query_metrics` against a fake SigNoz and checks the `query_range` body each sends.
  Each malformed shaping input is a row in the handler-level invalid-argument table. A builder
  test covers the formula-only decisions: formula inputs stay unbounded, and a formula
  having is never sent.
- e2e on a fresh foundry cast of the pinned release: `tests/e2e/tests/test_result_shaping.py`
  (5 checks that assert the requested effect: two aggregations plus `having` on logs drop the
  small group; `clampMax` caps every time-series value on traces and a scalar request reports
  functions as not applied; `selectFields` pins the log row keys to timestamp, id, and the
  selected field, and `orderBy` flips the order; `orderBy` on span rows; `limit`, `orderBy`,
  `having`, and typed `formulaQueries` on metrics, with a `having` beside a formula filtering
  query A) and the full suite.
- Wire catalog: only the five touched `tools/list` entries changed, confirmed by diffing each
  tool entry against the previous golden.
- Cursor probe against the cast, recorded in the decision above.

## Outcome

Shipped `aggregations` (one or several expressions, replacing `aggregation` and `aggregateOn`),
`having`, `functions`, and an array-or-string `groupBy` on the aggregate tools; `limit`,
`orderBy`, `having`, and `functions` on `signoz_query_metrics`; `orderBy` on both raw searches,
`selectFields` on `signoz_search_logs` with a default core set; and a typed item schema for `formulaQueries`.
Deferred: cursor paging (#520, blocked on SigNoz/nerve-pod#527) and `limitBy` (SigNoz ignores
it on the pinned release).
