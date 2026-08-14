# Plan: Upstream Error Guidance Fidelity

## Status
In Progress

## Context
The shared MCP upstream-error path preserves stable classification and a
bounded message, but it drops the SigNoz renderer's documentation URL,
top-level/detail suggestions, independently addressable detail messages, and
retry delay. It also treats arbitrary JSON objects as parsed envelopes and can
copy unrecognized non-authorization bodies into MCP text and server logs.

This focused follow-up implements ERR-6 after the official SDK migration. It
closes nerve-pod #191 and #164 without altering any advertised MCP catalog or
successful result.

## Approach

### 1. Add one bounded, positively recognizing client-boundary parser
- Add a small internal client value describing safe core fields, optional
  guidance, whether the shape is recognized, whether it resembles a SigNoz
  `status:"error"` response, and fixed optional-field drift names.
- Recognize only:
  1. `status:"error"` plus nested object `error` with non-empty string `code`
     and `message`; preserve optional string `type`.
  2. `status:"error"` plus non-empty string `errorType` and string `error`.
- Reject top-level renderer tuples, message-only proxy JSON, non-error status,
  non-object roots, malformed/trailing JSON, and over-budget bodies.
- Decode `url`, `suggestions`, `errors`, and `retry` independently. Support
  verified historical detail objects with `message` and modern detail
  suggestions.
  Accept only non-negative integer-nanosecond `retry.delay`.

### 2. Keep output deterministic and filter whole unsafe values
- Cap the full parse input, code/type tokens, URL, raw optional arrays, detail
  count, suggestion count, and every surfaced string. Reuse the existing 4 KiB
  message bound, 16 KiB detail-array gate, and five-detail limit; introduce
  only the corresponding small URL/token/suggestion caps.
- Trim and deduplicate safe guidance in source order. Continue inspecting
  entries after output caps so wrong-type drift remains detectable.
- Drop, rather than rewrite, an individual field containing a high-confidence
  named credential assignment/token, disallowed control character, genuinely
  active HTML, or active Markdown link/image. Preserve ordinary diagnostic
  prose—including angle-bracket placeholders and authentication wording—
  verbatim, and signal the fixed field name when a non-empty value is dropped.
- Accept only absolute `http`/`https` documentation URLs with a host, no
  userinfo/control characters, and no credential-bearing component.

### 3. Make the entire shared error boundary safe and observable
- Make `HTTPStatusError.Error()` render recognized safe guidance or a local
  status-derived fallback. Preserve wrapper context while ensuring no raw body
  is copied into tool/resource/partial-result text or span error descriptions.
- Remove raw response-body values from client retry and terminal status logs;
  retain status, attempt, retryability, and body byte size.
- On each outbound request, emit at most one value-free WARN when a
  `status:"error"` body has an unrecognized/oversized required shape or a
  recognized envelope has wrong-typed or unsafe fields. Detect transient retry
  drift too, without reparsing later attempts after the warning is emitted.

### 4. Expose complete ERR-6 guidance through MCP
- Keep stable MCP `code`, numeric `status`, `upstreamCode`, `upstreamType`,
  exact-summary `upstreamMessage`, and conditional `upstreamAuth` fields.
- Add only present safe fields:
  - `upstreamURL: string`
  - `upstreamSuggestions: string[]`
  - `upstreamDetails: [{message, suggestions}]`
  - `upstreamRetry: {delay}` where delay is integer nanoseconds
- Render the same guidance in the text block with fixed labels. Fold detail
  messages into text, not `upstreamMessage`, so downstream text-only consumers
  remain useful while structured fields remain faithful.
- Restrict Query Builder missing-key extraction to the recognized safe summary
  and details. Preserve its local guidance and `missingKeys` as additive data.
- Leave notification-channel partial-success semantics, retry policy,
  authorization decoration, stable error taxonomy, and all successful outputs
  unchanged.

### 5. Pin the intentional contract change without broad fixture updates
- Add focused parser tests for current and historical nested renderers, legacy
  query-service errors, required-shape rejection, malformed/trailing/oversized
  bodies, optional-field independence, collection and aggregate text bounds,
  deduplication, retry units, credential/active-markup filtering, verified
  real SigNoz placeholder/authentication guidance, and safe URLs.
