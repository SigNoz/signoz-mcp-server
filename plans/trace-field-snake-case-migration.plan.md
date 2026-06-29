# Plan: Trace Field Snake Case Migration

## Status
In Progress

## Context
Trace query-builder docs and server-generated payloads used deprecated camelCase aliases for intrinsic and calculated trace fields. The SigNoz backend currently maps those aliases to canonical snake_case fields, but that alias layer is explicitly deprecated. This migration updates the MCP server's own emitted trace queries and user-facing guidance together so future backend alias removal does not break trace tools.

## Approach
Migrate server-authored trace field names to canonical snake_case while preserving compatibility for caller-provided free-form expressions.

1. Update generated trace filters and payloads:
   - Change shortcut filters in `buildTraceFilterExpr` from `hasError`/`durationNano` to `has_error`/`duration_nano`.
   - Change `GetTraceDetails`' generated trace lookup from `traceID = ...` to `trace_id = ...`.
   - Change `BuildTracesQueryPayload` `SelectFields` to canonical names with explicit field metadata. Prefer `fieldContext: "span"` for intrinsic/calculated fields so server-authored payloads do not rely on backend ambiguity resolution.
   - Keep `name` and `timestamp` unchanged.
   - Replace deprecated `rpcMethod` with the canonical span/tag attribute `rpc.method` using `fieldContext: "tag"`.
   - Keep numeric span `kind` with `fieldContext: "span"` and `fieldDataType: "number"` while also selecting `kind_string`.

2. Use this field metadata target for server-authored trace `selectFields`:

   | Field | fieldContext | fieldDataType | Notes |
   |---|---|---|---|
   | `trace_id` | `span` | `string` | Replaces `traceID` |
   | `span_id` | `span` | `string` | Replaces `spanID` |
   | `parent_span_id` | `span` | `string` | Replaces `parentSpanID` |
   | `name` | `span` | `string` | Already canonical |
   | `timestamp` | `span` | `number` | Verify exact accepted payload spelling; backend treats this as a span field |
   | `duration_nano` | `span` | `number` | Replaces `durationNano` |
   | `has_error` | `span` | `bool` | Replaces `hasError` |
   | `status_code` | `span` | `number` | Replaces `statusCode` |
   | `status_code_string` | `span` | `string` | Replaces `statusCodeString` |
   | `status_message` | `span` | `string` | Replaces `statusMessage` |
   | `kind_string` | `span` | `string` | Replaces `spanKind` for string span kind values |
   | `kind` | `span` | `number` | Kept for numeric span kind compatibility |
   | `http_method` | `span` | `string` | Replaces `httpMethod` |
   | `http_url` | `span` | `string` | Replaces `httpUrl` |
   | `response_status_code` | `span` | `string` | Replaces `responseStatusCode`; do not use for numeric comparisons |
   | `rpc.method` | `tag` | `string` | Replaces deprecated `rpcMethod` row key |
   | `http.response.status_code` | `tag` | `number` | Numeric HTTP status attribute for comparisons |

3. Update user-facing guidance and metadata:
   - Rewrite `signoz://traces/query-builder-guide` examples and quick reference to canonical names.
   - Update trace tool descriptions and README examples to use `has_error`, `duration_nano`, `status_code_string`, `http_method`, etc.
   - Avoid translating numeric examples like `responseStatusCode >= 500` into `response_status_code >= 500`; use `has_error = true`, `status_code_string = 'Error'`, or a verified numeric attribute such as `http.response.status_code >= 500`.
   - Update Query Builder resource/tool metadata in `internal/handler/tools/query_builder.go`; it currently describes trace built-ins as camelCase.
   - Update stale examples in prompts, view examples, alert resources, and dashboard query guidance.
   - Review `manifest.json` and `docs/` for trace-name drift; no manifest change is needed when descriptions remain generic and already refer to canonical field expressions.
   - Treat `pkg/dashboard/query.go` carefully: it is ClickHouse SQL guidance, so update physical SQL column names only and do not apply Query Builder `fieldContext` rules there.

