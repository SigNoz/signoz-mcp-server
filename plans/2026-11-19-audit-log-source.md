# Plan: Audit log source on the log tools

Status: In Progress
Issue: https://github.com/SigNoz/keystone-pod/issues/214
PR: #464, #470

## Context

SigNoz's querier reads SigNoz audit events (who changed what in a workspace) when a logs builder
query sets `source: "audit"`; it routes the query to the `signoz_audit` database. The schema the
MCP server's client is generated from does not list `audit` in `TelemetrytypesSource` yet
(SigNoz/signoz#13655 adds it), but nothing validates that enum, so the value already works on the
wire. `signoz_execute_builder_query` forwards it verbatim today; `signoz_search_logs` and
`signoz_aggregate_logs` cannot reach audit events.

Audit events differ from application logs in ways agents must be told about:

- Field discovery does not help: `signoz_get_field_keys(source=audit)` returns only the intrinsic
  fields and five resource keys, and `signoz_get_field_values` returns nothing, so the audit
  attribute names (`signoz.principal.*`, `signoz.audit.*`, `signoz.resource.*`) must be documented.
- Raw rows carry `body.message`, one flat `attributes` object, and `resource.attributes`, not the
  `attributes_string` / `resources_string` maps of application logs (SigNoz integration test
  `tests/integration/tests/querieraudit/01_list.py`; not yet verified against a live instance).
- Every audit event has `service.name` `signoz`, so the `service` shortcut matches nothing else.
- SigNoz does not validate `source` on logs: any value other than `audit` silently reads
  application logs.

## Approach

Add an optional `source` parameter, enum `["audit"]`, to both log tools and set it on the logs
builder query. Agents learn about audit from one pointer sentence in each tool description and
the `source` parameter description; the logs query-builder guide holds the field names, values,
and row shape.

## Files to Modify

- `internal/handler/tools/logs.go`: `source` parameter and tool description sentence on both tools;
  audit empty-result and audit `searchText` notes.
- `internal/handler/tools/logs_helper.go`: read `source`; note for an ignored value; reject
  `search()` scopes with audit; detect an empty audit aggregate.
- `internal/handler/tools/query_param_descriptions.go`: `source` description; `service` steer.
- `internal/handler/tools/aggregate_helper.go`: carry the logs source on `AggregateRequest`.
- `pkg/querybuilder/request.go`: set `Source` on the logs query for search and aggregate.
- `guardrails/policy.go`: grandfather the 16-parameter `signoz_aggregate_logs` inventory.
- `pkg/querybuilder/logs_guide.go`: audit events section and a complete audit request example.
- `internal/handler/tools/query_builder.go`: logs guide resource description mentions audit.
- `internal/mcp-server/testdata/wire-catalog/`: the two tool entries and the guide's list and
  content entries.
- `README.md`, `manifest.json`: `source` on both log tools.
- `tests/e2e/`: audit queries through MCP (later step).

## Key Decisions

### 2026-11-19: `source` accepts only `audit`; anything else is ignored with a note

- Decision: an unsupported value runs against application logs and the result carries a note
  saying the rows are application logs and naming the one valid value; the sent value is not
  echoed, so the request keeps only a flag.
- Rationale: the user preferred not failing the call; SigNoz itself silently reads application
  logs for unknown values, so the note keeps the fallback from being silent.
- Alternatives rejected: a coded validation error; forwarding the raw value unchecked.
- SCH-2 exception (recorded 2026-11-26 after review on #464): SCH-2 says a supplied-but-invalid
  value MUST be rejected. The fallback stays because the schema enum already lists `audit` as
  the only value, so only a client that ignores the schema sends another one, and the
  `sourceIgnoredNote` tells the agent that the rows are application logs and names the valid
  value, so the substitution is never silent. An empty or null `source` is treated as omitted and
  gets no note; the README says so.

### 2026-11-19: `service` stays as is for audit

- Decision: no special case; the `service` description tells agents to omit it with
  `source="audit"`.
- Rationale: the agent reads the steer while filling the parameter; less code than a special case.

### 2026-11-19: no note on empty audit results (superseded 2026-11-26)

- Decision: rely on the existing "returned 0 rows" note on search; empty aggregates speak for
  themselves.
- Superseded by the 2026-11-26 decision below.

### 2026-11-26: empty audit results carry a note that they are not proof

- Decision: with `source="audit"`, an empty first search page, and an aggregate with no scalar
  rows or time-series points, or an ungrouped `count()` whose one row is zero, gets
  `auditEmptyResultNote`: the result can also mean this SigNoz does not record audit
  events (too old, or audit logging not enabled), so it is not proof that nothing happened.
- Rationale: on a SigNoz without the audit store, or with the auditor on its default `noop`
  provider, the `signoz.audit.%` condition turns every audit query into an empty success, which
  an agent reads as "nobody deleted that dashboard". The note says "can also mean", so it stays
  true when there are simply no matching events (OUT-7). It goes through `appendNotes` like the
  other notes. It is not a counted measurement note: the trigger reads the response, not the
  request shape, like `aiNoRowsNote`. Raised in review on #464.
- The ungrouped zero row is included because SigNoz answers an ungrouped `count()` over no rows
  with one row of `0`. Only a plain `count()` counts: a zero from `countIf` or another
  aggregation says nothing about whether events exist. A payload the walker cannot read gets no
  note and a WARN log.
- Alternatives rejected: a note on every audit result (noise on non-empty results); probing
  SigNoz for audit support before each query (an extra upstream call per query).

### 2026-11-26: `searchScope` other than `body` is rejected with `source="audit"`

- Decision: with `source="audit"`, `searchScope` `attribute`, `resource`, or `all` returns a coded
  validation error before any upstream call, naming a named audit field predicate in `filter` as
  the fix. An empty audit page after a body-only `searchText` gets `auditSearchTextNote`, which
  points at a named audit field in `filter`, instead of `bodyOnlySearchTextNote`, which points at
  those scopes.
- Rationale: those scopes build `search()`, and SigNoz's audit storage does not set
  `SupportsBodyFunctions`, so `RejectsBodyFunction` fails the query (SigNoz
  `pkg/telemetryschema/audittelemetryschema/storage.go` and `pkg/querybuilder/condition.go`). The
  cast e2e SigNoz answers such a query with HTTP 400, "function `search` is only supported for
  logs". The rejection applies
  whether or not `searchText` is set, so an audit call never reaches the "searchScope ignored"
  path with one of those scopes.

### 2026-11-19: grandfather `signoz_aggregate_logs` at 16 top-level parameters

- Decision: add `signoz_aggregate_logs` to `guardrails.GrandfatheredWideSchemaProperties` with
  its exact 16-parameter inventory; `source` takes it over `MaxTopLevelProperties` (15).
- Rationale: a separate audit aggregate tool would repeat every other parameter; the same
  exception already covers `signoz_aggregate_traces` (`queryType`) and `signoz_query_metrics`
  (`source` for Cost Meter). This is an intentional guardrail exception for the PR summary.
- Alternatives rejected: leaving `source` off the aggregate tool (audit breakdowns would need
  hand-written `signoz_execute_builder_query` JSON); dropping another parameter (breaking).

### 2026-11-19: no audit line in the server instructions

- Decision: leave `pkg/instructions` unchanged, though the issue lists it.
- Rationale: measured on 2026-11-26 by `TestGuardrail_WireContractBudgets`, the instructions are
  4091 of the 4096-byte `MaxServerInstructionsBytes` budget and the preamble 510 of the 512-byte
  `MaxServerPreambleBytes` budget, so a routing line needs other text cut first. Both log tool
  descriptions already tell agents to set `source="audit"` for SigNoz audit events, and the logs
  guide holds the details.
- Alternatives rejected: trimming other rules to fit a sentence without evidence; raising the
  guardrail budget.
- Open (SUR-11): whether the preamble should route questions such as "who deleted a dashboard?"
  to `source="audit"`. `pkg/instructions` stays unchanged pending a before/after prompt
  evaluation (EVL-1, EVL-2), run after this change.

### 2026-11-20: audit queries match only `signoz.audit.*` events

- Decision: with `source="audit"`, the server ANDs `event_name LIKE 'signoz.audit.%'` onto the
  filter of both log tools.
- Rationale: SigNoz routes `source: "audit"` from v0.119.0; an older SigNoz ignores the field and
  reads application logs, which would come back as audit events with no signal. Every audit event
  has a `signoz.audit.*` event name, so the condition changes nothing on a current SigNoz and
  turns the old-backend case into an empty result. Raised in review on #464.
- Alternatives rejected: checking returned rows (cannot cover aggregates); relying on the
  latest-release target alone (leaves the mislabelled result silent).

### 2026-11-19: cast the source until the spec lists `audit`

- Decision: send `apitypes.TelemetrytypesSource("audit")` with a TODO to use the generated
  constant.
- Rationale: generating from SigNoz `main` also pulls the whole v0.151.0 spec (new dashboard panel
  types, changed alert descriptions); that bump is its own change.

## Reference Links

- [SigNoz/signoz#13655: expose the audit source in the OpenAPI spec](https://github.com/SigNoz/signoz/pull/13655)
- [SigNoz/signoz#13438: querier changes for the audit schema](https://github.com/SigNoz/signoz/pull/13438)

## Verification

- `go test ./...`, `make check-guardrails`, and `make check-repo-docs` pass. Handler tests cover
  what reaches SigNoz for omitted, empty, `audit`, unsupported, and non-string `source` values on
  both tools, the `signoz.audit.*` condition with and without a caller filter, and the
  ignored-value note. Since 2026-11-26 they also cover the audit `searchScope` rejection (no
  request reaches SigNoz), the audit `searchText` note in place of the body-only note, and the
  empty audit result note on search pages and on scalar and time-series aggregate shapes.
- 2026-11-26, against the e2e SigNoz cast from #470 with an empty audit store, through the server
  built at 3ee1ba52: an audit search returns `rows: null`; an ungrouped `count()` returns
  `data: [[0]]`; a grouped `count()` returns `data: []`; a time-series `count()` returns
  `aggregations: null`; `searchScope` `attribute` and `all` return the HTTP 400 above. The handler
  tests use these shapes. No e2e test pins them yet.
- E2E: pending in the follow-up PR.

## Outcome

This PR merges ahead of its e2e test. #470, stacked on it, pins the e2e collector to the first
release with the `signoz_audit` schema, adds the e2e test, and marks this plan Done.
