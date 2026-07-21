# MCP Best Practices

This guide is the authoring standard for every client-visible MCP surface this
server exposes: tool names, input/output schemas, descriptions, annotations,
results, errors, resources, resource templates, prompts, and server
instructions. It governs qualitative design judgment — the things a reviewer
must weigh against a diff. Deterministic, countable enforcement (byte budgets,
schema-shape inventories, retry/replay rules, URI resolution, telemetry
measurement) lives in [`guardrails/README.md`](../guardrails/README.md) and CI;
this guide never duplicates those numbers or commands.

**Normative language.** `MUST`/`MUST NOT` are binding: violating one requires a
justified exception recorded in the feature context log or PR description.
`SHOULD`/`SHOULD NOT` are strong defaults: deviate only with a stated reason in
the PR. `MAY` marks an explicitly permitted option. Rules carry stable rubric
IDs (e.g. `SUR-1`); the PR checklist in section 11 is derived from them and is
the rubric source for the advisory reviewer.

## 1. Scope and agent-first principles

The consumer of every surface here is an AI agent acting for a user. Design for
the agent's decision sequence: choose the right surface, make a valid first
call, interpret the result, recover from failure, and survive contract
evolution.

- **[SUR-1] Delivery universality.** The more critical a piece of guidance is
  to a *correct first call*, the more universal and inline its placement MUST
  be. Tool descriptions and schemas are the most widely delivered surfaces;
  fewer clients read resources, and none can be assumed to read external docs.
  Correctness-critical rules go on the tool/parameter surface; depth goes in
  resources.
- **[SUR-2] Mis-calls are contract feedback.** A reasonable or recurring agent
  mis-call SHOULD be treated as evidence that the advertised contract needs
  improvement (clearer description, tighter schema, better error recovery).
- **[SUR-3] Fewer, sharper surfaces.** Prefer improving an existing surface
  over adding a new one. Measure real use before investing in new tools,
  resources, or prompts, and before removing existing ones.

## 2. Choosing, naming, and annotating MCP surfaces

- **[SUR-4]** Add a tool for a distinct *agent task*, not for a distinct
  backend endpoint. Before adding one, compare: a parameter on an existing
  tool, a resource, or a prompt. A new tool MUST justify why the agent needs a
  separately selectable action.
- **[SUR-5]** Names and descriptions MUST use agent/domain vocabulary
  (`signoz_search_logs`, `signoz_list_alert_rules`), not backend route or
  implementation vocabulary. Unavoidable backend terminology (e.g. the query
  builder, Alertmanager) SHOULD be briefly explained where it appears rather
  than banned.
- **[SUR-6] Nearest-neighbor boundaries.** Every tool whose scope could be
  confused with another MUST state the selection boundary against its nearest
  neighbor(s) — as `signoz_search_traces` does against `signoz_aggregate_traces`
  and `signoz_get_trace_details`. An agent reading only the descriptions should
  never face two tools that both plausibly claim the same request.
- **[SUR-7] Annotations are behavior-backed safety claims.** The advertised
  read-only/destructive/idempotent triple MUST match what the handler actually
  does, including side effects beyond the primary write (an update that also
  sends a live test notification is not idempotent). Annotations are hints for
  client policy, never an authorization or trust boundary.
- **[SUR-8]** Collapse predictable read round-trips where it removes a likely
  follow-up call (return IDs, names, and `webUrl` handles together). Mutations
  MUST stay atomic and observable: one tool call represents one coherent
  user-intended mutation. Any additional side effect MUST be declared in the
  description and annotations and reported accurately in the result.

## 3. Surface placement

| Content | Surface |
|---|---|
| What the tool does, when to pick it over its nearest neighbor, critical pre-call caveats, pointers to discovery tools/resources | Tool description |
| Field-local format, units, accepted forms, defaults, constraints, inter-field dependencies, short examples | Parameter description / input schema |
| Result shape, handles (IDs, `webUrl`), pagination and truncation metadata | Output schema / result envelope |
| Read-only / destructive / idempotent behavior | Tool annotations |
| Routing rules and policies shared across several tools | Server instructions |
| Long grammars, full schemas, complete worked examples, workflows, catalogs | Resources (`signoz://…`) |
| Live per-entity content addressed by ID | Resource templates (`signoz://alert/{id}/summary`) |
| Reusable user-invoked multi-step workflows | Prompts |
| Immediate correction and recognized backend guidance for a failed call | Error result (text + structured fields) |

- **[SUR-9]** Correctness-critical first-call rules MUST be inline on the
  tool/parameter surface. Resources provide progressive disclosure and MAY be
  recommended pre-reading, but MUST NOT be the only route to immediate repair
  of a failed call.
