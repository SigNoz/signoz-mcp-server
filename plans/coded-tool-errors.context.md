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

## Open Questions
- [x] Should this be added to PR #255? Resolved: no; PR #255 is merged, so publish a separate focused runtime PR.
- [x] Does nerve-pod#164 cover this? Resolved: no; #164 covers the backend error envelope after an upstream request, while this change covers local/pre-upstream and response-shaping tool errors.
