# Plan: Cap the Text Size of a Tool Result

Status: In Progress
Issue: https://github.com/SigNoz/signoz-mcp-server/issues/452
PR:

## Context

MCP clients reject or file away a tool result above their own limit. Some save a result
over 50,000 characters (or about 25,000 tokens) to a file, so the agent never reads the rows.
#452 reports it for `signoz_get_service_top_operations`, which #455 caps with its own `limit`.
Measurements on a real SigNoz instance show other read tools cross the same line on `main`:

- `signoz_search_traces`: 124,829 characters at `limit=100`, 1,247,127 at `limit=1000`.
- `signoz_search_logs`: 61,132 characters at `limit=100`.
- `signoz_get_trace_details`: 1,175,207 characters for one trace.

A small mock backend stays under the line where real span attributes do not, so the regression
test uses rows with a real instance's attribute volume.

## Approach

- `config.MaxResultChars` (env `MCP_MAX_RESULT_CHARS`, positive-only like `MCP_MAX_REQUEST_BYTES`,
  default 50,000) bounds the text characters of one successful tool result.
- `resultSizeDecorator` runs in `addTool`, so every registered tool passes through it. When the text
  blocks exceed the cap it cuts the first block at a character boundary, keeps later notes, drops
  `StructuredContent` (it would repeat the full payload), and appends a note that names the cap and
  tells the agent to call again with a smaller `limit` or a narrower time range, filter, or field
  selection. It logs a WARN with the original and capped sizes.
- Error results and tools that declare an output schema are not cut. A cut payload cannot validate
  against its schema, and those tools return small bounded results today.
- `guardrails.MaxToolResultChars` pins the ceiling for the default, and
  `TestGuardrail_ToolResultsStayWithinSizeBudget` checks it over the production HTTP handler for
  search_traces, search_logs and get_trace_details.

## Files to Modify

- `internal/config/config.go`: `MaxResultChars`, `MCP_MAX_RESULT_CHARS`, default.
- `internal/handler/tools/result_size.go`: the decorator and the truncation.
- `internal/handler/tools/schema_compat.go`: apply the decorator in `addTool`.
- `internal/handler/tools/result_size_test.go`, `internal/mcp-server/tool_result_size_test.go`: tests.
- `guardrails/policy.go`, `guardrails/tests.txt`, `guardrails/README.md`: budget and inventory.
- `README.md`: the environment variable row.

## Key Decisions

### 2026-11-19 — Cut the text instead of failing or paging

- Decision: truncate with a note rather than return an error or page automatically.
- Rationale: the agent still sees the first rows and is told how to ask for fewer. Paging belongs to
  each tool (`offset`, `limit`), which the note points at. A truncated JSON payload is not valid
  JSON, and the note says so.
- Alternatives: lowering each tool's default `limit` (as #455 does for top operations) fixes one tool
  at a time and does not cover `get_trace_details`, which has no `limit`.

### 2026-11-19 — 50,000 characters by default

- Decision: default to the 50,000-character line some clients apply, configurable per deployment.
- Rationale: a larger default would leave the reported results over the line; operators whose client
  accepts more can raise it.

## Reference Links

- [SigNoz/signoz-mcp-server#452](https://github.com/SigNoz/signoz-mcp-server/issues/452)
- [SigNoz/signoz-mcp-server#455: cap `signoz_get_service_top_operations` with `limit`](https://github.com/SigNoz/signoz-mcp-server/pull/455)
- `docs/mcp-best-practices.md` (OUT-3), `guardrails/README.md`

## Verification

- `go test -race -count=1 ./...`, `make check-guardrails`, `make lint`, and `make check-repo-docs READY=1`
  pass.
- The guardrail test fails on `main` for search_traces, search_logs and get_trace_details (about
  1.4 million characters of result text for the 1,000-span trace) and passes with the change.

## Outcome

Pending review. Results above `MCP_MAX_RESULT_CHARS` (default 50,000) are cut with a note. The check against a real
SigNoz instance is left to review: the fixture rows are sized to the real measurements in #452, not
recorded from a live backend.
