# Feature: Upstream Authz Error Classification — Context & Discussion

## Original Prompt
> Let's work on this issue. After making changes spin up independent agents for review and then create PR

## Reference Links
- [SigNoz/nerve-pod#27](https://github.com/SigNoz/nerve-pod/issues/27)
- [SigNoz/nerve-pod#49 follow-up review](https://github.com/SigNoz/nerve-pod/issues/49#issuecomment-5156802780)

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

### 2026-08-02 — Strict authorization-body sanitization and operation recovery
- Follow-up review of nerve-pod#49 found one remaining privacy gap: an upstream 401/403 body that is not valid JSON is still copied into the MCP error text. The same review noted that authorization recovery is generic rather than tied to the failed operation.
- Decision: never expose an unparseable 401/403 response body in MCP text or structured content. Use a canonical status-derived authentication/permission message instead. Continue preserving bounded fields from recognized JSON error envelopes, as required by ERR-6, and retain bounded raw diagnostics only in the existing server-side error log.
- Decision: append recovery centrally at registered-tool dispatch using the tool's behavior-backed `readOnlyHint`. Read failures tell the agent to obtain access to the read operation; writes tell it to obtain permission for the write operation. Both name the exact MCP tool and give the smallest next action. Tools without an explicit annotation fall back to neutral operation wording rather than being guessed as writes.
- This is a shared error-contract hardening change and applies uniformly to every registered tool that returns the shared upstream 401/403 result. It is being included in the existing organization-overview PR at the user's request.

### 2026-08-02 — Boundary-wide sanitization after independent audit
- A read-only audit found client-visible paths outside `upstreamError`: live resource templates return wrapped `HTTPStatusError` values directly, and notification-channel test/read-back failures can embed `err.Error()` inside successful results and warning notes after the primary mutation succeeds.
- Decision: make `HTTPStatusError.Error()` itself body-free and actionable for 401/403 while preserving the full `Body` field for parsing and the existing bounded client WARN diagnostic. This closes tool, resource, partial-success-note, wrapper, and span-status leaks at the shared upstream boundary. Non-authorization statuses keep their existing bounded-body error text.
- Operation recovery remains a registered-tool concern. The decorator requires the matching structured HTTP status as well as the stable code, so local missing-credential `UNAUTHORIZED` results are not conflated with an upstream 401. No viewer/editor/admin role is inferred; the only annotation-derived distinction is read, write, or unknown.
- Verification uses a deterministic local SigNoz HTTP server plus real client methods, production read/write handlers, registered decorators, and MCP JSON-RPC serialization. It proves malformed 401/403 bodies stay off the wire, bounded diagnostics remain in server logs, and neither status is retried. Live staging cannot deterministically emit malformed authorization bodies, so no credentialed mutation is appropriate for this regression.

### 2026-08-02 — Documentation and companion-skills audit
- `README.md` now states the promised `UNAUTHORIZED` / `PERMISSION_DENIED` codes, numeric status, operation-aware recovery, and malformed-body sanitization. `manifest.json` has no general error-contract metadata, and no tool name, schema, description, or manifest entry changes in this follow-up.
- Audited `SigNoz/agent-skills`: `signoz-mcp-setup` already teaches the 401/403 codes and recovery semantics, while `signoz-creating-alerts` carries the stronger domain-specific notification-channel permission guidance. This server change preserves those codes and is additive/sanitizing; it does not change a tool, parameter, payload shape, or taught workflow. No companion agent-skills PR is needed.

### 2026-08-02 — Adversarial recognized-envelope redaction
- Repeated protocol E2E passed but its independent reviewer found a recognized-envelope edge case: token redaction consumed only one whitespace-free value, so a Basic authorization credential or a quoted multi-word secret could leave a suffix client-visible.
- The shared sanitizer now removes the complete authorization value through the end of its line, handles quoted named-secret values atomically, and retains the standalone Bearer-token filter. Regression fixtures cover Basic credentials, quoted passwords, named tokens, and standalone Bearer values before the shared text and structured error paths consume the message.

### 2026-08-02 — Complete renderer fidelity and positive recognition
- The final rubric and Opus reviews found that centralizing the parser exposed two broader ERR-6 gaps: documentation URLs, top-level/detail suggestions, and retry delay were discarded, while unrecognized non-auth bodies still fell through to raw client text.
- The parser now recognizes only a complete current renderer tuple, the verified legacy `errorType` plus string `error` pair, or a complete top-level renderer tuple. A generic proxy object such as `{"status":"error","message":"..."}` is unrecognized. Every unrecognized body now produces status-only client text for all HTTP statuses; raw content remains only in bounded server diagnostics.
- Recognized renderer code, type, message, URL, suggestions, detail messages/suggestions, and retry delay are bounded and filtered once, rendered into text for resource compatibility, and exposed independently in structured `upstream*` fields for tools and partial outcomes. The parse budget increased to 1 MiB while the detail array remains separately capped, so large real renderer envelopes retain their main fields without unbounded output.
- Redaction was refined after Opus found that keyword-only matching destroyed legitimate prose such as `authorization: user lacks role editor`. Authorization scheme/opaque credentials, cookie headers, Bearer values, assignments, quoted secrets, and sufficiently opaque colon values are removed; ordinary authorization/token diagnostics remain faithful. Credential-shaped code/type tokens are rejected.

### 2026-08-02 — Shared recovery and oversized authorization responses
- Generic 401/403 `nextAction` now comes from the shared client boundary, so recognized and unrecognized resource errors and post-mutation partial outcomes all tell agents to re-authenticate or request the required access. Registered tools still supplement this with the exact tool and read/write annotation.
- Notification-channel authorization failures no longer advise editing webhook/channel configuration. They explicitly say the mutation already succeeded, forbid repeating it, carry `retryPrimaryOperation:false`, and direct the agent to retry only verification; read-back recovery names `signoz_get_notification_channel`.
- An oversized non-2xx body now returns a body-free `HTTPStatusError` instead of a generic size error. This preserves numeric status and `UNAUTHORIZED` / `PERMISSION_DENIED` classification even beyond the 64 MiB transport cap.

### 2026-08-02 — Corrected companion-skills audit
- The earlier entry's statement that payload shape did not change was too broad. Notification-channel partial outcomes do gain additive nested `code`, `status`, `operation`, `nextAction`, and `retryPrimaryOperation` fields; no existing field is removed or reinterpreted.
- Re-audited `SigNoz/agent-skills`: no skill parses or teaches the notification `test_notification` / `read_back` payload shape. `signoz-mcp-setup` already teaches the stable 401/403 codes and recovery, and `signoz-creating-alerts` already teaches notification permission gating. Under CMP-3 this additive metadata does not require a companion PR.

### 2026-08-02 — Independent optional-field, query-recovery, and retry-safety review
- The Opus and independent MCP reviews found that strict typed decoding of optional renderer guidance could discard an otherwise verified error tuple, and that the existing QB missing-key helper still scanned raw 400 bodies outside the positive-recognition boundary. Optional URL, suggestions, detail, and retry fields now decode independently; valid siblings survive wrong-type additions, late invalid entries are still inspected after output caps, and only filtered message/detail fields can supply QB recovery keys.
- Recognized `upstreamMessage` now remains the exact filtered renderer summary. Detail messages stay independently addressable under `upstreamDetails` and are folded only into human-readable text, avoiding duplicated/mutated structured fields.
- A `status:"error"` body with an unknown required shape emits a distinct WARN before retry. Recognized envelopes with malformed optional fields emit a field-name-only WARN; no upstream values enter the drift signal. This also catches a transient drifted 503 even when the retry later succeeds.
- Renderer retry delay is timing guidance, not proof that replay is safe. Registered reads add `retrySafe:true`; writes and unannotated tools add `retrySafe:false` plus a verify-current-state warning. Post-mutation nested failures retain `retryPrimaryOperation:false` and are never promoted to top-level errors that could trigger duplicate mutations.

### 2026-08-02 — Executable partial recovery and operation-specific authorization wording
- Generic shared `nextAction` owns the actual 401/403 remediation. The registered-tool decorator now adds only unique operation context: the exact failed tool and its behavior-backed read/write/neutral scope, followed by “retry only this operation” for 401. This removes duplicated recovery prose without weakening the immediate action.
- No test-only notification-channel MCP tool exists. A successful create/update followed by a failed test-send therefore instructs the agent not to replay the mutation and to use the existing channel's Test action in the SigNoz UI after remediation. Read-back failure continues to name the callable `signoz_get_notification_channel` recovery. README now documents `status` and authorization `nextAction` as conditional nested fields rather than universal ones.

### 2026-08-02 — Final adversarial renderer-sanitization boundary
- Repeated adversarial review expanded credential filtering across quoted/dotted/prefixed/bracketed names; short and multi-word assignments; Basic/Bearer/Digest/AWS authorization forms; cookies and session fields; JWTs and common key prefixes in prose/code/type; and signed or credential-bearing URLs in every guidance channel. Documentation URLs reject query data and unsafe host/path/fragment components; prose URLs lose query/fragment data and are rendered inert.
- Renderer prose is normalized before filtering, then raw HTML and every source Markdown bracket are made inert. The visible fullwidth `［REDACTED］` marker cannot itself become a Markdown link when attacker-controlled parentheses follow it. Known natural diagnostics such as `authorization: insufficient_permissions`, `token: signature_mismatch`, and `invalid token: signature mismatch` remain intact.
- Full MCP JSON-RPC serialization tests cover an unrecognized proxy 401, a recognized renderer 401 containing credential-shaped fields across message/URL/suggestions/details, and malformed HTML 403 on a mutation. They assert stable status/code/recovery, no retry, no credential canary on the wire, and bounded server diagnostics.

### 2026-08-02 — Final independent and Claude Opus 5 verification
- The final bounded adversarial review returned no findings after the client sanitizer/drift suite, authorization/retry decorator suite, and 20 consecutive real MCP-wire authorization runs passed. It confirmed `retrySafe:true` is restricted to registered reads, writes/unannotated tools receive `retrySafe:false` plus state verification, and post-mutation partials retain `retryPrimaryOperation:false` and `retrySafe:false`.
- The existing Claude review session `360efb22-52c0-4d48-8fde-cde3cc49de79` was resumed with explicit `claude-opus-5`, high effort, manual/read-only permissions, in the current repository. Its result reported no findings after re-reading the complete diff and section 11; `modelUsage.canonicalModel` confirmed `claude-opus-5`. Claude's attempted package vet command was permission-denied in manual mode; the local `go vet ./...`, focused/full tests, guardrails, build, and protocol inspector all ran separately and passed.
- EVL-1 direct evidence is the malformed and recognized 401 `signoz_get_org_overview` MCP-wire pair. Indirect evidence is the malformed 403 `signoz_delete_dashboard` write path and nested notification partial outcomes. Negative evidence keeps local no-status `UNAUTHORIZED` and non-auth upstream 500 results outside authorization decoration. Request counters prove no 401/403 retry, and mutation retry timing is explicitly unsafe unless current state is verified.

### 2026-08-02 — Maintainer stopped further review
- A final advisory pass suggested an additional before/after model-session comparison for recovery behavior. The maintainer explicitly directed the work to stop further reviews, so that optional EVL-1 comparison was canceled before completion. The retained evidence is the deterministic direct/indirect/negative MCP-wire suite, behavior tests, completed independent reviews, and verified Opus 5 static review above.

### 2026-08-02 — Fable overengineering review and cleanup boundary
- The maintainer asked for a Fable 5 review against the narrower nerve-pod#49 follow-up comment, which called only for canonical fallback text when 401/403 bodies are unparseable and operation-specific recovery. The verified `claude-fable-5` high-effort read-only session `c5a20f75-6171-4863-8c33-16f2f6cba492` judged the issue-49 portion substantially overengineered relative to that comment.
- Cleanup preserves the independently pre-existing ERR-6 contract: positive recognition, bounded faithful renderer guidance, status-only fallback for every unrecognized body, safe filtering, recognized-only QB recovery, and detectable optional-field drift. It also preserves typed status handling and nested post-mutation failure classification required by repository rules.
- Cleanup removes the unrelated `retrySafe` output contract, collapses the adversarial redaction matrix to high-confidence credential and markup handling, and simplifies duplicated notification recovery prose. Organization-overview source conservation and typed projection behavior remain unchanged.

### 2026-08-02 — Fable cleanup review follow-up
- The requested verified `claude-fable-5` high-effort read-only review session `e87cf121-f50d-4627-8ead-2b7e8bfd78db` found one fidelity regression in the simplified standalone Basic/Bearer matcher: ordinary prose such as “Basic authentication is required” matched as a credential. The matcher again requires a long or token-shaped value, and a regression test preserves ordinary guidance.
- The remaining ambiguous unquoted `authorization:` / `token:` matcher and diagnostic allowlist were removed instead of expanding another prose classifier. High-confidence coverage remains for quoted authorization values, explicit schemes, named assignments, JWTs, common key prefixes, credential-bearing URLs, and active markup.

### 2026-08-02 — Cleanup verification complete
- The cleanup removes 178 lines net from the prior branch state without changing organization-overview source conservation or typed projections. Focused client/tool tests, the authorization MCP-wire race test, guardrails, the full Go suite, vet, build, formatting/imports, and diff checks pass.
- The unchanged Inspector protocol script passed shell syntax but could not run locally because GNU `timeout` is unavailable in this macOS environment; the prior branch run and GitHub protocol check remain the protocol evidence. A focused continuation of the completed Fable session was attempted after the accepted P2 fix but Anthropic returned a session-limit error, so no additional reviewer instance was started.

## Open Questions
- [x] Should alert creation succeed for the Momentic user? No. The backend correctly rejects non-editor/non-admin users; the MCP server should make that denial machine-actionable.

### 2026-08-03 — Scope split to the exact issue comment
- The maintainer chose to keep this PR limited to the two gaps named in the nerve-pod#49 follow-up comment: canonical body-free fallback text for non-JSON 401/403 responses and recovery tied to the exact failed MCP tool.
- Decision: remove the broader renderer-fidelity parser, redaction framework, notification partial-result contract, retry-safety metadata, query-builder recognition gating, drift warnings, and expanded protocol fixture from this PR. Preserve the independent organization-overview work already on the branch.
- Created [SigNoz/nerve-pod#191](https://github.com/SigNoz/nerve-pod/issues/191) for the pre-existing broader ERR-6 guidance-fidelity work. That issue explicitly excludes expansion of this PR.
- The narrow implementation keeps the raw response body on `HTTPStatusError` for the existing bounded server log, returns canonical text only when a 401/403 body is invalid JSON, and adds exact-tool recovery only for results whose structured status and authorization code agree. Local credential errors and non-authorization failures are unchanged.

### 2026-08-03 — Narrow cleanup verification complete
- The final branch relative to `origin/main` contains the existing organization-overview feature plus the focused authorization fallback/recovery implementation and tests. The broader ERR-6 files and notification/query/retry contract changes are absent.
- Focused client/tool tests, guardrails, the full Go suite, `go vet ./...`, `go build ./cmd/server`, formatting/imports, diff checks, and protocol-script shell syntax pass. Local workflow lint could not run because `actionlint` is not installed; the GitHub guardrail workflow remains authoritative for that check.

### 2026-08-03 — Multi-agent P1: non-object JSON authorization bodies
- A focused scope reviewer found that `json.Valid` accepted scalar, array, and `null` bodies even though the SigNoz envelope parser cannot consume those shapes. The shared fallback could therefore still echo a syntactically valid but unparseable 401/403 body.
- Decision: treat only a non-null JSON object as a parseable authorization envelope at the shared client boundary. JSON strings, arrays, scalars, `null`, malformed JSON, HTML, plain text, and empty bodies use the same canonical body-free fallback. Add client coverage for string, array, and `null`, plus shared mapper coverage for the array case; do not add a general renderer parser or sanitizer.

### 2026-08-03 — Ready-for-review handoff
- The maintainer directed the feature plan to be marked `Done` and will move PR #267 from draft to ready for review after this focused P1 fix is pushed.
