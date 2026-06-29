# Feature: Trace Field Snake Case Migration - Context & Discussion

## Original Prompt
> Let's plan to work on this: [https://github.com/SigNoz/signoz-ai-assistant/issues/361](https://github.com/SigNoz/signoz-ai-assistant/issues/361)

## Reference Links
- [SigNoz/signoz-ai-assistant#361](https://github.com/SigNoz/signoz-ai-assistant/issues/361)
- [SigNoz/signoz-mcp-server#213](https://github.com/SigNoz/signoz-mcp-server/pull/213)

## Local References
- `/Users/makeavish-m1/signoz/signoz/pkg/telemetrytraces/const.go`
- `/Users/makeavish-m1/signoz/signoz/pkg/telemetrytraces/field_mapper.go`

## Key Decisions & Discussion Log
### 2026-06-27 - Issue Intake
- Issue #361 tracks migrating trace query-builder field names from deprecated camelCase aliases to canonical snake_case names.
- No existing plan/context pair matched this feature in `plans/`.
- The guide and server-emitted query-builder payloads must move together so `signoz://traces/query-builder-guide`, `signoz_search_traces`, `signoz_aggregate_traces`, and `signoz_get_trace_details` do not teach or emit conflicting field names.

### 2026-06-27 - Backend Mapping Verification
- Local backend checkout confirms the canonical trace mappings in `telemetrytraces`:
  `traceID -> trace_id`, `spanID -> span_id`, `parentSpanID -> parent_span_id`,
  `durationNano -> duration_nano`, `statusCode -> status_code`,
  `statusMessage -> status_message`, `statusCodeString -> status_code_string`,
  `spanKind -> kind_string`, `httpMethod -> http_method`, `httpUrl -> http_url`,
  `responseStatusCode -> response_status_code`, and `hasError -> has_error`.
- `name` and `timestamp` remain canonical as-is.
- `rpcMethod` is a deprecated calculated alias mapped to `attribute_string_rpc$$method`, not a canonical span field. The likely canonical query-builder representation is the span/tag attribute `rpc.method` with `fieldContext: "tag"`.

### 2026-06-27 - Scope Notes
- Besides the issue-listed files, repo scan found stale trace-name examples in `README.md`, `internal/handler/tools/traces.go`, `internal/client/client.go`, `pkg/prompts/prompts.go`, `pkg/views/examples.go`, `pkg/alert/resources.go`, and `pkg/dashboard/query.go`.
- `manifest.json` trace descriptions are generic and may not need changes, but it should be reviewed under the docs/metadata sync checklist.
- There is no local `/Users/makeavish-m1/signoz/agent-skills` checkout. The companion skills check should be completed during implementation through GitHub or by checking out the repo.

### 2026-06-27 - Compatibility Posture
- Initial migration should not rewrite arbitrary user-provided free-form `filter`, `aggregateOn`, or `orderBy` strings. Backend aliases still keep legacy caller input working while updated docs/tool descriptions steer models toward canonical names.
- Server-generated shortcut clauses should switch to canonical names (`has_error`, `duration_nano`) because those are emitted by this server.

### 2026-06-27 - Multi-Agent Review
- Three parallel reviewers checked the plan from backend-contract, MCP implementation/docs/tests, and rollout-compatibility angles.
- Backend review confirmed the core mappings and recommended explicit `fieldContext: "span"` for server-authored intrinsic/calculated `selectFields`, plus explicit data types. Backend examples under `/Users/makeavish-m1/signoz/signoz` use `fieldContext: "span"` for trace built-ins; `tag` is accepted as an alias for `attribute`.
- Reviewers flagged that raw `signoz_search_traces` row keys will change when `selectFields` names change because the backend aliases output columns to the requested field name. The plan now treats this as a downstream output-contract risk, not only an upstream backend-compatibility fix.
- Reviewers agreed `webUrl` enrichment should support `trace_id` primarily with `traceID` and `traceId` fallbacks during rollout.
- Reviewers identified `internal/handler/tools/query_builder.go` as an explicit stale metadata surface because it says trace built-in columns are camelCase.
- Reviewers warned that `response_status_code` is a string field; numeric HTTP status comparisons should use a verified numeric attribute such as `http.response.status_code`, or error-focused examples should use `has_error = true` / `status_code_string = 'Error'`.
- Reviewers recommended keeping `includeSpans` behavior out of this migration and making live verification a concrete matrix.

### 2026-06-27 - Implementation Start
- Status moved to `In Progress`.
- Implementation will migrate server-authored trace payloads and docs to canonical snake_case names while preserving legacy free-form caller input.

### 2026-06-27 - Post-Implementation Review
- Three post-implementation reviewers checked backend field contracts, Go/test/docs consistency, and rollout compatibility.
- Backend-contract review found no blocking issues and confirmed the migrated span fields match `telemetrytraces` canonical names and contexts. It flagged `http.response.status_code` as incorrectly typed as string in the default select list; implementation updated it to `fieldDataType: "number"` and pinned that in `BuildTracesQueryPayload` tests.
- Compatibility review found that partial `webUrl` enrichment could still fail silently if some rows had supported trace-id keys and some did not. Implementation now warns when `RowsSeen > RowsEnriched` and logs both counts.
- Compatibility review also confirmed the raw `signoz_search_traces` output key migration is a downstream contract risk. Decision: document the snake_case raw row keys in README/PR notes rather than duplicating deprecated aliases in this pass.
- Test/docs review found no stale deprecated trace field names in README/docs/manifest/tool/resource descriptions outside intentional compatibility tests and planning notes. It recommended stronger aggregate trace payload assertions; implementation now asserts canonical shortcut filters and `p99(duration_nano)` payloads.
- Manifest review: no `manifest.json` change needed because trace descriptions are generic and already refer to canonical filter expressions.
- Companion skills review via GitHub found `SigNoz/agent-skills` still teaches deprecated trace fields (`durationNano`, `hasError`) in published skills. Outcome: a matching agent-skills PR is required and should be linked from this PR summary.
- Local verification is limited by missing `go`/`gofmt` binaries on PATH; `git diff --check` is clean.

### 2026-06-28 - CI Failure Follow-up
- GitHub Actions showed only `test / go` failing; `build`, `fmt`, `lint`, and `deps` passed on the previous PR head.
- Two failures were brittle raw-JSON test assertions: `json.Marshal` escaped `>` / `<` as `\u003e` / `\u003c`, while the decoded filter expressions were correct.
- The remaining failure exposed a trace aggregate payload gap: `groupBy=service.name` was emitted with only `name` and `signal`, missing the resource `fieldContext` and string `fieldDataType` documented in the trace guide.
- Fix direction: decode captured query payloads in tests, attach known trace field metadata to aggregate `groupBy` fields, and align the trace guide's `timestamp` type with backend/MCP metadata (`number`).

### 2026-06-28 - CI Fix Review
- A follow-up multi-agent review found no blockers in the decoded test assertions or trace-scoped aggregate metadata path.
- One reviewer noted `messaging.system` was documented as a trace tag but missing from the aggregate metadata map; implementation added it before commit.
- Reviewers confirmed the direct CI failures are addressed, while noting the new aggregate metadata map duplicates the raw trace select-field list and should be kept in sync with future trace field changes.

### 2026-06-29 - Live E2E Coverage
- User requested live E2E verification against `https://app.us.staging.signoz.cloud` for the updated trace-field migration cases.
- Added a read-only `e2e`-tagged trace-field migration test that uses existing trace data and creates no resources.
- The live matrix covers canonical `search_traces` row keys and `webUrl`, canonical shortcut filters, legacy free-form `durationNano` pass-through, `aggregate_traces` with `duration_nano`, `aggregate_traces` grouped by `service.name`, and `get_trace_details` using a trace discovered from search.
- Credential handling remains env-only in the test; credentials are not hardcoded, logged, or persisted.

### 2026-06-29 - Live E2E Result
- Local non-credentialed compile/skip check passed for `go test -tags=e2e -run '^TestE2ETraceFields_SnakeCaseMigration$' -v ./internal/handler/tools/`.
- Focused local packages passed: `go test ./internal/handler/tools ./pkg/types ./internal/client ./pkg/util`.
- Delegated staging run initially exposed a test-parser issue: scalar aggregate responses return `columns` + `data`, not raw `rows[].data`. The test was updated to assert `service.name` in aggregate columns and non-empty grouped data rows.
- A subsequent delegated staging run briefly hit HTTP 503 while the staging workspace was updating, then passed on retry.
- Verified live fields/behaviors: canonical `search_traces` row fields `trace_id`, `span_id`, `duration_nano`, `has_error`, `service.name`, and `webUrl`; canonical shortcut filters using `has_error` and `duration_nano`; legacy free-form `durationNano` pass-through; `aggregate_traces` with `duration_nano`; `aggregate_traces groupBy=service.name`; and `get_trace_details` via a trace discovered from canonical `trace_id`.

## Open Questions
- [x] Should `search_traces` web URL enrichment accept both `trace_id` and `traceID` during rollout, or should it switch directly to `trace_id` with drift warnings covering old responses? Resolved 2026-06-27: use `trace_id` as the primary key and support `traceID` / `traceId` fallback, warning when rows are present and any row cannot be enriched.
- [x] Should the default raw trace select list replace `rpcMethod` with the tag field `rpc.method`, omit it, or keep it temporarily for response-shape compatibility? Resolved 2026-06-27: replace `rpcMethod` with `rpc.method` using `fieldContext: "tag"` and document that the raw row key changes.
- [x] Does `SigNoz/agent-skills` teach any of the old trace field names, requiring a companion PR? Resolved 2026-06-27: yes; a companion PR is required because published skills still teach `durationNano` / `hasError`.
- [x] Are raw `signoz_search_traces` row keys a public downstream contract that needs a temporary response-compatibility layer, or is the snake_case output-key migration acceptable with explicit PR/release notes? Resolved 2026-06-27: accept the snake_case output-key migration and document it in README plus PR/release notes; do not duplicate legacy aliases in this pass.
- [x] Should the current numeric `kind` select field be kept as `kind` with `fieldContext: "span"` and numeric type, or dropped in favor of `kind_string` only? Resolved 2026-06-27: keep numeric `kind` and also select `kind_string`.
- [x] Which backend versions/environments must pass live verification before rollout? Resolved 2026-06-29: run this PR's live trace-field verification against US staging (`https://app.us.staging.signoz.cloud`) using existing trace data; no live resource creation is needed for this read-only migration check.
