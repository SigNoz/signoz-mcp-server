# Feature: Upstream Error Guidance Fidelity — Context & Discussion

## Original Prompt
> Can we pick these as part of this migration as well?
> https://github.com/SigNoz/nerve-pod/issues/191
> https://github.com/SigNoz/nerve-pod/issues/164

> Ok add to plan to be done after merging migration

> PRs are merged, what next?

> Let's work on it

## Reference Links
- [SigNoz/nerve-pod#191 — Align MCP upstream errors with ERR-6 guidance fidelity](https://github.com/SigNoz/nerve-pod/issues/191)
- [SigNoz/nerve-pod#164 — Preserve SigNoz backend error guidance in MCP results](https://github.com/SigNoz/nerve-pod/issues/164)
- [Current SigNoz error JSON contract](https://github.com/SigNoz/signoz/blob/abf60c0878a994c846e8722a09bd2be869fde455/pkg/errors/http.go)
- [Current SigNoz HTTP error renderer](https://github.com/SigNoz/signoz/blob/abf60c0878a994c846e8722a09bd2be869fde455/pkg/http/render/render.go)
- [Current renderer wire tests](https://github.com/SigNoz/signoz/blob/abf60c0878a994c846e8722a09bd2be869fde455/pkg/http/render/render_test.go)
- [MCP best practices — ERR-6 and CMP-3](../docs/mcp-best-practices.md)

## Key Decisions & Discussion Log

### 2026-08-14 — Delivery boundary and implementation base
- Runtime SDK migration PR #286 and selected-conformance PR #287 are merged;
  nerve-pod #194 is closed. This follow-up starts independently from merged
  `main` at `e051a25ddc861a3a1d0978b2715b3f5eed13e70a` on branch
  `codex/upstream-error-guidance`.
- Implement #191 as the complete scope for overlapping #164 and close both
  from one focused PR. No SDK migration compatibility shim or second error
  path is needed.
- The discarded pre-scope-split commits from PR #267 are historical evidence,
  not implementation material. Do not cherry-pick the 2,256-line version or
  revive its general-purpose sanitizer, retry-safety framework, or unrelated
  notification-channel changes.

### 2026-08-14 — Authoritative current and legacy envelopes
- Refreshed the sibling SigNoz repository's remote refs without touching its
  unrelated dirty frontend files. Current remote `main` is `abf60c0`; the
  relevant renderer/error blobs match the local checkout.
- Positively recognize the current and historical renderer family only when
  the outer object has `status: "error"` and nested `error` is an object with
  non-empty string `code` and `message`. `type` is current-required but remains
  parser-optional because verified older renderers did not emit it.
- Also recognize the still-emitted legacy query-service shape only when it has
  `status: "error"`, non-empty string `errorType`, and string `error`.
- Do not trust message-only proxy JSON, an unwrapped top-level renderer tuple,
  missing-status bodies, arrays, HTML, or malformed/trailing JSON. No current
  source or recorded response verifies the previously proposed top-level
  tuple.
- Current optional guidance is `url`, top-level `suggestions`, detail
  `errors[].message` plus `errors[].suggestions`, and `retry.delay`. The delay
  is an integer Go `time.Duration` serialized in nanoseconds; duration strings
  are not part of any verified generation.

### 2026-08-14 — Lean client-visible contract
- Preserve existing stable `code`, numeric `status`, `upstreamCode`,
  `upstreamType`, `upstreamMessage`, and conditional `upstreamAuth` behavior.
- Reuse the previously reviewed additive names `upstreamURL`,
  `upstreamSuggestions`, `upstreamDetails` (`[{message,suggestions}]`), and
  `upstreamRetry` (`{delay}`). They were never released, but they match the
  existing `upstream*` namespace and avoid reopening naming without evidence.
- `upstreamMessage` becomes the exact filtered backend summary. Detail
  messages remain independently addressable under `upstreamDetails` and are
  folded into human-readable text for backward usability. Text also labels
  documentation, top-level/detail suggestions, and retry delay so consumers
  that retain only MCP text still receive the complete guidance.
- Tool-specific authorization and Query Builder recovery remain additive.
  `missingKeys` may derive only from a positively recognized summary/detail,
  never from raw unrecognized bodies.
- Unrecognized bodies use status-derived local text and never become client
  guidance. This intentionally stops echoing arbitrary non-auth JSON/plain
  bodies and is an approved safety difference, not a compatibility shim.

### 2026-08-14 — Bounds, filtering, and drift signal
- Parse once at the shared HTTP-client boundary so `HTTPStatusError.Error()`,
  client retry/terminal logs, tool results, resource errors, wrapper context,
  and partial-result notes share the same safe interpretation.
- Keep filtering deliberately narrow: bound every channel; accept only safe
  code/type tokens and absolute HTTP(S) documentation URLs without userinfo;
  drop an individual value containing a high-confidence credential assignment
  or token, control characters, active HTML, or active Markdown link/image.
  Do not substring-rewrite ordinary recovery prose or build a general secret
  scanner.
- Optional fields decode independently. A malformed optional sibling does not
  discard valid core fields or other guidance. Output counts are capped, but
  all supplied entries are inspected for wrong-type drift.
- Remove raw error-response bodies from retry/terminal log attributes. Emit at
  most one separate WARN per outbound request for an unrecognized
  `status:"error"` envelope or malformed recognized optional fields. The WARN
  carries only status, body size, and fixed field names—never upstream values.

### 2026-08-14 — Consumer and CMP-3 audit
- AI Assistant branches only on top-level `structuredContent.code`; direct
  calls retain full text and structure, while its agent proxy currently
  reduces structured errors to code-only but keeps the text. Therefore every
  new field must also be rendered in text, but no blocking Assistant change is
  needed.
- No companion `SigNoz/agent-skills` change is required: existing skills teach
  stable authentication/permission recovery and do not parse the upstream
  guidance fields. A future Assistant full-structure proxy improvement is a
  separate non-blocking consumer task.
- `README.md` needs one concise error-contract update. `manifest.json`, tool
  schemas/descriptions, success outputs, static resources, templates, prompts,
  server instructions, and normative ERR-6 text do not change.

### 2026-08-14 — Merged conformance bootstrap
- Manually dispatched the merged `protocol` workflow on `main`; Inspector and
  conformance both passed in run 31786456433.
- The active ruleset does not yet require `conformance`. Both available GitHub
  identities lack ruleset-admin permission, so the owner-only update is left
  explicit rather than silently broadening or replacing repository rules.

### 2026-08-14 — Live contract verification
- A delegated read-only staging probe exercised the current working-tree binary
  through legacy `2025-11-25` initialize/initialized and modern `2026-07-28`
  direct calls. A deterministic v5 Query Builder syntax error used the current
  nested renderer and was recognized without drift.
- Both eras returned the same `VALIDATION_FAILED` result with status 400,
  upstream code/type/exact summary, documentation URL, and one independent
  detail in structured content; text carried the same summary, detail, and URL.
  Staging emitted empty suggestions and no retry for this case, so the
  source-derived fixture remains the deterministic complete optional-field
  proof.
- No session header, credential-bearing log value, mutation, process, port, or
  temporary artifact remained after the probe.

### 2026-08-14 — Final two-model review decisions
- The requested Fable 5 high overengineering review found no P1 issue and
  confirmed the positive-recognition parser, value-free drift signal,
  body-free logging, source-derived fixture, and four-case dual-era wire test
  are justified rather than compatibility scaffolding.
- The requested Opus 5 xhigh exhaustive review found two P1 and two P2 issues.
  Source verification substantiated them: the initial markup/auth patterns
  suppressed real SigNoz placeholder and authentication guidance; fully
  filtered recognized 401/403 envelopes lost canonical recovery text; composed
  text lost its previous aggregate bound; and an oversized status-first
  renderer envelope skipped the dedicated drift WARN.
- The implementation now narrows filtering around high-confidence credentials
  and genuinely active markup, signals any non-empty value that cannot be
  preserved, reuses `HTTPStatusError.Error()` as the only text renderer,
  restores the aggregate text cap, and detects an oversized verified renderer
  prefix without parsing or trusting its values. Credential validation keeps
  its prior plain-error type.
- Rejected one proposed simplification: stripping all documentation URL query
  and fragment components would lose faithful benign guidance. The existing
  bounded URL parse and sensitive-component checks remain load-bearing.
- Rejected a separate drift classifier for every generic gateway/plain body:
  those failures already produce a value-free terminal WARN with URL, status,
  attempt, retryability, and body size. The dedicated envelope WARN remains for
  status-error contract drift and unsafe recognized fields, including transient
  retries.
- Correction to the earlier “parse once” shorthand: parsing policy is owned in
  one client package, but the immutable error value may be interpreted at the
  client, handler, and Query Builder boundaries. Memoizing error-path parsing
  would add state and complexity without changing the bounded contract.

## Open Questions
- [x] Which issues does this PR close? #191 and overlapping #164.
- [x] Which envelopes are trusted? Verified nested renderer generations and
  the verified legacy query-service pair only.
- [x] Which structured field names are used? `upstreamURL`,
  `upstreamSuggestions`, `upstreamDetails`, and `upstreamRetry`.
- [x] Is a general sanitizer or retry-safety framework required? No; use
  bounded whole-value filtering and preserve retry only as timing guidance.
- [x] Does CMP-3 require companion metadata or skills changes? No; update the
  README and record the additive consumer audit in the PR.
- [x] How is the external contract tested? Check in one source-derived,
  redacted canonical renderer fixture with exact source provenance, exercise
  it through the real client and registered MCP handler, and supplement it
  with a read-only live probe when staging can deterministically produce
  guidance. Do not mislabel the source-derived fixture as a recorded response.
