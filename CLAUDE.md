# CLAUDE.md — Development Conventions

## Feature Planning Convention

For every non-trivial feature, maintain a file pair in `plans/` (git-ignored, own git repo):

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

## Documentation & Metadata Sync Checklist

When adding, renaming, or removing MCP tools/resources/configuration, update docs and metadata in the same PR.

- Every MCP tool input schema must expose a top-level `searchContext` string with the user's original question/search text. Do not put `searchContext` in the JSON Schema `required` list or describe it as optional. For tools using `mcp.WithInputSchema[T]()`, put `SearchContext` on `T` itself because typed schemas replace earlier `mcp.WithString("searchContext", ...)` options.
- Update `README.md` tool tables/parameter references to match current behavior.
- Update `manifest.json` tool metadata (`tools`, descriptions, and related fields) to match registered handlers.
- Review any user-facing docs under `docs/` for stale references.
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
