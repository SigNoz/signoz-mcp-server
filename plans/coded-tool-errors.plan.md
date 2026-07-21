# Plan: Coded Tool Errors

## Status
In Progress

## Context
Tool error results are documented as carrying a stable machine-readable `code`, but many handlers still construct bare `mcp.NewToolResultError` values. Missing tenant credentials are the most important example: `GetClient` fails before any upstream request and the client receives no structured classification.

## Approach
1. Add small shared helpers for client/auth, local internal, malformed-upstream, and caller-validation error results using the existing stable taxonomy.
2. Replace every direct production `mcp.NewToolResultError` call outside the shared shaping helper with the appropriate coded helper, preserving existing human-readable messages.
3. Wrap the production tool-registration boundary with a fallback that assigns `INTERNAL_ERROR` to any future uncoded error result without changing already-coded results.
4. Add focused tests for missing-credential classification, the registration fallback, preservation of existing codes/messages, and the absence of direct uncoded constructors outside the shaping point.

## Files to Modify
- `internal/handler/tools/errs.go` — shared coded-result helpers.
- `internal/handler/tools/schema_compat.go` — registration-boundary fallback.
- `internal/handler/tools/*.go` — replace direct bare error results with specific coded helpers.
- `internal/handler/tools/*_test.go` — focused behavior and invariant coverage.
- `plans/coded-tool-errors.{context,plan}.md` — decision history and current plan.

## Verification
- Run focused `internal/handler/tools` tests, then `go test ./...`.
- Run `go vet ./...` and the repository lint target if available.
- Verify production tool sources contain no direct `mcp.NewToolResultError` use outside the shared shaping point.
- Run `git diff --check`.
- Documentation/metadata sync: this adds structured codes to existing error results without changing tool names, schemas, parameters, or success outputs. `README.md`, `manifest.json`, other `docs/`, and companion `SigNoz/agent-skills` should need no change; record the outcome in the PR.
