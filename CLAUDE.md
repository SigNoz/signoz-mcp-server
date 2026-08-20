# CLAUDE.md — Development Conventions

Consult `docs/` (architecture, MCP best practices) and `plans/` (per-feature context and plans) when you need background on a subsystem or an in-flight feature.

## Feature Planning Convention

For every non-trivial feature, maintain a file pair in `plans/` and commit it alongside the feature PR:

```
plans/
├── <feature>.context.md   ← prompt, links, open questions, discussion log
└── <feature>.plan.md      ← implementation plan
```

### Rules

1. **Before implementing**, check `plans/` for an existing context file for the feature.
2. **After each brainstorm exchange**, append a dated entry to the discussion log in `.context.md`.
3. **When the plan changes**, rewrite the relevant section in `.plan.md` and note the change with a dated entry in `.context.md`.
4. **Mark open questions as resolved** (with the answer inline) when they are settled in discussion.
5. **The discussion log in `.context.md` is append-only**: never rewrite or delete prior log entries; it is the audit trail of why decisions were made. Only the Open Questions checklist may be updated in place (rule 4).
6. **`.plan.md` is rewritten freely**: it always reflects current thinking, not history.
7. Add a `## Status` line at the top of every `.plan.md`:
   - `Planning`: actively being designed
   - `In Progress`: implementation underway
   - `Done`: shipped

File templates for both files live in `plans/TEMPLATES.md`.

## Git & PR

- Conventional commits: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`.
- PR titles must follow the same conventional-commit format, e.g.
  `chore(rate-limits): centralize override defaults`.
- Create GitHub issues in `SigNoz/nerve-pod` by default. Only use another repo when the user explicitly asks for that specific repo.
- Keep PR body in sync with actual changes.

## Code Style

- Avoid long inline code comments unless needed; keep comments concise and non-redundant.

## Client-Visible Writing Style

Applies to every string an MCP client or agent reads: tool and parameter descriptions, `signoz://` resource content (`pkg/alert`, `pkg/dashboard`, `pkg/views`, `pkg/promql`, `pkg/querybuilder`, `pkg/metricsrules`), prompts (`pkg/prompts`), server instructions (`pkg/instructions`), and `manifest.json` descriptions. These strings are instructions an agent executes, not prose that persuades a reader; keep them plain, concrete, and testable. Model-drafted text arrives with a recognizable set of tics; strip them before opening the PR.

- **No em dashes in prose.** Use a colon for a label gloss, a semicolon or period between independent clauses, parentheses for an aside, a comma for brief apposition. Two separator uses stay allowed: fixed-width table columns in guides (`has_error  bool  — whether the span has an error`) and markdown headings (`## 3. metric_promql — PromQL rule`).
- **Banned words.** delve, foster, leverage, utilize, facilitate, empower, streamline, robust, seamless, cutting-edge, paradigm shift, game changer, transformative, elevate, embark, supercharge, harness, ever-evolving, meticulous, intricate, paramount, realm, tapestry, beacon, multifaceted. Say the plain thing instead: "use", "cut", "reliable".
- **Cut empty phrases.** it's worth noting, it's important to note, at the end of the day, when it comes to, at its core, in today's world, the reality is, in order to, going forward. Delete them and state the instruction.
- **No AI-slop rhetoric.** Skip binary contrasts ("it's not X, it's Y"), faux-insight setups ("what most people miss"), colon reveals ("the detail that makes it work: ..."), importance puffery ("plays a vital role"), weasel attribution ("experts agree"), rhetorical questions, and summary-recap endings. Make the claim directly. Plain label colons ("One of: ...", "Example: ...") are fine.
- **No punchy fragments, aphorisms, or templated lead-ins.** State rules as rules. "Note: This panel is best used when..." repeated across five panels became "Best for: ...".
- **No superficial `-ing` analysis.** Drop trailing "highlighting X", "revealing Y", "making it Z" clauses. State the mechanism or the consequence.
- **Be concrete.** Name the tool, field, default, or limit. "Are capped at the top 100" beats "use top 100"; "operations ranked by p99 latency" beats "operation landscape".
- **Active voice, direct verbs.** "The server rejects a partial body", not "a partial body is rejected".
- **Formatting follows content.** No emoji in headings, no bold sprinkled mid-sentence for emphasis, no bullet list where two sentences read better.
- **Keep concept handles verbatim.** Phrases like "same still-current prepared operation" and "full replacement" are anchored across tool descriptions, server instructions, and guardrail tests. Reword a handle everywhere at once or not at all; reordering around a pinned handle is fine.
- **Out of scope.** Strings generated from upstream stay as generated: the dashboard input schemas (`internal/handler/tools/schemas/`, from the SigNoz OpenAPI spec) and `dashboard_templates.json` titles.

