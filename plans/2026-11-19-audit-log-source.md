# Plan: Audit log source on the log tools

Status: In Progress
Issue: https://github.com/SigNoz/keystone-pod/issues/214
PR:

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

- `internal/handler/tools/logs.go`: `source` parameter and tool description sentence on both tools.
- `internal/handler/tools/logs_helper.go`: read `source`; note for an ignored value.
- `internal/handler/tools/query_param_descriptions.go`: `source` description; `service` steer.
- `internal/handler/tools/aggregate_helper.go`: carry the logs source on `AggregateRequest`.
- `pkg/querybuilder/request.go`: set `Source` on the logs query for search and aggregate.
- `guardrails/policy.go`: grandfather the 16-parameter `signoz_aggregate_logs` inventory.
- Guide, instructions, README, manifest, wire catalog, and e2e: later steps of this plan.

## Key Decisions

### 2026-11-19: `source` accepts only `audit`; anything else is ignored with a note

- Decision: an unsupported value runs against application logs and the result carries a note
  saying the rows are application logs and naming the one valid value; the sent value is not
  echoed, so the request keeps only a flag.
- Rationale: the user preferred not failing the call; SigNoz itself silently reads application
  logs for unknown values, so the note keeps the fallback from being silent.
- Alternatives rejected: a coded validation error; forwarding the raw value unchecked.

### 2026-11-19: `service` stays as is for audit

- Decision: no special case; the `service` description tells agents to omit it with
  `source="audit"`.
- Rationale: the agent reads the steer while filling the parameter; less code than a special case.

### 2026-11-19: no note on empty audit results

- Decision: rely on the existing "returned 0 rows" note on search; empty aggregates speak for
  themselves.

### 2026-11-19: grandfather `signoz_aggregate_logs` at 16 top-level parameters

- Decision: add `signoz_aggregate_logs` to `guardrails.GrandfatheredWideSchemaProperties` with
  its exact 16-parameter inventory; `source` takes it over `MaxTopLevelProperties` (15).
- Rationale: a separate audit aggregate tool would repeat every other parameter; the same
  exception already covers `signoz_aggregate_traces` (`queryType`) and `signoz_query_metrics`
  (`source` for Cost Meter). This is an intentional guardrail exception for the PR summary.
- Alternatives rejected: leaving `source` off the aggregate tool (audit breakdowns would need
  hand-written `signoz_execute_builder_query` JSON); dropping another parameter (breaking).

### 2026-11-19: cast the source until the spec lists `audit`

- Decision: send `apitypes.TelemetrytypesSource("audit")` with a TODO to use the generated
  constant.
- Rationale: generating from SigNoz `main` also pulls the whole v0.151.0 spec (new dashboard panel
  types, changed alert descriptions); that bump is its own change.

## Reference Links

- [SigNoz/signoz#13655: expose the audit source in the OpenAPI spec](https://github.com/SigNoz/signoz/pull/13655)
- [SigNoz/signoz#13438: querier changes for the audit schema](https://github.com/SigNoz/signoz/pull/13438)

## Verification

Pending.
