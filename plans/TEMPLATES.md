# Plan File Templates

Templates for the `plans/<feature>.context.md` + `plans/<feature>.plan.md` pair
described in `CLAUDE.md` → Feature Planning Convention.

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
