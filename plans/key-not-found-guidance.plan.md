# Plan: Key-Not-Found Guidance

## Status
Done

## Context
nerve-pod#148: 162+ rows/7d of 400 `invalid_input` ``key `service.name` not found`` errors, concentrated on search_logs. Root cause: agents assume `service.name` exists on logs, but logs attributes are workspace-specific — the key is not spec-compulsory for logs, and pipelines may be unparsed. The backend hard-errors unknown keys for logs (while traces synthesize them), and the MCP server's own shortcut params, prompt, descriptions, and instructions steer agents into the failing filter. No backend fix is possible (legitimate tenant states); the MCP layer must guide agents to recover.

## Approach

1. **Error enrichment** (`internal/handler/tools/errs.go`):
   - `missingFilterKeys(err) []string` — on an HTTP 400 `HTTPStatusError`, regex ``key `X` not found`` over the raw body; dedup, cap. Fail-open.
   - `upstreamQueryError(err, signal) *mcp.CallToolResult` — wraps `upstreamError`; when missing keys are found, appends signal-aware guidance (discover keys via `signoz_get_field_keys`, logs-specific caveat that even `service.name` is not guaranteed, suggest `k8s.*` alternates or dropping the condition) and adds `missingKeys` to StructuredContent.
   - `parseUpstreamErrorBody` additionally collects `error.errors[].message` (new envelope) and appends them to the displayed detail so newer backends don't hide the per-term errors behind "Found N errors while parsing the search expression."
   - `logQueryFailure(ctx, msg, err)` on Handler — WARN with `missingKeys` attr when detected (expected agent mistake), else defer to `logUpstreamFailure`.

2. **Handlers**: `logs.go` (search/aggregate → signal "logs"), `traces.go` (search/aggregate → "traces"), `metrics_query.go` (query_metrics → "metrics"), `query_builder.go` (execute_builder_query → "" generic): swap `upstreamError`/`logUpstreamFailure` for the new pair on the QueryBuilderV5 error path — all six QueryBuilderV5 callers behave uniformly.

3. **Descriptions** (`logs.go`): `logsFilterParamDescription` + both `service` param descriptions state that log keys are workspace-specific and `service.name` is not guaranteed on logs; on key-not-found, discover keys and retry.

4. **Prompt** (`pkg/prompts/prompts.go`): `debug_service_errors` step 1 gains a recovery clause for the key-not-found failure.

5. **Instructions** (`pkg/instructions/instructions.go`): rule 2 gains the per-signal availability caveat + recovery guidance.

6. **Logs guide** (`pkg/querybuilder/logs_guide.go`): availability caveat next to "Unknown keys hard-error".

7. **Docs sync**: README updates — logs tools' `filter`/`service` parameter wording plus a **Key-not-found errors** bullet documenting the `missingKeys` structured error field on all six QB tools. manifest.json unchanged (only name + tool description stored; neither changes). agent-skills: `missingKeys` is an additive error-output field, and `signoz-generating-queries` already teaches signal-specific key discovery while deferring error-shape detail to the MCP server, so the contract the skills teach is unchanged — no skills PR needed; outcome stated in PR summary.

## Files to Modify
- `internal/handler/tools/errs.go` — detection, enrichment, envelope-additional extraction, WARN logging helper
- `internal/handler/tools/logs.go` — descriptions + new error path
- `internal/handler/tools/traces.go` — new error path
- `internal/handler/tools/query_builder.go` — new error path
- `internal/handler/tools/metrics_query.go` — new error path
- `pkg/prompts/prompts.go` — debug prompt recovery clause
- `pkg/instructions/instructions.go` — rule 2 caveat
- `pkg/querybuilder/logs_guide.go` — availability caveat
- `README.md` — logs tools parameter wording
- `internal/handler/tools/upstream_query_error_test.go` (new) — fixtures for both envelope generations, enrichment, structured fields, WARN level, bounded extraction, six-handler end-to-end table test

## Verification
- `go test ./...` (green), `go vet`, `gofmt -l` clean.
- Unit fixtures: old-format single message, new-format `error.errors[]`, `[]string` details fallback, malformed details shape (main fields preserved), multiple missing keys deduped, oversized keys dropped, scan/surface caps, non-400, non-key 400 (no enrichment), singular/plural guidance.
- Handler-level table test driving all six QueryBuilderV5 callers (search/aggregate logs & traces, query_metrics, execute_builder_query) through a failing mock: per-signal guidance noun + `missingKeys` in StructuredContent.
- Log-severity table test: key-not-found 400 → WARN with `missingKeys` attr; other 400s → ERROR; cancellation → DEBUG.
- Codex (gpt-5.6-sol, high) review of the diff before PR — 0 blockers; 5 should-fixes + 2 nits applied (see context log).
