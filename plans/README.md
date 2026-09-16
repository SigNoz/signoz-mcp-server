# Implementation Plans

Use one plan to record a non-trivial change's intent, constraints, decisions, and verified outcome.
Plan risky, multi-session, architectural, state, or external-contract changes; skip typos,
formatting, mechanical dependency updates, and obvious narrow fixes.
Current code, tests, schemas, and `docs/` take precedence.

## Lifecycle

1. Search `plans/` before investigating; reuse the plan for the same change.
2. For new work, create `plans/YYYY-MM-DD-<slug>.md` on the implementation branch and commit it
   with the change. No nested directories; issue/PR links belong in metadata.
3. Use `Planning`, `In Progress`, `Done`, or `Abandoned`. Keep approach and files current;
   date material decisions and their rationale. Resolve questions in Context.
4. Before merge, mark `Done`, record verification and outcome, and link deferred work to issues.
   For `Abandoned`, explain why work stopped.
5. Promote architectural truth to `docs/` or an ADR. Exclude secrets and raw transcripts;
   distinguish unverified claims from facts.

## Legacy Plans

Keep existing `.context.md` / `.plan.md` pairs; do not rename, merge, or bulk-convert them.
Continue in-flight work in its pair: update the plan, append dated discussion entries, and resolve
Open Questions inline. Preserve earlier entries. `TEMPLATES.md` points here for older references.

## Template

```markdown
# Plan: <Name>

Status: Planning
Issue:
PR:

## Context

<Why, evidence, and open questions.>

## Approach

<Approach and constraints, without prewritten code or step-by-step choreography.>

## Files to Modify

- `path/to/file.go` — change

## Key Decisions

### YYYY-MM-DD — <decision>

- Decision:
- Rationale:
- Alternatives rejected, if relevant:

## Reference Links

- [Title](url)

## Verification

<Commands, results, and gaps. For risky changes, include E2E; follow CLAUDE.md's live-verification
delegation rules. Link deferred checks to issues.>

## Outcome

<What shipped, deviations, and deferred work with issue links.>
```
