# Plan: Upstream Authz Error Classification

## Status
Done

## Context
The follow-up in [nerve-pod#49](https://github.com/SigNoz/nerve-pod/issues/49#issuecomment-5156802780) identifies two narrow gaps: an unparseable upstream 401/403 body can be copied into client-visible MCP text, and recovery does not identify the failed operation. Broader ERR-6 renderer fidelity work is tracked separately in [nerve-pod#191](https://github.com/SigNoz/nerve-pod/issues/191).

## Approach
- Preserve the existing typed `HTTPStatusError`, status-derived MCP codes, and parseable JSON-envelope behavior.
- When a 401/403 body is not valid JSON, make `HTTPStatusError.Error()` return a canonical body-free authentication or permission message. Keep the original body on the error so the existing bounded client log remains useful server-side.
- At registered-tool dispatch, append recovery only when both the upstream status and stable authorization code match. Name the exact MCP tool and tell the caller to re-authenticate or request access before retrying that operation.
- Leave local missing-credential errors, non-authorization statuses, notification partial outcomes, query-builder recovery, retry metadata, renderer parsing, and drift detection unchanged.
- Document only this narrow client-visible behavior. No manifest or companion agent-skills update is needed because no tool, parameter, or payload schema changes.

## Files to Modify
- `internal/client/client.go` and `internal/client/client_test.go`
- `internal/handler/tools/errs.go` and `internal/handler/tools/errs_test.go`
- `internal/handler/tools/schema_compat.go` and `internal/handler/tools/tool_error_codes_test.go`
- `README.md`
- `plans/upstream-authz-error-classification.context.md`

## Verification
- Run formatting and imports.
- Run focused client and tool error tests.
- Run guardrails, the full Go test suite, vet, and server build.
- Inspect the final diff against `origin/main` and the PR base to confirm unrelated organization-overview work is preserved and broader ERR-6 changes are absent.