Wording changes to these strings are MCP contract changes: sync the wire-catalog fixtures (mirror textual edits; recompute derived `size`/`serializedLength`/`sha256` through the oracle helpers, never re-record the catalog) and stay within the guardrail byte budgets. Reviewers should treat any pattern above in a diff as a change request; `grep -n '—'` over changed files is the fastest single check.

## Local Verification

- Tests: `go test ./...` (`make test` runs them verbose).
- Build: `go build ./cmd/server` (`make build` also rewrites formatting/imports).
- Formatting/imports: `make fmt goimports`.

## MCP Contract Changes

Changes to client-visible MCP surfaces must follow `docs/mcp-best-practices.md`
and its section 11 review checklist. Deterministic budgets and CI mechanics stay
in `guardrails/README.md`. The companion **SigNoz/agent-skills** repo depends on
these contracts; audit it on every contract change (CMP-3; see the sync
checklist below).

## Guardrail Changes

- Follow `guardrails/README.md`; keep policy in `guardrails/policy.go`, the sorted `TestGuardrail_*` inventory in `guardrails/tests.txt`, and package-sensitive tests beside their packages.
- Never weaken a guardrail merely to pass CI. Document intentional relaxations in the feature context log and PR summary.
- Run the workflow lint, focused guardrail suite, and full test suite documented in `guardrails/README.md` before handoff.

## Documentation & Metadata Sync Checklist

When adding, removing, renaming, or otherwise changing a client-visible MCP tool, resource, prompt, or configuration contract, update docs and metadata in the same PR.

- Every MCP tool input schema must expose a top-level `searchContext` string with the user's original question/search text. Do not put `searchContext` in the JSON Schema `required` list or describe it as optional. For tools using `mcp.WithInputSchema[T]()`, put `SearchContext` on `T` itself because typed schemas replace earlier `mcp.WithString("searchContext", ...)` options.
- Update `README.md` tool tables/parameter references to match current behavior.
- Update `manifest.json` tool metadata (`tools`, descriptions, and related fields) to match registered handlers.
- Review any user-facing docs under `docs/` for stale references.
- The companion **SigNoz/agent-skills** repo depends on this server's tool contracts. Apply CMP-3 in `docs/mcp-best-practices.md`: state in the PR summary whether agent-skills needs a companion change, and link that PR when needed. Changes to the contract skills teach (a renamed/removed tool or parameter, payload shape, documented behavior, like the `query`→`filter` rename) require a skills update; additive or internal changes do not.
- New or edited client-visible text follows **Client-Visible Writing Style** above.
- Mention these doc updates explicitly in the PR summary.

## End-to-End / Live Verification

Verifying against a live SigNoz instance (creating/reading/updating/deleting real alerts, dashboards, or views, or any multi-step API probing with credentials) should be delegated to a subagent (Agent tool), not run inline. The subagent must: delete every resource it creates and confirm it's gone; never print or persist credentials; report which fields round-tripped server-side; and prefer copying an existing resource's shape over hand-crafting one.

## Testing across external contracts

This server consumes the SigNoz backend / query-builder (QB) API upstream and produces tool outputs that MCP clients consume downstream. Fixture-based unit tests only prove our code matches our *assumption* of those contracts; they do not catch upstream drift (a renamed field, a changed QB envelope, a new output shape). For any code that parses an upstream response or shapes a tool output:

- **Test against reality where you can.** Beyond fixtures, add a periodic/integration test against a live SigNoz instance (or a recorded real response) so upstream drift fails a test, not a user.
- **When tests can't catch it, observability must.** Add a metric or WARN log that fires when the contract appears violated. Silent degradation that no test and no signal can catch is the failure mode to design against.
- **Do not hide global upstream failures inside partial item results.** SigNoz 401/403 must propagate through the shared coded error path (`upstreamError` for tools) so clients can re-authenticate or handle permissions.
- **Fail open, but never fail silent.** Prefer fail-open cross-boundary parsing, always paired with a detectable signal.
