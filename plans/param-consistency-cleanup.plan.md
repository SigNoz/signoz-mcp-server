# Plan: Read-Tool Parameter Consistency Cleanup (#360)

## Status
Done — Families A–E shipped (signoz-mcp-server #217–#221) + a follow-up correctness PR (#222, from the 2026-06-25 post-merge review). ONE item DEFERRED, not done: **N14** typed `tags` (tracked in SigNoz/nerve-pod#8 — see the N14 entry below and the 2026-06-25 entry in the .context.md). Scope was FULL harmonization with breaking changes allowed (agent-skills updated alongside).

## Tracking issues (one sub-issue per PR)
- Master: SigNoz/signoz-ai-assistant#360 (body holds the finalized plan + checklist)
- #363 Family A (silent-failures) · #364 Family B (error/validation helpers) · #365 Family C (output/structured content, depends on #364) · #366 Family E (original K1-K5) · #367 Family D (schema/types/descriptions + skills PR)
- De-scoped: SigNoz/nerve-pod#4 (N20 mutation envelope; also documents N13/N16 ordering), SigNoz/nerve-pod#5 (N7 groupBy field-context), SigNoz/nerve-pod#8 (N14 typed `tags`)

## Decisions Log (locked)

### Family A — silent-failures (decided 2026-06-24)
- **N1** `execute_builder_query` warnings: wire through existing `extractBackendWarningMessages` + `warnBackendWarnings` + `resultWithNotes` (query_builder.go:116-123).
- **N2** `stepInterval` numeric drop: route aggregate path through `parseIntLoose` (accepts number-or-string); WARN-log a present-but-unparseable value (aggregate_helper.go:160-165).
- **N3** booleans: declare `WithBoolean`; one shared parser accepts real bool OR "true"/"false" (case-insensitive); HARD-ERROR on any other value (fixes the silent-widening `error` trace filter). Touches includeSpans, error, send_resolved, list_alerts active/silenced/inhibited, isMonotonic.
- **N4** completeness signal: append a lightweight `hasMore` (inferred from `returned == limit`) + `nextOffset` note via `resultWithNotes` on the raw-passthrough tools (search_logs/traces, get_alert_history, list_metrics, get_top_metrics); include `total` only when the backend provides it.
- **N5** empty/non-array `data`: adopt the `list_views` coerce-to-empty-page pattern in `list_dashboards` + `list_notification_channels` (+ DebugContext log); never surface "invalid response format" for an empty collection.
- **N6** notification-channel writes: keep fail-OPEN (resource was created) but surface a failed test-send as a prominent warning note block + WARN log, uniform with other advisory notes. Do NOT flip IsError (avoids misleading error + duplicate-create retries).

### Family B — error & validation strings (decided 2026-06-24)
Introduce a small shared error helper set (e.g. `internal/handler/tools/errs.go`):
- **N21+N25** `validationError(field, reason)` → canonical `Parameter validation failed: "<field>" <reason>` (capital P, the majority form); route metricName/aggregation/execute_builder_query `query` and all lowercase sites onto it.
- **N24** `requireStringArg(args, key)` → two-tier (`must be a string` / `cannot be empty`); replaces the 3+ ad-hoc variants and the single-tier ones that mislabel a wrong-typed id as "empty".
- **N22** shared constant for the "arguments is not a JSON object" guard (collapse 4-6 variants; keep the body-tool "configuration object" variant).
- **N23** drop the misleading `Internal error:` prefix on get_trace_details timestamp-parse (traces.go:184/187); reframe as user/parameter error.
- **N27** fix stale `list_dashboards` → `signoz_list_dashboards` in update_dashboard error (dashboards.go:376).
- **N9** `upstreamError(err)` → UNIFORM prefix (e.g. `SigNoz API error: <msg>`) on every upstream failure incl. the 6 bare QueryBuilderV5 sites and the create_alert/dashboard/channel/view divergence; gives the LLM a machine-detectable upstream-vs-validation marker.

### Family C — output envelope & structured content (decided 2026-06-24)
- **N17** query_metrics JSON-first: move the `[Decisions applied]`/warnings preamble into a separate note block via `resultWithNotes` so JSON is content block 0 (metrics_query.go:187-201), matching the 4 siblings.
- **N10/N11** `structuredContent` on SUCCESS: add ONLY where the shape is code-controlled — `paginate.Wrap` list/summary tools, single-resource `get_*`, and mutation results. Keep raw QB passthrough (search/aggregate/query_metrics) text-only. Document the two-tier rule.
- **Error codes** (resolves deferred N9-c): add a light shared `code` taxonomy to the `validationError`/`upstreamError` helpers (e.g. `VALIDATION_FAILED`, `UPSTREAM_ERROR`, `NOT_FOUND`), surfaced via structuredContent on errors, extending the docs tools' pattern. Lets the LLM branch on a stable code (retry vs fix).
- **N20** mutation success envelope: OUT OF SCOPE for this plan — filed as **SigNoz/nerve-pod#4** (https://github.com/SigNoz/nerve-pod/issues/4).

### Family D — param schema & description harmonization (decided 2026-06-24)
- **N12** free-text naming: rename `search_docs` `query`→`searchText` + PERMANENT `query` alias. Requires a coordinated agent-skills PR (docs-search skill names the param).
- **N14** `tags`: DEFERRED (tracked SigNoz/nerve-pod#8) — keep `get_service_top_operations`'s `WithString` JSON-string passthrough. The planned `WithArray` of strings was abandoned during Family D: the backend expects structured `[]TagQueryParam`, not `[]string`, so an array-of-strings round-trips to a masking empty `[]`. Typed-`tags` is follow-up work, NOT shipped in this batch.
- **N15** schema enums — SPLIT to avoid backend↔MCP drift (per user's drift question):
  - Hard `enum` only on STABLE/MCP-owned sets: `requestType` (scalar/time_series), `sourcePage` (traces/logs/metrics/meter), `order` (asc/desc), alert `state`, `signal`.
  - Backend-owned/EVOLVING sets (`aggregation` operators, channel `type`, panel `type`, `DataSource`): keep documented free-strings (values in description, backend validates) + add a live/periodic test pinning the MCP-advertised set against the backend's accepted set so drift fails a test, not a user. (User may override to hard-enum-everything.)
- **N26** descriptions: unify `timeRange`/`stepInterval` wording to one accurate story (shared parser → the divergent "allowed sets" like get_top_metrics dropping minutes are fictional).
- **N8** bool defaults: keep `list_alerts` active/silenced/inhibited deferred to backend (correct for a thin proxy; just verify documented `true` matches backend reality); materialize client-side only where we construct the payload (send_resolved/includeSpans — handled by N3's parser).
- **N13/N16** ordering: DEFERRED ("no updates for now") → documented on SigNoz/nerve-pod#4.
- **N7** groupBy field-context: DEFERRED → SigNoz/nerve-pod#5 (recommend: document per-signal + improve metrics heuristic, don't force-unify).

### Family E — original 5 (decided 2026-06-24)
- **K1 time units:** magnitude AUTO-DETECT (infer ns/ms/s by integer size) + advertise `ms` uniformly + require string-typed `start`/`end` (kills the >2^53 float-drop). `list_metrics` gains a real `timeRange` + standard default + routes through the shared helper.
- **K2 default window:** DOCUMENT-ONLY — keep each tool's current window (1h / 6h / 7d) but emit a JSON-Schema `default` and state it in README + descriptions. No behavior change.
- **K3 limit:** flip `search_docs` `WithNumber`→`WithString`; add ONE shared loose parser that accepts number-or-string so numeric input never silently fails. Bundled: document every default + emit JSON-Schema `default`; add a uniform clamp+note to the unclamped list tools; route `get_alert_history` through `paginate` and drop its fake "1-1000" error; keep `search_docs`'s 25 ceiling but document the bleve-memory reason.
- **K4 requestType:** KEEP per-signal defaults (scalar for aggregate, time_series for metrics — each correct for its tool). Document the aggregate default in README (currently absent) + add arg-layer enum validation so unknown values hard-error uniformly (today aggregate passes through, metrics silently coerces).
- **K5 resource ids:** unify the 4 CRUD ids → canonical `id` (`ruleId`/`uuid`/`viewId`→`id`; channels already `id`). Keep `traceId` untouched (telemetry id, not a stored-resource handle). Keep legacy names as PERMANENT silent aliases (read-side fallback; document `id` canonical, legacy deprecated-but-accepted). Backend unchanged (upstream ids are positional path segments). Typed-schema wrinkle: `update_alert`/`update_dashboard` carry the id as `json:"ruleId"`/`json:"uuid"` struct fields → rename tag to `id`, read legacy key via rawConfig fallback, update schema_compat_test.

## Context
A 42-tool inventory (during #315) surfaced parameter-naming/semantics inconsistencies across sibling
read tools: time units (ms vs ns), default windows (1h vs 6h vs 7d), `limit` type/defaults/clamping,
`requestType` default, and resource-id naming. Same failure shape as the filter/query bug (#315/#213) —
a value/param that works on one tool silently misbehaves on its neighbor — but lower frequency. These are
public MCP param contracts, so changes must weigh backward compatibility. See `.context.md` for the
grounded, file:line verification of all five (and the five details the issue under-counted).

## Remediation tiers (the framework — appetite TBD per item)
- **Tier 0 — Document-only.** Make every param description / README / manifest accurately state the unit,
  default, type, and clamp. Zero behavior change, non-breaking, ships immediately.
- **Tier 1 — Backward-compatible normalize.** Converge the surface while still accepting old inputs:
  silent alias (filter/query pattern), magnitude auto-detect for timestamps, dual-accept for types,
  add a clamp/default that only affects previously-unbounded/undocumented paths.
- **Tier 2 — Hard normalize (breaking).** Pick one unit/default/name and break old callers; requires a
  version bump and coordinated client/skill updates.

## Per-item current state + candidate approaches (decisions pending — see `.context.md` Open Questions)

### 1. Time units (ms vs ns) + list_metrics has no timeRange
- ms: all time-windowed tools except `list_services` + `get_service_top_operations` (ns). `list_metrics`: manual ms, no timeRange/default.
- Candidates: (a) magnitude auto-detect ns/ms/s by integer size [compat]; (b) standardize ms + doc the change for the 2 ns tools [breaking-ish]; (c) doc-only. Plus: require string-typed timestamps (numeric floats > 2^53 are dropped today).

### 2. Default window (1h / 6h / 7d)
- Candidates: (a) unify read tools to one default (e.g. 1h) + sync docs; (b) keep per-tool, document consistently + emit JSON-Schema `default`; (c) doc-only. get_top_metrics' 7d is a deliberate cost-analysis window.

### 3. limit (type / default / clamp)
- Candidates: flip `search_docs` to WithString (handler already accepts both); unify defaults or just document them; add a clamp to unclamped list tools; fix `get_alert_history` double-parse + honest error text; reconcile search_docs' 25 ceiling vs MaxRawResultLimit.

### 4. requestType default (scalar vs time_series)
- Candidates: (a) keep per-signal default but document on aggregate tools (currently absent from README) [recommended — defaults are semantically right per signal]; (b) unify (breaking output-shape change). Plus: add arg-layer enum validation (aggregate does none; metrics silently coerces).

### 5. Resource-id naming (ruleId / uuid / viewId / id / traceId)
- Upstream is positional — rename is MCP-boundary-only, zero upstream risk.
- Candidates: (a) leave as-is (within-family consistent; rename is churn); (b) add unified `id` alias, keep old names advertised; (c) rename to a convention with old-name aliases (filter/query pattern); (d) doc-only naming convention.

## Files likely touched (depends on tier)
- `pkg/timeutil/time.go` — auto-detect / default-window logic (items 1,2).
- `internal/handler/tools/aggregate_helper.go` — `resolveTimestamps`, `clampLimit`, `intArg`, requestType default (items 2,3,4).
- `internal/handler/tools/metrics.go` / `metrics_helper.go` — list_metrics timeRange, query_metrics default (items 1,2,4).
- `internal/handler/tools/{services,traces,alerts,logs,docs,views,dashboards,notification_channels}.go` — descriptions, limit decls, id params (all).
- `internal/docs/index.go` — search_docs limit ceiling (item 3).
- `pkg/paginate/paginate.go` — list-tool defaults/clamp (item 3).
- `pkg/types/{alertrule,dashboard}.go` — typed id fields if ids renamed (item 5).
- `README.md`, `manifest.json` — doc/metadata sync (all; mandated by CLAUDE.md).
- Tests — pin every contract changed (per CLAUDE.md cross-contract mandate).

## Verification
- Table-driven parser tests pinning each new contract (unit auto-detect, default window, clamp, requestType default, id alias).
- Regression tests proving old inputs still work where Tier 1 is chosen (e.g. ns still accepted on services).
- Live/integration via subagent (CLAUDE.md mandate) for any item where only real data exposes drift.
- Doc/manifest sync verified in the same PR.
