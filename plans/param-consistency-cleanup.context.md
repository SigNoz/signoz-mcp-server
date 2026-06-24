# Feature: Read-Tool Parameter Consistency Cleanup (#360) — Context & Discussion

## Original Prompt
> I want to work on this: https://github.com/SigNoz/signoz-ai-assistant/issues/360
>
> Ask me questions around each consistency. Suggest solutions with pro and cons for each.

## Reference Links
- [Issue #360 — Parameter inconsistencies across read tools](https://github.com/SigNoz/signoz-ai-assistant/issues/360)
- [Issue #315 — filter/query silent-drop bug (the higher-priority sibling)](https://github.com/SigNoz/signoz-ai-assistant/issues/315)
- PR signoz-mcp-server#213 — filter/query standardization (canonical `filter` + silent `query` alias)
- `plans/filter-param-consistency.plan.md` — the precedent; its "Out of Scope" section (L116-120) explicitly defers these four items to a follow-up.

## Key Decisions & Discussion Log

### 2026-06-24 — Grounded verification of all 5 inconsistencies (workflow, 5 parallel investigators)
All five reported inconsistencies are STILL present in current code (post PR #213). Verification surfaced five details the issue under-counted:

1. **Time-unit (ms vs ns)** — confirmed. `timeutil.GetTimestampsWithDefaults(args, unit)` (pkg/timeutil/time.go:38-46) switches UnixNano vs UnixMilli on the unit string. `list_services` + `get_service_top_operations` pass `"ns"`; everything else `"ms"` (via `resolveTimestamps`, aggregate_helper.go:182-198, which always passes `"ms"`).
   - **NEW:** numeric float timestamps > 2^53 are silently dropped today (`maxExactJSONInteger`, time.go:104-108) — so numeric ns inputs are already partly broken; string-typed timestamps are the only safe path.
   - **NEW:** `signoz_get_top_metrics` (added after the issue was filed) is `"ms"` via `resolveTimestamps(args,"7d")`.
   - `list_metrics` truly has no `timeRange`, no default window — manual `strconv.ParseInt`, falls back to start=0/end=0 (unbounded/backend-default), metrics.go:102-112.

2. **Default window** — confirmed but **three-way, not two-way**: 1h (search/aggregate logs+traces, query_metrics), 6h (list_services, get_service_top_operations, get_trace_details, get_alert_history), **7d (get_top_metrics — new)**. The 6h is a hardcoded fallback in the helper (time.go:45-46); the others inject a per-tool default via `resolveTimestamps` before the helper runs. Defaults ARE documented per-tool in README + param descriptions, but `manifest.json` is silent. The 6h fallback is currently untested.

3. **`limit`** — all 4 sub-claims confirmed. `WithString` on 11 tools, `WithNumber` only on `search_docs` (docs.go:28). Defaults 100/50/10/20. Clamp: search/aggregate → MaxRawResultLimit=10000 (aggregate_helper.go:221); list tools + alert_history → no clamp.
   - **NEW:** `search_docs` clamps to a SEPARATE ceiling of **25** (internal/docs/index.go:146-150), not MaxRawResultLimit.
   - **NEW:** `get_alert_history`'s "between 1-1000" bound exists only in the error string — no actual upper bound is enforced (alerts.go:330-338). It calls `paginate.ParseParams` then discards the limit (`_, offset :=`, alerts.go:328) and re-parses with default 20.
   - `list_metrics` uses its own inline parse (default 50), not `paginate.ParseParams`.

4. **`requestType`** — confirmed: scalar (aggregate_logs/traces, aggregate_helper.go:155-158) vs time_series (query_metrics, metrics_helper.go:130-132). Divergent default is duplicated at the payload-builder layer too (querybuilder.go:337-338 / 398-399 / 469-470).
   - **NEW:** no enum validation at the MCP arg layer. metrics silently coerces unknown→time_series (querybuilder.go:236-238); logs/traces hard-error on unsupported (querybuilder.go:259/281). README omits `requestType` for the aggregate tools entirely (scalar default undocumented there).

5. **Resource-id naming** — confirmed: `ruleId` (alerts), `uuid` (dashboards), `viewId` (views), `id` (notification channels), `traceId` (trace details). Within each family the name is consistent across get/update/delete.
   - **NEW (important):** every upstream SigNoz endpoint takes the id as a POSITIONAL path segment (`/api/v2/rules/%s`, `/api/v1/dashboards/%s`, `/api/v1/explorer/views/%s`, `/api/v1/channels/%s`); traces interpolates into a filter expression (client.go:735). So upstream has NO named id param — renaming is contained entirely to the MCP boundary, zero upstream risk.
   - `ruleId` and `uuid` live in typed jsonschema structs (pkg/types/alertrule.go:28, pkg/types/dashboard.go:4) used by the update tools, pinned by schema_compat_test.go:71/81.
   - `traceId` has the smallest blast radius (read-only, single get). `id` (channels) collides with the generic JSON identity field used inside response bodies.

**Framing established this session:** three remediation tiers — (0) document-only (zero behavior change), (1) backward-compatible normalize via silent alias / magnitude auto-detect / dual-accept, (2) hard normalize behind a version bump. PR #213's silent-alias pattern is the proven Tier-1 template here.

### 2026-06-24 — Discovery sweep for ADDITIONAL inconsistencies (workflow: 9 lenses → synthesize → adversarial verify, 41 agents)
User asked to check for more inconsistencies before proceeding to questions. Swept the full ~42-tool surface on 9 axes, deduped vs the known-5, then adversarially verified each candidate. Result: **searchContext convention is COMPLIANT** across all tools (35 WithString + 4 typed structs with SearchContext on T; none Required; none misspelled). Beyond the known-5, **~28 net-new inconsistencies** confirmed (22 confirmed-new + 6 partly), grouped:

**Silent-failure (wrong/incomplete results, undetectable):**
- N1. `execute_builder_query` drops backend QB `data.warning.warnings[]` that all 5 sibling QueryBuilderV5 callers surface (note + WARN log). query_builder.go:116-123. Violates CLAUDE.md fail-open-not-silent.
- N2. `stepInterval` numeric JSON value (60 vs "60") silently dropped on aggregate_logs/traces (string-only assert, aggregate_helper.go:160-165) but honored on query_metrics (parseIntLoose, metrics_helper.go:105).
- N3. Boolean string params (no WithBoolean anywhere) handle malformed values 3 opposite ways: silent-coerce (includeSpans, traces.go:177-180), silent-DROP widening results (`error` filter, traces_helper.go:84-91), hard-error (send_resolved, notification_channels.go:308-315), silent-nil (list_alerts active/silenced/inhibited, alerts.go:129-137). isMonotonic uniquely accepts real bool.
- N4. List-shaped output split: 6 list tools emit `pagination{total,hasMore,nextOffset}` via paginate.Wrap; search_logs/search_traces/get_alert_history/list_metrics/get_top_metrics return raw body with NO next-page signal despite accepting limit/offset (descriptions even say "paginate with offset").
- N5. Empty/non-array upstream `data`: list_views coerces to empty page (views.go:380-399, tested); list_dashboards + list_notification_channels hard-error "invalid response format" (dashboards.go:177-181, notification_channels.go:244-248) → "0 results" reads as "instance broken".
- N6. Notification-channel create/update fail OPEN (IsError=false) on test-notification failure (bury in JSON, nc.go:346-366/441-461) while every other write fails CLOSED (NewToolResultError).

**Convention-violation:**
- N7. `groupBy` fieldContext auto-detected client-side for query_metrics (detectFieldContext, metrics_helper.go:147) but deferred to backend for aggregate_logs/traces (no FieldContext, aggregate_helper.go:121). Partly justified (signal attr-model differences); harm unproven.
- N8. Documented bool default 'true' materialized client-side (send_resolved/includeSpans) vs delegated to backend (list_alerts active/silenced/inhibited). No current behavioral divergence; doc-staleness risk only.
- N9. Upstream errors wrapped inconsistently: same QueryBuilderV5 error → "Query execution failed:" in query_metrics but bare in execute_builder_query + 4 search/aggregate siblings; create_alert/dashboard "SigNoz API Error:" vs create_channel "Failed to create…:" vs create_view verbatim.
- N10/N11. Docs tools are the ONLY ones emitting MCP `StructuredContent` + structured error codes (docs.go:157, internal/docs/errors.go); ~40 others text-only. Partly justified (docs = fixed struct; needs retry taxonomy) but no documented rule; server-shaped list tools could be structured too.

**Papercut:**
- N12. Free-text search input is `query` on search_docs but `searchText` on search_logs/list_metrics/get_field_keys/get_field_values.
- N13. Ordering param: aggregate `orderBy='<expr> <dir>'` (default desc) vs get_alert_history `order='asc'|'desc'` (default asc) — homograph + opposite defaults + incompatible grammars. (search tools expose neither — 3rd spelling.)
- N14. `tags` is WithArray in create_view (views.go:83) but WithString JSON-literal in get_service_top_operations (services.go:43). (WithArray appears exactly once in the whole non-typed surface.)
- N15. Only `sourcePage` carries a schema-level `mcp.Enum` (views.go:80 — the sole enum in the codebase). signal/aggregation/channel-type/state/order and typed AlertType/PanelType/DataSource are all bare WithString or prose-only.
- N16. search_logs/search_traces expose no ordering param (hardcoded timestamp desc, querybuilder.go:316/559); aggregate siblings do. Partly cosmetic (raw stream vs grouped).
- N17. query_metrics prepends prose "[Decisions applied]\n…---\n" before the JSON (metrics_query.go:187-201), breaking the repo's own JSON-first content-block contract (resultWithNotes, aggregate_helper.go:284-286) that the 4 search/aggregate siblings follow.
- N18. (overlaps N4) No partial-result/pagination signal on list_metrics/get_top_metrics/get_alert_history; the clamp note only fires above 10000, so search/aggregate share the default-limit blind spot too.
- N19. get_service_top_operations omits a buildable service webUrl (services.go:137 raw passthrough) that list_services + get_dashboard/get_alert/get_trace_details all inject. (search_logs lacks one too — justified: no "log" webUrl type.)
- N20. Mutation success envelopes incompatible: status-JSON `{"status":"success","ruleId":…}` (update/delete alert) vs prose "dashboard updated" (no id, dashboards.go:404/428) vs raw passthrough (create_alert, view CRUD) vs reshaped struct (channels). No shared helper.
- N21–N25, N27. Error-string drift for the same classes: "Parameter validation failed:" prefix casing flips (cap vs lowercase, ~43 vs ~18 sites); "arguments is not a JSON object" guard emits 4-6 strings; get_trace_details mislabels a user timestamp-parse error "Internal error:" (only 2 such sites, traces.go:184/187); required-string-id getters split 3+ ways on wrong-type-vs-empty; required-param phrasing diverges (metricName/aggregation/execute_builder_query drop the template); update_dashboard error cites unprefixed `list_dashboards` (stale, dashboards.go:376).
- N26. stepInterval + timeRange descriptions advertise the same shared parser (ParseTimeRange/parseIntLoose) with materially different grammars/examples (get_top_metrics omits minutes & 1h; '2h' only on services/alerts; metrics uses bare enums) — implies allowed-sets that don't actually differ.

**Cosmetic:** N28. offset garbage-handling: intArg hard-errors (search tools) vs paginate.ParseParams silently → 0 (list tools); get_alert_history mixes both within one tool.

**Excluded as justified (not real):** dashboard-variable UPPERCASE sort vs lowercase query order; get_alert_history asc vs summary-resource desc; `source` (query store filter) vs `sourcePage` (view surface) naming; uniform absence of having/threshold (coverage gap, not divergence); two-stage metrics aggregation vocabulary (domain-justified).

**Natural fix-families (many items collapse to one remedy):** (A) output completeness/fail-silent: N1,N2,N3,N4/N18,N5,N6; (B) shared validation/error-string helpers: N9,N21,N22,N23,N24,N25,N27; (C) output envelope/structured content: N10,N11,N17,N20; (D) param schema + description harmonization: N7,N8,N12,N13/N16,N14,N15,N26 + K1-K5 docs; (E) the original K1-K5 behavior changes.

Full verified evidence (corrected file:line + adversarial reasoning per item): task output wstlxh4x3 / tool-results/bt1bi8r7w.txt.

### 2026-06-24 — Plan published to #360 + sub-issues filed
Appended the finalized plan + a 5-item sub-issue checklist to the #360 body (original inventory preserved). Created one sub-issue per PR in signoz-ai-assistant: #363 (A), #364 (B), #365 (C, depends on #364), #366 (E), #367 (D + paired skills PR). De-scoped items live at nerve-pod#4 (N20 + N13/N16) and nerve-pod#5 (N7). Implementation not started.

### 2026-06-24 — Family D (param schema & description) decisions
N12 → rename query→searchText + permanent alias (needs paired agent-skills PR). N14 → WithArray + JSON-string back-compat. **N15 refined per user's drift question:** hard enum ONLY on stable/MCP-owned sets (requestType/sourcePage/order/state/signal); backend-owned evolving sets (aggregation/channel-type/panel-type/DataSource) stay documented free-strings + a live drift-test (avoids MCP enum rejecting a newly-valid backend value). N26 → unify timeRange/stepInterval descriptions. N8 → keep list_alerts backend-deferred (correct for a proxy), verify docs. N13/N16 ordering DEFERRED → documented on nerve-pod#4. N7 groupBy-context DEFERRED → nerve-pod#5.

### 2026-06-24 — Family C (output envelope & structured content) decisions
N17 → JSON-first via resultWithNotes. N10/N11 → structuredContent only where the shape is code-controlled (list/get/mutation), raw QB passthrough stays text. Error codes → yes, light shared taxonomy. **N20 (mutation success envelope) de-scoped from this plan at user's direction → filed as SigNoz/nerve-pod#4.** (Note: deviates from the usual signoz-ai-assistant issue-repo default per explicit user instruction.)

### 2026-06-24 — Family B (error/validation strings) decisions
All converged onto shared helpers (see `.plan.md`). N9 → uniform `upstreamError()` prefix (option a) so the LLM can distinguish upstream failure from a param bug (retry vs fix). Structured error codes deferred to Family C.

### 2026-06-24 — Family A (silent-failures) decisions
All recommendations accepted — see `.plan.md` Decisions Log. N1/N2/N5 are low-optionality fixes (reuse existing patterns). N3 booleans → `WithBoolean` + shared parser + hard-error on garbage. N4 → lightweight hasMore/nextOffset note (not full envelope). N6 → keep fail-open for the secondary test-send but make the failure a loud uniform warning note + WARN (not IsError), since the create itself succeeded.

### 2026-06-24 — Framing + Family E decisions
- **Framing:** scope = FULL harmonization (all ~33 items); backward-compat appetite = breaking changes ALLOWED, agent-skills repo updated alongside; delivery = interactive family-by-family in chat (questions + pros/cons), decisions recorded into `.plan.md`.
- **Family E (original 5) decided** — see `.plan.md` Decisions Log. Notable: K2 went DOCUMENT-ONLY (user kept per-tool windows rather than unifying), and K4 keeps the per-signal requestType default (not unified) because scalar/time_series are each correct for their signal. K5: unify to `id` with PERMANENT aliases (user asked for all-time alias), traceId excluded. Confirmed with user: backend is unchanged (upstream ids are positional), and permanent read-side alias has no runtime cost.

## Open Questions
- [ ] Overall appetite: Tier 0 (doc-only) / Tier 1 (compat-normalize) / Tier 2 (breaking) / split per item?
- [ ] Time units: magnitude auto-detect (accept ns/ms/s by size) vs standardize-on-ms-with-doc vs add explicit `unit` param? And: require string-typed timestamps to dodge the 2^53 float-drop?
- [ ] Default window: unify all read tools to one default vs keep per-tool but document + add JSON-Schema `default`? Does get_top_metrics' 7d stay special?
- [ ] `list_metrics`: add `timeRange` + standard default + route through the shared helper, or leave unbounded and only document?
- [ ] `limit`: flip search_docs to WithString? Unify defaults? Add a clamp to the unclamped list tools? Fix the alert_history double-parse?
- [ ] `requestType`: unify the default, or keep per-signal but document? Add arg-layer enum validation?
- [ ] Resource ids: leave as-is / add unified `id` alias keeping old names advertised / rename with old-name aliases / doc-only naming convention?
- [ ] Backward-compat mechanism preference across the batch: silent alias (like #213) vs auto-detect vs version bump?
