# Feature: MCP Tier 1 CI Guards — Context & Discussion

## Original Prompt
> Let's start implementing tier 1 of this issue: https://github.com/SigNoz/nerve-pod/issues/34

## Reference Links
- [SigNoz/nerve-pod#34](https://github.com/SigNoz/nerve-pod/issues/34)
- [Tier 1 reconciled plan](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5010045025)
- [Tool descriptions and resources best practices](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5014863452)
- [Wire-contract budget baseline, PR #247](https://github.com/SigNoz/signoz-mcp-server/pull/247)

## Key Decisions & Discussion Log

### 2026-07-19 — Tier 1 implementation started
- Tier 1 is the twelve blocking per-PR guards defined in the reconciled issue comment, not the later protocol/client lanes or advisory reviewer agent.
- PR #247 already implemented the initialized-wire instruction, description, and wide-property budget baseline. This work extends that real `initialize` / `tools/list` boundary instead of adding source-regex checks.
- The first follow-up tranche follows the issue's implementation order: close duplicate-registration and resource-pointer gaps, fix retry eligibility so ambiguous POSTs are never replayed, add JSON-RPC result safety, and measure complete serialized result size.
- The full Tier 1 rollout remains incremental. Semantic catalog diffing, per-tool-class response budgets, and exhaustive coded-error injection need separate focused changes because they require reviewed golden inventories and representative upstream fixtures.
- Duplicate registration is treated as a programming error and fails immediately during server composition. The checked registry is scoped to a Handler/server pair so independently constructed test servers do not share state.
- Retries are method-gated: GET, PUT, and DELETE may retry transient status/transport failures; POST is single-attempt because the upstream APIs do not accept idempotency keys and transmission can be ambiguous.
- JSON-unsafe tool results are converted before transport into a stable coded `INTERNAL_ERROR` result and logged. Invalid UTF-8 and control characters remain valid because Go's JSON encoder safely replaces/escapes them; NaN and infinities are rejected.

## Open Questions
- [ ] What normalized catalog format and explicit migration-note mechanism should authorize intentional semantic contract breaks?
- [ ] What default and maximum serialized-result byte budgets should apply to each tool class without breaking legitimate high-cardinality investigations?
- [ ] Which existing uncoded error paths should be migrated first when implementing the every-tool 401/403 injection matrix?
- [ ] Should the static resource catalog in `manifest.json` expand to every runtime resource in the semantic-diff tranche, or should manifest resource metadata be removed in favor of generated output?

### 2026-07-19 — Multi-agent simplicity and correctness review
- Three independent reviewers covered reuse, code quality/overengineering, and efficiency across the full diff.
- The original method-only retry decision above was too broad: it also removed retries from read-only query APIs that use POST only to carry a body (`services`, `top_operations`, Query Builder v5, and metrics treemap). Replay eligibility is now operation-aware: those read-only POSTs may retry, while create/test/mutating POSTs remain single-attempt.
- Single-attempt POST bodies are no longer copied into a retry buffer.
- Duplicate-registration tracking was flattened from nested maps to one composite-key set. A source AST guard now fails if production code calls the SDK registration methods outside the checked wrapper, closing the easy bypass path without introducing a server facade.
- Resource-pointer extraction now preserves dotted/query/escaped URI characters before exact lookup; the earlier restricted character class could have false-passed a truncated pointer.
- The broad ClickHouse schema initialization was removed from the shared test-server builder and kept only in the resource-content integrity test that needs it.
- Complete result marshaling remains on the request path. It adds one encode before mcp-go encodes for transport, but it is the least-complex reliable way to prevent NaN/infinity from becoming transport 500s and supplies exact result-size telemetry. A custom serializer or reflection walker would be more complex and easier to drift; revisit only with production evidence or a benchmark showing material cost.

### 2026-07-19 — Fable overengineering review follow-up
- A focused Fable 5 review judged the tranche proportionate and found no high-priority overengineering. The accepted cleanup findings were implemented without changing the Tier 1 contracts.
- The retry override is now POST-specific, so callers cannot accidentally mark an arbitrary HTTP method replay-safe. The internal request path accepts the byte slices every production caller already owns and creates a fresh reader per attempt, removing the redundant `io.ReadAll` copy while keeping mutating POSTs single-attempt.
- Complete result validation and byte measurement now call mcp-go's existing `CallToolResult.MarshalJSON` implementation directly. This preserves the exact SDK result representation and JSON-safety failure behavior while avoiding a redundant outer `json.Marshal` layer. Structured-content size coverage remains pinned in the telemetry test.
- The AST guard's jsonschema exemption now requires the exact `schema_compat.go` path as well as the `compiler.AddResource` call shape, preventing unrelated receivers named `compiler` elsewhere from bypassing checked MCP registration.
- Per-server registration scoping remains unchanged: the review was split on whether it was speculative, and its bounded pointer-key cost is smaller than the correctness risk of conflating legitimate registrations on distinct SDK servers.

### 2026-07-19 — Dedicated guardrail CI review surface
- The ordinary required `test / go` job already executes every Go test, but it does not make guardrail changes visually distinct during review. A contributor can also rename or remove a guarded test while changing production code, so a passing general test job is not a sufficient review surface by itself.
- The tranche's twelve guardrail tests now share the `TestGuardrail_` prefix and run in a dedicated `guardrails / contract` workflow.
- The workflow pins the exact discovered test names before executing them. Adding, removing, or renaming a guardrail therefore fails the job until the workflow inventory is deliberately updated, concentrating that review decision in `.github/workflows/guardrails.yaml`.
- The workflow follows the repository's existing internal-PR and `safe-to-test` fork policy, checks out the reviewed PR head, uses a read-only token, and runs without inherited secrets.
- CODEOWNERS protection was considered and explicitly declined; the dedicated workflow and central inventory provide visibility, while the existing repository review rules remain the approval mechanism.

### 2026-07-19 — Central guardrail policy directory
- Guardrail test implementations cannot all move into one Go directory without losing access to unexported retry, registration, middleware, and server-composition helpers or exposing production internals solely for tests.
- The review surface is centralized instead: `guardrails/policy.go` owns shared limits, aliases, and grandfathered exceptions; `guardrails/tests.txt` owns exact suite membership; and `guardrails/README.md` documents invariants and the intentional-change procedure.
- The dedicated workflow now reads `guardrails/tests.txt` and rejects unsorted, duplicate, missing, or unexpected test names before executing the suite.
- `CLAUDE.md` now instructs contributors and coding agents to treat guardrail edits as policy changes, document intentional relaxations, avoid CI-only weakening, and run both focused and full verification.

### 2026-07-19 — Fable and PR review follow-up
- Fable High and the unresolved PR review agreed that the registration source guard missed SDK registration methods passed as function values. The scanner now checks every matching selector expression, and its existing guarded test includes a method-value regression probe.
- The dedicated guardrail job does not need secrets, so it now runs only on the unprivileged `pull_request` event for internal, fork, and Dependabot PRs. This removes the stale `safe-to-test` label path and avoids checking out fork code in a privileged `pull_request_target` workflow.
- The repository's existing secret-dependent CI workflow keeps its current `safe-to-test` policy; changing that separate workflow is outside this guardrail tranche.

### 2026-07-20 — Serialized-schema byte ceiling removed
- The user explicitly chose to remove the total serialized-schema byte guardrail instead of raising or grandfathering it. Tool-name, description, top-level property inventory, and schema-nesting protections remain.
- This policy change does not alter runtime tool-call payload limits: streamable HTTP still uses the separately configurable `MCP_MAX_REQUEST_BYTES` limit, and stdio does not use that middleware.
