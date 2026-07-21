# Feature: MCP Tier 3 Best-Practices Guide — Context & Discussion

## Original Prompt
> While tier 2 gets merged, let's work on tier 3.
> I am planning to create a best-practices doc and then mention it in claude.md
> Let's finalize what goes inside that best practices doc.
>
> It should have different sections covering different areas, eg: tool descriptions and resources section, etc
> Based on [MCP Best practices](https://app.notion.com/p/signoz/MCP-Best-practices-38cfcc6bcd198071b904e3f2d911487d)
>
> Independently ask claude fable 5 as well

## Reference Links
- [Internal MCP Best practices](https://app.notion.com/p/signoz/MCP-Best-practices-38cfcc6bcd198071b904e3f2d911487d)
- [SigNoz/nerve-pod#34](https://github.com/SigNoz/nerve-pod/issues/34)
- [Reconciled Tier 3 reviewer plan](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5010045025)
- [Tool descriptions and resources best practices](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5014863452)
- [Tier 1 guardrail policy](../guardrails/README.md)

## Key Decisions & Discussion Log

### 2026-07-21 — Tier 3 planning started
- Tier 3 starts with a repository-owned MCP best-practices guide and a concise `CLAUDE.md` pointer. The guide will later be the qualitative rubric source for the advisory reviewer agent described in nerve-pod#34.
- This is documentation-governance planning while the Tier 2 Inspector PR is pending. No reviewer workflow, model call in CI, new runtime abstraction, or additional guardrail is part of this step.
- Work moved to a branch based on `origin/main` so the pending Tier 2 PR is not modified or made a dependency of the documentation design.

### 2026-07-21 — Source-page synthesis
- The internal Notion source supplies the core agent-first principles: treat reasonable mis-calls as contract feedback; make errors recoverable; normalize equivalent input representations; provide reliable defaults and realistic examples; prefer fewer, sharper tools; collapse predictable read round-trips while keeping mutations atomic; use domain language; keep the advertised contract stable; and make the server teach its own map.
- “Accept all and normalize internally” needs a safety qualification. The proposed rule is: accept equivalent representations when the schema advertises them, but reject ambiguous or semantically invalid values with an actionable error. Silent coercion or accepting values the schema rejects would make the contract less reliable, not more agent-friendly.
- “Generate schema, docs, and examples from the same source” is treated as a parity invariant, not a requirement to introduce code generation. Existing tests and shared definitions may enforce parity more simply.

### 2026-07-21 — Tier 3 issue additions
- The live Tier 3 issue adds stable topics that are not explicit in the Notion page: delivery universality and surface placement, error escalation, annotations as tested safety claims, output truncation/pagination, narrow gating, external-content provenance, telemetry privacy, measure-before-investing, eval-gated guidance changes, schema evolution, retry/idempotency, partial success, authentication on every request, tenant isolation, and the companion agent-skills check.
- Numeric budgets, exact client limits, guardrail inventories, and Inspector mechanics remain owned by `guardrails/`. The best-practices guide should link to that enforcement surface instead of copying values that can drift.
- The eventual reviewer rubric should remain qualitative and diff-local. Countable facts belong in deterministic CI; model findings should cite a rubric ID, changed line, evidence, client impact, concrete repair, and confidence.

### 2026-07-21 — Independent Claude Fable 5 review
- One requested read-only Fable 5 pass independently recommended `docs/mcp-best-practices.md` as a normative, public-repository guide with a compact checklist. It agreed that the guide should name existing repository idioms instead of inventing a framework.
- Fable independently recommended separating authoring guidance from `guardrails/README.md`, qualifying lenient input handling as “loose in representation, strict in value, honest in schema,” keeping mutations atomic, making truncation visible, treating aliases/error codes as contract decisions, and adding explicit anti-overengineering rules.
- Fable proposed examples on every parameter and aliases forever. The current draft intentionally leaves those as owner decisions because the Tier 3 issue warns against blanket example/default requirements, and permanent aliases can themselves accumulate contract complexity.
- Claude plan mode created a transient plan outside the repository despite the read-only prompt; it was removed after the review was captured. No repository file was changed by Claude.

### 2026-07-21 — Guide governance and scope finalized
- The owner approved all five recommendations as a batch.
- The guide will be normative, using `MUST`, `SHOULD`, and `MAY`. A justified exception must be recorded in the feature context or PR rather than silently weakening the rule.
- The compact PR checklist and stable rubric IDs will live in the guide itself, making it the source for the later advisory reviewer agent.
- Compatibility aliases have no unconditional forever promise. They remain until an explicit, evidence-backed deprecation and removal decision documents the migration risk and path.
- Realistic executable examples are required for non-trivial tools/workflows and syntax-heavy or ambiguous parameters, not every self-evident field.
- The guide includes stable security and privacy principles for request authentication, tenant isolation, remote-content provenance, and sensitive telemetry. Organization-specific retention and deployment values remain elsewhere.

### 2026-07-21 — Documentation tranche implemented
- Created `docs/mcp-best-practices.md` following the finalized 11-section plan: normative `MUST`/`SHOULD`/`MAY` rules with stable rubric IDs (`SUR`/`SCH`/`DSC`/`RES`/`OUT`/`ERR`/`SEC`/`CMP`/`EVL`), a compact surface-placement table, and a one-screen PR checklist derived from the body rules.
- Examples and terminology were verified against the current surface: tool names and boundary descriptions, `searchContext` invariant, the `StructuredContent` `code` taxonomy and `missingKeys` field, `signoz://` resource and template URIs, registered prompt names, annotation triples (including the non-idempotent channel update), and pagination metadata (`pagination.hasMore`/`nextOffset`, `data.nextCursor`).
- The guide separates qualitative authoring judgment from deterministic enforcement: it links to `guardrails/README.md` and copies no numeric budgets, client-version tables, exception inventories, or CI commands. Finalized decisions preserved verbatim in the rules: aliases retained until an evidence-backed deprecation decision (CMP-2); examples required for non-trivial tools/workflows and syntax-heavy or ambiguous parameters only (DSC-4); representation leniency only when honestly advertised, with actionable rejection otherwise (SCH-1, SCH-2); stable security/privacy principles with organization-specific retention/deployment values kept elsewhere (SEC-5).
- Added a concise `## MCP Contract Changes` pointer to `CLAUDE.md` requiring contract-touching changes to follow the guide and its checklist. Plan status moved to In Progress (not Done — not yet shipped). No runtime code, tests, guardrails, workflows, `README.md`, `manifest.json`, or companion-repo changes in this tranche.

### 2026-07-21 — Post-implementation verification
- Direct diff review corrected four precision issues without changing the approved scope: descriptions/schemas are the most widely delivered surfaces rather than literally visible to every client; accepted alternative representations must appear in JSON Schema rather than prose alone; atomic tools may have explicitly declared and observable secondary side effects; and a companion `SigNoz/agent-skills` change ships as a linked companion PR rather than inside the server PR.
- The future advisory reviewer output is now explicitly capped at its three highest-priority findings, matching the reconciled Tier 3 design.
- The `CLAUDE.md` pointer now says to review against the checklist rather than “run” it and remains three wrapped lines.

### 2026-07-21 — Backend error fidelity considered
- The owner proposed documenting that high-quality SigNoz backend errors, including documentation links, should reach the agent rather than being replaced by generic MCP prose.
- Direct inspection shows the current MCP `upstreamError` path already preserves a bounded backend message plus upstream code/type and HTTP status, and Query Builder errors may add MCP-specific recovery context. The newer SigNoz backend error envelope also exposes `url`, top-level and per-detail `suggestions`, and `retry`; those fields are not currently carried through the MCP result.
- The recommended rule is “faithful, not raw”: preserve recognized, safe, actionable backend fields verbatim and add the stable MCP classification around them. Bound size, redact secrets, reject unsafe markup/unrecognized payloads, and allow tool-specific recovery to supplement rather than rewrite backend guidance.
- Adding this as a normative rule would expose a small runtime conformance gap. Keep the documentation decision in Tier 3 and handle URL/suggestion/retry passthrough in a separate focused implementation change rather than silently expanding this documentation-only tranche.

### 2026-07-21 — Backend fidelity and owner edits reconciled
- The owner approved adding backend-error fidelity to the guide. `ERR-6` now requires faithful, bounded preservation of recognized SigNoz messages, documentation URLs, suggestions, detail messages, and retry hints while retaining stable MCP codes and allowing additive tool-specific recovery.
- The owner's manual simplification removed dedicated prompt-authoring, remote-fetch, telemetry, and code-generation rules. Those removals were preserved; the plan and section titles now match the narrower guide instead of restoring deleted material.
- Section 11 and the PR checklist were repaired after those removals: stale `RES-6`, `SEC-4`, and `SEC-5` references were removed; selection evaluation now covers recovery-critical guidance; atomic mutation coverage was added; passthrough fidelity is explicit; and validation-only wording no longer overgeneralizes all errors.

### 2026-07-21 — Final review finding addressed
- The final Fable 5 review found one valid P3 checklist-coverage gap: `SUR-5`, `SCH-6`, and `RES-4` were defined in the guide but absent from the compact PR checklist.
- The existing surface, schema, and resource checklist rows now cover agent/domain vocabulary, task-shaped inputs, and resource purpose/warnings/provenance. No rule or checklist row was added, and the review remains compact.

## Open Questions
- [x] Should the guide use normative `MUST`/`SHOULD`/`MAY` language, with exceptions recorded in the feature context/PR, or remain advisory prose? Resolved: normative, with justified exceptions documented in the feature context or PR.
- [x] Should the compact PR checklist and stable rubric IDs live at the end of the same guide, making it the direct source for the later reviewer agent? Resolved: yes.
- [x] Should compatibility aliases be indefinite by default, or require an explicit deprecation/removal policy based on usage and migration risk? Resolved: retain until an explicit, evidence-backed deprecation/removal decision; do not promise forever.
- [x] Should realistic examples be required once per non-trivial tool/workflow and only on syntax-heavy parameters, or on every parameter? Resolved: require them for non-trivial tools/workflows and syntax-heavy or ambiguous parameters only.
- [x] Should the guide govern security/privacy aspects of client-visible MCP behavior, including tenant isolation, remote-content provenance, and telemetry handling, while leaving organization-specific retention values elsewhere? Resolved initially: yes. Superseded by the owner's later edit: retain authentication, tenant isolation, destructive-action, and retry guidance; omit dedicated remote-fetch and telemetry rules from this tranche.
- [x] Should the guide require faithful, bounded passthrough of recognized SigNoz backend error guidance (`message`, documentation URL, suggestions, detail messages, and retry hints) while retaining stable MCP codes and safety filtering? Resolved: yes; MCP classification and tool-specific recovery may supplement but not replace recognized backend guidance.
