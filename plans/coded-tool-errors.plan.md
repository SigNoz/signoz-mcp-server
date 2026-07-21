# Plan: Coded Tool Errors

## Status
In Progress

## Context
Tool error results are documented as carrying a stable machine-readable `code`, but many handlers still construct bare `mcp.NewToolResultError` values. Missing tenant credentials are the most important example: `GetClient` fails before any upstream request and the client receives no structured classification.

## Approach
1. Add small shared helpers for client/auth, cause-aware cancellation/timeout handling, malformed-upstream responses, and caller-validation errors using the existing stable taxonomy. Reuse `InternalErrorResult` for local internal failures.
2. Replace every direct production `mcp.NewToolResultError` call outside the shared shaping helper with the appropriate coded helper, preserving existing human-readable messages.
3. Let the docs index identify invalid query-string syntax with a typed/sentinel error so the tool handler can distinguish caller validation from cancellation, timeout, and internal index faults.
4. Wrap the production tool-registration boundary with a fallback that assigns `INTERNAL_ERROR` to any future uncoded error result without changing already-coded results. JSON-normalize object-shaped structured content before merging the fallback code.
5. Add focused tests for missing credentials, cause-aware classification, typed structured-content preservation, and an AST invariant that permits the MCP constructor only inside the shared shaping point.

## Files to Modify
- `internal/handler/tools/errs.go` — shared coded-result helpers.
- `internal/handler/tools/schema_compat.go` — registration-boundary fallback.
- `internal/handler/tools/*.go` — replace direct bare error results with specific coded helpers.
- `internal/handler/tools/*_test.go` — focused behavior and invariant coverage.
- `internal/docs/index.go` — typed invalid-search-query classification.
- `internal/docs/index_test.go` — invalid-query classification coverage.
- `plans/coded-tool-errors.{context,plan}.md` — decision history and current plan.

## Verification
- Run focused `internal/handler/tools` tests, then `go test ./...`.
- Run `go vet ./...` and the repository lint target if available.
- Verify production tool sources contain no direct `mcp.NewToolResultError` use outside the shared shaping point.
- Run `git diff --check`.
- Documentation/metadata sync: this adds structured codes to existing error results without changing tool names, schemas, parameters, or success outputs. `README.md`, `manifest.json`, other `docs/`, and companion `SigNoz/agent-skills` should need no change; record the outcome in the PR.
