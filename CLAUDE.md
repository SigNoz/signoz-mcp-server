# CLAUDE.md — Development Conventions

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
5. **`.context.md` is append-only** — never rewrite or delete prior entries. It is the audit trail of why decisions were made.
6. **`.plan.md` is rewritten freely** — it always reflects current thinking, not history.
7. Add a `## Status` line at the top of every `.plan.md`:
   - `Planning` — actively being designed
   - `In Progress` — implementation underway
   - `Done` — shipped

## Git & PR

- Conventional commits: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`.
- PR titles must follow the same conventional-commit format, e.g.
  `chore(rate-limits): centralize override defaults`.
- Create GitHub issues in `SigNoz/nerve-pod` by default. Only use another repo when the user explicitly asks for that specific repo.
- Keep PR body in sync with actual changes.

## Code Style

- Avoid long inline code comments unless needed; keep comments concise and non-redundant.

## Documentation & Metadata Sync Checklist

When adding, renaming, or removing MCP tools/resources/configuration, update docs and metadata in the same PR.

- Every MCP tool input schema must expose a top-level `searchContext` string with the user's original question/search text. Do not put `searchContext` in the JSON Schema `required` list or describe it as optional. For tools using `mcp.WithInputSchema[T]()`, put `SearchContext` on `T` itself because typed schemas replace earlier `mcp.WithString("searchContext", ...)` options.
- Update `README.md` tool tables/parameter references to match current behavior.
- Update `manifest.json` tool metadata (`tools`, descriptions, and related fields) to match registered handlers.
- Review any user-facing docs under `docs/` for stale references.
- Check whether the companion **SigNoz/agent-skills** repo needs a matching change. Skills (e.g. `signoz-creating-alerts`, `signoz-creating-dashboards`) deliberately defer field/schema detail to the MCP server, so they need updating only when a change alters the **tool contract they teach** — a renamed/removed tool or parameter, a changed payload shape, or changed documented behavior (as the `query`→`filter` rename did). Additive or internal changes that don't alter that contract (e.g. surfacing already-authored field descriptions) need no skills change. State the outcome of this check in the PR summary, and link the companion agent-skills PR when one is needed.
- Mention these doc updates explicitly in the PR summary.

### File templates

**`<feature>.context.md`**
```markdown
# Feature: <Name> — Context & Discussion

## Original Prompt
> <paste full user prompt here>

## Reference Links
- [Title](url)

## Key Decisions & Discussion Log
### YYYY-MM-DD — <topic>
- <decision or note>

## Open Questions
- [ ] <question>
```

**`<feature>.plan.md`**
```markdown
# Plan: <Name>

## Status
Planning

## Context
<why this change is needed>

## Approach
<implementation details>

## Files to Modify
- `path/to/file.go` — what changes

## Verification
<how to test end-to-end>
```

## End-to-End / Live Verification

Verifying against a live SigNoz instance — creating/reading/updating/deleting real alerts, dashboards, or views, or any multi-step API probing with credentials — should be delegated to a subagent (Agent tool), not run inline. The subagent must: delete every resource it creates and confirm it's gone; never print or persist credentials; report which fields round-tripped server-side; and prefer copying an existing resource's shape over hand-crafting one.

## Testing across external contracts

This server sits between external parties: it consumes the SigNoz backend / query-builder (QB) API (upstream) and produces tool outputs that MCP clients and the AI assistant consume (downstream). Unit tests that assert behavior against hard-coded fixtures only prove our code matches our *assumption* of those contracts — they do not catch the contract drifting out from under us (a renamed field, a changed QB response envelope, a new route/output shape).

For any code that parses an upstream response or shapes a tool output:

- **Pin the contract, and test against reality where you can.** Beyond fixture-based unit tests, add a periodic/integration test against a live SigNoz instance (or a recorded real response) so upstream drift fails a test, not a user. Per-PR fixture tests are necessary but not sufficient.
- **When tests can't catch it, observability must.** If a break only manifests against real data, add a metric or WARN log that fires when the contract appears violated (e.g. a passthrough enrichment that found rows but could not locate the expected field). Silent degradation that no test and no signal can catch is the failure mode to design against.
- **Fail open, but never fail silent.** Prefer fail-open behavior for cross-boundary parsing, but always pair it with a detectable signal so "fail-open" does not become "fail-silent."
