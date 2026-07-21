# Plan: MCP Tier 3 Best-Practices Guide

## Status
In Progress

## Context
Tier 1 puts deterministic contract invariants behind a central CI review surface. Tier 2 adds real-server Inspector compatibility. Tier 3 documents the qualitative design decisions that deterministic checks cannot judge: whether an agent can choose the right surface, make a valid first call, recover from errors, interpret results, and survive compatible contract evolution.

The guide will be the repository's stable MCP authoring standard and the future source rubric for an advisory reviewer agent. It must be concrete enough to review against a diff without duplicating numeric guardrails, CI mechanics, or per-tool documentation.

Use `MUST`, `SHOULD`, and `MAY` normatively. Record any justified exception in the feature context or PR. Keep stable rubric IDs and the compact checklist in this same guide so the later reviewer agent consumes the human-reviewed source directly.

## Goals
- Create one repository-owned guide for every client-visible MCP surface.
- Turn the internal agent-first principles into concrete, reviewable rules.
- Keep measurable enforcement in `guardrails/` and qualitative judgment in the guide.
- End with a compact checklist and stable rubric identifiers reusable by the later reviewer agent.
- Add only a concise pointer in `CLAUDE.md`.

## Proposed Document Structure

### 1. Scope and agent-first principles
- Define which surfaces are governed: names, schemas, descriptions, annotations, results, errors, resources, resource templates, prompts, and server instructions.
- State the delivery-universality rule: the more critical a rule is to a correct first call, the more universal and inline its placement must be.
- Treat reasonable or recurring agent mis-calls as evidence that the advertised contract needs improvement, without promising to accept ambiguous or unsafe input.
- Prefer fewer, sharper surfaces and measure real use before adding new ones.
- Point to `guardrails/README.md` for numeric enforcement and workflow mechanics.

### 2. Choosing, naming, and annotating MCP surfaces
- Add a tool for a distinct agent task, not merely for a distinct backend endpoint; compare an existing-tool parameter, resource, or prompt first.
- Use agent/domain vocabulary and explain unavoidable backend terminology instead of imposing a blanket jargon ban.
- Require clear nearest-neighbor selection boundaries.
- Treat annotations as behavior-backed safety claims, not authorization or trust boundaries.
- Collapse predictable read round-trips where useful; keep mutations atomic, observable, and accurately classified for retry/idempotency.

### 3. Surface placement
- Include a compact table assigning content to tool descriptions, parameter descriptions/schema, output schemas/results, annotations, server instructions, resources, prompts/skills, and execution errors.
- Keep correctness-critical first-call rules inline; resources provide progressive disclosure, never the only route to immediate repair.
- Gate only for authentication, consent, or destructive action. Do not block a tool call on optional documentation fetches.

### 4. Input schemas and parameter behavior
- Be lenient across explicitly advertised equivalent representations and strict about meaning: normalize safe number/string, boolean/string, and similar forms only when the schema says they are accepted.
- Reject unparseable, ambiguous, or semantically invalid values with field-specific recovery guidance; never silently replace an invalid supplied value with a default.
- Keep schema, handler validation, defaults, examples, and requiredness aligned.
- Distinguish MCP-owned stable enums from workspace- or backend-evolving values that should use discovery guidance.
- Include the repository's `searchContext` invariant without duplicating implementation details unnecessarily.
- Prefer agent-shaped flat inputs over exposing backend payload structure.

### 5. Tool descriptions and examples
- Optimize for correct selection and a successful first call, not maximal detail or minimum byte count.
- Lead with intent/result; name only the nearest confusing alternative; include critical pre-call caveats and exact resource/discovery pointers.
- Put field-local format, units, constraints, dependencies, defaults, and examples on the parameter/schema surface.
- Require realistic, executable examples for every non-trivial tool/workflow and for grammars, filters, or ambiguous parameter formats; do not require an example on every self-evident field.
- Do not include routes, implementation history, volatile catalogs, or guarantees the handler does not enforce.

### 6. Resources, resource templates, and server instructions
- Use resources for long grammars, schemas, workflows, catalogs, and complete examples; use templates only for genuinely parameterized content.
- Make discovery metadata state what the surface contains, when to use it, and which workflows it supports.
- Keep URIs stable, resolvable, authenticated where needed, correctly typed, non-empty, and bounded.
- Put purpose and warnings before detail; record provenance/freshness for mutable content.
- Use server instructions only for routing or policies shared across several tools.

