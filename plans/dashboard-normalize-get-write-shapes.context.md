# Feature: Normalize dashboard GET vs write shapes — Context & Discussion

## Original Prompt
> Two API gotchas worth noting (the first update attempt failed and needed retries):
> 1. `variables.*.dynamicVariablesSource` must be capitalized ("Metrics", not "metrics") on write — even though GET returns lowercase.
> 2. `queryData[].having` must be sent as an array (`[]`), not the `{expression: ""}` object the GET returns.
>
> Aren't these part of resources?
>
> Follow-up: "how to fix these? Should you expose these as input schema via resource so that mcp client or agent is able to construct these correctly in the 1st pass OR adding these in resource as info is good enough?"
>
> Resolution: "fix both"

## Reference Links
- Validation entry point: `internal/handler/tools/dashboards.go:177,220`
- Existing constants: `pkg/dashboard/dashboardbuilder/defaults.go:116` (`ValidDynamicVariableSources`)
- Strict having validator (where unmarshal fails today): `pkg/dashboard/pipeline.go:90`

## Key Decisions & Discussion Log
### 2026-04-24 — design choice: server-side coercion over schema tightening or doc-only
- Rejected: tightening MCP tool input schema (`types.Dashboard` / `types.UpdateDashboardInput`) with enums/unions. Reason: the LLM's natural flow is GET → mutate → PUT, so rejecting echoed GET shapes just causes retries. Union-typing `having` in JSON Schema is also awkward and clients handle it inconsistently.
- Rejected: documentation-only fix. Reason: LLMs frequently miss subtle casing/shape rules in resource text; the GET → PUT echo pattern is the path of least resistance.
- Accepted: permissive-in / strict-out normalization inside the existing `pkg/dashboard/dashboardbuilder` pipeline, with docs updated as a secondary safety net.

### 2026-04-24 — `having` coercion scope
- Decided to coerce only the empty/whitespace-expression object form (`{expression: ""}` → `[]`). Non-empty expression object forms are left untouched so validation can surface a clear error if they ever appear — in practice the GET API only emits the empty form.
- Applied to both `QueryData` and `QueryFormulas`. `QueryTraceOperator` skipped (not observed to carry `having`).

### 2026-04-24 — source casing normalization
- Case-insensitive lookup against `ValidDynamicVariableSources` with trim; unknown values pass through so the existing `validate.go:68-70` enum error still fires.
- Applied in `convert.go` during `rawDashboardVariable → DashboardVariable` conversion (JSON ingest path only). The fluent `builder.go` path sets the source via constants directly, so no change needed there.

### 2026-04-24 — third asymmetry discovered: filter operator casing
- GET responses may return lowercase or mixed-case operators for `queryData[].filters.items[].op` / `queryFormulas[].filters.items[].op` (e.g. `"in"`, `"Not_In"`, `"like"`), but the write API's `validFilterOperators` map (`pkg/dashboard/panelbuilder/panel_validator.go:218-228`) is strictly uppercase (`IN`, `NOT_IN`, `LIKE`, `EXISTS`, ...).
- Added `uppercaseFilterOpsInQueryMaps` in `normalize.go` that trims whitespace and uppercases each item's `op`. Symbol operators (`=`, `!=`, `>=`, `>`, `<=`, `<`) are unaffected by `strings.ToUpper`. Unknown operators still surface via the downstream `validFilterOperators` check, so we retain error quality.
- Wired into both `ApplyDefaults` and `applyBuilderDefaults` for parity (same pattern as `coerceHavingInQueryMaps`).

### 2026-04-24 — `"All telemetry"` UI-label alias
- Some SigNoz versions return the UI-facing label `"All telemetry"` for the all-sources variant, while the write enum is `"all sources"`. Added a dedicated alias inside `canonicalDynamicSource` (case-insensitive, trim-tolerant) that maps `"all telemetry"` → `DynamicSourceAllSources`. Kept alongside the lowercase-echo normalization rather than splitting into a separate helper.

