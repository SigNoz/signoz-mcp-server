# Feature: Filter/Query Param Consistency — Context & Discussion

## Original Prompt
> I want to fix this issue: https://github.com/SigNoz/signoz-ai-assistant/issues/315
> Suggest an ideal solution. Also get review from codex xhigh 5.5 fast

## Reference Links
- [signoz-ai-assistant#315](https://github.com/SigNoz/signoz-ai-assistant/issues/315) — "Claude gets confused filtering logs via MCP: `filter` vs `query` param inconsistency silently drops the filter"
- Project memory: `project_filter_param_inconsistency` (known issue, fix previously deferred 2026-06-09)

## Key Decisions & Discussion Log

### 2026-06-23 — Codebase analysis (5-reader workflow) confirmed and extended the issue
- The issue's 6-tool table is correct; full inventory is 42 tools. The filter-expression param has THREE
  names: `query` (search_logs, search_traces), `filter` (aggregate_logs, aggregate_traces, query_metrics),
  and nested-in-object (execute_builder_query, create/update view). So only the **2 search tools** are the
  outliers — `filter` is already the majority (3/5) name.
- Confirmed false friends (same word, different language, must NOT be aliased): `list_alerts.filter`
  (Alertmanager matcher), `search_docs.query` (BM25 text), `execute_builder_query.query` (whole QB object).
- mcp-go performs NO arg validation; unknown keys are silently passed through, never rejected/stripped
  (mcp-go@v0.49.0 server.go, mcp/tools.go GetArguments). An alias is therefore a pure handler-read concern.
- Empty filter is sent verbatim as `{"expression":""}` for logs/traces (match-all) — that is the silent
  broadening. For metrics, an empty filter is omitted entirely (querybuilder.go:429/506) — same net effect,
  different JSON shape.
- Backend warnings live at `data.warning.warnings[].message` and are NEVER surfaced (pure 2xx passthrough,
  client.go:410). Insertion point = shared result wrappers in aggregate_helper.go (+ metrics decisions block).
- LATENT BONUS BUG: `mcp.WithInputSchema[T]` uses google/jsonschema-go v0.4.2, which reads only the
  `jsonschema` tag (as description) and IGNORES `jsonschema_extras:"description=..."`. So rich field docs on
  create/update alert/dashboard never reach the model, and `jsonschema:"required"` emits the literal word
  "required" as the description. Out of scope here — file separately.

### 2026-06-23 — Codex review (gpt-5.5, xhigh, fast, read-only)
Codex independently verified the claims against the code (and cross-checked the SigNoz backend repo for the
warning envelope). It agreed with the core design and tightened four points:
- **Canonical = `filter`: confirmed.** QB v5 shape is `filter.expression` (querybuilder.go:125/148); `query`
  is overloaded across docs/QB-object/PromQL/ClickHouse. No reason to prefer `query`.
- **Hidden alias: confirmed.** Do not advertise both names — advertising both would preserve the confusion.
- **Conflict handling — TIGHTEN:** a log-only WARN is "too invisible." If both `filter` and `query` are
  non-empty AND differ (after `strings.TrimSpace`), prefer a **hard error** with an actionable message
  (or at minimum an in-band note in the tool result, never log-only). Never log the raw expression values.
  Otherwise: `filter` then legacy `query`. → **Decision: hard error on differing non-empty values.**
- **New false friend caught:** `query_metrics` has a top-level `filter` (alias this) AND nested
  `formulaQueries[].filter` (metrics.go:59, metrics_query.go:147). Alias ONLY the top-level one; do not recurse.
  Same for nested `compositeQuery` filters in views — leave untouched.
- **Layer 2 plumbing — TIGHTEN:** the aggregate_helper.go wrapper currently has only one optional note path
  (clamp note, :203); warning-surfacing must preserve raw JSON first and compose MULTIPLE notes.
- **Layer 3 — caution:** write an honest, logs-specific guide; do NOT copy the traces guide wholesale (logs
  syntax differs — body/JSON search, severity_text, logs field contexts).
- **Latent jsonschema bug: confirmed** independently (infer.go:330; alertrule.go:24/28/39/111).
- Codex verdict: 🟢 main fix sound; conflict handling + warning-surfacing are the two places to tighten.

### 2026-06-23 — agent-skills cross-repo audit (read-only agent)
Audited `~/signoz/agent-skills` for drift under the `filter` standardization. Blast radius is tiny because
the repo's own rule (CONTRIBUTING.md:31-32) forbids transcribing MCP input schemas into skills ("MCP is the
source of truth… read the `signoz://*` resources rather than transcribing schema — duplicated schema rots").
- **Only ONE skill needs updating:** `plugins/signoz/skills/signoz-generating-queries/SKILL.md` — it is the
  single place that violated the no-transcribe rule by naming the `query` param literally:
  - L104 (`signoz_search_logs` row): "Use `searchText` for body text, `query` for field filters…" → `filter`.
  - L105 (`signoz_search_traces` row): "…plus `query` for field filters." → `filter`.
  - L124: "less error-prone than building `query` expressions." → `filter`.
  - L125: "Combine shortcut params with `query`/`filter`…" → collapse to just `filter`.
- **No "Supports any log field/attribute" phrase exists in agent-skills** (that wording lives only in
  signoz-mcp-server). Change-item C is MCP-repo-only.
- **No explicit filter-vs-query gotcha/workaround** to remove anywhere.
- **False friends correctly present and must be left alone:** `signoz_search_docs.query` (BM25,
  signoz-searching-docs/SKILL.md:23), `execute_builder_query.query` + traces-guide ref
  (signoz-generating-queries/SKILL.md:108), dashboard widget `query` object, alert/QB `filter`, nested
  `compositeQuery`/`formulaQueries[].filter`, and generic "with filter:" prose in signoz-investigating-alerts
  (already canonical-aligned).
- **Optional (item D):** once `signoz://logs/query-builder-guide` exists, 3 traces-guide references could gain
  a logs sibling (signoz-creating-dashboards/SKILL.md:331, signoz-modifying-dashboards/SKILL.md:123,
  signoz-generating-queries/SKILL.md:108) — optional per the convention, not required.
- Plugin manifests need touching only if skills are added/removed (CONTRIBUTING.md:35) — not triggered here.

### 2026-06-23 — Implementation (Codex gpt-5.5/xhigh/fast) + adversarial multi-agent review
- Codex implemented all THREE layers on branch `fix/filter-param-consistency` (uncommitted). `go build ./...`
  and full `go test ./...` pass (verified independently). Decision: **all three layers ship in ONE PR** (not
  Layer 1 first) — resolves the scope open question below. The latent jsonschema bug was filed separately as
  signoz-ai-assistant#359 (and param-inconsistency cleanup as #360) — resolves that open question below.
- Ran a 6-dimension adversarial review workflow (17 agents): each finding refuted-or-confirmed by a 2nd agent.
  Result: **0 blockers, 0 majors** (both claimed-major test-coverage findings downgraded to minor on
  verification). Highest-risk dimensions — readFilterExpr correctness and FALSE-FRIEND safety — came back
  fully CLEAN (false friends list_alerts/search_docs/execute_builder_query/views/formulaQueries[].filter
  confirmed untouched; readFilterExpr confined to the 5 intended tools). Warning JSON path
  `data.warning.warnings[].message` verified against the real SigNoz backend types.
- Verified minor/nit findings being addressed before commit: (a) add unit test for
  extractBackendWarningMessages (fail-open on malformed body / blank-message skip / multi-message order);
  (b) add query_metrics backend-warning handler test (unique [Decisions applied] composition path);
  (c) extend conflict-error HANDLER assertion from search_logs-only to all 5 handlers; (d) add false-friend
  guard tests for formulaQueries[].filter and view create/update; (e) document the pre-existing
  signoz://traces/query-builder-guide symmetrically in manifest.json + README (the PR added only the logs
  sibling). Plan Status bumped to In Progress.