- **[SUR-10]** Gate a tool call only on authentication, consent, or
  confirmation of destructive action. A tool MUST NOT hard-require an optional
  documentation or resource fetch before it will work.

## 4. Input schemas and parameter behavior

- **[SCH-1] Loose in representation, strict in meaning.** Accept equivalent
  representations (number-as-string, boolean-as-string, single-value-as-list)
  only when the JSON Schema honestly advertises them, for example with a union
  type. Descriptions MAY clarify accepted forms but cannot substitute for the
  schema. Normalizing a form the advertised schema rejects makes the contract
  dishonest; advertising a form the handler rejects makes it broken. Both
  MUST NOT happen.
- **[SCH-2]** Unparseable, ambiguous, or semantically invalid values MUST be
  rejected with field-specific recovery guidance. A supplied-but-invalid value
  MUST NOT be silently replaced with a default — silent substitution answers a
  question the agent didn't ask.
- **[SCH-3] Parity.** Schema, handler validation, defaults, examples, and the
  `required` list MUST agree with each other and with runtime behavior. Shared
  definitions and parity tests are the preferred mechanism; do not introduce
  code generation solely for documentation.
- **[SCH-4] Enums.** Use a schema enum only for values this server owns and
  keeps stable (e.g. `signal`, `requestType`). Workspace- or backend-evolving
  vocabularies (field keys, metric names, channel types that track the backend)
  MUST instead point to a discovery tool (`signoz_get_field_keys`,
  `signoz_get_field_values`) or resource.
- **[SCH-5] `searchContext`.** Every tool's input schema MUST expose a
  top-level `searchContext` string carrying the user's original request
  verbatim. It MUST NOT appear in the JSON Schema `required` list and MUST NOT
  be described as optional. (Mechanics for typed schemas are in `CLAUDE.md`.)
- **[SCH-6]** Inputs SHOULD be shaped for the agent's task (flat, task-named
  parameters like `filter`, `limit`, `start`/`end`), not mirror the backend
  payload structure, except for deliberate body-carrying tools (dashboard/alert
  create and update) where the resource definition *is* the domain object.

## 5. Tool descriptions and examples

- **[DSC-1]** Optimize a description for correct selection plus a successful
  first call — not for maximal detail, and not for minimum byte count. Byte
  budgets are enforced deterministically; within them, spend words where they
  change agent behavior.
