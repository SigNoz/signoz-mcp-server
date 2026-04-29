# Plan: Normalize dashboard GET vs write shapes

## Status
Done

## Context
SigNoz dashboard GET and write APIs accept subtly different shapes for several fields. An agent that fetched a dashboard and echoed fields back on PUT would hit one of three rejections:

1. `variables.*.dynamicVariablesSource` — GET may return any casing (e.g. `"metrics"`) or the legacy alias `"all sources"`; the canonical write form (matching the SigNoz UI dropdown's `AttributeSource` enum) is `"Traces"`, `"Logs"`, `"Metrics"`, or `"All telemetry"`.
2. `queryData[].having` / `queryFormulas[].having` — GET returns `{expression: ""}`; write requires `[]HavingClause` (empty `[]` for no HAVING). Strict unmarshal into `panelvalidator.BuilderQuery.Having` at `pkg/dashboard/pipeline.go:90` fails with an opaque error.
3. `queryData[].filters.items[].op` / `queryFormulas[].filters.items[].op` — GET may return lowercase/mixed-case (e.g. `"in"`, `"Not_In"`); write's `validFilterOperators` (`pkg/dashboard/panelbuilder/panel_validator.go:218-228`) is strictly uppercase.
4. `queryData[].filters.items[].key` shape — some dashboards (including ones authored via the SigNoz UI) have items with missing `key.dataType`, non-canonical `key.id` (collapsed to just `<key>` instead of `<key>--<dataType>--<type>`), and `value` wrapped in a single-element `$var` array. The list renderer tolerates this via the top-level `filter.expression` string, but the edit modal's autocomplete hydration fails.

All three are fixed at the `dashboardbuilder` layer with a *permissive-in / strict-out* normalization pass: accept either shape from the LLM, emit the canonical form to SigNoz.

## Approach

Three helpers in `pkg/dashboard/dashboardbuilder/normalize.go`, all pass-through on unknown/absent input:

- `canonicalDynamicSource(s)` — case-insensitive, trim-tolerant lookup against `ValidDynamicVariableSources`, with a dedicated alias mapping the legacy `"all sources"` (any case) → canonical `"All telemetry"`. Unknown values pass through so `validate.go:68-70` still surfaces a clear enum error.
- `coerceHavingInQueryMaps(entries)` — rewrites `having: {expression: ""}` (empty/whitespace only) to `having: []`. Array form, nil, missing, and non-empty-expression object forms are left untouched.
- `uppercaseFilterOpsInQueryMaps(entries)` — trims whitespace and uppercases each `filters.items[].op`. Symbol operators are unaffected by `strings.ToUpper`; unknown operators still surface via the downstream `validFilterOperators` check.
- `normalizeFilterItemsInQueryMaps(entries)` — heals filter-item key/value shape: fills `key.dataType` (inferring from a well-formed id when possible, else defaulting to `"string"`), rebuilds `key.id` to `<key>--<dataType>--<type>` only when clearly malformed (empty or missing `--`), and unwraps `["$var"]` → `"$var"` only when the sole element is a `$`-prefixed string. Canonical 3-part/4-part ids and genuine multi-/single-value arrays pass through untouched.

One strict check (not a normalizer):
- `validateFilterExpressionConsistency` in `validate.go` — rejects builder queryData and queryFormula entries whose `filter.expression` omits any `filters.items[].key.key`. Enforced even though the SigNoz backend accepts the mismatch, because the list/graph renderer and the edit-modal's expression-reconstruction disagree otherwise. Only runs when both sides are non-empty — single-source-of-truth configurations (expression-only or items-only) pass through.

Wiring:
- `convert.go` — apply `canonicalDynamicSource` during `rawDashboardVariable → DashboardVariable` conversion (JSON ingest path).
- `defaults.go` — call `coerceHavingInQueryMaps` and `uppercaseFilterOpsInQueryMaps` on both `QueryData` and `QueryFormulas` inside the builder-nil-init block of **both** `ApplyDefaults` (JSON-ingest path) and `applyBuilderDefaults` (fluent-builder path). Fluent-builder callers that pass constants directly are unaffected; parity keeps the two paths behaviorally identical.

Because `dashboardbuilder` owns serialization to SigNoz via `DashboardData.ToJSON()`, the normalized shape reaches the API. Because normalization runs before `widgetToPanel` in `pipeline.go`, the strict `panelvalidator` types see the canonical form and unmarshal cleanly.

## Files Modified

- `pkg/dashboard/dashboardbuilder/normalize.go` — new; the three helpers.
- `pkg/dashboard/dashboardbuilder/normalize_test.go` — new; unit coverage for all three helpers including `"All telemetry"` aliases, uppercase-with-trim, nil/missing/wrong-type edge cases.
- `pkg/dashboard/dashboardbuilder/convert.go` — call `canonicalDynamicSource` at the variable-conversion site.
- `pkg/dashboard/dashboardbuilder/defaults.go` — call `coerceHavingInQueryMaps` and `uppercaseFilterOpsInQueryMaps` on `QueryData` and `QueryFormulas` in both defaults paths.
- `pkg/dashboard/pipeline_test.go` — `TestValidate_NormalizesGetShapePayload` covers source case, `having` shape, `"All telemetry"` alias, and filter-op casing in a single round-trip; `TestValidate_InvalidDynamicSourceStillRejected` guards the negative path.
- `pkg/dashboard/basics.go` — clarifies that the server normalizes lowercase source and the `"All telemetry"` alias on write.
- `pkg/dashboard/widgets_examples.go` — adds `having (write-shape)` and `filters.items[].op (write-shape casing)` notes so LLMs reading the resource see both asymmetries in one place.

## Verification

- `go test ./pkg/dashboard/...` — all green, including new unit tests and the end-to-end `TestValidate_NormalizesGetShapePayload`.
- `go test ./...` — no regressions across the repo.
- `go build ./...` — clean.
- Negative-path check: `TestValidate_InvalidDynamicSourceStillRejected` + existing `validate_test.go` negative case confirm truly invalid values still surface the enum error.
- Read-through sanity check on `widgets_examples.go`: an LLM reading the resource sees the `having` and `filters.items[].op` write-shape notes adjacent to each other, making the GET → write asymmetries discoverable without separate tool calls.

## Out of Scope
- Tightening MCP tool input schemas (`types.Dashboard` / `types.UpdateDashboardInput`) — server coercion makes it unnecessary.
- Changes to alerts, views, or other entities.
- `README.md` / `manifest.json` updates — the fix is transparent and adds no user-visible tool metadata.
- Any additional API-shape normalizations not yet observed in the wild.
