# Feature: Upstream Authz Error Classification — Context & Discussion

## Original Prompt
> Let's work on this issue. After making changes spin up independent agents for review and then create PR

## Reference Links
- [SigNoz/nerve-pod#27](https://github.com/SigNoz/nerve-pod/issues/27)

## Key Decisions & Discussion Log
### 2026-07-03 — Initial scope
- Production evidence from the Momentic tenant showed `signoz_create_alert` failed on `POST /api/v2/rules` with upstream HTTP 403 and backend code `authz_forbidden`.
- The current MCP contract collapses that non-retryable authorization failure into structured code `UPSTREAM_ERROR`.
- Fix scope: classify upstream HTTP 401/403 failures into a more actionable structured error while preserving the existing visible text.

### 2026-07-03 — Downstream auth classifier compatibility
- Independent review found that a top-level MCP code of `FORBIDDEN` would collide with the AI assistant's exact-match auth-expiry heuristic for SigNoz upstream envelope code `forbidden`.
- Decision: use top-level MCP code `PERMISSION_DENIED` for HTTP 403 while preserving `status: 403` and backend `upstreamCode: authz_forbidden`.

### 2026-07-03 — Future-proofing and staging e2e
- Status-derived classification is the durable boundary: HTTP 401/403 classification works even if the upstream JSON error envelope changes or becomes non-JSON.
- Added a regression test for HTTP 403 with an unparseable body to prove `PERMISSION_DENIED` does not depend on `error.code` or `error.message` parsing.
- Staging e2e against `https://app.us.staging.signoz.cloud` with the supplied user token succeeded for alert create/delete, so the 403 path did not fire for that token. Cleanup was confirmed by a subsequent `get_alert` returning upstream 404.

### 2026-07-03 — Upstream 404 classification
- Follow-up decision: HTTP 404 from the SigNoz backend should map to top-level MCP code `NOT_FOUND`, not generic `UPSTREAM_ERROR`.
- The staging cleanup probe exposed the old behavior (`UPSTREAM_ERROR` with `status: 404`), so the status-derived classifier now routes 404 to `NOT_FOUND` while still preserving parsed `upstreamCode` / `upstreamMessage`.

### 2026-07-03 — Backend error class alignment
- Checked `~/signoz/signoz/pkg/errors` and `~/signoz/signoz/pkg/http/render/render.go` for the upstream error vocabulary and renderer status mapping.
- Also checked the legacy query-service responder and found HTTP 422 is `ErrorExec`, not validation.
- Decision: keep MCP top-level codes distinct from the raw SigNoz envelope, but classify by durable HTTP status classes: validation, unauthorized, permission denied, not found, conflict, rate limited, unsupported, license unavailable, canceled, and timeout.
- Unknown statuses and backend internals remain `UPSTREAM_ERROR`, with `status`, `upstreamType`, `upstreamCode`, and `upstreamMessage` attached when available.

### 2026-07-03 — Review tightening
- Independent review caught two over-broad mappings: 422 looked validation-like by HTTP convention but is legacy query execution in SigNoz, and 405 is defined in `pkg/errors` but not emitted by the checked renderer path.
- Decision: remove 422/405 status classification and pin them as `UPSTREAM_ERROR` unless SigNoz later exposes them through the renderer contract.
- Live read-only staging probe for `signoz_get_alert` with a nonexistent UUID confirmed MCP `code: NOT_FOUND`, `status: 404`, `upstreamCode: not_found`, and no resource creation.

### 2026-07-03 — Downstream scanner hardening
- Independent review noted that a future backend 403 with generic envelope code `forbidden` could still trip the AI assistant's auth-expiry scanner if the raw upstream JSON remained in the MCP text block.
- Decision: for parseable upstream HTTP error envelopes, show `unexpected status <status>: <upstream message>` in the text block and keep `upstreamCode`, `upstreamType`, and `upstreamMessage` in structured content. Unparseable bodies still fall back to the raw body.
- Added legacy `errorType` / string `error` parsing so legacy query-service errors expose `upstreamType` and `upstreamMessage` even when they stay `UPSTREAM_ERROR`.
- Updated the 429 fixture to use the canonical backend `too_many_requests` code.

### 2026-07-03 — PR review body preservation
- GitHub review noted that storing a logging-truncated response body on `HTTPStatusError` could break JSON parsing for long but valid upstream envelopes.
- Decision: keep the full response body in `HTTPStatusError.Body` for parsing, and truncate only when rendering `Error()` or writing log fields.
- Added regression coverage proving a long JSON error envelope remains parseable while the error string and log response stay truncated.

### 2026-07-03 — PR review wrapper context and text bounds
- Follow-up GitHub review noted that sanitizing HTTP status text dropped caller wrapper context such as formula-query metadata fallback guidance.
- Decision: replace only the inner `HTTPStatusError` text inside `err.Error()` with sanitized status/detail text, preserving caller context while avoiding raw upstream JSON in text.
- Follow-up review also noted that full parseable messages or unparseable bodies could be returned unbounded in MCP error text.
- Decision: bound all returned upstream message/body text and structured `upstreamMessage` values with the same truncation helper used for logs.

### 2026-07-03 — Multi-agent review fixes
- Independent review found that stripping raw 401 JSON also removed the assistant's existing auth-expired classifier signal.
- Decision: for upstream HTTP 401 only, expose a nested `upstreamAuth.code` bridge when the backend code is one of the assistant's existing auth envelope codes. This keeps 401 auth-expired behavior without letting 403 `forbidden` permission denials trip the same scanner.
- Independent review found that parseable envelopes without a `message` still fell back to raw JSON text.
- Decision: once an upstream body parses as an envelope, never return the raw body in MCP text just because the message is absent; use status-only text instead.
- Also classified legacy query-service `503` responses with `errorType: timeout` / `canceled` into `TIMEOUT` / `CANCELED`, while leaving other 503s as `UPSTREAM_ERROR`.

## Open Questions
- [x] Should alert creation succeed for the Momentic user? No. The backend correctly rejects non-editor/non-admin users; the MCP server should make that denial machine-actionable.