- Add real-client tests for safe `HTTPStatusError.Error()`, raw-log exclusion,
  once-per-request drift WARN (including a retried transient), and a checked-in
  redacted canonical renderer response with authoritative source provenance.
- Add tool tests for exact text/structured shaping, wrapper context,
  authorization decorator composition, and recognized-only Query Builder
  recovery.
- Add a narrow registered-tool wire test for one recognized and one
  unrecognized upstream failure under legacy `2025-11-25` and modern
  `2026-07-28` HTTP flows. Reuse existing raw request helpers and do not create
  another catalog fixture or repeat the full transport matrix.
- Make that focused wire table the exact representative error-contract
  assertion; do not add a second JSON golden corpus. Keep the existing
  43-tool/22-resource/2-template/4-prompt catalog oracle byte-unchanged and run
  it to prove no advertised or successful content drift.

### 6. Documentation, consumer audit, and delivery
- Update the README's existing upstream-error note with the four optional
  fields and text-fallback promise. Keep `manifest.json`, tool docs, resources,
  prompts, and server metadata unchanged.
- Record CMP-3: no companion agent-skills PR is needed; note the Assistant
  proxy's code-only structured reduction as a non-blocking consumer limitation.
- Delegate any credentialed staging probe to a subagent. It must remain
  read-only, avoid logging credentials, and report whether a real recognized
  error carried each expected field. The checked-in canonical fixture remains
  the deterministic complete-envelope proof.
- Before opening the PR, run Fable 5 high for overengineering and Opus 5 xhigh
  for exhaustive correctness. Use no third model for the same final review.
- Open one conventional `fix(mcp): preserve recognized backend error guidance`
  PR against `main`, with `Closes SigNoz/nerve-pod#191` and
  `Closes SigNoz/nerve-pod#164`.

## Files to Modify
- `internal/client/error_envelope.go` — verified envelope recognition, bounds,
  whole-value filtering, safe text, and drift metadata.
- `internal/client/error_envelope_test.go` — source-derived contract fixture and
  parser safety tests.
- `internal/client/client.go`, `internal/client/client_test.go` — safe
  `HTTPStatusError`, body-free status logs, and once-per-request drift WARN.
- `internal/handler/tools/errs.go`, `internal/handler/tools/errs_test.go` — MCP
  text/structured guidance shaping and safe fallback.
- `internal/handler/tools/upstream_query_error_test.go` — recognized-only Query
  Builder extraction and additive recovery.
- Focused existing/new `internal/mcp-server` test file — dual-era registered
  wire cases using shared harnesses.
- `README.md` — concise structured/text error contract.
- `plans/upstream-error-guidance.context.md` and this plan.
- `plans/official-go-sdk-migration.context.md` and `.plan.md` — post-merge
  completion record only.

## Verification
- `gofmt`/`goimports` on changed Go files and `git diff --check`.
- Focused normal and race tests for `internal/client`,
  `internal/handler/tools`, and the dual-era wire cases.
- Guardrail lint/inventory and `TestGuardrail_WireCatalogGoldens`.
- `go test -count=1 ./...`, `go test -race` on changed packages, `go vet ./...`,
  and `go build ./cmd/server`.
- Exact `golangci-lint` version used by CI plus relevant shell/action lint when
  workflow/script files change (none are planned).
- Credential-free source-derived client/MCP contract test, then a delegated
  read-only staging probe if a deterministic renderer-guidance error is
  available.
- Fable 5 high overengineering review followed by Opus 5 xhigh exhaustive
  review; independently validate and fix only substantiated findings, rerun the
  affected gates, and keep the two-review maximum.

## Stop Conditions
- Any tool schema, description, annotation, output schema, success payload,
  resource/template content, prompt, or server instruction changes.
- Any stable MCP error code/status/auth field is removed, renamed, or changes
  classification.
- Unrecognized or unsafe upstream values reach MCP text/structured content,
  logs, spans, or analytics.
- Valid guidance is lost because an unrelated optional sibling drifts.
- Query Builder recovery reads raw unrecognized bodies or overwrites backend
  guidance.
- Implementation starts recreating a general secret scanner, retry-policy
  framework, duplicate parser, or historical PR #267's broad partial-success
  redesign.
