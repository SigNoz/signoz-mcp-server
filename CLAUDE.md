# CLAUDE.md — Development Conventions

Consult `docs/` (architecture, MCP best practices) and `plans/` (implementation records) when you need background on a subsystem or an in-flight feature.

## Feature Planning Convention

- For non-trivial or multi-session work, check `plans/` for related work and follow
  [plans/README.md](plans/README.md). Commit the plan alongside the implementation.
- Use one `plans/YYYY-MM-DD-<slug>.md` file per new change; keep context, dated decisions,
  verification, and outcome together. Do not create a separate context log.
- Keep the approach, affected files, and decisions accurate while implementing. Current code,
  tests, schemas, and canonical `docs/` take precedence over plans.
- Before merging, mark the plan `Done` and record the actual outcome and verification results.
- Existing `.context.md` / `.plan.md` pairs remain as history. Continue related in-flight work
  in its existing pair and preserve the append-only discussion log; do not bulk-convert old files.

## Git & PR

- Conventional commits: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`.
- PR titles must follow the same conventional-commit format, e.g.
  `chore(rate-limits): centralize override defaults`.
- Create GitHub issues in `SigNoz/nerve-pod` by default. Only use another repo when the user explicitly asks for that specific repo.
- Keep PR body in sync with actual changes.

## Code Style

- Avoid long inline code comments unless needed; keep comments concise and non-redundant.

## Client-Visible Writing Style

When editing tool or parameter descriptions, `signoz://` resources, prompts, server instructions, or `manifest.json` descriptions, follow [`docs/client-visible-writing-style.md`](docs/client-visible-writing-style.md). Skip it for code, tests, and internal docs.

## Local Verification

- Tests: `go test ./...` (`make test` runs them verbose).
- Build: `go build ./cmd/server` (`make build` also rewrites formatting/imports).
- Formatting/imports: `make fmt goimports`.
- E2E (live SigNoz via foundry): `make test-e2e` (`make test-e2e-reuse` reruns against the cached environment; see `tests/README.md`).
- E2E Python style: `cd tests && uv run ruff format --check . && uv run ruff check .`

## MCP Contract Changes

Changes to client-visible MCP surfaces must follow `docs/mcp-best-practices.md`
and its section 11 review checklist. Deterministic budgets and CI mechanics stay
in `guardrails/README.md`. The companion **SigNoz/agent-skills** repo depends on
these contracts; audit it on every contract change (CMP-3; see the sync
checklist below).

## Guardrail Changes

- Follow `guardrails/README.md`; keep policy in `guardrails/policy.go`, the sorted `TestGuardrail_*` inventory in `guardrails/tests.txt`, and package-sensitive tests beside their packages.
- Never weaken a guardrail merely to pass CI. Document intentional relaxations in the plan's Key Decisions section (or the existing context log for legacy plans) and PR summary.
- Run the workflow lint, focused guardrail suite, and full test suite documented in `guardrails/README.md` before handoff.

## Documentation & Metadata Sync Checklist

When adding, removing, renaming, or otherwise changing a client-visible MCP tool, resource, prompt, or configuration contract, update docs and metadata in the same PR.

- Every MCP tool input schema must expose a top-level `searchContext` string with the user's original question/search text. Do not put `searchContext` in the JSON Schema `required` list or describe it as optional. For tools using `mcp.WithInputSchema[T]()`, put `SearchContext` on `T` itself because typed schemas replace earlier `mcp.WithString("searchContext", ...)` options.
- Update `README.md` tool tables/parameter references to match current behavior.
- Update `manifest.json` tool metadata (`tools`, descriptions, and related fields) to match registered handlers.
- Review any user-facing docs under `docs/` for stale references.
- The companion **SigNoz/agent-skills** repo depends on this server's tool contracts. Apply CMP-3 in `docs/mcp-best-practices.md`: state in the PR summary whether agent-skills needs a companion change, and link that PR when needed. Changes to the contract skills teach (a renamed/removed tool or parameter, payload shape, documented behavior, like the `query`→`filter` rename) require a skills update; additive or internal changes do not.
- New or edited client-visible text follows [`docs/client-visible-writing-style.md`](docs/client-visible-writing-style.md).
- Mention these doc updates explicitly in the PR summary.

## End-to-End / Live Verification

Verifying against a live SigNoz instance (creating/reading/updating/deleting real alerts, dashboards, or views, or any multi-step API probing with credentials) should be delegated to a subagent (Agent tool), not run inline. The subagent must: delete every resource it creates and confirm it's gone; never print or persist credentials; report which fields round-tripped server-side; and prefer copying an existing resource's shape over hand-crafting one.

## Testing across external contracts

This server consumes the SigNoz backend / query-builder (QB) API upstream and produces tool outputs that MCP clients consume downstream. Fixture-based unit tests only prove our code matches our *assumption* of those contracts; they do not catch upstream drift (a renamed field, a changed QB envelope, a new output shape). For any code that parses an upstream response or shapes a tool output:

- **Test against reality where you can.** Beyond fixtures, add a periodic/integration test against a live SigNoz instance (or a recorded real response) so upstream drift fails a test, not a user.
- **When tests can't catch it, observability must.** Add a metric or WARN log that fires when the contract appears violated. Silent degradation that no test and no signal can catch is the failure mode to design against.
- **Do not hide global upstream failures inside partial item results.** SigNoz 401/403 must propagate through the shared coded error path (`upstreamError` for tools) so clients can re-authenticate or handle permissions.
- **Fail open, but never fail silent.** Prefer fail-open cross-boundary parsing, always paired with a detectable signal.