- Deferred follow-up (needs a live SigNoz instance): a recorded-real / live warning-envelope contract test so
  an upstream field rename (warning→warnings, message→msg) fails a test rather than silently returning nil.
  Mitigation already in place: the WARN log fires when warnings are found (detectable, not fail-silent).
- The "ambiguous keys default to `resource` context" wording in the filter descriptions/logs guide is
  grounded in the backend's own warning text quoted in #315 ("Using `resource` context by default…"), so it
  is accurate; the live/recorded test above is what would pin it long-term.
- Companion skills change shipped as draft agent-skills#51 (blocked until this MCP change is released).

### 2026-06-23 — Post-review wording refinements (commit 4d05f59 on PR #213)
- Owner flagged that the discovery nudge said "discover real **keys** first with
  signoz_get_field_keys/**signoz_get_field_values**" — but get_field_values finds VALUES for a known key,
  not keys (it requires name=<key>). Reworded both filter param descriptions (logs.go:15, traces.go:18) to
  a two-step: "Discover valid keys with signoz_get_field_keys, then confirm values with
  signoz_get_field_values, before filtering." Rationale: keys-discovery avoids `key not found` + resolves
  resource/attribute ambiguity; value-discovery avoids the right-key/wrong-value → empty-results spiral.
- Owner also noted logs (and traces) filter expressions support OR, but the TOOL DESCRIPTIONS only showed
  AND examples (the param description is what most clients read; the guide is a passive resource). Confirmed
  OR + parentheses are supported (logs_guide.go:14,23 already document them). Added "Combine conditions with
  AND, OR, and parentheses" + an OR example to both param descriptions and the 4 README filter rows. The
  pre-existing "Combined with service/... params using AND" suffix on the aggregate tools is correct (it
  describes shortcut-param attachment, always AND) and was left unchanged. No test pins description strings.

### 2026-06-23 — Deep verification of logs_guide.go against real backend (~/signoz/signoz @ b8567664)
Ran a 12-agent workflow (6 dimensions × adversarial double-check) verifying every claim in
`pkg/querybuilder/logs_guide.go` against the authoritative backend source (filterquery grammar,
querybuildertypesv5, telemetrylogs). Payload shape, `count()`, IN-lists, `body CONTAINS/ILIKE`, `body.<path>`,
and `resource.`/`attribute.` prefixes all verified CORRECT. Found + fixed:
- **BLOCKER:** the guide taught inline `timestamp >= <ms>` and "timestamp uses Unix ms". The `timestamp`
  COLUMN is UInt64 **nanoseconds** (telemetrylogs/field_mapper.go:26); a user NUMBER literal is passed raw with
  no ms→ns scaling (where_clause_visitor.go:884-890 → condition_builder.go:96-103), so a 13-digit ms value
  matches essentially every row → silent unfiltered data. Fix: steer to top-level `start`/`end` (ms,
  auto-scaled to ns); inline `timestamp` must use ns. Rewrote the TIMESTAMP FORMAT section + removed the bad
  example.
- **MAJOR:** `has(body.tags, 'production')` needs the `[*]` array suffix — `has(body.tags[*], 'production')`
  (json_string.go:94-99; bare form errors in the new path / does scalar has() in the old). Fixed.
- **MAJOR:** operator list omitted `REGEXP`/`NOT REGEXP` (real: builder_elements.go:109-110,
  where_clause_visitor.go:620-624) — also added `NOT ILIKE`, `BETWEEN`/`NOT BETWEEN` for completeness.
- **MINOR:** "ambiguous key → resource context" is only the resource↔attribute tiebreaker (and warns); other
  multi-context matches are ORed (where_clause_visitor.go:982-994). Tightened the wording.
- **MINOR:** added omitted built-ins `id`, `trace_flags`, `scope_name`/`scope_version` (const.go IntrinsicFields;
  field_mapper.go logsV2Columns); softened `severity_number int64` → `number`; quick-ref context column now
  uses literal `fieldContext` enum values (`log`/`body`) instead of prose labels.
Build + tests green. Committed on PR #213.

## Open Questions
- [x] Canonical name `filter` vs `query`? → **`filter`** (matches QB `filter.expression`; majority; `query`
      overloaded). Agreed by Claude + Codex.
- [x] Old `query` name visibility? → **Hidden back-compat alias** (advertise only `filter`; handler still
      reads `query`). Agreed by Claude + Codex.
- [x] Conflict resolution when both keys differ? → **Hard error** (actionable message), not log-only WARN.
- [x] Surface backend warnings in-band? → **Yes**, as a composed note block in the shared wrapper + WARN log.
- [x] Scope: ship Layer 1 first or all layers together? → **All three layers in ONE PR** (2026-06-23).
- [x] File the latent jsonschema_extras description-drop bug separately? → **Yes**, filed as
      signoz-ai-assistant#359 (param-inconsistency cleanup as #360). Issues go in signoz-ai-assistant, not
      signoz-mcp-server (owner direction 2026-06-23).
