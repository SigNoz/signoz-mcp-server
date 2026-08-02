# Plan: Upstream Authz Error Classification

## Status
Done

## Context
SigNoz HTTP failures cross two contracts: the backend renderer response and the MCP client-facing error result. Issue #49's follow-up showed that malformed 401/403 bodies could leak verbatim and recovery did not name the failed operation. The final implementation must classify authorization failures durably, preserve recognized renderer guidance faithfully, and keep unrecognized upstream content off every MCP surface.

## Approach
- Preserve non-2xx HTTP status and body in `HTTPStatusError`; if an error response exceeds the transport cap, discard its body but keep the typed status so 401/403 classification and recovery survive.
- Positively recognize only current nested renderer tuples, legacy `errorType` plus string `error`, or complete top-level renderer tuples. Treat message-only proxy JSON, malformed JSON, and bodies beyond the parse budget as unrecognized.
- Centralize bounded filtering for code, type, message, documentation URL, top-level/detail suggestions, detail messages, and retry delay. Reject credential-shaped code/type values; redact authorization, cookie/session, bearer, named-secret, and signed-URL values without deleting known ordinary authorization diagnostics; neutralize HTML and active Markdown.
- Decode optional renderer fields independently so drift in one field does not erase the verified tuple or valid siblings. Record field names only and emit a distinct WARN before any transport retry; keep scanning the bounded input for late drift even after output caps are filled.
- Preserve the exact recognized renderer summary and every other guidance field independently in structured `upstream*` fields; fold detail messages only into human-readable text. Use status-only local text for every unrecognized body and retain bounded raw body diagnostics only in server logs.
- Classify durable HTTP status classes into the stable MCP code taxonomy, including `UNAUTHORIZED`, `PERMISSION_DENIED`, and `NOT_FOUND`, while preserving caller wrapper context.
- Add a generic status-derived `nextAction` at the shared client boundary so resources and post-mutation partial failures get immediate 401/403 recovery. Supplement registered tool errors with exact tool-name and read/write/neutral guidance from `readOnlyHint`, without inferring viewer/editor/admin roles.
- Qualify renderer retry timing with `retrySafe:true` only for behavior-backed read operations. Mark writes and unannotated operations false and tell clients to verify current state before replaying; nested post-mutation failures always retain `retryPrimaryOperation:false`.
- When notification-channel creation/update succeeds but test-send or read-back fails, keep the overall result successful to prevent duplicate mutation. Return nested `code`, `operation`, and `retryPrimaryOperation:false`, plus `status`/authorization `nextAction` when available; name `signoz_get_notification_channel` for read-back recovery and direct test-send recovery to the existing channel's SigNoz UI Test action because no test-only MCP tool exists.
- Route QB missing-key extraction through the same positively recognized and filtered envelope fields; never scan raw 400/proxy bodies for client-visible recovery data.
- Keep the existing 401 `upstreamAuth.code` compatibility bridge only for recognized assistant auth-envelope codes; never expose it for 403.
- Synchronize README behavior, append-only decision history, current plan, and the companion agent-skills audit. The partial payload additions are additive and no existing skill teaches those fields, so no companion skills change is required.

## Files to Modify
- `internal/client/client.go` and `client_test.go` — typed status preservation, body-free client errors, generic auth recovery, oversized-error behavior, pre-retry shape-drift warnings, and transport tests.
- `internal/client/error_envelope.go` and `error_envelope_test.go` — positive recognition, independent optional-field parsing, complete renderer guidance preservation, deterministic budgets, and adversarial credential/URL/markup filtering.
- `internal/handler/tools/errs.go` and `errs_test.go` — status classification, complete structured guidance, shared partial-failure shape, and wrapper/error tests.
- `internal/handler/tools/schema_compat.go` and `tool_error_codes_test.go` — centralized operation-specific recovery, retry-safety qualification, and direct/negative decorator coverage.
- `internal/handler/tools/notification_channels.go`, `notification_channels_test.go`, and `resource_templates_test.go` — safe partial/resource recovery paths.
- `internal/handler/tools/authz_error_e2e_test.go` — real client/handler/decorator/MCP wire verification for unrecognized proxy JSON, recognized credential-shaped renderer fields, malformed 403, no retry, and bounded diagnostics.
- `internal/handler/tools/upstream_query_error_test.go` and adjacent existing tests — recognized-only QB recovery, exact summary/detail separation, renderer budgets, and compatibility coverage.
- `README.md` — stable codes/status, complete recognized guidance, unrecognized-body withholding, operation recovery, and non-retryable partial outcomes. `manifest.json` has no general error-contract metadata.
- `plans/upstream-authz-error-classification.context.md` — append-only decisions, review repairs, eval evidence, and final CMP-3 audit.

## Verification
- Format and run focused client/tool suites, including the auth wire E2E normally, repeatedly, and under the race detector.
- Run workflow lint and the focused/full guardrail commands from `guardrails/README.md`.
- Run `go test -count=1 ./...`, `go vet ./...`, and `go build ./cmd/server`.
- Cover direct, indirect, and negative recovery with deterministic final catalog/error wire tests. An additional before/after model-session comparison was canceled at the maintainer's explicit direction; record that decision in the context log.
- Run independent MCP-rubric and adversarial security reviews, then resume the verified `claude-opus-5` high-effort read-only review until no actionable findings remain.
- Do not attempt to manufacture malformed auth responses against staging: the deterministic local upstream provides the real client/protocol boundary without credential or mutation risk.