4. Handle raw output compatibility and web URL enrichment:
   - Raw `signoz_search_traces` rows will return keys matching the selected field names, so `traceID`/`durationNano` outputs become `trace_id`/`duration_nano` when the default select list migrates.
   - Treat this as an acceptable downstream output-key migration for canonicalization, documented in README and PR/release notes. Do not duplicate legacy aliases in this pass.
   - Update `enrichSearchTracesWebURL` to prefer `trace_id` and fall back to `traceID` / `traceId` for mixed rollout responses.
   - Update `pkg/util/weburl.go` comments, drift warning text, and tests so warnings mention `trace_id` primary and fire when any rows cannot be enriched while rows are present.

5. Keep legacy caller input fail-open:
   - Do not normalize arbitrary user-provided filter/order/aggregation strings in this first pass.
   - Continue accepting legacy inputs because backend aliases still resolve them today.
   - Let docs/tool descriptions and server-generated shortcut filters produce only canonical names.

## Files to Modify
- `pkg/querybuilder/traces_guide.go` - canonical trace field guide, selectFields examples, filter examples, quick reference.
- `pkg/types/querybuilder.go` - canonical raw trace select fields and comments/examples.
- `internal/handler/tools/traces_helper.go` - canonical shortcut filter clauses.
- `internal/handler/tools/traces.go` - tool descriptions, enrichment key, warning text, comments.
- `internal/handler/tools/query_builder.go` - Query Builder tool/resource metadata that currently says trace built-ins are camelCase.
- `internal/client/client.go` - canonical `get_trace_details` trace lookup filter.
- `README.md` - trace tool parameter examples.
- `pkg/prompts/prompts.go` - latency prompt aggregate field examples.
- `pkg/views/examples.go` - trace view example filter.
- `pkg/alert/resources.go` - trace alert example filter.
- `pkg/dashboard/query.go` - trace SQL guidance examples that still use deprecated names.
- `pkg/util/weburl.go` - comments and, if needed, helper support for multi-key row ID lookup.
- `manifest.json` and `docs/` - review for stale trace-field metadata.
- Tests under `pkg/types`, `internal/handler/tools`, `internal/client`, `pkg/util`, and live E2E families as needed.
- Companion `SigNoz/agent-skills` - matching update needed because published skills still teach deprecated trace field names.

## Verification
- Add unit tests that assert `BuildTracesQueryPayload` selects canonical fields with expected `fieldContext`/`fieldDataType` metadata and omits deprecated aliases unless a legacy compatibility exception is documented.
- Update shortcut parsing tests to expect generated `has_error` and `duration_nano` clauses.
- Add tests showing legacy free-form caller input still passes through unchanged: `filter`/`query` using `hasError`, `aggregateOn=durationNano`, and `orderBy=avg(durationNano) desc`.
- Add or update client/handler tests to assert `get_trace_details` and `search_traces` emit canonical filters and row keys.
- Add `internal/client` coverage that captures the `GetTraceDetails` `/api/v5/query_range` body and asserts `trace_id = '...'`.
- Add mixed-row `webUrl` enrichment fixtures for `trace_id`, `traceID`, and `traceId`, preferring `trace_id` and warning when rows are present but one or more rows cannot be enriched.
- Update live E2E contract coverage to use canonical `duration_nano` for trace aggregation drift checks.
- Run focused tests when Go is available: `go test ./pkg/types ./internal/handler/tools ./internal/client ./pkg/util`.
- Run `rg` for deprecated trace names after implementation and review remaining hits intentionally.
- For live SigNoz verification, delegate to a subagent: run read-only trace search/aggregate/detail checks against a real instance, confirm canonical fields round-trip server-side, do not print or persist credentials, and report any fields that fail.
- Live verification matrix: canonical `search_traces` returns snake_case row keys and `webUrl`; legacy caller input still succeeds; `aggregate_traces` works with `duration_nano`; `get_trace_details` works with a trace found from search; `rpc.method` replacement behavior is confirmed or rejected.
- State in the PR summary that `SigNoz/agent-skills` needs a matching update and link the companion PR when created.
