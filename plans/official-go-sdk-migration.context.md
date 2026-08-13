# Feature: Official Go SDK Migration — Context & Discussion

## Original Prompt
> Can we work on this? Create a plan first<br>
> https://github.com/SigNoz/nerve-pod/issues/194

## Reference Links
- [SigNoz/nerve-pod#194 — Migrate to the official Go SDK](https://github.com/SigNoz/nerve-pod/issues/194)
- [Official Go SDK v1.7.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- [Official Go SDK protocol guide at v1.7.0](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/docs/protocol.md)
- [MCP 2026-07-28 release notes](https://blog.modelcontextprotocol.io/posts/2026-07-28/)
- [Official MCP conformance framework](https://github.com/modelcontextprotocol/conformance)
- [mark3labs/mcp-go#928 — 2026-07-28 support](https://github.com/mark3labs/mcp-go/issues/928)
- [`docs/mcp-best-practices.md`](../docs/mcp-best-practices.md)
- [`guardrails/README.md`](../guardrails/README.md)
- [`plans/stateless-server.plan.md`](stateless-server.plan.md)
- [`plans/mcp-go-v0.56-upgrade.plan.md`](mcp-go-v0.56-upgrade.plan.md)
- [`plans/mcp-tier-2-protocol-client-lanes.plan.md`](mcp-tier-2-protocol-client-lanes.plan.md)

## Planning Baseline
- Date: 2026-08-12
- Local commit: `30acf69`
- Branch state: `main` matches `origin/main`; the only worktree entries are this untracked plan pair.
- Runtime today: Go `1.25.5`, `github.com/mark3labs/mcp-go v0.56.0`, 43 production tools, 22 resources, 2 resource templates, and 4 prompts.
- Target runtime: `github.com/modelcontextprotocol/go-sdk v1.7.0`, the stable release named by issue #194.

## Issue Scope Captured
- Preserve the complete client-visible tool/resource/prompt catalog and existing handler behavior.
- Serve legacy `2025-11-25` and modern `2026-07-28` clients on the same HTTP and stdio endpoints.
- Add `server/discover`, per-request metadata, and standardized MCP header validation without requiring modern clients to initialize.
- Preserve stateless HTTP, authentication/OAuth, request limits, readiness, cancellation, shutdown, coded tool errors, structured content, and observability.
- Replace mark3-specific hooks/tracing with official receiving middleware and existing HTTP OpenTelemetry instrumentation.
- Add official conformance coverage for both eras and representative-client verification.
- Do not introduce MRTR confirmations, Tasks, MCP Apps, new tools, schema changes, or SDK OAuth helpers as part of the migration.

## Key Decisions & Discussion Log

### 2026-08-12 — Planning-only boundary
- This exchange produces only this context file and the paired implementation plan. It does not change source code, dependencies, tests, CI, documentation outside `plans/`, commits, or pull requests.

### 2026-08-12 — Treat the work as a wire migration
- Replacing imports is the smallest part of this task. The SDKs differ in request representation, content pointer types, capability inference, lifecycle, transport behavior, middleware, pagination, panic handling, and shutdown return values.
- The migration is therefore gated on a pre-swap, canonical `2025-11-25` contract fixture and semantic parity tests. Existing manifest, inventory, budget, error, and normalization tests remain independent oracles; migration code must not update them to bless accidental drift.

### 2026-08-12 — Preserve `Handler.addTool` as the policy boundary
- All production tools continue through one checked path: normalize advertised schemas, compile input/output validators, decorate with fail-open validation, decorate coded errors, claim the registration, then call the official low-level `(*mcp.Server).AddTool`.
- The generic official `mcp.AddTool[In, Out]` is explicitly excluded. It rejects invalid input before the current handler, applies defaults, validates output, synthesizes structured/text content, and converts Go errors; those are client-visible policy changes.
- Official low-level registration still performs registration-time checks: input schema must be non-nil and object-rooted, schemas must be JSON-marshalable, and `x-mcp-header` annotations must be valid. A preflight must prove all 43 normalized production tools satisfy those checks. A failure blocks the swap; it must not drop a tool or move validation into the generic API.

### 2026-08-12 — Use a thin repository-owned tool contract adapter
- Official low-level handlers receive raw `json.RawMessage` arguments, while current handlers consume a decoded value plus exact `RawArguments` and call `GetArguments()`.
- Add only the narrow internal layer required to preserve that contract: a local tool request/handler type, exact raw-byte retention, and the currently used schema-builder subset. Do not clone the full old SDK API.
- The decode adapter must reproduce the current default `encoding/json` behavior, including float64 conversion/rounding for numeric literals. It must not globally enable `UseNumber`, because current handler parsing and the existing >2^53 characterization intentionally rely on string values as the exact-value escape hatch.
- Omitted or JSON `null` arguments remain safe for no-argument and required-argument handlers; raw non-object values, aliases, number/string unions, unknown fields, and nil cases get explicit adapter tests.

### 2026-08-12 — Freeze schemas, annotations, results, and coded errors on the wire
- Compare canonical JSON, not Go struct shapes. Official v1.7.0 serializes false-valued `readOnlyHint` and `idempotentHint`, while `destructiveHint` and `openWorldHint` are pointers; all existing hints must be explicitly reconstructed to reproduce current wire JSON.
- Preserve the exact duplicate-registration guard even though both SDKs otherwise replace duplicate names.
- Preserve exact raw dashboard schemas, typed schema generation, schema normalization, the output-schema allowlist, fail-open notices, structured result JSON/text duplication, and coded error recovery guidance.
- Ordinary tool failures remain successful JSON-RPC responses with `isError: true` and stable `structuredContent.code`; only protocol failures return Go/JSON-RPC errors.

### 2026-08-13 — Allow only unavoidable official response fields on legacy wire
- Official v1.7.0 unconditionally serializes `ttlMs: 0` and `cacheScope: "public"` on catalog lists, resource reads, and `server/discover`, including legacy responses. The SDK also adds `resultType` and server-info metadata to modern responses.
- Treat these SDK-owned cache hints as an explicitly allowlisted additive migration delta rather than inventing a protocol-specific response rewriter. `ttlMs: 0` is immediately stale and does not change cache behavior. Every existing payload field remains exact; no other legacy golden diff is accepted.

### 2026-08-12 — Make server defaults explicit
- Capture the current initialized capabilities before the swap. Official server defaults infer tools/resources/prompts with `listChanged: true` and enable logging when capabilities are nil; construct capabilities explicitly from the captured legacy baseline rather than accepting inference.
- Pin an explicit page size at or above the complete current catalog and assert empty `nextCursor`. The SDK default is currently 1000 and is sufficient for 43 tools/22 resources, but relying on it creates a future threshold regression.
- `server/discover` and the legacy initialize result must each be tested against their protocol-era requirements. Do not hand-roll `server/discover` to mask an SDK incompatibility.

### 2026-08-12 — Preserve transport and middleware ownership
- Keep the request path `otelhttp -> maxBytes -> auth -> official StreamableHTTPHandler`. Liveness/readiness/health and custom OAuth routes stay outside MCP auth exactly as today.
- Configure `StreamableHTTPOptions.Stateless: true`, `PropagateRequestCancellation: true`, the repository logger, and an explicit request limit adapter. Preserve current direct-config semantics where `MaxRequestBytes <= 0` disables the cap; official SDK zero means its default 4 MiB, so non-positive values must map to the SDK's negative disable value. Keep the outer limit for the current early `Content-Length` rejection and chunked-body protection; use the same mapped value inside the SDK and test that the two layers agree.
- Official stateless HTTP intentionally returns 405 for GET and DELETE. The current mark3 GET listener and heartbeat become obsolete because this server sends no server-to-client messages. Retire both intentionally and update architecture documentation and tests.
- Seed stdio API credentials, SigNoz URL, auth header name, and default caller source on the base context passed to `Server.Run`. Normalize `context.Canceled` to nil so graceful signal shutdown does not become process exit 1.
- Keep the custom OAuth product flow and challenges. Do not adopt official SDK OAuth helpers in this migration.

### 2026-08-12 — Replace hooks and tracer with one lifecycle owner
- Use official receiving middleware as the single SDK-dispatched method-lifecycle boundary. It extracts protocol version, client identity, client capabilities, headers, tenant/correlation context, and produces exactly one terminal method observation for success, handler/protocol error, panic, cancellation, timeout, and unknown registered targets.
- Some HTTP failures (oversize/auth, content negotiation, malformed bodies, unsupported headers, and standardized-header mismatches) are rejected before official receiving middleware. A narrow outer `/mcp` response observer must cover those pre-dispatch outcomes and coordinate with the receiving observer through a shared request-scoped exactly-once marker. Do not claim receiving middleware can see transport rejections that never dispatch.
- Stdio decoding, request-shape checks, per-request metadata validation, and removed-method gating also occur before receiving middleware. The official SDK exports `Transport`, `Connection`, and `jsonrpc` message types, so wrap the official stdio transport/connection: tag decoded requests with an internal observation token, correlate responses by request ID, and complete only requests the receiving middleware did not claim. Malformed bytes still have no trustworthy method and remain bounded transport errors; do not log raw frames or copy unexported SDK framing code.
- Keep a per-tool registration decorator for search context, tool name, result serialization safety, tool result classification, tool metrics/logging/analytics, fail-open schema validation, and coded-error enforcement.
- Add explicit panic recovery because official v1.7.0 has no equivalent of the current `WithRecovery`. Recovery must sit inside the lifecycle observer so every panic is classified and terminated exactly once.
- Preserve the outer `otelhttp` span and replace the mark3 tracer package rather than layering a second SDK tracer over it. Modern requests obtain client/protocol/capability attribution from every request; legacy requests fall back to initialize/session data.

### 2026-08-12 — Split production compatibility from fixture conformance
- The full official conformance requirements are not catalog-agnostic. They call fixed `test_*` tools, `test://` resources, and test prompts; the 2026 set also scores input-required/MRTR fixture scenarios. Running full requirements directly against the SigNoz catalog would fail for missing fixtures and baselining those failures would make a false conformance claim.
- The production gate therefore uses the real `cmd/server`, real SigNoz catalog, raw wire probes, and Inspector across the HTTP/stdio × legacy/modern matrix.
- A separate non-shipping conformance binary registers the official fixture catalog and exercises the same official SDK server factory, registration adapters, middleware, and transport assembly. Its legacy profile may be stateful where callback/subscription scenarios require it; its modern profile is stateless. MRTR fixture behavior exists only in that binary and is never advertised by production.
- Add dependency/catalog leakage guards so `cmd/server` cannot import fixture code and production lists cannot expose `test_*` or `test://` entries.

### 2026-08-12 — Pin independent protocol referees
- Keep the pinned Inspector harness as an independent initialized-client check over HTTP and stdio. Do not claim a modern Inspector cell unless a pinned release is first proven to support explicit `2026-07-28` selection and machine-readable output; raw Go wire tests own modern coverage meanwhile.
- The current stable conformance package does not contain the frozen `2026-07-28` requirements interface. The first applicable official package is currently a prerelease; pin its exact version and lockfile, and record the exception in the PR. Do not float a dist-tag.
- Official conformance does not replace focused header tests: its current server header-validation scenarios are pending/non-scored. Raw HTTP tests must assert `-32020`, handler non-entry, and no session header for protocol/method/tool-name mismatches.

### 2026-08-13 — Cold-review corrections
- The modern required-metadata matrix follows official v1.7.0 exactly: missing/invalid client capabilities is invalid params (`-32602`); `-32021` is reserved for a request whose declared capabilities do not satisfy a feature requirement; unsupported modern protocol is `-32022`.
- `server/discover` advertises the transport-filtered official SDK version set. Require at least `2025-11-25` and `2026-07-28` and pin the full v1.7.0 list in a source-verified test; do not falsely claim the product supports exactly two versions.
- Inspector owns independent legacy negotiation/catalog/invocation checks but does not expose raw response headers. Raw HTTP/stdio tests own modern lifecycle, `Mcp-Session-Id`, and standardized-header assertions.

### 2026-08-13 — PR #283 comparison and requested Fable/Opus reviews
- Compared [SigNoz/signoz-mcp-server#283](https://github.com/SigNoz/signoz-mcp-server/pull/283) at `cd188cb` with this plan, then ran independent read-only reviews with Claude Fable 5 at high effort and Claude Opus 5 at xhigh effort. Both returned `REVISE` and independently agreed on the main corrections below.
- Adopt PR #283's SDK-type-free raw-HTTP golden harness, production `registerHandlers` seam, HTTP status/framing capture, guarded regeneration command, and guardrail inventory entry. Extend that one harness rather than building a parallel semantic fixture/exporter.
- Reject PR #283's proposed removal of `capabilities.logging` and legacy `logging/setLevel`. Official v1.7.0 still advertises logging and implements `logging/setLevel` for legacy sessions; only modern requests reject it as a removed method. Explicit capabilities must preserve the current legacy initialize payload, including absent/false `listChanged` flags.
- Reject PR #283's `santhosh-tekuri/jsonschema/v6` removal and hand-rewrite of roughly 362 option-builder calls. The current validator's error text is client-visible in fail-open notices, and the broad schema rewrite creates avoidable catalog drift. Preserve the validator and implement only the small builder/request conversion needed by official low-level registration.
- Set `StreamableHTTPOptions.JSONResponse: true`. Official v1.7.0 otherwise defaults single POST responses to SSE, whereas the current endpoint and PR #283 goldens use `application/json` framing.
- Keep all client-visible discovery arrays in their current order. PR #283 sorts catalogs to normalize SDK differences; that would conceal the known resource-order delta (mark3 sorts resources by display name, official v1.7.0 by URI). If the official runtime changes the order, add a narrow legacy-compatible reorderer or stop; do not normalize the oracle to accept it.
- Preserve resource payloads exhaustively: freeze every field and byte for all 22 static reads (including a fixed docs-index sitemap), plus deterministic exemplars for both templates using a mock client. Official resource URI/MIME auto-fill must remain a no-op because every adapter result sets both explicitly. Cache hints are allowed only on the outer result, never within `contents`.
- Preserve all four prompt definitions and deterministic `prompts/get` messages, plus focused handler-contract cases for successful structured content, fail-open notice text, coded error envelopes, omitted/null arguments, and unknown targets. Existing SDK-coupled Go tests must be ported with assertions preserved; they cannot literally remain unchanged.
- Keep the pre-swap golden immutable during the migration. The post-swap comparison may ignore only the reviewed outer `ttlMs: 0` and `cacheScope: "public"` additions. Do not regenerate the old expected payload after the SDK swap or bless drift with a broad update.
- Accept official malformed-stdio behavior for this migration: a malformed frame terminates the one-client process instead of returning a parse error and continuing. Pin and document the delta; do not implement a custom JSON-RPC transport without evidence that a supported client depends on continuation.
- Simplify pre-dispatch observability: HTTP keeps the existing `otelhttp` span and bounded SDK diagnostics for requests rejected before receiving middleware; it does not synthesize MCP method/tool telemetry. For stdio, start with receiving middleware plus bounded SDK transport logging and a coarse connection wrapper only if a focused parity test proves a missing terminal signal; do not pre-commit to token injection, a request registry, or a CAS protocol.
- `PropagateRequestCancellation` only affects modern HTTP requests. Characterize legacy disconnect cancellation before the swap and record the loss explicitly if no official supported hook can preserve it; do not claim full cancellation parity without proof.
- Preserve current POST framing, DNS-rebinding behavior, unknown-resource `-32002` behavior, and graceful shutdown semantics explicitly. Prefer a repository-owned error adapter for unknown resources over the SDK's temporary `MCPGODEBUG=customresnotfounderrcode=1` switch, which is documented for removal.
- Keep official conformance as a separate, bounded follow-up commit/PR in the issue's delivery series. Run it against a non-shipping fixture because the full official requirements use fixed test surfaces; keep real production HTTP/stdio dual-era tests as the migration merge gate. Do not duplicate the SDK's own everything server—reuse the smallest official fixture implementation that exercises this repository's server/transport composition, or narrow the conformance claim if that cannot be done honestly.

### 2026-08-13 — Superseded observability and harness details
- The detailed 2026-08-12 stdio token/request-registry/CAS design above is superseded by the review outcome: measure first, use receiving middleware and bounded SDK logs, and add at most a coarse exported-interface connection decorator when a named parity test fails. No custom framing transport is planned.
- The proposed modern Inspector matrix is also superseded unless a pinned Inspector release proves an explicit modern-lifecycle control. Raw Go HTTP/stdio tests own `2026-07-28`; current Inspector remains an independent initialized-client lane.
- The main branch is now refreshed to `30acf69`; earlier discussion about three missing release commits is historical. `server.json` is already at v0.12.0 and needs only a final truthfulness audit.
- A final executable-plan gate clarified three adapter seams: restore current resource list order before serialization because official feature sets sort by URI; rewrite only official unknown-resource errors in receiving middleware because lookup fails before any resource handler; and wrap the SDK logger to bound/redact records and demote expected modern removed-method probes and graceful cancellation.

### 2026-08-13 — Client-visible contract freeze clarified
- The migration permits no changes to tool names, descriptions (including whitespace), input/output schemas, annotations, resource/template descriptors, static or template resource contents, prompt definitions/messages, or existing handler results/errors.
- Allowed differences are limited to protocol-owned behavior that cannot be represented identically by official v1.7.0: outer zero-TTL/public cache hints, modern-only result/server metadata, stateless GET/DELETE 405 with heartbeat removal, modern removal of deprecated methods, malformed-stdio connection termination, and any explicitly approved legacy disconnect-cancellation limitation. Each allowed delta needs a named test and documentation; it may not become a general golden-diff allowance.

### 2026-08-13 — User simplification decisions supersede strict legacy parity
- Do not preserve legacy `capabilities.logging` or promise `logging/setLevel` compatibility. Omit the capability, add no suppression/preservation shim or legacy parity test for the method, and keep only the test that proves modern requests reject it as the official SDK specifies.
- Do not preserve discovery-list ordering. Catalog entries and all entry fields remain frozen, but order is explicitly non-contractual. Canonical comparison may sort top-level tool/resource/template/prompt collections by their stable identity key; nested schema arrays, resource `contents`, and prompt messages retain order.
- Do not preserve the mark3-specific unknown-resource `-32002` code. Accept official v1.7.0's standards-aligned `-32602 Invalid Params` resource-not-found response and document it as an intentional error-contract difference.
- Keep a lean pre-swap characterization baseline, not an exhaustive wire archive. Fully freeze all 43 tool descriptions/input/output schemas/annotations and all resource/template/prompt descriptors. For resource and prompt payloads, use deterministic per-entry content digests plus size/type/URI/MIME metadata, with literal fixtures only for small or shape-sensitive cases (sitemap, both resource templates, representative prompts, structured/coded tool results). Any diff fails with a concise path-level report; the reviewer either fixes it or adds a narrowly named accepted-difference entry with rationale. No generic ignore list or duplicated giant payload corpus.
- A schema-library rewrite is allowed and preferred when it uses the official stack: represent advertised schemas as official `google/jsonschema-go` `*jsonschema.Schema` values (or `json.RawMessage` for schemas that cannot round-trip through the typed representation), resolve/validate with `jsonschema.Schema.Resolve`, and remove `santhosh-tekuri/jsonschema/v6` if focused replay tests prove the existing fail-open handler policy and advertised schemas remain intact. Do not use generic `mcp.AddTool[In, Out]`, apply defaults, or let SDK validation reject calls before handlers.
- Validation-library error text is not a stable client contract. Replace library-derived details with one exact repository-owned notice sentence and use deterministic `<root>` / `schema` telemetry metadata unless metadata can be derived independently of validator errors. Record this wording change in the accepted-differences table while retaining the notice prefix and best-effort execution.
- Official low-level registration and repository validation have different failure boundaries. SDK-invalid advertised schemas are registration-fatal; a syntactically valid object-rooted schema that official registration accepts but `Schema.Resolve` cannot compile still registers, emits the existing compile-failure signal, and runs without repository validation. Rewrite the synthetic fail-open test to exercise the latter.
- Before removing the old validator, dual-run both accepted and rejected examples for every validation-affecting keyword family used by the production schema inventory. Exposed schema JSON remains exact; any validation-classification delta needs a named review.
- The outer HTTP observer has an independent fixed metadata-capture ceiling when the configured request limit is unlimited. It stops decoding and records HTTP-only telemetry after that ceiling while allowing downstream handling to continue.
- The alert-template golden normalizes only runtime timestamps for literal comparison and separately asserts those timestamps match the captured request window and exact six-hour span.
- Delivery is one runtime migration PR plus at most one linked conformance follow-up. The runtime PR can merge after its own compatibility gates, but this plan stays `In Progress` and issue #194 remains open until conformance lands.
- These decisions remove three compatibility shims from the plan: no logging-capability shim, no catalog reorderer, and no unknown-resource error rewriter.

### 2026-08-12 — CMP-3 outcome
- The initial audit found no tool name, parameter, payload, or documented tool behavior change taught by `SigNoz/agent-skills`; the expected result is “CMP-3 audited, no companion change required.”
- Repeat the audit against the final branch diff. Any actual taught-contract change is a stop condition and requires a linked companion PR.

### 2026-08-13 — Comparative audit of github/github-mcp-server
- Audited `github/github-mcp-server` at commit `2198e8599bbbcb98a0d6cd7cabe9a48629acdf29` with three parallel read-only reviews covering architecture, contracts/tests, and transport/security/operations. The external repository is directly comparable because it uses official go-sdk v1.7.0 and `google/jsonschema-go` v0.4.3.
- Adopt copy-on-write registration transforms. GitHub clones shared tool/schema branches before adding registration metadata and race-tests that originals remain unchanged. SigNoz normalization/annotation conversion must likewise leave reusable definitions untouched across production, tests, and conformance, without introducing a generic deep-copy package.
- Align the direct `google/jsonschema-go` requirement from v0.4.2 to the v0.4.3 selected by go-sdk v1.7.0.
- Add exact parameterized `Content-Type` regression cases, following GitHub's test for an earlier official-SDK strict-media-type bug. Also add a small realistic `Accept` matrix as a separate SigNoz SDK-contract check; the audited GitHub commit sets the normal combined value but does not carry an equivalent matrix.
- Make the already-planned server factory explicitly own complete options, capabilities, middleware order, recovery, and registration for all transports/profiles. Split new migration code into focused files inside `internal/mcp-server` because the current `server.go` is 1,432 lines; do not add a public inventory framework or perform a separate file-move refactor.
- Keep the existing high-value choices already validated by the comparison: low-level registration with ordered decorators, explicit capabilities, direct official schema representation, immutable surface goldens, auth/body limits outside the SDK, a shared HTTP/stdio semantic matrix, explicit panic recovery, and bounded/redacted logging.
- Explicitly reject GitHub-specific machinery: per-request server/catalog construction, toolsets/read-only/feature filtering, shared schema cache for that model, typed/eager handler decoding, global order-insensitive snapshots, permissive CORS/cross-origin posture, raw stdio frame logging, plain text-only error helpers, and its legacy branch-diff script as a conformance oracle.
- Defer generated README sections, broader cross-platform CI, and release provenance work. They are useful follow-ups but do not justify expanding the runtime migration PR.

### 2026-08-13 — ERR-6 issues #191 and #164 sequencing
- Audited live [nerve-pod#191](https://github.com/SigNoz/nerve-pod/issues/191), [#164](https://github.com/SigNoz/nerve-pod/issues/164), and [#194](https://github.com/SigNoz/nerve-pod/issues/194), the current shared upstream-error implementation/tests, the verified SigNoz renderer envelope, and the history of PR #267 with parallel contract and delivery reviews.
- Do not implement #191 or #164 in the #194 runtime migration PR. #194's diagnostic strength comes from freezing the existing coded-error wire contract before swapping SDKs; ERR-6 intentionally changes that same text and structured payload with URL, top-level/detail suggestions, retry guidance, positive envelope recognition, filtering, fallback, and drift signals. Treating those as migration differences would make regressions hard to attribute and couple two independent revert boundaries.
- #191 is the substantive successor to #164: it contains #164's guidance-field requirements and adds the trust boundary, observability, and real-contract evidence needed to implement them safely. Deliver one post-migration ERR-6 PR against official result/content types and close/link both trackers from it rather than maintaining two implementations.
- Preferred order is #194 runtime migration, then #191/#164 ERR-6 follow-up; the ERR-6 PR may proceed before the separately allowed conformance follow-up. If ERR-6 becomes release-critical, make it a release-promotion gate, not part of #194's migration diff. The only safe alternative is a separate ERR-6 PR before Phase 0, followed by a fresh migration baseline; never land it after the baseline is frozen and before the SDK swap completes.
- The follow-up stays lean: shared parser/recognition and bounds tests, credential/markup and drift tests, composition with query-builder and authorization decorators, and one recognized plus one unrecognized wire case across legacy and modern HTTP. It deliberately updates its representative coded-error fixture as a product change and repeats README/error-contract and CMP-3 review.

### 2026-08-13 — ERR-6 timing fixed to post-migration merge
- The maintainer chose the single delivery order: merge the #194 runtime migration PR to `main` first, then implement #191 (covering #164) in its own official-SDK follow-up PR.
- This supersedes the earlier fallback option of landing ERR-6 before Phase 0. Do not begin the ERR-6 implementation on mark3, place it on the migration branch, or treat its client-visible changes as accepted migration differences.
- The ERR-6 follow-up remains outside #194's merge/completion criteria; it may be a separate release-promotion gate if required.

### 2026-08-13 — Sequential PRs instead of a stacked runtime series
- Do not use a phase-per-PR or feature-branch stack for #194. The SDK type boundary crosses tool registration, all handler families, resources, prompts, results, server transports, and observability; splitting adapters, tool families, transport, or recovery into independently merged PRs would require unused code, disabled behavior, or temporary dual-SDK compatibility machinery.
- Repository CI, guardrail, and protocol workflows currently trigger only for pull requests targeting `main`. The repository also allows only squash merge and deletes merged branches, so child PRs based on feature branches would lose the meaningful checks and require review-churning rebases after each parent squash.
- Use sequential `main`-based PRs: first strengthen and merge #283; then create one atomic production runtime migration PR from updated `main`; then, after that merges, create the bounded conformance follow-up from the new `main`. Logical commits inside the runtime PR provide review structure without inventing unsafe merge boundaries.
- Local runtime development may temporarily branch from #283 for parallelism, but no stacked child PR is opened. After #283 merges, rebase onto `main`, confirm the groundwork diff disappears, run all gates, and open against `main`.
- After the runtime merge, conformance and ERR-6 may proceed in parallel as independent branches from `main`. ERR-6 never depends on the conformance branch and neither is part of an unmerged runtime stack.

### 2026-08-13 — Closed PR #283 removed from delivery sequence
- The maintainer confirmed that #283 was a temporary PR and closed it. Live GitHub state is closed and unmerged at `cd188cb`; it is not a prerequisite, planned merge, stack base, or delivery unit.
- Supersede the preceding delivery wording that said to strengthen and merge #283. Create one runtime migration branch from refreshed `main`; its first green commit builds and captures the full SDK-free Phase 0 oracle under mark3, and later commits perform the official-SDK cutover without regenerating that baseline.
- #283 remains historical design input only for the raw-HTTP harness, registration seam, status/framing capture, and guardrail wiring. Re-evaluate and implement those ideas in the current plan; do not cherry-pick its incomplete discovery-only fixture set or obsolete plan decisions wholesale.
- The no-stacked-PR decision is unchanged: the runtime is one PR to `main`, followed after merge by independent conformance and ERR-6 branches from the new `main`.

### 2026-08-13 — Implementation started and PR review gate fixed
- Refreshed/pruned origin and confirmed local `main` at `30acf69` matched `origin/main` (`HEAD...origin/main = 0 0`), then created `codex/official-go-sdk-migration`. Phase 0 begins before any dependency change.
- The plan status is now `In Progress`. The complete SDK-independent compatibility oracle is the first green commit on this branch; its fixtures are frozen before any later commit changes `go.mod`.
- Before creating every PR in this delivery series, run Claude Fable 5 at high effort first for overengineering, then Claude Opus 5 at xhigh effort for exhaustive review. Record verified model/effort evidence, address accepted findings, and rerun the affected and full PR gates before publication. Missing or unverified requested-model review blocks PR creation.

### 2026-08-13 — Pre-migration wire oracle frozen
- Added the SDK-independent raw HTTP oracle through the real production handler and captured full discovery descriptors, all 22 static resource content inventories, both resource-template literals, all four prompt inventories, representative literal prompts, structured/fail-open/coded tool results, omitted/null arguments, and unknown targets.
- Kept the freeze lean: the complete 43-tool descriptor/schema catalog is literal because every exposed field must remain exact; large deterministic resource and prompt bodies use per-content type/URI/MIME/size/digest records, with literal fixtures only where shape or raw bytes matter.
- Regeneration is allowed only while mark3 is the sole direct MCP SDK requirement. Adding the official SDK as a direct requirement closes the update path even during a temporary dual-SDK compile stage, preventing the migration from blessing its own drift.
- Focused oracle tests, the full `TestGuardrail_*` inventory, repeated read-only comparison, JSON fixture parsing, formatting, and `git diff --check` passed before the dependency swap.

### 2026-08-13 — Runtime documentation and protocol smoke aligned
- Updated README, architecture, MCP best practices, and guardrail guidance for official Go SDK v1.7.0, the legacy `2025-11-25` and modern `2026-07-28` lifecycles, stateless JSON POST, absent session headers, GET/DELETE 405, heartbeat removal, and the narrowly accepted protocol-owned differences.
- Kept `manifest.json` and `server.json` unchanged after auditing them: the migration does not change their tool/resource catalog or package transport metadata. CMP-3 remains “audited, no companion change required” because no tool name, parameter, payload, description, resource content, or prompt contract changed.
- Removed the Inspector logging capability/setLevel assertions. Added only a bounded real-binary stdio smoke for legacy initialize/list and modern discover/direct-list, including graceful SIGTERM; focused Go tests remain the authoritative 2x2 wire and header matrices.

### 2026-08-13 — Runtime cutover accepted standard unknown-target wording
- The official v1.7.0 runtime returns `-32602` with `unknown tool "<name>"` and
  `unknown prompt "<name>"`; mark3 returned the same code with package-specific
  wording. We accepted the official messages rather than add two response
  rewriting shims. The frozen oracle retains the old fixtures, normalizes only
  these exact probe responses for comparison, and separately asserts the exact
  official code/message. Tool definitions, schemas, descriptions, resource
  contents, prompt definitions/messages, coded tool results, and successful
  handler responses remain unchanged.

### 2026-08-13 — Runtime safety audit removed transport-parser duplication
- A read-only runtime audit found that a planned outer body-tee observer would
  duplicate official v1.7.0 request parsing and require cross-layer
  deduplication state solely to synthesize MCP metrics for requests the SDK did
  not dispatch. Removed that observer from the plan. Dispatched requests retain
  exactly-once method/tool telemetry; pre-dispatch protocol/header/media/body
  rejections retain the outer HTTP span/status and bounded SDK diagnostics and
  intentionally emit zero MCP method/tool metrics.
- Resolved legacy HTTP disconnect cancellation as an accepted capacity-impact
  limitation: official `PropagateRequestCancellation` applies to the
  `2026-07-28` request lifecycle only. Legacy clients retain protocol
  `notifications/cancelled`; we will not replace the SDK transport to make an
  ephemeral stateless legacy POST cancellable after its carrier disconnects.

### 2026-08-13 — Per-request telemetry attribution restored
- A targeted comparison against the pre-migration telemetry contract found that
  the new receiving middleware was not reading the official request accessors.
  Dispatched spans now carry protocol version, bounded client name/version, and
  fixed boolean capability-presence fields; none are metric dimensions.
- Successful modern tool/prompt/resource analytics reuse those per-request
  protocol/client fields. `MCP Client: Initialized` remains a legacy initialize
  event; `server/discover` is optional and does not generate a misleading
  initialization event. Stateless legacy HTTP can retain the protocol header on
  each POST but cannot carry initialize-only client identity into later POSTs;
  legacy stdio retains the SDK session fallback.

### 2026-08-13 — Simplification review removed dead compatibility work
- The three-pass reuse/quality/efficiency review found that the local tool
  request deep-cloned official client metadata, headers, token information, and
  `_meta` even though no business handler consumed them. Removed those fields
  and clones; receiving middleware reads protocol/client/capability data from
  the official request where it belongs.
- Tool arguments are now decoded once per dispatched call and shared through
  request context by search-context extraction, the local adapter, validation,
  and handlers. Exact raw bytes remain available for the frozen wire contract.
- Removed schema-reference traversal that only fed constant `<root>` / `schema`
  validation labels. Kept the same fail-open notice and telemetry values.
- Added the remaining lean review gates: unknown-name cardinality classification
  must use repository registration state rather than official error wording,
  and one real registered-tool pipeline test must cover lifecycle composition.

### 2026-08-13 — Final audit closed contract and security gaps
- Tightened every accepted-difference assertion before normalizing to the frozen
  mark3 oracle: exact validation-notice wording, cache metadata on every
  cacheable catalog/read method, exact unknown-resource message/data, and the
  approved legacy HTTP disconnect limitation are now explicit.
- Added standard-library cross-origin protection around `/mcp`. This follows the
  official v1.7.0 recommendation and complements, rather than replaces, its
  localhost Host/DNS-rebinding check. Same-origin and non-browser requests pass;
  cross-origin browser POSTs fail before auth or dispatch.
- Restored span-only `mcp.search_context` after the shared one-pass argument
  decode, while retaining its exclusion from metric dimensions.
- Expanded the shared IOTransport matrix for both eras to cover all catalogs and
  one deterministic tool call, resource read, and prompt get. The real-binary
  smoke remains deliberately thin and validates framing, lifecycle, and signal
  shutdown rather than duplicating the deterministic in-process matrix.

### 2026-08-13 — Pre-publication gates and requested-review blocker
- Full tests, focused race tests, guardrails, vet, build, formatting/imports,
  shell syntax/lint, JSON metadata parsing, diff checks, and an isolated clean
  module-cache `go mod verify` passed. Agent CI completed `protocol / inspector`
  and contract guardrails; its remaining jobs did not start because the local
  runner lacks repository Primus, Docker Hub, GoReleaser, and GitHub secrets.
- Invoked Claude Code 2.1.229 with exact model `claude-fable-5`, effort `high`,
  read-only plan mode, and no fallback. The response metadata confirmed
  canonical model `claude-fable-5`, but the service returned HTTP 429 / session
  limit with reset at 23:30 Asia/Calcutta before producing review findings.
  This is not counted as the required review and blocks PR creation. Do not
  substitute another model; retry Fable after reset, then run Opus 5 xhigh.
- While publication was blocked, three additional read-only contract,
  security/runtime, and consistency audits plus an ultra-effort final review
  found and closed the Origin, span attribution, accepted-difference, and stdio
  matrix gaps. These are supplementary and do not replace Fable or Opus.

### 2026-08-13 — Maintainer-authorized Grok 4.6 publication review
- The maintainer explicitly replaced the unavailable Fable/Opus publication
  gate with one Grok 4.6 subagent review for this runtime PR; this is an
  intentional substitution, not a silent model fallback.
- Ran the complete committed range `30acf69..HEAD` through exact model
  `cursor-grok-4.6-xhigh` in read-only plan mode. It reviewed overengineering,
  SDK/runtime semantics, preserved catalogs and content, auth/security,
  cancellation, panic recovery, observability, dual-era transports, tests,
  docs, and accepted differences. Result: no verified P1/P2 findings.
- Review evidence: `model=cursor-grok-4.6-xhigh`, `mode=plan/read-only`,
  `scope=30acf69..HEAD`. Its only residual notes—blank URI/MIME adapter failure
  coverage, concurrent-registration race coverage, and the intentionally
  tested `tools/call` method-metric exclusion—were not demonstrated production
  defects and require no migration workaround.

### 2026-08-13 — PR lint follow-up
- GitHub `lint / go` exposed nine mechanical findings not covered by the local
  pre-publication commands: four unchecked test-session cleanup errors, three
  deprecated-capability reads, one switch simplification, and one dead test
  helper. Build, test, format, dependency, contract, and Inspector checks were
  already green.
- Explicitly ignored cleanup-only close errors, used active elicitation for the
  request-isolation test, simplified the test switch, and removed the unused
  helper. Kept legacy roots/sampling span attribution with two line-scoped
  `staticcheck` suppressions because official v1.7.0 still exposes those
  deprecated capabilities during its compatibility window. No runtime contract
  or client-visible surface changed, so the completed Grok review remains
  applicable.

### 2026-08-13 — Credential-safe staging E2E
- Extracted only the staging base URL and bearer credential from the supplied
  browser curl; cookies, browser fingerprint headers, and Sentry/PostHog tracing
  data were unnecessary. The credential stayed transient and was never printed
  or written to a repository file.
- Ran 35 selected read-only build-tagged staging tests: 35 passed, zero failed,
  and zero skipped. Explicitly excluded notification-channel send/lifecycle,
  mutation structured-content, and saved-view CRUD tests.
- Built the PR binary and ran the HTTP/stdio x legacy `2025-11-25`/modern
  `2026-07-28` matrix against staging. Every cell observed 43 tools, 22 static
  resources, two resource templates, and four prompts; preserved a coded
  `VALIDATION_FAILED` result; completed a live read-only
  `signoz_list_services` call; and shut down cleanly.
- Both HTTP eras returned no `Mcp-Session-Id`; GET and DELETE returned 405 with
  `Allow: POST`. Legacy used initialize/initialized, while modern used
  `server/discover` and direct requests with the required standard headers.
  No tenant resources were created, so no cleanup was required.
- A full browser OAuth success test cannot reuse the supplied user-session JWT:
  the repository OAuth form validates a service-account API key through
  `SIGNOZ-API-KEY`. A temporary staging service account was created without a
  role or key, and work paused at the required confirmation boundary before
  assigning viewer access or creating/revoking credentials.

### 2026-08-13 — Staging mutation and cleanup authorization
- The maintainer explicitly authorized broader staging E2E, including temporary
  resource and credential creation, provided every temporary resource is
  deleted and verified absent afterward. Resume the paused OAuth lifecycle with
  the built-in viewer role and shortest-practical key lifetime; keep all other
  client checks read-only unless a temporary artifact materially improves the
  test, and apply the same verified-cleanup rule.

### 2026-08-13 — Live Assistant OAuth regression and fix
- Full browser DCR, PKCE, consent, authorization-code exchange, refresh, bearer
  challenges, and live MCP calls passed against the built PR server, but the
  actual SigNoz AI Assistant consumer initially exposed a release blocker. It
  always sends the server-issued OAuth bearer together with `X-SigNoz-URL`; the
  middleware used the presence of that header to skip OAuth decryption and
  forwarded the encrypted token upstream as a direct bearer, yielding coded
  `UNAUTHORIZED`.
- Changed only credential precedence: with OAuth enabled, first attempt to
  decrypt every Authorization bearer. A valid token's encrypted API key and
  tenant are authoritative and auxiliary custom-URL metadata is ignored; an
  expired server token always returns the OAuth 401 challenge. Only a bearer
  that is not a server token retains the existing direct-Authorization fallback
  through `X-SigNoz-URL` or configured URL. This removes tenant-override and
  expiry-downgrade ambiguity without adding a compatibility shim or new wire
  error.
- Regression tests cover valid OAuth with absent, matching, conflicting, and
  malformed custom URLs, plus expired OAuth with a conflicting header. Existing
  tables retain opaque/JWT direct bearer, configured URL, OAuth-disabled, URL
  validation, and allowlist behavior. Focused normal/race tests, full MCP/OAuth
  package tests, full repository tests, vet, and build passed.
- The rebuilt live rerun passed OAuth access and refreshed-token calls with the
  Assistant header set. The real Assistant `McpToolClient` saw all 43 tools,
  completed one live read-only services call, returned coded
  `VALIDATION_FAILED` for an empty dashboard request, and emitted exactly one
  correctly correlated terminal record for each call. Eight concrete
  credential canaries were absent from bounded server logs.
- Cleanup was verified: the temporary key disappeared and returned 401 from the
  service-account identity endpoint; the temporary account became DELETED
  (staging retains its audit row but it is not ACTIVE); previously issued OAuth
  tokens then produced coded upstream `UNAUTHORIZED`. Local callback/server
  listeners, binaries, logs, harnesses, and in-memory browser secrets were
  removed.

### 2026-08-13 — Fable 5 high overengineering review
- After the model limit reset, the maintainer restored the original review
  sequence and capped identical review scopes at two models: Fable 5 high first
  for overengineering, then Opus 5 xhigh for exhaustive correctness/security.
  Grok 4.6 remains available only for a distinct targeted question, not as a
  duplicate third opinion.
- Exact `claude-fable-5-high` reviewed the complete branch plus the live OAuth
  fix in read-only plan mode. It found no unjustified major component: the
  43-tool fixture dominates additions by design, production Go remains roughly
  size-neutral, and the narrow `internal/mcpcontract` adapter avoids a much
  riskier rewrite of every tool definition.
- Accepted its concrete simplifications: removed the permanently closed
  wire-oracle regeneration flag, Go-module parser, and parser test; removed two
  unused/test-only contract exports and stale raw-schema comments; deleted a
  redundant pure log-level table and no-op/dead test scaffolding; narrowed the
  validation telemetry table to its two unique mismatch cases; reused existing
  error classification; avoided one raw-argument copy; and corrected the plan's
  cross-origin middleware order. The branch additions fell from 16,215 to
  16,069 before final formatting/gates, without deleting contract fixtures or
  weakening a client-visible assertion.
- Rejected suggestions that would trade away named invariants: exact raw
  argument bytes remain exposed to handlers, the outer body-limit layer remains
  for pre-auth declared-length rejection and middleware-order parity, literal
  prompt fixtures remain readable shape evidence, and the explicit OAuth-token
  early return remains a clear tenant-authority boundary.

### 2026-08-14 — Opus 5 xhigh exhaustive review
- Exact `claude-opus-5-thinking-xhigh` reviewed the post-Fable complete branch
  in read-only plan mode. Per the maintainer's two-model cap, this was the final
  review of the runtime PR surface; no Grok review was added for the same work.
- It found two migration-caused P1 observability regressions hidden by
  synthetic tests. Legacy initialize analytics switched on the client-side
  `mcp.InitializeRequest`, while official v1.7.0 dispatches a server-side
  `ServerRequest[*InitializeParams]`. HTTP failure logs serialized the whole
  server request, whose transport `Extra` contains a callback and cannot be
  marshaled, so `mcp.request` became `<unmarshalable>`.
- Fixed initialize analytics by dispatch method plus `request.GetParams()`, and
  added a real HTTP initialize test asserting the client/protocol/correlation
  event. Failure logs now serialize only `CallToolParamsRaw`, which preserves
  name/arguments while structurally excluding headers, token info, and the SDK
  callback. Real HTTP coded-error and Go-error tests assert useful content,
  argument redaction, no header/cookie leakage, and no sentinel fallback.
- Added a focused OAuth-token allowlist test: an allowed caller header cannot
  override a disallowed tenant encrypted in the server-issued token. Updated
  README validation-notice wording, official stdio/middleware/cache docs, the
  long-running POST timeout rationale, and stale plan commands/filenames.
- Repeated the CMP-3 search across `SigNoz/agent-skills` and
  `signoz-ai-assistant`; neither teaches or parses the old validator-detail
  notice wording, so no companion skills change is required.
- Opus also identified pre-existing OAuth hardening opportunities (registration
  body bounds, broader private-address rejection, single-use codes, and refresh
  rotation). They are not caused by this SDK migration and were not folded into
  its scope; track them independently rather than obscuring this PR's runtime
  revert boundary.

### 2026-08-14 — Final post-review verification
- Reran formatting, the guardrail suite, the full repository suite, focused
  race tests, vet, build, module tidy/verification with a clean module cache,
  exact `golangci-lint v2.12.2`, shell/JSON/workflow checks, and the complete
  real-binary protocol script after applying the Fable/Opus findings. All
  executable repository checks passed.
- Agent CI independently completed the contract and Inspector workflows. Its
  five remaining `ci.yaml` jobs could not start locally because the runner does
  not have repository Primus and Docker Hub credentials; those were environment
  setup blockers, not test or lint failures. The equivalent commands passed
  directly, and the pushed commit's GitHub checks were already green before
  this final patch.
- Added one production-stack order assertion: a declared oversized `/mcp`
  request returns 413 before authentication, while an under-limit request with
  no credential returns 401. This proves the retained outer limiter's purpose
  without adding an injectable middleware framework.
- Final branch accounting is 16,377 additions and 3,635 deletions: production
  code is net -7 lines, tests plus immutable fixtures are net +11,304, and
  plans/docs/CI are net +1,445. The 8,997-line exact 43-tool catalog fixture is
  the majority of the apparent growth and is intentionally readable JSON.

### 2026-08-14 — Maintainer-requested final Opus/Grok review gate
- The maintainer changed the publication gate after the first ready transition:
  return PR #286 to draft, run Opus 5 xhigh and Grok 4.6 in parallel over the
  final pushed commit, resolve their findings, then make the PR ready and start
  the conformance work as a stacked PR. This supersedes the earlier no-stacking
  delivery decision for the immediate conformance successor.
- Exact `claude-opus-5-thinking-xhigh` and `cursor-grok-4.6-xhigh` ran in
  read-only plan mode against `30acf69..06f9224`. Opus reported no
  migration-caused P1/P2 findings. Grok found one P2 test-oracle hole: the raw
  HTTP fail-open capture normalized the new validation notice without first
  asserting its exact live wording, so a regression to old validator detail
  could pass through another same-SDK test.
- Fixed the P2 by asserting exactly one live HTTP notice with the complete
  deterministic repository-owned sentence and no old `jsonschema validation
  failed` detail before golden normalization. Kept the frozen pre-migration
  fixture unchanged.
- Accepted only the reviewers' lean P3 corrections: close the descriptor
  parity test's dropped-last false-pass/addition panic; exercise the production
  stdio cancellation normalization through one private transport helper; pin
  the SDK logger's two exact demotions and near-match behavior; and classify an
  official unknown-tool `-32602` as `invalid_params` rather than `internal` in
  bounded tool telemetry.
- Deferred broader pre-dispatch zero-metric enumeration, per-property schema
  revalidation for validation-path diagnostics, stale historical-plan wording,
  and pre-existing OAuth hardening. None is a migration blocker, and adding
  those mechanisms here would expand scope beyond the verified findings.

### 2026-08-14 — Restore actionable fail-open validation guidance
- The maintainer found that `inputValidationNoticePrefix` had become a
  test-only sentinel while production ignored the validation error and emitted
  one generic sentence. This was safe but no longer told agents which argument
  to repair, so the earlier deferral of parameter attribution is superseded.
- A focused Opus 5 xhigh review confirmed the lean safe design: compile one
  repository-owned diagnostic probe per tool from the advertised top-level
  properties, required fields, and local definitions. On the mismatch path,
  validate only declared properties and derive missing required fields without
  parsing validator-library error strings. Parameter names are schema-owned;
  client keys, values, schema URIs, and validator text never enter the notice
  or metric dimensions. Complex root-only failures retain the generic fallback.
- The notice now uses `inputValidationNoticePrefix`, names one or more safely
  attributed top-level parameters, and preserves best-effort execution.
  Telemetry uses the parameter name for one mismatch, `<multiple>` for several,
  and `<root>` when attribution is unavailable. The frozen pre-migration wire
  fixture remains unchanged; only the exact accepted new-side assertion moves.
- Repeated CMP-3 search across `SigNoz/agent-skills` and
  `signoz-ai-assistant`; neither teaches nor parses validation-notice wording,
  so no companion change is required. Tool names, schemas, descriptions,
  annotations, resources, templates, prompts, and structured results remain
  unchanged.

### 2026-08-14 — Stacked conformance audit and STOP condition
- After PR #286 returned to ready with seven green checks, created
  `codex/official-go-sdk-conformance` at exact parent `0e33180` as requested.
  No conformance PR has been opened yet; its eventual base will be the runtime
  branch while stacked, then `main` after #286 merges.
- Verified current official sources and npm metadata. Stable/latest remains
  `0.1.16`; exact prerelease `0.2.0-alpha.11` is the only current package with
  frozen `--requirements 2025-11-25` and `--requirements 2026-07-28`, and one
  alpha.11 pin can run both eras. The package was published from git commit
  `c321dd32035556e6769d3724a8ee97d87c3faaac`.
- Added that exact package to `tools/mcp-ci`. Its initial compatible dependency
  resolution contained four dev-only advisories; kept alpha.11 fixed and moved
  only transitive packages within their declared ranges. A clean `npm ci`,
  both requirement-list commands, and `npm audit` now pass with zero findings.
- Enumerated the requirement sets: the server leg scores 30 legacy scenarios
  and 37 modern scenarios. Full coverage requires nearly all behavior in the
  official Go SDK's 1,316-line everything server, including media/mixed
  content, completion, logging/progress, sampling/elicitation, subscriptions,
  four fixture prompts, and fourteen MRTR scenarios. A supposedly minimal
  local fixture would therefore be a large upstream copy and mostly retest SDK
  code; the plan's explicit STOP condition is satisfied.
- Ran a no-write production-binary spike with fake tenant credentials. Six
  catalog-independent legacy scenarios and six modern scenarios passed with
  wire-schema checks. Modern `server-stateless` passed 24 of 28 scored checks;
  the four remaining checks were untestable solely because production omits
  three named diagnostic `test_*` tools, as intended by issue #194's out-of-
  scope product-feature rule.
- The next implementation decision materially changes the claim: either add a
  lean selected-scenario production lane and revise #194's full-suite
  acceptance wording, or accept a large nonshipping fixture to satisfy all
  frozen requirements. Do not silently pick one or disguise missing fixture
  behavior with a broad baseline.

## Open Questions
- [ ] Which honest conformance claim should the stacked PR ship? Recommended:
  selected catalog-independent official scenarios against the actual
  production binary, with #194's full-suite acceptance wording revised. The
  alternative is a large nonshipping fixture recreating nearly all of the
  official 1,316-line everything server to pass all 30/37 scored scenarios.
- [ ] Is an exactly pinned prerelease `@modelcontextprotocol/conformance` dependency acceptable as a release-blocking referee until its `0.2.x` line becomes stable? Recommended: yes; use its frozen `--requirements` sets, document the exception, and upgrade deliberately when stable.
- [ ] Should protected SigNoz AI Assistant/Claude Code/Codex/Cursor smokes block merge or block only release promotion? Recommended: keep deterministic raw/Inspector/conformance checks merge-blocking and make credentialed native-client checks protected pre-release gates with an explicit owner.
- [x] Can legacy HTTP disconnect cancellation be preserved through an official supported hook? Resolved: not in v1.7.0 stateless HTTP. Accept the bounded capacity-impact limitation, retain protocol cancellation notifications, and avoid a custom transport.
- [x] Use low-level or typed official tool registration? Resolved: low-level only for the migration.
- [x] Preserve or retire the stateless GET heartbeat listener? Resolved: retire it; official stateless GET/DELETE return 405 and the product has no server-message use case.
- [x] Run the full official requirements against the production catalog? Resolved: no; use a test-only fixture server plus a separate real-production compatibility matrix.
- [x] Preserve legacy logging? Resolved: no; omit `capabilities.logging`, add no shim or parity assertion for legacy `logging/setLevel`, and let the official SDK reject it for modern requests.
- [x] Preserve catalog order? Resolved: no; compare top-level discovery collections order-insensitively while retaining all fields and nested order.
- [x] Preserve mark3's unknown-resource `-32002`? Resolved: no; accept official `-32602 Invalid Params` and document it.
- [x] Keep the old schema validator? Resolved: not required; prefer official `google/jsonschema-go` if parity/replay gates pass, while keeping low-level tool registration and fail-open handler policy.
- [x] Preserve JSON or accept default SSE POST framing? Resolved: preserve JSON with `JSONResponse: true`.
- [x] Hand-roll a tolerant stdio transport? Resolved: no; accept and document official malformed-frame termination unless a real supported client proves continuation is required.
- [x] Replace the custom OAuth flow with SDK helpers? Resolved: no; explicitly out of scope.
- [x] Does `SigNoz/agent-skills` require a planned companion change? Resolved for the intended design: no, subject to a repeat audit of the implementation diff.
- [x] Include nerve-pod#191/#164 in the SDK migration PR? Resolved: no. Merge the #194 runtime migration PR first, then implement #191 as the complete successor scope for #164 in one independent official-SDK follow-up.
- [x] Use stacked PRs for the SDK migration? Resolved by latest maintainer
  direction: keep runtime PR #286 atomic against `main`, then temporarily stack
  the conformance successor on its ready branch and rebase/retarget after #286
  merges. Keep ERR-6 independent from `main`. Closed, unmerged #283 is
  reference material only.