- **[DSC-2]** Lead with intent and result ("Use this when the user wants …; it
  returns …"). Name only the nearest confusing alternative(s), include
  pre-call caveats that commonly break first calls (default time window,
  workspace-specific fields), and give exact discovery pointers
  (tool name or `signoz://` URI), as the existing tool descriptions do.
- **[DSC-3]** Field-local detail — format, units, constraints, dependencies,
  defaults, short examples — belongs on the parameter/schema surface, not in
  the tool description.
- **[DSC-4] Examples.** Every non-trivial tool or workflow MUST have at least
  one realistic, executable example somewhere the agent can reach it
  (tool/parameter description or a linked `signoz://` resource);
  the same applies to any grammar, filter syntax, or ambiguous parameter
  format. Self-evident scalar fields do not need examples. Examples MUST use
  real SigNoz shapes and stay valid against the current handler.
- **[DSC-5]** Descriptions MUST NOT contain backend routes, implementation
  history, volatile catalogs (current workspace values, version tables), or
  guarantees the handler does not enforce.

## 6. Resources, resource templates, and server instructions

- **[RES-1]** Use resources for content too long or too structured for a
  description: query grammars (`signoz://logs/query-builder-guide`), payload
  schemas and worked examples (`signoz://dashboard/widgets-examples`),
  multi-step workflows. Use resource templates only for genuinely parameterized
  live content (`signoz://alert/{id}/summary`).
- **[RES-2]** Resource names and descriptions MUST state what the content is,
  when an agent should read it, and which tools/workflows it supports — they
  are selection surfaces, like tool descriptions.
- **[RES-3]** Advertised URIs MUST stay stable, resolve to non-empty content of
  the declared MIME type, be bounded in size, and require the same
  authentication as the data they expose.
- **[RES-4]** Inside a resource, put purpose and warnings before detail. For
  content that mirrors mutable external material, record provenance and
  freshness so staleness is detectable.
- **[RES-5]** Server instructions are for routing and policies that span
  several tools (signal selection, filter-key discovery, timestamp handling,
  `webUrl` usage). Per-tool detail MUST NOT migrate into server instructions.

## 7. Results, pagination, and partial success

- **[OUT-1]** Return predictable envelopes. Include reusable handles — IDs,
  names, `webUrl` deep links — when they remove a likely follow-up lookup.
  Clients are told to use `webUrl` verbatim, so it MUST be correct and
  tenant-scoped.
- **[OUT-2] Read–modify–write.** Where the domain supports it (alerts,
  dashboards, channels, views), the shape a read tool returns MUST be writable
  back through the corresponding update tool without undocumented reshaping.
- **[OUT-3]** Clamps, truncation, pagination, and known-incomplete data MUST be
  explicit in machine-readable result metadata with the next step attached
  (`pagination.hasMore`/`nextOffset`, `data.nextCursor`), never silent. An
  agent that cannot see truncation will present partial data as complete.
- **[OUT-4] Partial success** is legitimate only for genuinely independent
  per-item work (one panel of several failing enrichment). Global failures —
  authentication, permission, upstream unavailability — MUST surface as
  top-level coded errors, never folded into per-item results.
- **[OUT-5]** Passthrough tools that return upstream JSON verbatim MUST NOT
  silently reshape it into a misleading local contract; either pass it through
  honestly or own the envelope completely. Cross-boundary parsing may fail
  open, but MUST pair with a WARN log or metric (see `CLAUDE.md`, "Testing
  across external contracts").

## 8. Errors and recovery

- **[ERR-1]** Every tool error result MUST carry a stable machine-readable
  category in `StructuredContent` (`code`: `VALIDATION_FAILED`, `UNAUTHORIZED`,
  `NOT_FOUND`, `UPSTREAM_ERROR`, …) plus concise agent-readable text. Other MCP
  surfaces MUST use their protocol-appropriate error channel with equally
  stable semantics. Keep the code set small, add codes deliberately, and treat
  removals/renames as breaking changes (section 10).
- **[ERR-2]** Agent-correctable validation errors MUST name the failing field
  or operation, explain what is wrong, state the accepted forms or
  alternatives, and give the smallest next action — the canonical shape is
  `Parameter validation failed: "<field>" <reason>`. Extra structured fields
  (e.g. `missingKeys`) SHOULD be added when clients would otherwise
  string-match prose.
- **[ERR-3]** Put the immediate correction inline in the error; link a
  resource or discovery tool for deeper guidance. Resource retrieval MUST NOT
  be the only recovery path.
- **[ERR-4]** Upstream authentication and permission failures MUST propagate
  through the shared top-level coded path so clients can re-authenticate or
  handle permissions — never hidden inside partial results or converted to
  empty successes.
- **[ERR-5]** Distinguish expected agent mistakes (invalid filter key → WARN
  with recovery guidance in the result) from server/upstream faults (ERROR).
  Neither may be dropped: fail open, never fail silent.
- **[ERR-6] Backend guidance fidelity.** When a recognized SigNoz error
  envelope contains actionable guidance, the MCP layer MUST preserve its
  message, documentation URL, top-level and detail suggestions, detail
  messages, and retry hints faithfully. Stable MCP classification and
  tool-specific recovery MAY supplement that guidance but MUST NOT paraphrase,
  contradict, or replace it. Bound the preserved content and filter secrets or
  unsafe markup; malformed or unrecognized bodies fall back to the local coded
  error contract rather than raw passthrough.

## 9. Safety and security

- **[SEC-1]** Every request MUST be authenticated and resolve its instance and
  tenant scope from the request itself — never from cached caller identity. A
  cross-tenant identifier in the arguments MUST NOT leak another tenant's
  data; it fails like any other not-found/forbidden reference.
- **[SEC-2]** Destructive actions MUST be explicit, narrowly scoped single
  tools (`signoz_delete_dashboard` deletes exactly one dashboard by ID), with
  irreversibility stated in the description and side effects observable in the
  result. No tool may perform an undisclosed destructive side effect.
- **[SEC-3]** Retry eligibility MUST derive from actual handler idempotency —
  what the call does end-to-end — not from HTTP method or annotation wording
  alone. (Replay behavior is guardrail-enforced; classifying a new tool
  correctly is a review judgment.)

## 10. Contract evolution and synchronized documentation

The surface advertised at initialization is a stable executable promise.

- **[CMP-1]** These are breaking changes and MUST NOT ship silently: removing
  or renaming a tool, parameter, resource URI, or prompt; narrowing an
  accepted type or format; making an optional parameter required; removing an
  enum value; changing an annotation safety claim; changing a result envelope
  clients parse; removing or renaming an error code.
- **[CMP-2]** An intentional break MUST ship with an explicit compatibility
  path and a migration note in the PR. Compatibility aliases (e.g. a silently
  accepted legacy parameter name) MUST be retained until an explicit,
  evidence-backed deprecation/removal decision documents the migration risk
  and path — they are not promised forever, and each alias is contract
  complexity to be tracked, not accumulated freely.
- **[CMP-3]** When contracts overlap, production registration, validators,
  `README.md`, `manifest.json`, `docs/`, and tests MUST change in the same
  server PR (see the `CLAUDE.md` sync checklist). When the tool contract taught
  by the companion `SigNoz/agent-skills` repository changes, create and link
  its companion PR; internal or additive changes need no skills update.

## 11. Evaluation and PR review checklist

Deterministic facts — counts, byte budgets, schema compilation, URI integrity,
protocol compatibility — stay in CI (`guardrails/`, Inspector checks). This
section governs the judgment layer.

- **[EVL-1]** A change to selection- or recovery-critical guidance
  (descriptions, boundary language, error results, server instructions) SHOULD
  be evaluated with direct, indirect, and negative prompts using comparable
  model/catalog sessions before and after. Check the affected behaviors among
  tool choice, argument construction, clarification, abstention, and recovery
  from representative failed calls.
- **[EVL-2]** Substantive changes to broad guidance (this guide, server
  instructions) MUST be backed by evidence: eval results, recurring real
  failures, or production usage. Do not canonize dated usage statistics in
  this document.
- **[EVL-3] Reviewer output.** The advisory reviewer (human or model) judges
  the diff against this rubric. Findings MUST be diff-local and cite: rubric
  ID, changed line, evidence, client impact, concrete repair, and confidence.
  Countable violations belong to CI, not findings. Report at most the three
  highest-priority findings; "no findings" is a valid result. Findings are
  advisory, not blocking.

### PR checklist

For any PR touching a client-visible MCP surface, review the applicable items:

**Surface choice & placement**
- [ ] New surface justified over extending an existing one; names use agent/domain vocabulary; boundary vs nearest neighbor stated (SUR-3, SUR-4, SUR-5, SUR-6)
- [ ] First-call-critical guidance inline, not resource-only; no optional-doc gating (SUR-1, SUR-9, SUR-10)
- [ ] Annotations match end-to-end behavior; mutations are atomic and disclose/report all side effects (SUR-7, SUR-8)

**Schemas**
- [ ] Accepted representations honestly advertised; invalid values rejected with recovery, never silently defaulted; inputs are task-shaped, not backend-mirrored (SCH-1, SCH-2, SCH-6)
- [ ] Schema, validation, defaults, examples, required-list agree (SCH-3)
- [ ] Enums only for server-owned stable values; evolving vocab → discovery (SCH-4)
- [ ] Top-level `searchContext`, not required, not described optional (SCH-5)

**Descriptions & examples**
- [ ] Description leads with intent/result; caveats and discovery pointers present; field detail on params (DSC-2, DSC-3)
- [ ] Realistic executable example for non-trivial tools and syntax-heavy params; examples still valid (DSC-4)
- [ ] No routes, history, volatile catalogs, or unenforced guarantees (DSC-5)

**Resources & instructions**
- [ ] Resource metadata says what/when/for-which-workflows; content leads with purpose and warnings and records provenance; URIs stable, typed, bounded, authenticated (RES-2, RES-3, RES-4)
- [ ] Server instructions contain only cross-tool routing and policy (RES-5)

**Results**
- [ ] Handles returned where they save a lookup; reads write back cleanly (OUT-1, OUT-2)
- [ ] Truncation/pagination explicit with next step; partial success only for independent items (OUT-3, OUT-4)

**Errors**
- [ ] Stable tool-error `code`; validation text names field, problem, accepted forms, next action (ERR-1, ERR-2)
- [ ] Inline correction first; recognized backend guidance preserved safely and faithfully; auth/permission failures top-level (ERR-3, ERR-4, ERR-6)
- [ ] Passthroughs remain faithful; fail-open parsing emits a WARN/metric (OUT-5, ERR-5)

**Safety & security**
- [ ] Per-request auth + tenant scoping; no cross-tenant leakage (SEC-1)
- [ ] Destructive actions explicit, narrow, observable; retry classification matches real idempotency (SEC-2, SEC-3)

**Compatibility**
- [ ] No silent breaking change; intentional breaks carry migration note; aliases tracked, not dropped or promised forever (CMP-1, CMP-2)
- [ ] README/manifest/docs/tests sync done; companion agent-skills outcome stated and linked when needed (CMP-3)