### 7. Results, pagination, and partial success
- Return predictable envelopes and reusable handles such as IDs and web URLs when they remove a likely follow-up lookup.
- Keep read results writable back without undocumented reshaping where the domain supports read-modify-write.
- Make clamps, truncation, pagination, and incomplete data explicit in machine-readable metadata with the next step or cursor.
- Define when per-item partial success is legitimate. Global authentication, permission, or upstream availability failures must remain top-level coded errors.
- Do not silently reshape passthrough upstream results into a misleading local contract.

### 8. Errors and recovery
- Return stable machine-readable error categories and concise agent-readable text.
- Name the failing field or operation, explain the problem, state accepted forms or alternatives, and give the smallest next action.
- Put the immediate correction inline, then link a resource for deeper guidance; never make resource retrieval the only recovery path.
- Propagate authentication and permission failures through the shared top-level path.
- Preserve recognized SigNoz backend messages, documentation URLs, suggestions, detail messages, and retry hints faithfully in bounded, safe form. Stable MCP codes and tool-specific recovery supplement rather than replace that guidance.
- Pair fail-open cross-boundary parsing with a WARN log or metric; distinguish expected agent mistakes from server/upstream faults without hiding either.

### 9. Safety and security
- Authenticate and resolve tenant scope on every request; do not rely on cached caller identity or allow cross-tenant identifiers to leak data.
- Keep destructive actions explicit and narrowly scoped; make side effects observable.
- Tie retry eligibility to actual idempotency, not merely HTTP method or annotation wording.

### 10. Contract evolution and synchronized documentation
- Treat the initialized advertised surface as a stable executable promise.
- List breaking changes: removal/rename, type narrowing, optional-to-required, enum removal, annotation safety changes, result-envelope changes, and error-code changes.
- Require an explicit compatibility path and migration note for intentional breaks. Retain compatibility aliases until an explicit, evidence-backed deprecation/removal decision documents the migration risk and path; do not promise aliases forever.
- Keep production registration, validators, README, `manifest.json`, `docs/`, tests, and companion `SigNoz/agent-skills` teaching synchronized when their contracts overlap.

### 11. Evaluation and PR review checklist
- Keep deterministic counts, schema compilation, URI integrity, and protocol compatibility in CI.
- Evaluate selection- or recovery-critical guidance changes with direct, indirect, and negative prompts; check the affected tool choice, arguments, clarification, abstention, and failed-call recovery behaviors using comparable model/catalog sessions.
- Change broad guidance only with evidence from evals, recurring failures, or production use; do not canonize dated usage statistics in the guide.
- End with a one-screen checklist grouped by stable rubric IDs: surface choice, schema, description, resources/instructions, results, errors, safety/security, and compatibility.
- Future reviewer findings cite rubric ID, changed line, evidence, client impact, repair, and confidence; stay diff-local, cap noisy output, and allow “no findings.”

## Explicit Non-goals
- Copying numeric budgets, current client-version tables, exception inventories, or CI commands from `guardrails/`.
- Building the reviewer workflow, running models in CI, or promoting model findings to blocking status.
- Requiring a new abstraction, code generator, snapshot, completion surface, or guardrail merely because the guide mentions a principle.
- Maintaining a per-tool catalog or duplicating README/manifest documentation.
- Requiring examples/default prose on every parameter regardless of complexity.
- Encoding organization-specific secret, retention, or deployment values in a public design guide.
- Adding dedicated prompt-authoring, external-fetch provenance, or telemetry-retention policy beyond the placement and security rules retained in this tranche.

## Expected Files
- `docs/mcp-best-practices.md` — normative principles, area-specific rules, placement table, and compact review checklist.
- `CLAUDE.md` — concise pointer requiring contract-touching changes to follow the guide and its checklist.
- `plans/mcp-tier-3-best-practices.context.md` — append-only discussion and decision history.
- `plans/mcp-tier-3-best-practices.plan.md` — current design and verification plan.

No `guardrails/`, workflow, runtime code, `manifest.json`, or companion `SigNoz/agent-skills` change is expected from the documentation-only tranche.

## Verification
- Check every source principle and Tier 3 issue addition maps to one body section or an explicit non-goal.
- Check every checklist item is directly traceable to a body rule and can be evaluated against a changed diff.
- Verify all referenced repository paths, helper names, MCP URIs, and examples against the current initialized surface before committing the guide.
- Run Markdown/link checks available in the repository and `git diff --check`.
- Confirm the `CLAUDE.md` addition stays concise and does not duplicate the guide.
- Record the README/`docs/`/`manifest.json` and companion agent-skills sync outcome in the PR summary.
