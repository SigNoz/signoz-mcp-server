# CLAUDE.md

Go MCP server that gives MCP clients access to SigNoz metrics, logs, traces, alerts, dashboards,
saved views, notification channels, and docs over stdio and stateless streamable HTTP. It calls the
SigNoz backend and query-builder (QB) APIs upstream. Downstream, MCP clients, the
SigNoz/agent-skills repo, and custom agents and clients built on this server consume its tool
contracts.

## Commands

- `make ci`: run before pushing. Runs everything the PR gate runs except the live e2e suite:
  `check-fmt`, `lint` (golangci-lint pinned to CI's version), `check-deps`, `check-build`,
  `test-race`, `check-guardrails`, `check-protocol`, `check-conformance`, `check-e2e-style`, and
  `check-repo-docs`. Each step is also its own target. Needs Node, uv, goimports
  (`make install-goimports`), and GNU `timeout` (on macOS, `brew install coreutils`). CI uses
  Go 1.26; with a newer local Go, run `GOTOOLCHAIN=go1.26.0 make ci` to match it.
- `make test`: all Go tests, verbose. One test: `go test ./internal/handler/tools -run TestName -count=1`.
- `make fmt goimports`: rewrite formatting and imports. `make build` runs both first.
- `make test-e2e`: e2e suite against an ephemeral SigNoz cast by foundry. `make setup-e2e-env`
  keeps the environment and `make test-e2e-reuse` reruns against it. See `tests/README.md`.
- `make docs-index`: rebuild the embedded docs corpus in `internal/docs/assets/`. Diff the
  manifest and commit both files.
- `make check-repo-docs READY=1`: the plan rules CI applies once a PR leaves draft.

## Tests

- Assert observable behavior: tool results, coded errors, structured content, and the requests
  the client sends upstream. A refactor that keeps behavior should leave every test green. Prefer
  exercising the handler or client over unexported helpers.
- No change-detector tests. That means asserting a constant equals its own literal, pinning
  internal call order, or mirroring the implementation step by step. If the only way a test can
  fail is someone editing the code under test, don't write it.
- Before adding a test, search for one that covers the same behavior and extend it. Cover each
  behavior once, at the lowest layer that runs the real code. Name test files and functions after
  the behavior they cover, not the review round or issue that prompted them.
- Exception: drift pins are fine when they guard a contract, such as the wire catalog, `guardrails/`
  budgets, and recorded upstream responses. Say what drift the test catches in its name or a
  one-line comment.

## Changing a Tool, Resource, Prompt, or Configuration Contract

Read `docs/mcp-best-practices.md` before adding or changing a tool, parameter, description,
resource, prompt, error, or result shape, then review the diff against its section 11 checklist
before opening the PR. Record any `MUST` exception, and the reason for any `SHOULD` deviation, in
the PR. Budgets and CI mechanics live in `guardrails/README.md`. In the same PR:

- Every tool input schema exposes a top-level `searchContext` string with the user's original
  question (SCH-5). Don't list it in `required` or describe it as optional. With
  `mcp.WithInputSchema[T]()`, put `SearchContext` on `T`, because typed schemas replace earlier
  `mcp.WithString("searchContext", ...)` options.
- Handler tests cover the happy path and the most important failure path.
- `manifest.json` tool metadata matches the registered handlers. Tool names are checked by
  `internal/mcp-server/integration_test.go`.
- `README.md` tool tables and parameter references match current behavior.
- In `internal/mcp-server/testdata/wire-catalog/`, update only the entries the change intends to
  alter. Never re-record the catalog.
- Update `guardrails/tests.txt` when a `TestGuardrail_*` test is added, removed, or renamed.
- Add an e2e test in `tests/e2e/tests/` when the behavior depends on SigNoz.
- Client-visible text follows `docs/client-visible-writing-style.md`.
- `docs/` has no stale references.
- A breaking change (CMP-1) ships with a compatibility path and a migration note (CMP-2). The
  compatibility path for a retired or renamed input is a coded validation error that names its
  replacement, as for the log `query` alias and flat notification-channel parameters, not a bare
  unknown-field error. The migration note goes in the PR body.
- Top-level integer and boolean parameters also accept their string forms (`intOrStringType` and
  `boolOrStringType`, or `intOrString` and `boolOrString` fields in typed argument structs),
  because some clients send every scalar as a string.
- Each server release targets the latest SigNoz release. Don't name SigNoz versions in tool or
  parameter descriptions, `signoz://` resources, or README tool sections.
- Only expose features the SigNoz UI can render. An agent can save a query into a dashboard or
  saved view, so a request type or panel the UI lacks (for example heatmaps) breaks it there.
- The PR summary lists the doc and metadata updates and says whether SigNoz/agent-skills needs a
  companion change (CMP-3), with a link when it does. Changes to what skills teach need one: a
  renamed or removed tool or parameter, a payload shape, or documented behavior, like the
  `query`→`filter` rename. Additive or internal changes don't.

## Client-Visible Writing Style

When editing tool or parameter descriptions, `signoz://` resources, prompts, server instructions,
or `manifest.json` descriptions, follow `docs/client-visible-writing-style.md`. Skip it for code,
tests, and internal docs.

## Guardrail Changes

- Follow `guardrails/README.md`. Keep policy in `guardrails/policy.go`, the sorted
  `TestGuardrail_*` inventory in `guardrails/tests.txt`, and package-sensitive tests beside their
  packages.
- Never weaken a guardrail merely to pass CI. Document intentional relaxations in the plan and PR
  summary.
- `make check-guardrails` runs the inventory check and the guarded tests. Lint changed workflows
  with `actionlint`.

## External Contracts

Upstream, this server consumes the SigNoz backend and QB APIs. Downstream, MCP clients,
SigNoz/agent-skills, and custom agents and clients parse its tool outputs, error codes, and
`signoz://` resources, so those are contracts too (CMP-1). Fixture tests only prove our code
matches our assumption of a contract. They don't catch drift such as a renamed field, a changed QB
envelope, or a new output shape. For code that parses an upstream response or shapes a tool output:

- Test against reality where you can: an e2e test or a recorded real response, so upstream drift
  fails a test, not a user.
- When tests can't catch it, observability must. Add a metric or WARN log that fires when the
  contract appears violated.
- Fail open, but never fail silent. Pair every fail-open cross-boundary parse with a detectable
  signal.
- Don't hide global upstream failures inside partial item results. SigNoz 401/403 must propagate
  through the shared coded error path (`upstreamError` for tools) so clients can re-authenticate or
  handle permissions.

Delegate verification against a live SigNoz instance to a subagent (Agent tool). That covers
creating, reading, updating, or deleting real alerts, dashboards, or views, and any multi-step API
probing with credentials. The subagent must delete every resource it creates and confirm it's gone,
never print or persist credentials, report which fields round-tripped server-side, and prefer
copying an existing resource's shape over hand-crafting one.

## Done Bar

- Tests cover the happy path and the most important failure path, written to the Tests rules.
- Tool, resource, prompt, or configuration changes: the checklist above, plus a review against
  the `docs/mcp-best-practices.md` section 11 checklist.
- Upstream parsing changes: an e2e test or recorded real response, plus a WARN log or metric
  for contract violations.
- Transport or protocol-runtime changes: prove both protocol eras on every production transport
  (CMP-4 to CMP-6) and run `make check-protocol check-conformance`.
- Docs search or ranking changes: `internal/docs/golden_test.go` still passes its per-style recall
  and precision gates against the baseline.
- Architecture changes: update `docs/architecture.md`.
- `make ci` passes.

## Code Style

- Comments: only for a non-obvious *why* (gotcha, invariant, cross-cutting reason), in 1–3 lines.
- Logging: structured `log/slog`, with variable data in attributes rather than the message. Never
  log API keys, `Authorization` headers, OAuth tokens or encrypted blobs, or other credentials.
- Errors: tool handlers return coded results (`upstreamError`, `errorWithCode`) instead of Go
  errors, so clients get a code and recovery guidance.
- Don't silence the gate: fix the cause instead of adding `//nolint`. When one is unavoidable,
  name the linter and say why, e.g. `//nolint:errcheck // best-effort close`.
- Generated files: rebuild the docs corpus with `make docs-index`. Never hand-edit
  `internal/docs/assets/corpus.gob.gz`.

## Plans

- For non-trivial or multi-session work, check `plans/` and follow `plans/README.md`: one
  `plans/YYYY-MM-DD-<slug>.md` per change, committed with the implementation, with no separate
  context log. Current code, tests, schemas, and `docs/` take precedence over plans.
- Before a PR leaves draft, mark its plan `Done` with the outcome and verification results, or
  explain in Outcome why it merges incomplete. The `checks / repo-docs` job fails ready PRs
  otherwise.
- Legacy `.context.md` / `.plan.md` pairs are history. Continue in-flight work in its pair and
  preserve the append-only discussion log. Don't create new pairs or bulk-convert old ones.

## Git & PR

- Conventional commits and PR titles: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`,
  e.g. `chore(rate-limits): centralize override defaults`.
- One concern per PR. If the description says "also", split it.
- PR body: the problem in a sentence or two, then how you fixed it, then how you verified it. End
  with the model and harness that did the work. Update the body whenever the diff changes.
- Create GitHub issues in `SigNoz/nerve-pod` by default, and in another repo only when asked.
  Include a `Context:` line naming the docs or plans an agent should read, e.g.
  `Context: docs/mcp-best-practices.md`.
- When babysitting a PR, poll checks and comments newer than the last push. Verify each bot
  finding against the source, fix the real ones, and dismiss false positives with a written reason.
  Stay quiet when nothing is new. Stop when checks pass on the latest commit and every bot finding
  has a fix or a reply.