### 2026-04-24 — `applyBuilderDefaults` parity resolved
- Earlier plan flagged a risk that only `ApplyDefaults` called the new normalizers. Confirmed both `coerceHavingInQueryMaps` and `uppercaseFilterOpsInQueryMaps` are now called in `applyBuilderDefaults` too (`defaults.go:337-340`), matching the JSON-ingest path. Fluent-builder callers that pass constants directly are unaffected, but the parity keeps the two code paths behaviorally identical.

### 2026-04-24 — docs kept as secondary safety net
- `basics.go:67` now mentions both the lowercase-source normalization and the `"All telemetry"` alias.
- `widgets_examples.go` has the `having (write-shape)` block plus a new `filters.items[].op (write-shape casing)` block listing the canonical operator set and noting server-side normalization.
- Docs are deliberately framed as "prefer canonical form; server normalizes" — not load-bearing, but they keep an LLM that reads resources before writing from producing obviously-wrong payloads.

### 2026-04-24 — canonical all-sources value flipped: `"all sources"` → `"All telemetry"`
- Verified upstream against `SigNoz/signoz` (commit `156459e`). `frontend/src/container/DashboardContainer/DashboardSettings/DashboardVariableSettings/VariableItem/DynamicVariable/DynamicVariable.tsx` defines the dropdown enum:
  ```
  enum AttributeSource {
      ALL_TELEMETRY = 'All telemetry',
      LOGS          = 'Logs',
      METRICS       = 'Metrics',
      TRACES        = 'Traces',
  }
  ```
  The UI dropdown writes `'All telemetry'` to `dynamicVariablesSource`; `'all sources'` only survives in two older hooks (`useDashboardVariableUpdate.ts`, `useDashboardVarConfig.tsx`) and the SigNoz frontend tolerates both on read (`.toLowerCase() === 'all telemetry'`).
- The SigNoz Go backend has zero references to any of these strings (`gh search code repo:SigNoz/signoz language:Go` for `dynamicVariablesSource`, `"all sources"`, `"All telemetry"` all return empty); dashboards are stored as opaque JSON, so backend acceptance is unconstrained. The frontend is the sole enforcer.
- Action: flipped `DynamicSourceAllSources` from `"all sources"` to `"All telemetry"` (`defaults.go:52`); reversed the alias in `canonicalDynamicSource` so legacy `"all sources"` (any case) now normalizes *to* `"All telemetry"`. Updated `normalize_test.go`, `pipeline_test.go`, `basics.go`, and the `builder.go:154` doc comment to match. All tests green.
- Reason: matches what the live SigNoz UI writes today, so dashboards we emit round-trip cleanly through the SigNoz frontend; the legacy alias keeps backward compatibility with older payloads.

## Open Questions
- [x] Should `{expression: "<non-empty>"}` be coerced? Decided: no — leave for validation error. If SigNoz later supports expression-form HAVING, revisit.
- [x] Should MCP input schema be tightened? Decided: no — server coercion is sufficient and more ergonomic.
- [x] Should `applyBuilderDefaults` also call the new normalizers? Decided: yes — parity with the JSON-ingest path, zero downside for fluent-builder callers.
- [x] Should we alias `"All telemetry"` → `"all sources"`? Decided: yes — observed in the wild from some GET responses; the canonical form is unambiguous.

