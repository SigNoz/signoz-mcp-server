# Feature: Coded Tool Errors — Context & Discussion

## Original Prompt
> Fix and create PR for getclient and similar issues

## Reference Links
- [PR #255 review: qualify uncoded tool-error paths](https://github.com/SigNoz/signoz-mcp-server/pull/255#discussion_r3619662862)
- [MCP best practices: errors and recovery](../docs/mcp-best-practices.md#8-errors-and-recovery)
- [nerve-pod#164: backend error guidance passthrough](https://github.com/SigNoz/nerve-pod/issues/164)

## Key Decisions & Discussion Log

### 2026-07-21 — Scope and design
- The merged PR review identified that `GetClient` failures return bare MCP tool errors without `StructuredContent.code`. A production-source audit found the same constructor used for client setup, local validation, internal serialization, and malformed upstream success responses.
- This is a separate runtime follow-up from the merged documentation PR. It does not implement nerve-pod#164, which remains focused on preserving recognized fields from SigNoz backend error envelopes.
- Existing messages remain stable. Current call sites receive the most specific existing code: missing tenant credentials are `UNAUTHORIZED`, caller-fixable input/config failures are `VALIDATION_FAILED`, malformed upstream success responses or remote-template failures are `UPSTREAM_ERROR`, and local serialization/index failures are `INTERNAL_ERROR`.
- All production tool registration gains a final fallback that adds `INTERNAL_ERROR` to any future bare error result while preserving its text. This is a runtime invariant, not only a test assertion.

### 2026-07-21 — Implementation and verification
- Replaced all 76 direct production call sites: 39 `GetClient` failures and 37 validation, upstream-response, or internal-result failures. The only remaining `mcp.NewToolResultError` call is the shared coded shaping point in `errs.go`.
- Added a registration-boundary fallback that preserves the original error text and existing structured fields, emits a WARN with the tool name, and assigns `INTERNAL_ERROR` only when a handler returns an uncoded error.
- Focused package tests, the full Go test suite, `go vet ./...`, and the race-enabled tool-handler suite pass. No tool name, input schema, success result, README/manifest entry, or companion agent-skill contract changed.

### 2026-07-21 — First multi-agent review
- Three independent reuse, quality, and efficiency reviews found no blocker or critical issue, but identified valid cause-classification gaps: docs search/fetch cancellation and deadlines were labeled internal, invalid docs query syntax and whitespace-only formulas were labeled internal instead of validation failures, and malformed notification-channel success bodies were labeled internal instead of upstream contract failures.
- Two reviewers independently found that the registration fallback preserved only `map[string]any`, dropping valid struct-backed or `map[string]string` structured objects. The fallback will JSON-normalize object-shaped content before merging the code.
- The source invariant will resolve the actual MCP import alias and allow the constructor only inside `errorWithStructuredContent`, instead of excluding all of `errs.go`. The duplicate `internalError` alias will be removed in favor of the existing `InternalErrorResult` constructor.
- The plan now includes a typed docs-search validation error so the handler can distinguish caller syntax from index faults without string matching. This is a classification change only; search behavior and error text remain otherwise stable.

### 2026-07-21 — First-review fixes verified
- Added cause-aware cancellation and timeout codes, a typed invalid-docs-query signal, validation classification for rejected metric formulas, and upstream classification for malformed notification-channel success bodies. Remote dashboard-template cancellation now preserves the same cause identity.
- The registration fallback now preserves struct-backed and typed-map JSON objects, while the AST invariant resolves the real MCP import and permits the bare constructor only in the shared shaping helper. Removed the duplicate internal-error helper and reused `InternalErrorResult` throughout.
- Focused docs/tools tests, `go test ./...`, `go vet ./...`, the race-enabled tools suite, and `git diff --check` all pass before the second independent review.

### 2026-07-21 — Second multi-agent review
- All three reviewers independently found that typed structured objects carrying an existing known code were normalized only after code detection, causing the fallback to replace the valid code with `INTERNAL_ERROR`. Known-code extraction will accept any JSON-object representation before the fallback runs.
- The source invariant will cover all three bare-error constructors exposed by the MCP SDK and reject dot imports. The invalid-docs-query wrapper will retain the original Bleve parser message while still supporting typed classification.

### 2026-07-21 — Second-review fixes verified
- Known-code extraction now recognizes typed maps and structs before the registration fallback, so already-coded errors remain unchanged. The invariant rejects the SDK's basic, formatted, and error-appending bare constructors plus MCP dot imports.
- Invalid docs syntax retains the original parser message while exposing `VALIDATION_FAILED`. Focused tests, `go test ./...`, `go vet ./...`, the race-enabled tools suite, and `git diff --check` all pass after these fixes.

## Open Questions
- [x] Should this be added to PR #255? Resolved: no; PR #255 is merged, so publish a separate focused runtime PR.
- [x] Does nerve-pod#164 cover this? Resolved: no; #164 covers the backend error envelope after an upstream request, while this change covers local/pre-upstream and response-shaping tool errors.
