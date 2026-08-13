# Plan: Official Go SDK Migration

## Status
In Progress

## Planning Baseline
- Priority: P1
- Estimated effort: Large, multi-day delivery series: one atomic runtime migration PR (including the pre-swap oracle as its first green commit) and one bounded post-merge conformance PR
- Risk: High; this changes the protocol runtime beneath every client-visible surface
- Planned from: local `30acf69` on 2026-08-13; `main` matched `origin/main`
- Implementation branch: `codex/official-go-sdk-migration`, created from `30acf69` after confirming `HEAD...origin/main` was `0 0`
- Target: `github.com/modelcontextprotocol/go-sdk v1.7.0`
- Issue: [SigNoz/nerve-pod#194](https://github.com/SigNoz/nerve-pod/issues/194)

Before implementation, refresh the branch and check the exact migration surfaces for drift:

```bash
git fetch origin
git rev-list --left-right --count HEAD...origin/main
git diff --name-status 30acf69..origin/main -- \
  go.mod go.sum internal/mcp-server internal/handler/tools internal/docs \
  pkg/otel pkg/prompts pkg/toolerrors scripts tools/mcp-ci \
  .github/workflows guardrails docs README.md manifest.json server.json
```

If those paths changed, update this plan first and append the reason to the context log. Rebase or otherwise refresh according to the repository's normal branch workflow; do not implement on the stale baseline.

## Context
The current server uses `github.com/mark3labs/mcp-go v0.56.0`, whose wire lifecycle ends at `2025-11-25`. Issue #194 requires the official Go SDK so the same HTTP and stdio endpoints support both legacy initialization and the sessionless `2026-07-28` lifecycle (`server/discover`, per-request `_meta`, and standardized headers).

The repository already has a deliberately non-default contract:

- 43 tools pass through `Handler.addTool`, which normalizes the advertised schema, compiles input/output schemas, validates fail-open, adds a corrective notice to successful mismatched calls, preserves coded tool errors, and rejects duplicate registrations.
- Handler arguments use both the decoded JSON tree and exact raw bytes. Existing parsing intentionally accepts flexible string/number forms.
- Tool results duplicate stable structured JSON into text for older clients and preserve coded recovery envelopes.
- HTTP owns authentication, OAuth, a request-size limit, readiness, cancellation, shutdown, tenant context, and root OTel spans outside the MCP SDK.
- Mark3 hooks, tool middleware, and tracer code jointly produce method/tool metrics, spans, logs, analytics, and cancellation fallbacks.

Official v1.7.0 differs at each of those seams. This plan freezes the old wire contract first, creates only the thin adapters needed to retain repository policy, then swaps the runtime and proves both eras independently.

## Goals
- Preserve every production tool/resource/template/prompt name and every entry field that models or clients use: descriptions (including whitespace), input/output schemas, annotations, resource contents, prompt messages, structured results, and coded tool errors. Top-level discovery ordering is not part of this contract.
- Freeze this surface with compact, reviewable fixtures: complete catalog JSON for descriptors/schemas, per-entry content digests and metadata for all deterministic resource/prompt payloads, and literal fixtures for small or shape-sensitive results.
- Support legacy `2025-11-25` initialize/list/call/read/get and modern `2026-07-28` discover/direct-call flows over HTTP and stdio.
- Preserve direct API-key and OAuth auth branches, middleware order, request limits, readiness, cancellation, shutdown, tenant/correlation attribution, and exactly-once observability.
- Add deterministic production compatibility and official conformance gates without exposing conformance-only product behavior.
- Keep README, architecture, manifest metadata, guardrails, and CMP-3 documentation truthful.

## Non-goals
- Deliberate product-level additions, removals, or renames to tools, resources, templates, prompts, parameters, schemas, or result shapes. The small protocol-owned response differences are enumerated below and nowhere else.
- Implementing the client-visible ERR-6 guidance expansion tracked by [nerve-pod#191](https://github.com/SigNoz/nerve-pod/issues/191) and its earlier overlapping tracker [#164](https://github.com/SigNoz/nerve-pod/issues/164). Migrate the current coded-error contract unchanged. Only after the #194 runtime migration PR has merged to `main`, implement #191 against the official SDK in a dedicated follow-up PR; do not add it to `acceptedMigrationDifferences`.
- Generic/typed official tool registration, SDK-owned input/output validation, or default application.
- Compatibility shims solely for legacy logging, catalog ordering, or mark3's custom unknown-resource error code.
- MRTR confirmations, input-required product flows, Tasks, MCP Apps, sampling, elicitation, resource subscriptions, or new server-to-client traffic in production.
- Replacing the custom OAuth product flow with official SDK OAuth helpers.
- Refactoring unrelated handler/business logic or changing upstream SigNoz API behavior.
- Treating fixture-only conformance features as part of the production contract.

## Invariants
1. `Handler.addTool` remains the only production tool registration and policy path.
2. The official generic `mcp.AddTool[In, Out]` is never used for production tools.
3. Schema mismatch remains fail-open and detectable; ordinary tool errors remain coded `isError` results, not JSON-RPC errors.
4. The real production catalog remains exactly 43 tools, 22 resources, 2 templates, and 4 prompts unless baseline refresh reveals an intentional upstream change.
5. The pre-swap characterization baseline is immutable during the SDK migration. Every delta must be either fixed or recorded in a small, path-specific accepted-differences table with rationale and a regression assertion; there is no broad ignore list.
6. HTTP request order is `otelhttp -> mux -> cross-origin protection -> maxBytes -> auth -> official MCP handler`; `maxBytes` remains the authoritative body limit.
7. Production HTTP is stateless, emits no `Mcp-Session-Id`, requires no sticky routing, and preserves current JSON POST framing with `JSONResponse: true`.
8. The migration intentionally omits `capabilities.logging`, does not guarantee or actively suppress legacy `logging/setLevel`, accepts official discovery ordering, and uses official `-32602` resource-not-found semantics. These decisions are documented rather than hidden behind compatibility shims.
9. Every request that reaches official SDK dispatch produces one terminal method observation and, for `tools/call`, one terminal tool observation across success/error/cancellation/panic paths. Pre-dispatch transport/lifecycle rejections retain the outer HTTP span/status and bounded SDK diagnostics without fabricated MCP metrics; stdio parity is measured before adding fallback machinery.
10. Conformance fixtures never enter the production dependency graph, discovery results, README, manifest, or guardrail inventory.

## Delivery Plan

### Phase 0 — Refresh and freeze the pre-migration contract

Implementation checkpoint: complete on 2026-08-13. The frozen oracle was
captured and verified on mark3 before any direct official-SDK dependency was
added. The production branch now contains no regeneration path; the oracle is
compare-only.

#### 0.1 Refresh and inventory
- Refresh from `origin/main`, record the implementation-base SHA in both plan files, and rerun the scoped drift command above.
- Record exact production counts and names for tools, resources, resource templates, and prompts.
- Record current `initialize` capabilities, server information, instructions, lack of session header, one-page list behavior, and current stateless GET/DELETE behavior.
- Confirm the current request-size, auth, readiness, shutdown, and stdio cancellation tests before touching dependencies.

#### 0.2 Create the SDK-free wire oracle as the first migration commit
Before changing `go.mod`, add and commit the complete Phase 0 oracle on the migration branch while production still runs mark3. Hand-written JSON-RPC requests drive the real production HTTP handler, the capture imports no MCP SDK type, and each fixture records the request, HTTP status, Content-Type/framing, and decoded response. Add the shared `registerHandlers` production seam and guardrail inventory entry. Remove the one-time capture/update path before the dependency-changing commit so the official runtime cannot rewrite its own baseline.

The closed, unmerged [PR #283](https://github.com/SigNoz/signoz-mcp-server/pull/283) at `cd188cb` is historical design input only. Reuse its narrow raw-HTTP harness ideas where they still match this plan, but do not treat the PR/branch as a prerequisite, merge target, or source of truth, and do not cherry-pick its obsolete plan decisions or incomplete discovery-only fixture set wholesale.

Do not add a second semantic exporter. Store canonical JSON with volatile request IDs and the build-stamped version normalized. Sort only the top-level discovery collections (`tools`, `resources`, `resourceTemplates`, `prompts`) by stable identity so SDK ordering changes do not create noise. Preserve every nested array order, including JSON Schema composition/enums/required fields, resource `contents`, and prompt messages.

Capture:

- `initialize`: negotiated protocol, server name, instructions, and complete capabilities;
- `tools/list`: every name, description, exact input/output schema, complete annotations, title/icons/meta if present, and `nextCursor`;
- `resources/list`, `resources/templates/list`, and `prompts/list`: every entry field and cursor behavior, compared order-insensitively at the top level;
- all 22 static `resources/read` results as a compact inventory keyed by URI: content count/order, kind, URI, MIME, `_meta`, serialized length, and SHA-256 of each serialized content item. Initialize `dashboard.InitClickhouseSchema()` and use a fixed small docs-index snapshot so the sitemap is deterministic; keep a literal sitemap fixture because it is small and shape-sensitive;
- both resource-template descriptors and fixed reads with literal expected payloads. Use a mock SigNoz client; dashboard JSON must round-trip byte-for-byte. For the alert template, normalize only `asOf` and `historyWindow.start/end` to fixed sentinels for the literal comparison, then separately assert the original emitted values match the captured mock-request window and span exactly six hours. Do not add a production clock abstraction solely for a golden;
- all four `prompts/get` results using per-message type/role/length/digest records, plus literal fixtures for small representative prompts that cover text and embedded-resource shapes;
- focused deterministic tool cases: successful structured content plus legacy text copy, fail-open input mismatch and exact notice text, output-validation telemetry behavior, coded error envelope/recovery, omitted arguments, and `null` arguments;
- unknown tool/resource/prompt errors, with resource-not-found expected to move from mark3's `-32002` to official `-32602`;
- omitted/null arguments and exact raw argument retention.

Canonicalization must not remove nested array order, false values, null-vs-absent distinctions, schema keywords, required/default/additionalProperties fields, MIME types, annotations, `_meta`, or error data. Freeze the pre-swap fixture after this commit. Add one explicit `acceptedMigrationDifferences` table keyed by method plus JSON path/status behavior; initially it contains only:

- removal of `result.capabilities.logging`; legacy `logging/setLevel` is neither guaranteed nor actively blocked, so no shim or parity assertion is added;
- top-level discovery collection ordering;
- outer `result.ttlMs: 0` and `result.cacheScope: "public"` on cacheable methods;
- unknown `resources/read` code `-32002 -> -32602` with the official message/data;
- unknown tool and prompt responses keep `-32602` but use the official SDK's
  standard `unknown tool "<name>"` / `unknown prompt "<name>"` messages instead
  of mark3-specific wording;
- fail-open input-validation notice detail from validator-library error text to the exact repository-owned sentence defined in Phase 1; the notice prefix and best-effort behavior remain;
- modern-only `resultType` and server metadata;
- transport differences already named in this plan (GET/DELETE 405, malformed-stdio termination, and any approved legacy-disconnect limitation).

Every entry names the exact old/new values, has a focused assertion, and is called out in the PR. Any other difference fails with a concise JSON path or digest mismatch. Future product-contract changes update source/baseline separately; do not expand the table merely to make the migration pass.

Make the freeze mechanical without a second manifest system: generation is a Phase 0-only operation whose helper is removed before `go.mod` changes. Ordinary tests only read fixtures. A post-swap `go test` command has no path that overwrites them.

Keep the assertions in these independent tests as migration oracles. Their SDK-coupled harnesses must be ported, so they cannot literally remain unchanged; review the assertion diff separately from mechanical type changes:

```text
internal/mcp-server/contract_budget_test.go
internal/mcp-server/integration_test.go
internal/handler/tools/schema_inventory_test.go
internal/handler/tools/annotations_inventory_test.go
internal/handler/tools/registration_test.go
internal/handler/tools/normalization_replay_test.go
internal/handler/tools/output_schema_test.go
internal/handler/tools/structured_content_test.go
internal/handler/tools/tool_error_codes_test.go
```

Verification for Phase 0:

```bash
go test -count=1 ./internal/handler/tools ./internal/mcp-server \
  -run 'Test.*(SDKMigration|Schema|Annotation|Registration|Normalization|StructuredContent|ToolError|Contract)'
go test -count=1 -run '^TestGuardrail_' ./...
git diff --check
```

Expected result: the SDK-free baseline is generated by mark3 and immediately passes against mark3; it covers every advertised schema/description and every deterministic resource/prompt payload named above; existing guardrails remain green. No dependency has changed.

### Phase 1 — Introduce the narrow contract adapters and convert registrations

#### 1.1 Pin the official SDK
- Add `github.com/modelcontextprotocol/go-sdk v1.7.0`.
- Align the existing direct `github.com/google/jsonschema-go` requirement to `v0.4.3`, which is the version selected by go-sdk v1.7.0; keep it direct because repository code constructs and resolves schemas itself.
- Keep mark3 only while the migration compiles in staged commits; remove it once no import remains.
- Use the official SDK's direct schema dependency, `github.com/google/jsonschema-go/jsonschema`, for schema representation, resolution, and validation. Remove `github.com/santhosh-tekuri/jsonschema/v6` after focused schema-replay and fail-open tests pass.
- Do not use MCPGODEBUG compatibility flags to hide descriptor or annotation drift. Construct the correct wire values explicitly.

#### 1.2 Add a thin repository-owned descriptor/request layer
Add a small internal tool-contract layer with only the pieces used by this repository:

- tool definition and handler aliases around official low-level types;
- official `*jsonschema.Schema` definitions/builders for the currently used primitive/object/array/raw/typed operations, using `json.RawMessage` only for valid schemas that the typed representation cannot round-trip without changing advertised JSON;
- typed schema generation through the same `google/jsonschema-go` configuration used by the official SDK;
- explicit annotation conversion, including false-valued booleans and pointer defaults;
- a local request type exposing only decoded `Arguments`, exact `RawArguments`, `GetArguments()`, and tool name—the fields current business handlers consume;
- one adapter from official `*mcp.CallToolRequest` to the local request. Protocol/client/capability metadata stays on the official request and is consumed by receiving middleware instead of being cloned into an unused second request model;
- one request-scoped argument decode shared by search-context extraction, the adapter, repository validation, and business handlers. Retain the exact raw bytes for compatibility characterization without reparsing them in validation.

All registration-time descriptor/schema transforms must be copy-on-write. Definitions may be reused by production, tests, and the conformance profile, so normalization or annotation conversion must not mutate the original `mcp.Tool`, `*jsonschema.Schema`, `Properties`, property schemas, `Extra` maps, `Required` slices, or raw-schema bytes. Add original-unchanged, repeat-registration, and concurrent-registration race tests. Clone only the branches actually modified; do not add a general deep-copy framework.

The adapter's JSON contract is frozen by table tests:

| Wire arguments | Required behavior |
|---|---|
| omitted | decoded nil/empty behavior matches current handlers; raw bytes absent |
| `null` | validation treats it as `{}` where current policy does; handlers do not panic |
| `{}` / object | standard `encoding/json` map decode plus exact raw bytes |
| number literals | current float64 behavior, including the characterized >2^53 rounding |
| numeric strings | exact strings retained for per-field parsers |
| arrays/primitives | retained and surfaced without adapter panic; handler/policy determines outcome |
| malformed JSON | protocol parse error before handler, with HTTP transport observation only |

Do not use blanket `UseNumber`, bind all arguments into generated structs, apply schema defaults, or reject unknown fields.

#### 1.3 Preserve registration and fail-open policy
Rewrite `Handler.addTool` around official low-level registration in this exact order:

```text
construct/normalize official schema
-> obtain exact advertised input/output JSON
-> resolve official validators with `Schema.Resolve` (record failures, fail open)
-> adapt raw request to repository request
-> fail-open input/output validation decorator
-> coded-error decorator
-> per-tool context/panic-safety decorator
-> duplicate-registration claim
-> official (*mcp.Server).AddTool
```

Add one registered-pipeline test through official dispatch. It must prove validation, coded-error enforcement, panic safety, result safety, and terminal tool observability compose without duplicate lifecycle signals.

The policy layer continues to:

- append a stable repository-owned input validation notice only to successful mismatched calls. Do not parse or embed validator-library error text. Pin this exact replacement: `Input validation notice: the arguments did not fully match this tool's input schema. The call still ran best-effort: mismatched values may have been ignored or replaced with defaults. Review the arguments and re-call if the results look off.` Keep telemetry metadata at the deterministic `<root>` / `schema` fallback unless it can be derived independently of validator error strings;
- keep output mismatch and missing structured content telemetry-only;
- preserve authorization operation fields and fallback error codes;
- serialize the entire official `CallToolResult` with `json.Marshal` for size/JSON-safety checks;
- substitute the same coded internal error when a result cannot serialize.

Distinguish registration failures from repository-validator failures:

- a nil, non-object-rooted, non-marshalable, or invalid `x-mcp-header` advertised schema rejected by official low-level `AddTool` is registration-fatal. All 43 production tools must pass this preflight before the transport swap;
- a syntactically valid object-rooted schema that low-level `AddTool` accepts but `Schema.Resolve` cannot compile still registers, emits the existing detectable compile-failure signal, and runs with repository validation disabled. Rewrite the synthetic fail-open test around this case (for example, an unresolved reference) rather than expecting the official SDK to register an invalid root schema.

Before removing the old validator, dual-run the current accepted replay corpus and a focused reject corpus covering each validation-affecting keyword family actually used by the 43 schemas. The advertised JSON must remain exact; any change in which calls receive the fail-open notice is fixed or added as its own reviewed response difference.

#### 1.4 Convert definitions and handler signatures mechanically
Do the transitive SDK-type swap as one mechanical commit rather than introducing a dual-registration shim solely to compile tool families independently. Convert the existing builder vocabulary to official `*jsonschema.Schema` values, or rewrite definitions directly where that is clearer; either approach is acceptable because the catalog fixture compares all 43 advertised schemas. Prefer changing handler parameters to the local request type, leaving business parsing untouched. Update result content construction/type assertions for official pointer content implementations. Preserve exact `structuredResult` JSON and text bytes, including `json.Number` literals.

When raw schemas are simpler or required for exact unions/boolean subschemas, pass their normalized bytes as `json.RawMessage` to official low-level `AddTool`; the API explicitly accepts any JSON-marshalable object-rooted schema. Keep schemas colocated/generated from current definitions and never make checked-in golden data the runtime source of truth.

#### 1.5 Convert resources, templates, and prompts
- Adapt resource handlers from `[]ResourceContents` to official `*ReadResourceResult` without changing content order, MIME type, URI, text/blob encoding, or size behavior. Accept only the separately named unknown-resource JSON-RPC change from `-32002` to official `-32602`.
- Require every returned content item to set URI and MIME explicitly; fail the adapter test on blanks so official v1.7.0's post-handler auto-fill cannot conceal a conversion mistake. Add a synthetic blob adapter test to prevent base64 double-encoding even though production resources are currently text-only.
- Preserve URI-template registration keys and duplicate checks.
- Accept the official SDK's stable feature ordering. Tests compare top-level discovery collections by identity, not serialized order.
- Convert prompt definitions/handlers to official pointer request/result types while preserving argument schemas and generated messages.
- Keep resource/prompt analytics outside these adapters; Phase 2 receiving middleware owns lifecycle attribution.

Verification for Phase 1:

```bash
rg -n 'github.com/mark3labs/mcp-go' --glob '*.go' .
go test -count=1 ./internal/handler/tools ./internal/docs ./pkg/prompts ./pkg/toolerrors
go test -count=1 ./internal/mcp-server -run 'Test.*(SDKMigration|Contract|Integration|Arguments)'
go test -count=1 -run '^TestGuardrail_' ./...
```

Expected result: `rg` prints no production mark3 import or santhosh validator dependency by the end of the phase; all 43 advertised tool schemas/descriptions/annotations and all resource/template/prompt entry fields match the frozen baseline; only the named accepted differences remain.

### Phase 2 — Swap server composition, transport, and observability

#### 2.1 Construct the official server explicitly
Replace `newSDKServer` with one fully configured-and-registered official server factory used by HTTP, stdio, in-process tests, and, later, the test-only conformance binary. Callers supply only the bounded profile differences; they must not reassemble capabilities, middleware order, recovery, or registration independently. The production profile sets:

- implementation name/version and existing instructions;
- explicit non-logging capabilities matching the intended migrated surface, with non-nil empty tools/prompts/resources capability structs so `listChanged` remains absent/false rather than inferred true;
- explicit page size large enough for the whole static catalog;
- the bounded/redacting SDK logger adapter defined below;
- receiving middleware in a documented order;
- no generic AddTool validation, completion/subscription/sampling/elicitation/MRTR/Tasks/Apps features.

Assert legacy `initialize` matches the canonical baseline and modern `server/discover` advertises the intended server identity, capabilities appropriate to the modern protocol, and the transport-filtered official version set. For v1.7.0 that set is `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`, and `2024-11-05`; tests must require at least the issue's two target eras and make any future SDK version-list drift reviewable.

Keep the structure lean within `internal/mcp-server` rather than introducing a public inventory framework. The following are ownership boundaries, not required file scaffolding:

```text
server.go              lifecycle: New, Run, Shutdown, analytics drain
server_factory.go      official options, capabilities, middleware order, registration
transport_http.go      HTTP mux, auth/body-limit composition, Streamable HTTP options
transport_stdio.go     stdio context, run, and cancellation normalization
observability.go       receiving/tool observers, panic recovery, bounded SDK logger
```

Keep these responsibilities cohesive; do not perform a separate move-only refactor or split small helpers merely to match the external repository.

Do not advertise logging. Do not add middleware to preserve or suppress legacy `logging/setLevel`; official v1.7.0's legacy handling is incidental rather than a product contract. Assert only that initialize omits the capability and that the SDK rejects the method for modern requests. Accept official resource-not-found `-32602` and official feature ordering without receiving-middleware rewrites.

Wrap the SDK's `slog.Logger` with a bounded/redacting adapter. Preserve its useful transport diagnostics, but demote the known modern removed-method probe and graceful `server run cancelled` record from ERROR to DEBUG; never demote arbitrary internal/session failures. Pin message/attribute matching in tests so an upstream wording change fails rather than broadening the downgrade.

#### 2.2 Consolidate method lifecycle observability
Replace mark3 hooks and `pkg/otel/mcp.go` tracer integration with official receiving middleware plus the existing `otelhttp` request span. Receiving middleware owns every request that reaches official SDK dispatch. Transport/lifecycle validation that happens earlier remains visible through the outer HTTP span/status and bounded SDK diagnostics but emits no MCP method/tool metric. Do not tee and reparse request bodies or add cross-layer duplicate-suppression state solely to synthesize MCP telemetry for calls the SDK did not dispatch; focused tests pin zero MCP metric emission on representative pre-dispatch failures.

For stdio, first measure the public v1.7.0 pipeline: request checks, param unmarshal, modern metadata/lifecycle gating, malformed frames, and SDK error logging can occur before receiving middleware. Preserve receiving middleware for dispatched requests and configure bounded repository logging. Add only the smallest exported-interface `Connection` decorator needed by a failing parity test—for example, a coarse pre-dispatch rejection count. Do not inject observation tokens, build a request registry/CAS handshake, or reimplement framing by default.

Pin the known malformed-frame delta: mark3 returns a parse error and keeps reading, whereas official v1.7.0 terminates the one-client stdio connection. Document and accept this protocol-owned behavior for the initial migration. Reconsider a tolerant custom transport only with evidence that a supported client sends garbage and depends on continuing the same process.

The receiving method observer must:

- start before dispatch and terminate in a defer;
- decorate the active HTTP/stdio span with normalized method, tenant URL, caller correlation, protocol version, bounded client name/version, and an allowlist of boolean capability-presence flags; keep client/protocol/capability fields off metrics;
- extract client metadata from each modern request and from legacy session state where the transport retains it. Stateless legacy HTTP attributes the protocol header on every call and client identity on `initialize`, but cannot carry initialize-only client identity into later independent POSTs;
- classify success, coded tool error, handler-level invalid params, unknown tool/resource/prompt, panic, client cancellation, deadline, and internal error after dispatch; transport/lifecycle rejections that occur before middleware remain transport observations;
- emit one method count/duration/log/span terminal outcome, even if the context cancels before downstream returns;
- preserve DEBUG severity for expected unsupported probes and cancellations, ERROR for real protocol/internal failures;
- schedule existing analytics without losing `WaitForAnalytics` shutdown behavior.

Tool-specific observation remains registration-owned for context/panic safety, while receiving middleware emits one terminal tool count/duration/log/analytics outcome for both registered calls and dispatched unknown-tool failures. Remove the mark3 observation tombstone machinery; the defer-based receiver has no cross-layer state or cleanup race. Stdio keeps the same dispatched-request observer without mirroring HTTP transport internals.

#### 2.3 Add explicit panic recovery
Add recovery inside the receiving lifecycle observer and around tool invocation as needed so:

- method/tool terminal telemetry observes the recovered failure exactly once;
- no panic escapes the HTTP server or stdio loop;
- a handler panic does not leak request arguments or stack data to clients;
- the response uses the existing protocol-vs-coded-tool-error boundary;
- the log contains a bounded stack for operators without credentials/tool input.

Pin panic behavior with HTTP, stdio/in-memory, tool, resource, and prompt tests before deleting mark3 `WithRecovery` assumptions.

#### 2.4 Replace HTTP transport without changing the outer product stack
Build `mcp.NewStreamableHTTPHandler` with:

```text
Stateless:                    true
JSONResponse:                 true
Logger:                       bounded/redacting SDK logger adapter
PropagateRequestCancellation: true
MaxRequestBodyBytes:          mapped config limit (>0 unchanged, <=0 becomes -1)
DisableLocalhostProtection:   false (preserve current protection)
```

Wrap the resulting `/mcp` chain in the standard library's
`http.NewCrossOriginProtection().Handler(...)`, as official v1.7.0 recommends.
Accept non-browser and same-origin requests; reject cross-origin browser POSTs
before body limiting, authentication, or SDK dispatch. Keep the SDK's separate
localhost Host/DNS-rebinding protection enabled.

Retain outer composition exactly:

```text
otelhttp(
  mux{
    /mcp: crossOrigin(maxBytes(auth(officialHandler)))
    /livez, /readyz, /healthz
    custom OAuth metadata/register/authorize/token routes
  }
)
```

Keep the outer body limiter to preserve early `Content-Length` 413 behavior and current direct-config semantics; pass the same mapped limit to the official handler for bounded downstream reads. The ordering is fixed structurally and by focused limit/auth tests; do not add an injectable transport solely to count internal entries.

`JSONResponse: true` is load-bearing: official v1.7.0 otherwise changes every single-response POST from the current `application/json` body to SSE framing. Keep localhost/DNS-rebinding protection enabled, port the existing loopback/non-loopback Host matrix, and make an explicit future security decision rather than disabling it to fix a harness Host header.

Intentionally remove mark3 heartbeat/listener options. Under official stateless transport:

- POST serves both eras;
- GET `/mcp` returns 405;
- DELETE `/mcp` returns 405;
- no response reads or writes `Mcp-Session-Id`.

Keep `ReadTimeout`/streaming timeout choices under explicit review: the product no longer holds GET listeners, but POST responses may still stream notifications. Do not opportunistically tighten server timeouts in the migration.

#### 2.5 Replace stdio transport and normalize shutdown
- Seed the run context with configured API key, auth header name, SigNoz URL, and `ClientSourceUserClient` before calling `s.Run(ctx, &mcp.StdioTransport{})`.
- Preserve stderr-only application logs and newline-delimited JSON on stdout.
- Return nil for `context.Canceled` and preserve real transport errors.
- Adapt or filter the official SDK's known `server run cancelled` ERROR record so graceful shutdown remains an expected non-error log outcome; keep real session failures at ERROR.
- Retain early-cancel startup behavior and bounded analytics draining.

#### 2.6 Transport/auth/readiness test matrix
Add focused tests for:

- direct API key, Authorization bearer, OAuth token, missing/invalid auth, SigNoz URL precedence/validation, and OAuth `WWW-Authenticate` challenges;
- middleware order for oversize+unauthenticated, authenticated+oversize, valid request, and non-MCP routes;
- declared/chunked request bodies at, below, and above the configured limit; `MaxRequestBytes == 0` remains unlimited for directly built configs;
- `/livez` independent of docs readiness; `/readyz` and `/healthz` transition with the docs index;
- request-context cancellation reaches modern handlers. Before claiming legacy parity, characterize current HTTP-disconnect cancellation and official v1.7.0 behavior: `PropagateRequestCancellation` is modern-only. Preserve explicit legacy cancellation notifications; if disconnect propagation has no supported official hook, record the capacity-impact delta for approval instead of hiding it;
- HTTP shutdown before/after listener publication, stdio signal cancellation, and no process exit 1 on graceful cancellation;
- stateless POST concurrency, no session header, no sticky state, GET/DELETE 405.
- pre-dispatch HTTP outcomes (413, 401/403, 405, content negotiation as emitted by the SDK, malformed payload, and header mismatch) do not create duplicate method/tool metrics; unparseable requests stay HTTP-only;
- dispatched stdio requests retain terminal telemetry; SDK-rejected stdio requests produce bounded transport signals without raw frames. Add a coarse connection decorator only if the focused parity test proves one is required;
- successful POST responses remain HTTP 200 `application/json`; request `Content-Type` accepts exact `application/json`, `application/json; charset=utf-8`, and case-variant charset parameters, while missing/wrong media types follow the exact official status/body contract. Cover realistic `Accept` combinations for JSON and SSE rather than asserting only “not 415.” Batches at protocol versions that forbid them reject with the official HTTP/status/error behavior; localhost Host protection matches the existing matrix.

Verification for Phase 2:

```bash
go test -count=1 ./internal/mcp-server \
  -run 'Test.*(HTTP|Stdio|Auth|OAuth|RequestBody|Ready|Health|Shutdown|Cancel|Panic|Telemetry|Analytics|Session)'
go test -race ./internal/mcp-server ./internal/handler/tools ./pkg/otel
go test -count=1 -run '^TestGuardrail_' ./...
```

Expected result: both transport modes preserve product behavior except the named accepted migration differences and any approved legacy-disconnect limitation; modern aborts cancel handlers; graceful shutdown returns success without an ERROR log; HTTP telemetry proves exactly-one completion on every terminal path, and stdio retains measured/bounded signals without speculative machinery.

### Phase 3 — Prove both protocol eras against the real production server

#### 3.1 Add raw wire tests
Add production-handler tests for this minimum matrix:

| Transport | `2025-11-25` | `2026-07-28` |
|---|---|---|
| HTTP | initialize + initialized notification, all lists, deterministic call/read/get | `server/discover`, then direct lists/call/read/get without initialize and with per-request `_meta` |
| stdio | initialize + initialized notification, all lists, deterministic call/read/get through the official IO transport; thin real-binary framing/shutdown smoke | discover and direct lists/call/read/get through the official IO transport; thin real-binary framing/shutdown smoke |

Drive both transports from the same logical request/expectation fixtures where framing permits; transport-specific assertions remain beside the shared semantic expectations. Do not maintain two independently curated catalog matrices.

Legacy assertions:

- canonical initialize/catalog/result comparison with top-level discovery order ignored and only the path-specific accepted differences allowed;
- successful `signoz_search_docs`, `signoz://docs/sitemap`, and `debug_service_errors` flows;
- no `Mcp-Session-Id` and no required session header;
- ordinary coded error retains `isError`, stable `structuredContent.code`, text guidance, and extra fields.
- initialize omits logging, unknown `resources/read` uses official `-32602`, and fail-open notices use the exact repository-owned wording; each is reported as an expected migration difference rather than a compatibility regression. Do not assert legacy `logging/setLevel` behavior.

Modern assertions:

- `server/discover` includes at least `2025-11-25` and `2026-07-28`, pins the full transport-filtered v1.7.0 version list for review, and returns stable server identity/instructions and expected capabilities;
- direct calls work without initialize;
- required protocol version/client capabilities and optional client info are read from each request independently;
- two consecutive callers do not leak identity/capabilities across requests;
- the existing `MCP Client: Initialized` analytics event remains legacy-only; successful modern tool/prompt/resource events carry request protocol/client identity without fabricating an initialization event for optional `server/discover`;
- modern result/meta/cache fields are accepted as intentional protocol additions, not backported into the legacy golden.
- modern `logging/setLevel` is rejected by the official lifecycle gate.

#### 3.2 Test standardized header validation
For modern HTTP, send correct headers, then independently mismatch:

- `Mcp-Protocol-Version` vs body `_meta.io.modelcontextprotocol/protocolVersion`;
- `Mcp-Method` vs JSON-RPC `method`;
- `Mcp-Name` vs `tools/call.params.name`.

Each mismatch must reject with the official standardized header-mismatch JSON-RPC code (`-32020`), preserve the request ID, omit `Mcp-Session-Id`, avoid the tool handler/upstream client, remain visible on the outer HTTP span/status, and emit zero MCP method/tool metrics because official dispatch did not occur. Separately test missing/invalid required client capabilities as invalid params (`-32602`), a capability-gated fixture request as missing required capability (`-32021`), unsupported modern protocol metadata (`-32022`), missing/mismatched protocol headers (`-32020` or invalid params exactly as official v1.7.0 specifies), malformed headers, and legacy requests without modern headers.

#### 3.3 Keep Inspector as an independent initialized-client lane
Run the pinned Inspector against the actual production binary over HTTP, and a
small bounded raw-client smoke against the real stdio binary. Exercise:

- tool/resource/template/prompt lists;
- `signoz_search_docs` deterministic call;
- docs sitemap read;
- `debug_service_errors` prompt get;
- one coded validation/error result;
- server identity/protocol negotiation and successful logical responses.

Keep bounded startup/readiness/command timeouts, loopback binding, dummy configured credentials, PID-scoped cleanup, and bounded failure logs. Inspector is an independent client, not the source of exact catalog or response-header assertions; raw HTTP owns `Mcp-Session-Id` absence.

Do not create decorative modern Inspector cells. First verify an exact Inspector release really exposes explicit `2026-07-28` lifecycle selection and machine-readable output; only then add those cells as additive independent coverage. Until then, the raw HTTP/stdio matrix in 3.1 is the authoritative modern proof.

Verification for Phase 3:

```bash
npm ci --ignore-scripts --prefix tools/mcp-ci
bash -n scripts/test-mcp-protocol.sh
shellcheck scripts/test-mcp-protocol.sh
scripts/test-mcp-protocol.sh
```

Expected result: the Inspector HTTP initialized-client lane and real-binary
legacy/modern stdio smokes pass, the Go raw production matrix independently
proves both eras, modern calls never initialize, legacy calls still do, and
mismatch probes reject before handler execution.

### Phase 4 — Add honest official conformance coverage as a bounded post-merge follow-up

Deliver this phase as one explicitly linked, sequential follow-up PR branched from `main` only after the production runtime PR has merged and the raw dual-era matrix is green. Do not open it as a child PR based on the unmerged runtime branch: the repository's CI, guardrail, and protocol workflows trigger only for pull requests targeting `main`, and the repository squash-merges and deletes merged branches. The runtime PR may merge without conformance, but this plan remains `In Progress` and issue #194 does not close until the conformance PR lands. Do not split any other phase into another tracker merely to reduce the diff.

#### 4.1 Pin the official referee
- Add an exact `@modelcontextprotocol/conformance` version to `tools/mcp-ci/package.json` and lockfile. At planning time the applicable package is prerelease `0.2.0-alpha.11`; confirm the exact current release before implementation and use a stable `0.2.x` instead if one now provides both frozen requirements sets.
- Record why a prerelease is blocking if still necessary. Never use `latest`, `next`, or an unbounded range.
- Use the runner's frozen `--requirements 2025-11-25` and `--requirements 2026-07-28` sets; `--requirements` replaces `--suite` and `--spec-version`. Start with no expected-failures file. Any proposed entry requires a per-check root-cause review; never baseline a whole scenario or inherit the upstream SDK baseline as this repository's result.

#### 4.2 Reuse the smallest non-shipping official fixture profile
The official frozen requirements call fixed fixture names and cannot run truthfully against the SigNoz catalog. Use go-sdk v1.7.0's own `conformance/everything-server` as the upstream behavior reference. Do not copy that large server into this repository or claim that rerunning it alone proves SigNoz composition. First enumerate the exact frozen requirements with `list --requirements <revision>`, then implement the smallest local fixture profile capable of passing them through this repository's option builder, low-level registration adapter, panic/lifecycle middleware, and HTTP wrapper composition. If that necessarily duplicates most of the upstream everything server, stop and resolve the scope/acceptance claim with maintainers rather than adding baselines.

```text
internal/mcpconformance/       # fixture registrations/behavior only
cmd/conformance-server/       # CI-only entry point
```

Factor production server construction just enough that both binaries exercise the same:

- official SDK version and server option builder;
- descriptor/registration adapters;
- receiving/panic/observability middleware;
- Streamable HTTP option assembly and header validation;
- request-size/logging transport plumbing where relevant.

The fixture binary may expose the official `test_*`, `test://`, and test prompt surfaces required by the frozen requirements. It may implement callback/subscription/MRTR fixtures only in this profile. It must not import SigNoz credentials or call a live backend. If passing the frozen set requires duplicating most of upstream's everything server and therefore mostly retests unmodified SDK behavior, stop: record upstream pinned SDK evidence and ask maintainers whether issue #194 should accept explicit selected scenarios instead. Do not silently change the claim or add a broad expected-failures baseline.

Run separate profiles/processes as the frozen requirement sets require:

- legacy `2025-11-25`: stateful when required by the frozen sampling/elicitation/subscription scenarios;
- modern `2026-07-28`: stateless and sessionless.

This profile difference is confined to the conformance binary. The production endpoint remains stateless for both eras and is covered by Phase 3.

#### 4.3 Add leakage guards
- `cmd/server` dependency closure must not include `internal/mcpconformance`.
- Production `tools/list`, resources, templates, and prompts must not contain `test_*`, `test://`, or conformance prompt names.
- Fixture surfaces must not appear in `manifest.json`, README tables, initialized-wire budgets, or production catalog counts.
- MRTR, Tasks, and Apps must remain absent from production discovery.

#### 4.4 Add a separate CI job
Keep the existing stable `protocol / inspector` check and add `protocol / conformance`. The conformance job:

- builds the test-only fixture binary;
- starts bounded legacy and modern processes on different loopback ports;
- runs `server --requirements 2025-11-25` and `server --requirements 2026-07-28` separately;
- retains each report for failure diagnostics without uploading secrets;
- fails on every required scored scenario and its synthetic wire-schema failure;
- reports non-required extension/pending outcomes separately without enabling those features in production.

If the team wants wire-schema failures from non-scored scenarios to block too, add explicit report post-processing and document that policy; the conformance CLI exit code does not promote them by itself.

Do not use `tier-check`; repository/client assessment and GitHub tokens are
outside this server gate. The official runner covers HTTP only, so the raw Go
matrix and bounded real-binary smoke remain responsible for stdio.

Verification for Phase 4:

```bash
node tools/mcp-ci/node_modules/@modelcontextprotocol/conformance/dist/index.js \
  list --server --requirements 2025-11-25
node tools/mcp-ci/node_modules/@modelcontextprotocol/conformance/dist/index.js \
  list --server --requirements 2026-07-28
actionlint .github/workflows/mcp-protocol.yaml
```

Expected result: both frozen requirement sets pass against the isolated shared-stack fixture with no broad baseline, and the production 2×2 raw matrix independently proves the SigNoz endpoint/catalog. If maintainers explicitly approve selected-scenario scope instead, rewrite this phase and the acceptance claim before implementation.

### Phase 5 — Documentation, companion audit, and representative clients

#### 5.1 Update repository documentation and metadata
- `README.md`: supported protocol-era/transport table, `server/discover` and legacy initialize behavior, stateless POST-only endpoint, no session ID, and unchanged client configuration.
- `docs/architecture.md`: official SDK ownership, per-request modern identity/capabilities, receiving middleware, HTTP wrapper order, `JSONResponse: true`, explicit GET/DELETE 405, removal of heartbeat, malformed-stdio behavior, cancellation limitations, and shutdown behavior.
- `docs/mcp-best-practices.md`: record the dual-era review expectations and exact compatibility/conformance ownership if section 11 needs it.
- `guardrails/README.md`: runtime/tool pins, raw/Inspector/conformance layers, required check names, update procedure, no-baseline/leakage policy, and local commands.
- `manifest.json`: expect no semantic tool/resource change; compare rather than churn. Update only SDK/protocol metadata fields that genuinely describe the new endpoint.
- `server.json`: audit after refreshing main; it is already at v0.12.0 on `30acf69`, so change only metadata that is genuinely made stale by the SDK migration.
- Remove stale mark3 references from comments and documentation.

Explicitly document that official stateless mode no longer offers the old GET listener/heartbeat. This is an intentional transport cleanup, not an unnoticed regression.

#### 5.2 Repeat CMP-3 audit
Compare final tool/parameter/payload/documented behavior against `SigNoz/agent-skills`, including MCP setup references and native client config files. Expected result: no companion change because the production catalog and taught behavior remain unchanged. State that result in the PR summary. If the final diff changes any taught contract, stop and open/link the companion PR before release.

#### 5.3 Run representative-client smokes
Use a delegated, credential-safe protected/manual run for live clients:

- SigNoz AI Assistant;
- Claude Code;
- Codex;
- Cursor;
- at least one local stdio client configuration.

For each, record exact client version, transport, negotiated protocol era, discover/initialize outcome, catalog success, a read-only `signoz_search_docs` call, one coded validation/error result, auth mode, session-header absence for HTTP, and Assistant correlation behavior. Do not create or mutate tenant resources. Never print or persist credentials.

Deterministic wire goldens, raw dual-era tests, and Inspector block the runtime migration merge. The separate conformance follow-up must land before issue #194 closes or release promotion. Unless the open question is resolved differently, credentialed native-client smokes also block release promotion rather than untrusted pull requests.

Completed runtime-PR staging evidence on 2026-08-13:

- 35/35 selected read-only build-tagged live tests passed; all channel/view and
  other mutation tests were excluded.
- The built production binary passed HTTP and stdio under both `2025-11-25`
  and `2026-07-28`, including all four catalog counts, representative local
  resource/prompt reads, coded validation, one live read-only backend tool,
  stateless HTTP method/session behavior, and clean shutdown.
- The read-only matrix created no staging resource. The later OAuth run used one
  temporary viewer service account and expiring key; the key was revoked and
  verified unauthorized, and the account was deleted (retained only as the
  backend's soft-deleted audit row).

Browser OAuth and the actual Assistant consumer subsequently passed on the
built server, including DCR, PKCE, consent, code/token/refresh, bearer
challenges, live read-only calls, coded validation, correlation, and log-secret
scans. That run found and fixed one auth-precedence defect: server-issued OAuth
tokens are now decrypted before considering `X-SigNoz-URL`, so their encrypted
tenant is authoritative and cannot be downgraded into a direct bearer.

Representative native clients were also exercised against the built binary.
Codex completed catalog, local docs, and coded-error calls; Claude connected and
read the catalogs but its provider account limit blocked invocation before MCP;
Cursor initialized and listed all tools, while its noninteractive invocation
was rejected client-side before `tools/call`. The actual SigNoz Assistant
consumer path completed live success and coded-error calls through OAuth with
exact correlation, which is the production consumer boundary for this server.

The final Opus review additionally required real-transport proof for two
observability contracts. Production HTTP initialize now proves the legacy
client analytics event through the SDK-created server request, and HTTP
tool-failure tests prove request capture projects only marshalable MCP params,
redacts credential-shaped arguments, and cannot copy headers or token state.

### Phase 6 — Final verification and delivery

Run the runtime migration PR's documented gates from a clean checkout:

```bash
npm ci --ignore-scripts --prefix tools/mcp-ci
bash -n scripts/test-mcp-protocol.sh
shellcheck scripts/test-mcp-protocol.sh
actionlint .github/workflows/mcp-protocol.yaml
actionlint .github/workflows/guardrails.yaml
scripts/test-mcp-protocol.sh

go test -count=1 -run '^TestGuardrail_' ./...
go test -count=1 ./...
go test -race ./internal/handler/tools ./internal/mcp-server ./pkg/otel
go vet ./...
go build ./cmd/server
go mod tidy -diff
go mod verify

git ls-files -co --exclude-standard -z -- '*.go' | xargs -0 gofmt -l
git ls-files -co --exclude-standard -z -- '*.go' | xargs -0 goimports -l
git diff --check
```

Both formatting commands must print nothing. Run the configured GitHub `lint`, `fmt`, `test`, `deps`, `build`, guardrail, and Inspector jobs.

When Phase 4 is present in its linked follow-up, run the exact pinned
conformance-runner command introduced by that PR; do not overload the production
Inspector script with an unused suite variable.

Require the conformance job before completing this plan and closing issue #194. Promote `protocol / conformance` to a required check only after its first clean default-branch bootstrap.

Updated maintainer direction uses a temporary stack for the immediate
conformance successor: PR #286 still targets `main`, and the conformance PR is
branched from PR #286 after it becomes ready. Rebase the conformance PR onto
`main` after #286 merges; keep ERR-6 independent.

1. Branch one atomic runtime migration PR from refreshed `main`.
2. Its first green commit is `test(mcp): freeze pre-migration wire contracts`. Create the complete Phase 0 SDK-free oracle and capture all fixtures while the branch still uses mark3; run the Phase 0 gates and review the fixture diff before changing `go.mod` in any later commit.
3. Later commits in the same PR implement Phases 1–3 and 5: the complete dependency/type/registration/runtime/transport/observability cutover, ported existing tests, raw dual-era tests, Inspector updates, documentation, metadata, and CMP-3 result. Never regenerate the pre-swap fixtures after the first dependency-changing commit. Use logical commits for review, but do not create adapter-only, tool-family, transport-only, or observability-only PRs: no intermediate boundary is both production-functional and free of temporary dual-SDK shims.
4. Merge the runtime PR only when its complete parity and production gates pass. The clean operational/revert boundary is mark3 runtime versus official runtime.
5. Branch the Phase 4 conformance PR from the ready runtime branch as a stacked PR targeting that branch. It contains only the pinned referee, non-shipping fixture, leakage guards, CI job, and conformance-specific documentation. After #286 merges, rebase/retarget it to `main`. Keep #194 open until it lands.
6. After the runtime PR merges, the conformance PR and the independent #191/#164 ERR-6 follow-up may proceed in parallel from `main`; neither depends on the other.

### Post-merge follow-up — ERR-6 guidance fidelity (#191 and #164)

After the #194 runtime migration PR has merged to `main`, implement [nerve-pod#191](https://github.com/SigNoz/nerve-pod/issues/191) as a separate `fix(mcp): preserve recognized backend error guidance` PR against the official result/content types. Do not start its implementation on the mark3 runtime or merge it into the migration branch.

Treat #191 as the complete implementation scope for the overlapping [#164](https://github.com/SigNoz/nerve-pod/issues/164), and close both from that one PR rather than building two error paths. The follow-up must:

- positively recognize verified current and legacy SigNoz renderer envelopes;
- preserve bounded message, documentation URL, top-level/detail suggestions, detail messages, and retry guidance without changing stable MCP codes/status/auth fields;
- fall back to the local coded-error contract for malformed or unrecognized bodies;
- filter credential-bearing structured values and active markup without creating a general prose sanitizer;
- retain query-builder and authorization recovery as additive guidance;
- emit one bounded, value-free drift WARN/metric;
- cover parser bounds/safety, decorator composition, and one recognized plus one unrecognized tool failure over legacy and modern wire flows;
- deliberately update only the representative coded-error fixture as a product-contract change, then update README/error-contract documentation and repeat CMP-3.

This follow-up is outside #194's merge and completion criteria. If ERR-6 is release-critical, make its independent PR a release-promotion gate rather than coupling its implementation or revert boundary to the SDK migration.

The eventual PR title should follow repository convention, for example `feat(mcp): migrate to the official Go SDK`. Keep the PR body synchronized with actual catalog parity, intentional GET/DELETE change, CI pins, native-client results, and CMP-3 outcome.

## Files to Modify

### Runtime and registration
- `go.mod`, `go.sum` — official SDK dependency and mark3 removal.
- `internal/handler/tools/schema_compat.go` — official low-level registration, request adaptation, existing validation/error policy.
- `internal/handler/tools/registration.go`, `register.go` — official server types and duplicate-checked registration.
- `internal/handler/tools/annotations.go` — explicit official hint construction with wire parity.
- `internal/handler/tools/params.go` and tool family files — local request type plus official result/content types; no business behavior changes.
- New narrow tool-contract/request adapter files under `internal/handler/tools/` (or a tightly scoped `internal/mcpcontract/` if avoiding an import cycle).
- `internal/handler/tools/resource_templates.go` — official resource/result adapters.
- `pkg/prompts/prompts.go` — official prompt types and handler signatures.
- `internal/docs/errors.go`, `pkg/toolerrors/errors.go` — official result/content types with unchanged coded envelopes.

### Server, transport, and observability
- `internal/mcp-server/server.go` — lifecycle, shared state, `Run`, shutdown, and analytics drain.
- Keep factory/transport/observability ownership cohesive within `internal/mcp-server`; split files only when it improves navigation. Avoid a public inventory/config framework.
- `pkg/otel/mcp.go` — remove/replace mark3 tracer coupling while preserving shared MCP attributes/helpers.
- Focused files under `pkg/otel/`, `pkg/log/`, `pkg/analytics/`, or `internal/util/` only where required to carry per-request protocol/client/capability metadata.

### Characterization and focused tests
- New `internal/mcp-server/testdata/wire-catalog/` with complete descriptor/schema catalogs, compact per-entry resource/prompt content digests, and literal shape-sensitive tool/resource/prompt/error results.
- New `internal/mcp-server/wire_catalog_golden_test.go` with the SDK-free raw-HTTP harness, top-level identity sorting, deterministic mocks/docs index, path-level diff reporting, and the small accepted-differences table; no second contract fixture or duplicate full payload corpus. The closed #283 implementation may be consulted as historical input, but this plan owns the final behavior.
- `internal/mcp-server/protocol_matrix_test.go` and `protocol_headers_test.go` — dual-era HTTP/stdio and standardized-header matrices.
- New/updated stdio and cancellation tests under `internal/mcp-server/`.
- `internal/mcp-server/server_test.go`, `integration_test.go`, `e2e_docs_test.go`, `mcp_go_upgrade_test.go`, `nil_arguments_e2e_test.go`, `contract_budget_test.go` — port harnesses, preserve assertions.
- `internal/handler/tools/*_test.go`, especially registration/schema/normalization/output/structured/error inventories — port types while retaining behavior.
- `pkg/otel/mcp_test.go`, `pkg/toolerrors/errors_test.go`, `pkg/prompts/*_test.go` — official types and lifecycle parity.

### Protocol CI and fixture conformance
- `tools/mcp-ci/package.json`, `package-lock.json` — exact Inspector and conformance pins.
- `scripts/test-mcp-protocol.sh` — 2×2 production matrix and conformance suite modes.
- `.github/workflows/mcp-protocol.yaml` — retain Inspector check, add conformance check.
- Minimal `internal/mcpconformance/` and `cmd/conformance-server/` only if needed to wrap/reuse the pinned official fixture catalog and shared server/transport composition.
- `guardrails/policy.go`, `guardrails/tests.txt` only if new package-sensitive leakage tests meet guardrail ownership rules; otherwise keep tests beside packages.

### Docs and metadata
- `README.md`, `docs/architecture.md`, `docs/mcp-best-practices.md`, `guardrails/README.md` — protocol/runtime/CI behavior.
- `manifest.json`, `server.json` — audit and update only truthful metadata; production catalogs should remain semantically unchanged.
- `plans/official-go-sdk-migration.context.md`, `plans/official-go-sdk-migration.plan.md` — append decisions and keep status current through delivery.

## Verification Matrix

| Contract | Primary gate | Independent gate |
|---|---|---|
| Legacy catalog/result compatibility | SDK-free catalog fixtures + content digest inventory + named differences | manifest/inventory/budget tests |
| Modern discover/direct calls | raw HTTP + stdio tests | Inspector only if explicit modern selection is proven |
| Legacy initialize compatibility | raw HTTP + stdio tests | Inspector legacy cells |
| Header mismatch `-32020` | focused raw HTTP tests | official pending scenario for visibility only |
| Fail-open validation/coded errors | handler + E2E tests | production Inspector calls |
| Auth/request limit/wrapper order | focused handler tests | real-binary protocol harness |
| Cancellation/shutdown/readiness | race-focused Go tests | real process cleanup in scripts |
| Exactly-once telemetry | in-memory/fake meter/span/log/analytics tests | staging Assistant correlation smoke |
| Official conformance | frozen `--requirements` sets against isolated shared-stack fixture | production 2×2 raw matrix (different claim) |
| Fixture non-leakage | dependency/catalog tests | manifest/README audit |

## Risks
- **HIGH — Catalog drift:** different schema/annotation structs and SDK serialization can change valid but client-sensitive JSON.
- **HIGH — Resource-content drift:** official post-processing, blob representation, URI/MIME defaults, or a mutable docs index can change client-visible resource bytes even when descriptors look identical.
- **HIGH — Handler decode drift:** raw arguments, nil semantics, float precision, defaults, aliases, or unknown fields can change behavior across all 43 tools.
- **HIGH — Error-channel drift:** generic SDK helpers can turn coded tool failures into JSON-RPC errors or replace structured recovery content.
- **HIGH — Observability lifecycle:** replacing hooks/tracer/middleware can duplicate or lose terminal metrics, spans, logs, analytics, tenant, or Assistant correlation.
- **HIGH — False conformance claim:** full official scenarios require fixture names/features absent from the production catalog; direct production runs or broad baselines would be misleading.
- **MEDIUM — Capability/pagination drift:** official inference defaults (`listChanged`, page size) can alter initialize/discover/list responses beyond the accepted logging removal.
- **MEDIUM — Transport drift:** official JSON/SSE default, stateless GET/DELETE 405, request-cap defaults, legacy disconnect cancellation, malformed stdio, DNS-rebinding protection, and `Server.Run` cancellation/logging differ from mark3.
- **MEDIUM — Prerelease CI referee:** the applicable conformance package may still be prerelease and needs an exact, reviewed pin.
- **MEDIUM — Shared-SDK blind spot:** server and some tests use the same SDK; raw wire, Inspector, canonical fixtures, and the external conformance runner remain necessary.
- **LOW — Documentation/version drift:** `server.json` is current at v0.12.0 on the planning baseline, but SDK/protocol documentation and metadata can still become stale during the migration.

## STOP Conditions
- STOP if refreshed main changes a migration surface; re-audit and update both plan files before coding.
- STOP any proposed PR split that leaves an intermediate branch non-functional, disables required production behavior, introduces a migration-only dual-SDK adapter/registration path, or avoids the CI/guardrail/protocol workflows by targeting a feature branch instead of `main`.
- STOP on any unlisted tool/resource/template/prompt entry-field diff, including description whitespace, schema keywords, output schemas, annotations, capabilities beyond logging removal, cursors, MIME types, prompt arguments, or instructions. Top-level discovery order is ignored; nested order is not.
- STOP if any deterministic static/template resource or prompt digest/metadata changes without a path-specific review. Never allow cache hints inside resource `contents`, rely on official URI/MIME auto-fill, double-encode a blob, or generate the sitemap from live/mutable data.
- STOP if the post-swap implementation can regenerate or overwrite the frozen pre-swap baseline. Contract changes require a separate reviewed source-plus-baseline change.
- STOP if any official low-level registration preflight fails. Fix conversion/normalization; do not use generic typed registration or drop the entry.
- STOP if any valid-but-schema-mismatching call no longer reaches the handler and returns its original result plus the stable repository-owned notice.
- STOP if an ordinary tool error loses `isError`, stable code, text/recovery guidance, extra structured fields, or moves to the JSON-RPC error channel.
- STOP if #191/#164 renderer-guidance fields, filtering, fallback, or drift behavior are introduced before the #194 runtime migration PR is merged to `main`. Ship that intentional error-contract change only from the dedicated post-merge follow-up instead of weakening the migration oracle.
- STOP if either list paginates/drops entries or emits an unexpected cursor.
- STOP if legacy initialize gains `listChanged: true`, or any named accepted migration differs from its explicit old/new assertion. Legacy `logging/setLevel` itself has no parity assertion.
- STOP if legacy HTTP or stdio cannot initialize/call, or modern HTTP or stdio requires initialize.
- STOP if `server/discover` is missing, advertises unsupported product features, or omits either target era. Review any drift from the official v1.7.0 five-version transport list.
- STOP if a successful POST changes from HTTP 200 `application/json`, production HTTP emits/accepts `Mcp-Session-Id`, requires sticky state, or does not return 405 for stateless GET/DELETE.
- STOP if a standardized header mismatch reaches the handler, returns a code other than `-32020`, or lacks the request ID.
- STOP if auth/request-limit/readiness/DNS-rebinding/shutdown order changes, graceful stdio cancellation exits non-zero or logs as an operational error, or legacy disconnect cancellation changes without an explicitly approved limitation and capacity-impact note.
- STOP if any SDK-dispatched path loses or duplicates terminal method/tool telemetry, leaves spans open, leaks client metadata between modern requests, or loses tenant/correlation attribution. Pre-dispatch HTTP paths must remain on HTTP telemetry only and emit zero MCP method/tool metrics. For stdio, stop on loss of existing dispatched-request telemetry or unbounded/raw transport logs; add fallback machinery only in response to a named failing parity test. Do not label wholly unparseable frames with an invented method.
- STOP if the full official requirements are pointed directly at the production SigNoz catalog or missing fixtures are hidden with broad baselines.
- STOP if fixture code/surfaces/MRTR/Tasks/Apps enter `cmd/server`, production discovery, README, manifest, or guardrail inventories.
- STOP if a blocking prerelease conformance dependency is not approved; rewrite the acceptance claim rather than silently substituting an older suite.
- STOP release if a representative client fails or a taught contract changes without a linked `SigNoz/agent-skills` companion PR.

## Done Criteria
The runtime migration PR is mergeable when Phases 0–3, 5, and the applicable Phase 6 gates pass. If conformance is split, keep this plan `In Progress` and the issue open; mark it `Done` only after Phase 4 also lands.

Before creating each PR in this delivery series, run at most two independent
read-only reviews of the same surface. For this runtime PR, use Fable 5 high for
the primary overengineering review and Opus 5 xhigh for the final exhaustive
correctness/security review. Record exact model/mode/scope evidence and
findings in the context log, fix every accepted finding, and rerun affected
focused checks plus the PR's complete gates. Grok 4.6 may review a distinct
targeted question later, but must not duplicate either review.

- Mark3 imports and dependency are gone; official Go SDK v1.7.0 (or a separately reviewed newer stable target) owns HTTP and stdio.
- The immutable SDK-free characterization covers every advertised tool schema/description/annotation and every resource/template/prompt descriptor; compact digests cover all deterministic resource/prompt payloads; literal fixtures cover shape-sensitive handlers/errors. Only the named accepted differences remain, and independent inventories, budgets, normalization, structured-result, and coded-error tests pass.
- The production HTTP/stdio × legacy/modern matrix and exact standardized-header tests pass.
- Official frozen `--requirements` sets for both eras pass against the minimal isolated shared-stack fixture in the bounded follow-up with no broad baseline; fixture leakage guards pass. If maintainers approve narrower selected-scenario scope instead, the plan/issue claim is updated before coding.
- Auth/OAuth, request limits, readiness, JSON POST framing, DNS-rebinding protection, approved cancellation behavior, shutdown, panic recovery, and dispatched-request exactly-once observability retain parity; pre-dispatch HTTP failures remain transport-only, while stdio behavior is measured, bounded, and documents malformed-frame termination.
- README, architecture, MCP best practices, guardrails, manifest/server metadata, and CMP-3 statement are synchronized.
- Representative client results are recorded, all repository gates pass, and the plan status is updated to `Done` only after the feature ships.