### 2026-04-24 — fourth asymmetry: malformed filter-item key / wrapped $var values
- Discovered while triaging dashboard `019dbe7e-c0bd-70fc-b863-86950b526a48` ("CPU Used" value panel rendering fine on the dashboard list but failing to hydrate in the edit modal). Root cause: a `filters.items[]` entry for `k8s.node.name` with `key.dataType` missing, `key.id` collapsed to just `"k8s.node.name"` (no `--<dataType>--<type>` suffix), and `value` wrapped in a single-element `$var` array. Two sibling widgets on the same dashboard (CPU Usage by Pod, CPU Usage by namespace) had the same shape.
- Why our pipeline missed it: `dashboardbuilder` uses `[]map[string]any` for queryData (no strict typing on filter items), and `panelvalidator.validateTagFilter` (`pkg/dashboard/panelbuilder/panel_validator.go:526`) only checks `filter.Op` and `item.Op` against a whitelist — it never inspects `item.Key` structure or `item.Value` shape. `TagFilterItem.Value` is typed `any`, so scalar vs. single-element-array both unmarshal cleanly. SigNoz backend is also lenient (ClickHouse only consumes `filter.expression`); only the React edit-modal filter-row component is strict.
- Action: added `normalizeFilterItemsInQueryMaps` in `normalize.go`, wired into both `ApplyDefaults` and `applyBuilderDefaults` (parity). It (a) fills `key.dataType` (inferring from a well-formed id when possible, defaulting to `"string"` otherwise), (b) rebuilds `key.id` to `<key>--<dataType>--<type>` only when clearly malformed (empty or missing `--`), leaving canonical 3-part and 4-part ids alone, (c) unwraps `["$var"]` → `"$var"` only when the sole element is a `$`-prefixed string, preserving multi-element arrays and single-element arrays of real values.
- Tests: six `TestNormalizeFilterItems_*` cases in `normalize_test.go` (heal, preserve canonical, preserve 4-part, infer from id, selective value unwrap, nil/missing safety) plus one end-to-end `TestValidate_NormalizesMalformedFilterItem` in `pipeline_test.go`.
- Docs: added a `filters.items[].key (write-shape for edit-modal hydration)` block in `widgets_examples.go` showing canonical vs. broken forms and flagging the server-side auto-heal.
- Permissive-in / strict-out preserved: rejecting would have been user-hostile (GET returns this shape, legacy dashboards have it, echoing back is natural). Unknown/ambiguous inputs still pass through for downstream validators to surface.

### 2026-04-24 — strict validation: reject `filter.expression` / `filters.items[]` mismatch
- Scenario: CPU Used widget was eventually traced to the SigNoz backend server-authoritatively rewriting `temporality` (`null` → `""`) and `filter.expression` (3-clause → 2-clause) for Sum-type metrics (`k8s.node.cpu.usage`, `k8s.pod.cpu.usage`). Neither field survives a PUT through our MCP tool, which means we cannot *normalize* them into consistency. Separately, during diagnosis it became clear that an inconsistent payload — where `filter.expression` has fewer predicates than `filters.items[]` — is something an LLM-authored dashboard could produce at creation time, and the SigNoz backend accepts it silently, letting the list view and edit modal drift.
- Decision: add a strict **reject** check for `filter.expression` / `filters.items[]` mismatches, even though SigNoz accepts them. Permissive-upstream does not mean correct. Only runs when both sides are non-empty; expression-only or items-only payloads pass through unchanged (either is a deliberate single-source-of-truth configuration).
- Implementation: `validateFilterExpressionConsistency` in `pkg/dashboard/dashboardbuilder/validate.go`, called per queryData and queryFormula entry from `validateBuilderQuery`. For each `filters.items[i].key.key`, requires the key string to appear in `filter.expression`; on any miss, emits a field error naming the missing keys and showing the offending expression.
- Scope decision: this is the one case where we tighten validation rather than permissively normalize. The earlier normalizers (source, having, op, filter-item shape) fix *shape* drift where the canonical repair is unambiguous. Here the "repair" would be rebuilding the expression — but SigNoz rewrites expression on save, so any rebuild we do is cosmetic. Rejecting forces the agent to produce coherent payloads at generation time, which is both sides' intent and the only layer where coherence is actually enforceable.
- Tests: five unit cases in `validate_test.go` (mismatch rejected, consistent passes, empty expression skipped, empty items skipped, formula-side mismatch rejected) plus an end-to-end `TestValidate_RejectsFilterExpressionMismatch` in `pipeline_test.go`.
- Docs: `widgets_examples.go` NOTE updated to state the rejection rule and the rationale (list view vs. edit modal drift).
