# Plan: July–September 2026 released-feature parity (#232)

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/232
PR: https://github.com/SigNoz/signoz-mcp-server/pull/313
Companion PR: https://github.com/SigNoz/agent-skills/pull/98

The independently reviewed hard-cut plan was approved by the user on 2026-09-18.
Implementation, PR CI corrections, and companion-main synchronization are complete.

## Context

Issue [#232](https://github.com/SigNoz/nerve-pod/issues/232) identifies gaps between
released SigNoz capabilities and the MCP server/companion skills. Target the
released v0.142.0 source at `57268e50b4907194cf4a6628143df3d6aaa1424e`.
Current MCP planning baseline: `d227e4d91b8b933bee0dd03a1d624ca719cdb95e` (rechecked 2026-09-18).
The original source audit used `4be75edb6deb639059ad37b6c4fbd420d8b47486`;
the pulled changes do not modify the feature contracts covered here.

The user requested planning and independent review before their approval of
implementation. The parent agent acts as orchestrator/reviewer; implementation
is now authorized.

### Open questions

- [x] User approved the revised hard-cut plan and implementation on 2026-09-18.

## Delivered scope

Implemented five bounded changes: queryless Markdown dashboard panels, explicit
full-text log search guidance/quoting, raw heatmap query support, four new
notification providers, and accurate system dashboard guidance. Include
companion skills and pinned backend contract tests.

Target SigNoz v0.142.0 or newer with a hard cut to canonical contracts.
Adopt notification v2 APIs and `config.kind/spec`; remove legacy channel
parameters and v1 fallback. Do not add dual-mode adapters or older-backend
support. Record breaking changes and update companion skills in the same release. Defer dedicated infra/GCP/preferences/admin
APIs, AI observability, and ongoing release-sync automation as described below.

## Approach

Extend existing tools and resources around the released canonical contracts.
Backward compatibility is explicitly not a requirement. Remove obsolete aliases
when changing the corresponding surface; do not expand this into a repository-wide
alias cleanup. Keep domain normalization needed for canonical read-modify-write
(such as excluding server-owned dashboard fields), not legacy payload adapters.

Use a single minimum backend of v0.142.0. No endpoint/version fallback,
legacy notification wire format, dual schemas, or old-backend test matrix.
Reject obsolete argument forms clearly; do not silently ignore them. Document
the intentional breaks and coordinated server/skills rollout. Existing query
bounds and body-search behavior may remain because they are useful semantics,
not because old callers must be supported.
Add no Heatmap dashboard plugin: this release ships a heatmap query contract.
AI observability is excluded; ongoing release-sync automation belongs to #233.

### 1. Text panels and system dashboards

Source evidence (pinned v0.142.0, commit `57268e50b4907194cf4a6628143df3d6aaa1424e`,
local clone `/tmp/signoz-v0.142.0`):

- `signoz/TextPanel` is registered in `pkg/types/dashboardtypes/perses_plugin_wrappers.go`
  (plugin map and variant list) with spec fields `mode`, `text`, `presentation`,
  `headerOptions` defined in `pkg/types/dashboardtypes/perses_signoz_plugins.go`.
  The released OpenAPI (`docs/api/openapi.yml`) adds it to the panel-plugin
  discriminator/oneOf and defines `DashboardtypesTextPanelSpec`,
  `DashboardtypesTextMode`, `DashboardtypesTextPresentation`, and
  `DashboardtypesHeaderOptions`. `mode` is `markdown` only; `textAlign` is
  `left|center|right`; `verticalAlign` is `top|center|bottom`;
  `presentation.background` is a nullable string. Hex-color enforcement lives on
  the Go validate tag and is NOT advertised in OpenAPI, so the generated schema
  must not invent it; guidance documents it and the server validates.
- Query-count semantics are kind-conditional
  (`pkg/types/dashboardtypes/perses_dashboard_data.go`,
  `validatePanelQueryCount`): every panel must send a non-null `queries` array;
  TextPanel must send `queries: []` (null rejected; one or more entries rejected
  with "renders without a query and must have queries: []"); every other panel
  kind still requires exactly one query.
- Server-side defaulting (`pkg/types/dashboardtypes/perses_dashboard_test.go`,
  `TestValidateTextPanel`): omitted fields marshal back as `mode: "markdown"`,
  `text: ""`, `textAlign: "left"`, `verticalAlign: "top"`,
  `headerOptions.hide: false`; `background` has no default (omitted stays
  omitted). Guidance recommends explicit values; round-trip tests assert the
  materialized defaults rather than echoing input.
- System dashboard semantics use `DashboardV2` in
  `pkg/types/dashboardtypes/perses_dashboard.go` and released handlers/store in
  `pkg/modules/dashboard/impldashboard/`, not legacy `Dashboard` helpers.
  `source` is `user|system|integration`. `DashboardV2.ErrIfNotMutable` rejects
  every non-user source; update/patch call `ErrIfNotUpdatable`, which also
  checks locks. System and integration dashboards cannot be modified, deleted,
  or lock/unlocked by users. Internal unsafe updates are reconciler-only.
  Clone permits user/integration, not system; no MCP clone tool is planned.
  Do not teach the legacy public-sharing rule as a released v2 MCP capability.
- The released v2 store's `ListV2`/`ListForUser` already exclude system rows.
  MCP must follow that upstream list behavior, without adding a second filter.
  Get-by-ID may return `source: "system"`; keep that successful result and its
  source intact. No dedicated system-dashboard or system-by-name tool is needed.
  Keep only brief source/operation guidance on existing tools and resources.
  `DashboardtypesUpdatableDashboardV2` omits source, so canonical update
  normalization must continue excluding that server-owned field.
- Source-filter correction (pinned source, to be live-confirmed): in v0.142.0 the
  dashboard list-filter DSL does NOT accept `source` as a filterable column.
  `ReservedOps` in `pkg/types/dashboardtypes/list_filter.go` covers only
  `name`, `description`, `created_at`, `updated_at`, `created_by`, `locked`.
  `source` is only a reserved tag name (`reservedDSLKeys`), so `source =
  'system'` would parse as a tag filter. For returned user/integration records,
  source filtering must be client-side across pages. It cannot discover the
  system dashboards excluded upstream. Treat `reservedKeywords` as authoritative.

Changes:

- `internal/handler/tools/schemas/extract_schemas.py` — pin the fetch URL to the
  released v0.142.0 tag (no `main`), record tag + commit provenance in the
  docstring/recipe. Keep `searchContext` injection, discriminator pinning,
  and nullable conversion. Use canonical `id` only; remove the legacy `uuid`
  input alias from the changed dashboard contracts and their generator hooks.
- `internal/handler/tools/schemas/dashboard_create.json` and
  `dashboard_update.json` — regenerate from the pinned spec; diff must show only
  released TextPanel additions plus the explicitly planned canonical-input changes.
- `internal/handler/tools/schemas/dashboard_patch.json` — regenerate from the
  same pinned spec for consistency (no TextPanel-specific structural change
  expected; it references `DashboardtypesJSONPatchOperation`).
- `pkg/dashboard/widgets.go` — replace the unconditional "a panel holds exactly
  ONE query" rule with the kind-conditional rule above; add TextPanel selection
  guidance (static Markdown runbooks/notes, `queries: []`, markdown-only mode).
- `pkg/dashboard/widgets_examples.go` — add one worked TextPanel panel example
  with `queries: []` and explicit `presentation`/`headerOptions`.
- `pkg/dashboard/dashboard_examples.go` — include a full worked dashboard that
  places a TextPanel (Markdown notes) alongside a query panel, exercising layout
  `content.$ref` linkage in the same payload.
- `pkg/dashboard/patch.go` — add TextPanel recipes: two-op add (panel +
  grid-item `$ref`), edit text at `/spec/panels/<id>/spec/plugin/spec/text`,
  and a note that query-replacement paths do not apply to queryless panels.
- `pkg/dashboard/list_filter.go` and the dashboard instructions resource text —
  briefly document source values, upstream list exclusion of system rows,
  get-by-ID preservation, non-user immutability, and the source-filter limitation.
  Denied update/patch/delete calls use the shared coded upstream-error path.
  Do not create a system-dashboard feature or discovery workflow.
- `README.md` and `manifest.json` — refresh resource descriptions that enumerate
  panel types/examples (widgets-examples and widgets-instructions metadata).
- Companion `SigNoz/agent-skills` (baseline `70746a2`):
  `signoz-creating-dashboards/SKILL.md`,
  `signoz-modifying-dashboards/SKILL.md`, and both
  `references/dashboard-to-query-builder-v5.md` copies — add TextPanel usage,
  `queries: []`, skip dry-run for queryless panels, and system/integration
  immutability. Repair stale v1 shapes (e.g. `panelTypes`, `queryData`,
  `panelMap`, `selectedLogFields`) only in paths this feature exercises so the
  dashboard examples are coherent end-to-end; defer unrelated broad v1→v2 skill
  cleanup to a separate companion follow-up.

Contract decisions:

- No `signoz/HeatmapPanel` invention (registry has none in v0.142.0).
- No clone tool and no system-dashboard-by-name tool in this scope.
- Keep schema-driven stripping of `source` on update; never write it.
- Do not encode hex-color validation in the JSON Schema; document and let the
  server validate.
- Retain canonical dashboard normalization for server-owned fields. Do not
  retain legacy aliases or old panel shapes in the regenerated contracts.
  Schema/handler parity tests must reject removed forms with clear guidance.

Acceptance evidence:

- Unit/regression: extend
  `internal/handler/tools/dashboard_schema_test.go` so widget examples cover a
  TextPanel; add a guard asserting both create and update plugin unions advertise
  `signoz/TextPanel` (catches unpinned-regen drift); extend
  `pkg/dashboard/patch_test.go` resource checks for the TextPanel recipes; pin
  `source` preservation in get and returned user/integration list fixtures,
  upstream system-list exclusion, and stripping on update in
  `internal/handler/tools/dashboards_test.go`. Full local gates per the
  Verification section (`make fmt goimports`, `go test ./...`,
  `go build ./cmd/server`).
- Live (delegated subagent, ephemeral pinned v0.142.0 backend, full cleanup and
  absence verification): create → get a dashboard containing a TextPanel with
  explicit fields → patch text/presentation/header → get. Assert Markdown,
  presentation, header settings, layout `content.$ref`, and `queries: []`
  round-trip, and that server defaults materialize as pinned-source tests
  predict. Confirm on the live build that the list response's
  `reservedKeywords` excludes `source` (closing the source-filter correction),
  system rows are absent from list, get-by-ID preserves a known system row,
  and denied update/patch/delete of system/integration rows return top-level
  coded errors. Use seeded existing system records; never mutate or delete
  platform-owned records as cleanup.

### 2. Explicit full-text log search

Source evidence (pinned v0.142.0, commit
57268e50b4907194cf4a6628143df3d6aaa1424e):

- pkg/querybuilder/where_clause_visitor.go (VisitSearchCall) implements
  search('term'[, body, attribute, resource, log]). The term is literal and
  case-insensitive; with no scope it fans out to every searchable log column.
  Multiple scopes are ORed, while surrounding filter expressions retain normal
  AND/NOT semantics. Valid scopes are exactly body, attribute, resource, and
  log (quoted or bare). The upstream visitor emits its cross-field cost warning
  and marks the statement for the querier scan guard.
- pkg/telemetryschema/logstelemetryschema/filter_expr_search_test.go pins
  quoted and bare terms, NOT, field-filter composition, scoped forms, invalid
  scopes, and the twelve bound parameters emitted for an unscoped search.
  pkg/telemetryschema/logstelemetryschema/storage.go (searchColumns) is the
  source of truth for those searchable columns.
- pkg/querybuilder/where_clause_visitor.go (trimQuotes) and
  where_clause_normalizer.go (FilterStringLiteral) define expression-string
  quoting: replace each literal backslash with two backslashes, replace each
  apostrophe with backslash-apostrophe, then wrap in single quotes. Upstream
  reverses those two escapes before parameter binding; a double-quoted term
  uses the same escape rules.
- pkg/types/savedviewtypes/spec.go persists saved-view queries as full v5
  QueryEnvelope values and validates their filter expressions, so an expression
  containing search() requires no separate persistence model.

Proposed behavior:

- Keep filter as an expression string and pass explicit search() through
  unchanged. Teach, but do not rewrite, search() as the correct predicate when
  the target field is unknown; once discovery identifies a field, prefer a field
  predicate such as body CONTAINS 'timeout' or a structured attribute filter.
- Keep signoz_search_logs.searchText as an explicit body-text convenience:
  it constructs body CONTAINS; use filter search() for cross-field search.
  This is a task distinction, not a compatibility guarantee. Describe both.
  When searchText and filter are both supplied, combine them with AND.
- Add one shared literal-quoting helper for service, severity, and searchText
  literals composed into the log filter; do not quote the raw filter expression. It must escape backslashes
  before apostrophes. Preserve the caller's expression bytes except for the
  existing documented joining behavior; never concatenate untrusted text into an
  unescaped literal.
- Reconcile #149 this way: a bare full-text token is still an error when the
  backend has no configured full-text column; explicit search() is valid on
  backends with the released full-text function; and body CONTAINS is valid but
  body-only. Do not rewrite old backend failures. Propagate upstream
  validation/cost errors through the shared coded error path and preserve any
  upstream search() warning.
- Update the logs query-builder guide and signoz_search_logs parameter text
  with exact syntax, scopes, AND/OR semantics, quoting, cost guidance, and the
  searchText distinction. Keep searchContext present and outside required.
- Add create/update saved-view preservation tests whose raw builder
  filter.expression contains quoted, escaped, scoped, negated, and composed
  search() expressions. The server may remarshal its raw map; the nested
  expression and query shape must remain semantically unchanged.
- Companion SigNoz/agent-skills changes are required because this changes taught
  query behavior, not the wire contract: update
  plugins/signoz/skills/signoz-generating-queries/SKILL.md,
  signoz-managing-views/SKILL.md, and any query reference that still says full
  text is unsupported. Replace the current “use searchText for body text”
  shortcut guidance with the body-only caveat and an explicit cross-field
  filter: "search('timeout')" example. Add scoped search, literal escaping,
  minimum-version guidance, canonical filters, and a worked saved-view example.

Released contract:

- Require v0.142.0+ for this release. The historical function/product release
  dates remain provenance, not a supported-backend matrix. No fallback from
  search() to body CONTAINS and no older-backend compatibility layer.
- Use canonical `filter`; remove the legacy `query` alias from changed log
  tools. Caller-authored expressions are forwarded without semantic rewrites.
  `searchText` stays body-only as an intentional convenience with explicit copy.
- Quoting fixes apply consistently to every literal the log helper constructs.

Acceptance tests:

- Unit-test the helper with plain text, apostrophes, backslashes, combined
  escapes, double quotes, and Unicode; pin generated expressions for searchText,
  service, and severity.
- Fixture-test explicit search() round-trip in QueryPayload validation and
  execute-builder request capture; include NOT, a field predicate, and every
  scope. Assert the upstream body is not rewritten.
- Test malformed syntax, invalid scope, and canonical-input rejection without
  silently substituting body CONTAINS or accepting the removed query alias.
- Test saved-view create/update preservation for escaped search() expressions.
- E2E against pinned v0.142.0: run unscoped, scoped, negated, and composed
  searches; capture and preserve the upstream cost warning; verify searchText
  still misses a value present only in a non-body field while unscoped search()
  finds it. Use read-only queries and never persist credentials.

### 3. Heatmap query requests and results

Source evidence (pinned v0.142.0):

- pkg/types/querybuildertypes/querybuildertypesv5/req.go defines bucketOptions
  on builder queries and formulas as a kind-discriminated union. kind is linear
  or log; spec is required. A log "spec": {} requests its defaults; a
  linear spec must supply maxValue. Linear requires finite maxValue > 0; omitted or zero numBuckets resolves to
  60, and the hard maximum is 512. Log omits scale for default 4 and accepts
  integer scales from -4 through 4. Unknown fields are rejected by upstream's
  strict decoder.
- validation.go allows requestType "heatmap"; requires exactly one enabled
  output; accepts one metric builder query, one formula over metric inputs, one
  ClickHouse query, or one PromQL query; rejects logs/traces signals,
  fillGaps, per-query functions, and non-empty having (including disabled
  formula inputs); and rejects enabled-output bucketOptions outside heatmap. Structural bucket
  decoding applies to every supplied options object, but semantic bucket
  validation and axis application use only the enabled query/formula.
- heatmap.go resolves absent options to the finest log axis (log, scale 4).
  Explicit linear/log axes apply to gauge, sum, and summary metrics. Histograms
  derive their axis from le labels and reject options; exponential histograms
  and metrics with an unknown type are rejected after metadata resolution.
- request_type.go, querier.go, and postprocess.go run formula inputs as time
  series, then bucket the enabled formula output. resp.go returns
  TimeSeriesData.aggregations[].meta.buckets (ascending upper bounds) and
  per-timestamp values with one count per bound plus a final open-above overflow
  count; scalar value is omitted. series_limit.go ranks heatmap series by the
  sum of bucket counts.
- pkg/types/savedviewtypes/spec.go accepts any valid v5 request type and
  validates the persisted QueryEnvelope with heatmap options, while its
  rendering panelType enum remains value/graph/table/list/trace. The pinned
  release has no dashboard Heatmap panel.

Proposed server behavior:

- Add typed BucketOptions, linear/log spec, strict discriminator decoding, and a
  nullable bucketOptions JSON field to both types.QuerySpec and
  types.FormulaSpec in pkg/types/querybuilder.go. Preserve the supplied bucket fields and values without changing the kind
  or turning {} into defaulted fields; JSON whitespace need not be preserved.
  Explicit null spec follows upstream zero-value decoding: log requests defaults,
  enabled linear fails maxValue validation, and disabled inputs remain structurally valid.
  Locally enforce kind, required spec, linear finite positive maxValue, linear
  numBuckets in 0..512 (0 means default), log integer scale in -4..4, and
  unknown-field rejection. Defer metric-type-dependent and metadata-dependent
  failures to upstream.
- Allow heatmap for raw signoz_execute_builder_query envelopes. Permit an
  enabled builder_formula, with metric inputs disabled as needed, but require
  exactly one enabled output overall. Reject enabled non-heatmap bucketOptions, heatmap
  fillGaps, heatmap functions, and non-empty heatmap having consistently with
  upstream. Pass ClickHouse and PromQL heatmap envelopes with their existing
  typed checks and let upstream own their execution-specific validation.
- Preserve authored limit, offset, and order. Keep existing protective checks
  (negative limit/offset, limit above 10,000). For omitted bounds, retain MCP's
  bounded defaults rather than sending zero: standalone/enabled metric and
  formula outputs use the 100-series default; named disabled formula inputs use
  10,000; heatmap metric defaults use result-descending order like time series.
  Record every injected decision in the existing applied-bounds note. This uses
  upstream's heatmap-aware series ranking rather than stripping limits.
- Return the upstream heatmap JSON verbatim. Do not flatten meta.buckets,
  remove the overflow count, convert values to scalar value, or reshape the
  response for older clients. Existing backend-warning extraction and notes must
  remain out-of-band and must not alter the response body.
- Do not invent signoz/HeatmapPanel or advertise dashboard panel support. Cover
  raw heatmap-query preservation through the existing saved-view contract only.
  Preserve the existing source/panelType on a compatible saved-view fixture;
  do not recommend a rendering panelType or promise heatmap UI rendering.
  Backend persistence acceptance does not establish released UI support.
- Keep the dedicated convenience signoz_query_metrics limited to scalar and
  time_series in this slice. Heatmap requires bucket-axis and result-shape
  guidance that belong to the raw builder contract; adding a convenience
  surface can be a separate additive proposal.
- Update the raw query-builder tool description, query resources, README.md,
  and manifest.json with the exact heatmap matrix, bucket constraints, output
  shape, limit decisions, and version caveat. Keep searchContext unchanged.
- Companion SigNoz/agent-skills changes are required: extend
  signoz-generating-queries/SKILL.md and its shared request-matrix/reference
  with heatmap envelopes, one-enabled-output rules, disabled formula inputs,
  bucket variants/defaults, metric-type constraints, output interpretation, and
  limits. Update view-management guidance to preserve heatmap specs without
  claiming a Heatmap dashboard panel exists.

Released contract:

- Require v0.142.0+; use heatmap only when explicitly requested. Keep ordinary
  scalar/time_series behavior and bounded query defaults because they serve
  distinct query tasks. No older-backend fallback or dual response format.
- Treat authored bucket fields and returned bucket metadata/counts as the
  canonical contract. Unsupported or malformed requests return coded errors.

Acceptance tests:

- pkg/types/querybuilder_test.go: round-trip metric query, metric query plus
  disabled formula input, and enabled formula with linear/log/absent options;
  assert field preservation, defaults are not injected into bucketOptions, and
  every released invalid combination.
- Handler tests: execute-builder request capture for each enabled-output type;
  non-heatmap options rejection; fillGaps/functions/having rejection (including
  disabled inputs); exactly-one-enabled-output enforcement; authored bounds
  preservation and applied-bounds notes.
- Response-preservation tests using a sanitized real v0.142.0 response (or a
  provenance-recorded recorded response while live setup is unavailable): assert
  labels, timestamps, meta.buckets, per-point values, final overflow count,
  omitted scalar value, and byte-equivalent upstream result data aside from the
  existing note envelope.
- Series-limit tests: heatmap ranking follows summed bucket counts; formula
  inputs get the 10,000 default and enabled output/formula gets the 100 default;
  authored values remain untouched.
- Saved-view create/update tests: heatmap requestType, enabled/disabled formula
  graph, and both bucket variants survive raw-map remarshal; invalid upstream
  specs return the upstream error rather than local mutation.
- E2E against pinned v0.142.0 with a discovered metric: run absent-options log
  axis, explicit linear and log axes, and a formula heatmap; verify bucket and
  overflow shape. No older-backend lane is required. Use read-only
  queries, clean up any temporary saved view, and never print credentials.

### 4. Notification channels

Source-audited against pinned v0.142.0 commit `57268e50b4907194cf4a6628143df3d6aaa1424e` (`/tmp/signoz-v0.142.0` and `/tmp/signoz-src-v0142`). This is a breaking hard cut: all five tools use only `/api/v2/notification_channels*` and canonical `config.kind/spec`; the minimum backend is v0.142.0. No v1 receiver envelope, flat provider parameters, fallback route, dual schema, or old-backend lane remains.

**Audited upstream behavior.** v2 create accepts `name,generateName,displayName,config`; update and test accept only `config`; get returns `name,displayName,config,id,createdAt,updatedAt`. Strict decoding rejects unknown fields at body and spec levels. Create requires either a DNS1123-label `name` or `generateName:true` with empty name; omitted `displayName` defaults to explicit `name`, and generated names slugify `displayName` plus a random suffix. `name` and `displayName` are immutable on update. New IDs are generated as UUIDv7; the released UUID parser accepts all valid UUID versions. Existing display names create HTTP 409 `alertmanager_channel_already_exists`; invalid identity/config is HTTP 400. Update is a full config replacement and can change kind. Test is config-only, ephemeral, and never takes an ID.

List passes `query,kind,sort,order,limit,offset` upstream. `query` is case-insensitive display-name search; sort is `updated_at|created_at|name` (`name` orders by display name); order is `asc|desc`. Defaults are `updated_at desc` and limit 20, limit above 200 is clamped server-side, negative values and query over 1024 characters are invalid. Output is config-free `channels[]` plus `total` for the filtered set; each item has `id,name,displayName,kind,createdAt,updatedAt`. Get is the only routine secret-bearing call: it returns plaintext credentials in `config.spec` because `format: password` is only a schema hint. Alert rules and routing policies continue to reference `displayName`, not DNS1123 `name`. Deletion can be rejected while a routing policy references the channel; propagate that dependency error.

**Canonical provider schemas.** The tool input exposes one discriminated `config` object with `kind` and `spec` required. All specs allow boolean `sendResolved`; omission follows the upstream provider default, canonical get materializes the effective value, and verbatim update preserves it. Do not restore the old forced `true` default. Required and optional fields, matching `channelconfig.go`, are:

| Kind | Required | Optional |
| --- | --- | --- |
| slack | apiUrl | channel, title, text |
| email | to | html, headers |
| webhook | url | username, password, bearerToken (basic auth requires both parts; bearer cannot combine with them) |
| pagerduty | routingKey | url, source, client, clientUrl, description, severity, component, group, class, details |
| opsgenie | apiKey | apiUrl, message, description, source, details, priority |
| msteams | webhookUrl | title, text |
| googlechat | webhookUrl | title, text |
| jira | site, project, issueType, email, apiToken | summary, description, priority, labels, resolveTransition, reopenTransition, canonical reopenDuration, wontFixResolution, customFields |
| jsmops | apiKey | message, description, priority, tags |
| incidentio | url, token | title, description, metadata |

Typed schemas should advertise these fields and reject snake_case receiver fields, top-level `type`, and former `<provider>_<field>` arguments. `searchContext` remains top-level. Unknown fields are honestly rejected now because upstream strict decoding rejects them; if a future upstream accepts one, preserve it rather than silently dropping it. Invalid or mixed legacy storage must make get fail rather than become an invented empty config.

**Tool shapes and outcomes.**

- Create sends canonical identity plus `config` and returns the complete gettable channel. It supports explicit-name and generate-name modes.
- Get takes `id` and returns the complete canonical channel.
- Update takes `id` plus complete replacement `config`; it has no name/displayName fields. Copy a current get `config` verbatim, merge only requested spec changes, and send the whole object so every modeled field and secret survives. Return the upstream updated channel, not state synthesized from the request.
- List and delete stay ID/display-purpose tools as described above.
- `test` is explicit boolean, default `false`, on create and update. `false` makes no external send. `true` sends the exact accepted config to the config-only test endpoint only after mutation succeeds. No standalone test tool is in scope.
- Local schema and identity failures return coded `VALIDATION_FAILED` before HTTP. Upstream 400/404/409 and 401/403 use the shared coded `upstreamError` path; auth failures never hide in partial list results. A 409 create means no channel was created and replay needs a new identity. Ordinary post-write test-delivery/read-back failures return the known mutation result with a bounded advisory, never a false claim of delivery. Post-write SigNoz 401/403 must still use the shared top-level coded error path, augmented with the known channel ID and `mutationCommitted:true` so the caller can re-authenticate and inspect without repeating the mutation. Do not downgrade auth failures into notes. Report test status explicitly as skipped/succeeded/failed/unknown; a transport success is not proof of final provider delivery. Update retains `readOnly=false,destructive=true,idempotent=false`: tool annotations apply to every accepted call, and `test:true` can send again. Create remains `readOnly=false,destructive=false,idempotent=false`. No mutation/test is automatically replayed after an ambiguous failure. Pin annotations, committed-write/auth-failure outcomes, and negative legacy-argument cases.

Never log canonical config/request JSON, get/update bodies, test payloads, or credential-bearing errors. Instruct agents to copy secrets from get into update without echoing them. List remains config-free.

**Files and tests.** Replace v1 client methods with v2 create/list/get/update/delete/test calls and query encoding. Rewrite `internal/handler/tools/notification_channels.go`, typed schemas, README, `manifest.json`, alert guidance, wire-catalog/guardrail fixtures, and companion skills together. Retire the old receiver builder unless the client still needs a narrow canonical wire type.

Acceptance, with no external sends during planning: per-kind mock schema and wire fixtures for all ten providers; strict unknown-field, discriminator, required-field, webhook auth, Jira duration, and type failures; list defaults, filters, sort, negative errors, 200 clamp, filtered total, config-free rows, and 401/403 propagation; identity/generate-name, immutable-name, valid UUIDs (including existing non-v7 IDs), 404, and 409 replay cases; get-to-verbatim-update preservation for every field and secret, kind change, and explicit versus omitted `sendResolved`; default no-test and opt-in config-only test behavior, including mutation-success/test-failure notes; and sanitized provenance-recorded real v0.142.0 responses (or delegated live contract verification with cleanup). Any live `test:true` case may target only a local capture sink. CMP-3 requires companion-skill updates for canonical config, displayName routing, full preservation, removed flat inputs, and opt-in tests.

### 5. Broader product coverage

Explicit scope decisions (defer all four; do not expose tools in this feature):

| Area | Decision | Follow-up acceptance boundary |
| --- | --- | --- |
| Infrastructure Monitoring v2 / container inventory | Defer dedicated entity APIs. Raw metrics/list queries remain available but must not be presented as inventory coverage. | Revisit only alongside existing host-list work in [signoz-mcp-server #67](https://github.com/SigNoz/signoz-mcp-server/pull/67); any accepted follow-up must define entity endpoints, filter/sort contracts, pagination, and live round-trip coverage against v0.142.0+. |
| GCP / cloud integration management | Defer; v0.142.0 exposes `/api/v1/cloud_integrations/*` including credentials, so secret handling needs its own design. | A follow-up is acceptable only with a credentials/secret-handling review, explicit permission-scope mapping, and tests that never print/persist secrets or contact real cloud providers. |
| Quick-filter preferences | Defer; preference CRUD is a UI-preference surface with no existing MCP tool boundary. | A follow-up must justify the tool boundary (who reads/writes preferences), cover per-source scoping, and add live CRUD round-trip tests with cleanup. |
| Newly permissioned administrative APIs | Defer; admin surfaces need a security/authorization review before any MCP exposure. | A follow-up must enumerate the permissioned endpoints, map required scopes, document deny-path behavior through the shared coded error path, and pass a dedicated authz review. |

Cross-cutting rules: dedicated entity/configuration APIs must not be presented
as covered merely because raw metrics can be queried. Reuse
[key CRUD issue #223](https://github.com/SigNoz/nerve-pod/issues/223) for
key-CRUD adjacency rather than widening this feature. After user approval of
this plan, propose new follow-up issue drafts (one per deferred area, plus the
companion v1→v2 skill-shape cleanup) for review before any external write; no
such writes happen during planning.

## Files to Modify

Detailed changes and acceptance tests are listed under each workstream above.
The main implementation surfaces are:

- `internal/handler/tools/schemas/` and `pkg/dashboard/`: pinned schema
  generation, TextPanel examples, patching, and source semantics.
- `internal/handler/tools/{logs,logs_helper,query_builder}.go`,
  `pkg/types/querybuilder.go`, and query guidance resources: quoting, explicit
  search, heatmap inputs, bounds, and result preservation.
- `internal/handler/tools/notification_channels.go` and
  `pkg/types/notification_channels.go` and the channel client methods: v2
  channel contracts, full-config preservation, and explicit test-send behavior.
- `pkg/log/` and `internal/mcp-server/server.go`: prevent notification mutation
  arguments, copied user text, and secret-bearing upstream errors from entering
  logs or traces; retain safe error classification.
- Package-local tests and `tests/e2e/tests/`: regression coverage and real
  contract evidence; `tests/casting.yaml` and relevant E2E configuration for
  the backend pin.
- `README.md`, `manifest.json`, and affected wire-catalog fixtures: synchronized
  feature metadata. Preserve the docs-search changes from #309 when editing.
- Companion `SigNoz/agent-skills` dashboard, query, saved-view, and alert
  guidance identified above, coordinated with the server release.

## Delivery and delegation

1. Complete source-backed planning, obtain a separate agent review, reconcile
   findings, then request user approval of this document.
2. Use a feature branch and bounded, disjoint agent assignments. Reserve
   parent work for orchestration, integration, and review.
3. Implement query/search, dashboard, and notification changes as bounded
   workstreams on the feature branch. Shared query/schema metadata changes
   have one owner and are integrated serially to avoid overlapping edits.
   Commit the coordinated server contract with its plan and the companion
   skills change in their respective repositories.
4. Update companion skills alongside the relevant server changes; link the
   server and companion PRs and explicitly state the CMP-3 outcome. Keep this
   dated plan with the feature PR and record decisions as scope changes.
5. Parent agent reviews integration and evidence; independent agents review
   substantive implementation changes. Do not mark the issue complete while
   acceptance evidence or companion changes remain outstanding.

## Contract and documentation synchronization

- Preserve top-level string `searchContext` on every changed input schema,
  including typed schemas; keep it outside `required` and do not call it optional.
- Synchronize handler validation, schemas, resource examples, `README.md`,
  `manifest.json`, relevant `docs/`, and companion skills.
- Follow `docs/client-visible-writing-style.md`; keep critical first-call
  constraints inline and detailed workflows in reachable resources.
- Review affected surfaces against section 11 of `docs/mcp-best-practices.md`.
  Expose upstream 401/403 via the shared top-level coded error path.
- Own and document the canonical result shapes, including intentional channel
  v2 changes. New cross-boundary parsing must have real-response
  coverage or a WARN/metric for unexpected shapes; fail-open paths must signal
  degradation without logging secrets.
- Update only specifically changed wire-catalog fixtures and their derived
  sizes/hashes through existing helpers. Do not re-record the catalog or relax
  guardrail budgets merely to pass tests. Record removed legacy parameters and
  changed result shapes in the migration note and retire only their specific alias-policy/fixture entries. The user
  authorized a hard cut, so no compatibility shim is required. Publish the
  canonical replacement forms and minimum version with linked skill updates.

## Key Decisions

Earlier entries preserve the audit trail. The active Approach and latest
2026-09-18 decisions supersede conflicting 2026-09-17 compatibility and
dashboard assumptions.

### 2026-09-17 — Planning requested; implementation gated on user approval
- Scope is issue #232's confirmed released functionality: Text panels, explicit log search, heatmap query contract, notification providers/v2 APIs, system dashboard semantics, and explicit disposition of broader product areas. AI observability and release-sync automation are excluded.
- Checked existing `plans/`; no context pair already covers #232. Related dashboard normalization/migration, query bounds, error guidance, and contract plans remain background for this work.
- Baseline MCP commit: `4be75edb6deb639059ad37b6c4fbd420d8b47486`; companion agent-skills: `70746a27d87d0b0c9ea1e4bcd640a1e9aba9a942`. Both worktrees initially clean.
- Resolved the released upstream tag to commit `57268e50b4907194cf4a6628143df3d6aaa1424e` for reproducible evidence/schema generation.
- Delegated three disjoint read-only investigations: dashboards/system semantics, heatmaps/log search, and notification channels. No implementation or live tenant mutation is authorized in this planning phase.
- Issue comment recommends `search()` as an entry point when the field is unknown, then field predicates once discovered. Preserve that distinction in guidance.
- Existing E2E CI pins Foundry v0.2.17. The implementation must prove which SigNoz image it renders and add a released-version contract lane if needed, rather than assuming the Foundry version proves feature coverage.

### 2026-09-17 — Dashboard/system source audit findings (pinned v0.142.0)
- TextPanel confirmed in the released plugin registry and OpenAPI: spec is `mode` (markdown-only), `text`, `presentation` (`textAlign` left|center|right, `verticalAlign` top|center|bottom, nullable `background`), and `headerOptions.hide`. MCP create/update schemas advertise only the seven query panel kinds today, so a valid TextPanel fails the advertised union.
- Query-count rule is kind-conditional: every panel needs a non-null `queries` array; TextPanel must send `queries: []`; all other panel kinds still require exactly one query. Current MCP widget/patch guidance says "exactly one query" unconditionally and must be corrected.
- Server materializes TextPanel defaults on write (`markdown`, empty text, left/top alignment, shown header; background stays omitted) — round-trip acceptance must assert these defaults rather than echo input.
- System dashboard semantics: `source` is `user|system|integration`; integration dashboards are immutable, system dashboards additionally cannot be deleted/locked/made public, and only user+integration dashboards can be cloned. `source` rides list/get results but is absent from `DashboardtypesUpdatableDashboardV2`, so existing schema-driven stripping on update is correct and stays.
- Source-filter correction accepted: in pinned v0.142.0 the list-filter DSL does not accept `source` as a column filter (`ReservedOps` lacks it; `source` is only a reserved tag name). Document client-side filtering and treat the list response's `reservedKeywords` as authoritative; live confirmation on the pinned backend is acceptance evidence. This corrects the parent issue's "source filtering" phrasing.
- Companion agent-skills are in mixed v1/v2 shape. Decision: repair stale v1 shapes (`panelTypes`, `queryData`, `panelMap`, `selectedLogFields`) only in dashboard paths this feature exercises so TextPanel examples are coherent end-to-end; defer unrelated broad v1→v2 skill cleanup to a proposed follow-up issue after plan approval.
- Decision: include one full worked dashboard example pairing a TextPanel with a query panel (exercises layout `content.$ref` linkage), in addition to the single-panel widget example.
- Scope decisions recorded for plan section 5: defer infra/container inventory (boundary tied to existing MCP PR #67), GCP/cloud integration management (secret-handling review required), quick-filter preferences, and administrative APIs (authz review required); key-CRUD adjacency stays with nerve-pod #223. Follow-up issue drafts will be proposed only after user approval; no external writes during planning.

### 2026-09-17 — Query/channel investigations integrated; independent review started
- Query scope now includes explicit/scoped search, literal quoting, unchanged body-only `searchText`, saved-query preservation, and companion guidance. Heatmap scope extends raw builder envelopes and preserves bucket metadata/counts; the dedicated metrics convenience tool remains unchanged.
- Channel proposal evaluates v2 but keeps v1 routes, which remain present in v0.142.0 and accept the four new providers. It preserves routing display names, response shapes, and default test sends; an additive `test=false` opt-out is proposed. These are plan recommendations awaiting review and user approval.
- Confirmed Foundry v0.2.17 `DefaultSigNoz()` and its generated compose example use `signoz/signoz:latest`. Pin the actual backend image and record digest/version for acceptance, rather than relying on the Foundry pin.
- Separate reviewer started after all draft sections were filled. Reviewer has no authorship/exploration role and will inspect pinned evidence and repository contract rules.
- No production code, schemas, skills, external issues, or live resources changed during planning. Only this plan/context pair is edited.

### 2026-09-17 — Independent review corrections
- Independent reviewer returned conditional approval with two findings: raw saved-view heatmap acceptance does not prove graph/heatmap rendering, and flat channel updates cannot accept nested get configs verbatim or guarantee untouched values without real update evidence.
- Removed the saved-view rendering recommendation. Scope is raw query preservation only; existing fixture source/panelType remains unchanged, with no claim of UI heatmap support.
- Replaced channel verbatim-resend language with complete documented nested-get-to-flat-update mappings for all modeled fields. Added a new-provider preflight that rejects unrepresentable populated fields/multiple entries before mutation, without exposing values; existing six-provider behavior is unchanged. This is a proposed limitation, disclosed in descriptions and companion guidance, rather than silent field loss.
- Added per-provider create/get/change-one-field/update/get tests asserting all untouched fields and credentials survive; secrets are compared in memory. Alert routing verification must disable delivery or use validation-only evidence. Added companion guidance for the mapping, limitation, and test opt-out.
- Clarified log bucket `spec: {}` versus required linear maxValue and that JSON whitespace is not a preservation requirement. These are wording corrections, not expanded feature scope.
- Requested focused independent confirmation of these corrections before user handoff.

### 2026-09-17 — Focused independent confirmation; ready for user review
- The independent reviewer confirmed both findings are resolved and approved the revised plan for user review. No remaining blocker from the reviewed findings; raw heatmap preservation, channel mapping/preflight, untouched-secret update tests, and companion guidance are now explicit.
- Reviewer also confirmed the corrected bucket wording: empty log spec requests defaults; linear requires maxValue. Original and focused review artifacts are in `/tmp/signoz-232-independent-review.md`; the durable findings/resolutions are recorded in this log.
- User asked whether the plan is grounded in the SigNoz repo. Confirmed grounding against released commit `57268e50b4907194cf4a6628143df3d6aaa1424e`, using upstream OpenAPI, dashboard validation, query-builder types/behavior, and notification handlers. Distinguished source audit from live verification, which remains acceptance work after approval.
- Planning-phase validation: only the plan/context pair changed; no implementation, schema generation, companion edits, live API calls, or test runs. Full-file whitespace/placeholder checks are performed before handoff; implementation validation commands remain planned, not claimed as run.

### 2026-09-18 — Adopt the merged single-file convention after pulling main

- User confirmed the pull and requested this task be updated accordingly.
- Consolidated only the two uncommitted files created for #232 under the stale
  checkout into this dated plan. Kept all prior dated decision/review entries
  intact; their pair/context references describe the historical state. Removed
  the raw prompt transcript and retained its intent in Context. Existing legacy
  planning pairs elsewhere in the repository remain untouched.
- Read current `plans/README.md`, `CLAUDE.md`, and the changes to MCP/guardrail
  standards. PR #307 requires one dated file for new changes.
- Compared `4be75ed..d227e4d`: #308 changes docs refresh/memory and #309 changes
  docs-search ranking/guidance. Neither changes dashboard, heatmap, log-search,
  notification-channel, or E2E contracts covered by this plan. Preserve their
  changes to shared README, manifest, wire catalog, telemetry, and guardrails
  during implementation; do not reuse stale versions of those files.
- The feature approach and independent-review resolutions remain unchanged.
  This is a planning-record update, not implementation approval. Use the
  current baseline above when implementation is approved.

### 2026-09-18 — User requires hard cut; system-dashboard correction

- User explicitly removed the backward-compatibility requirement and supplied
  system-dashboard context for verification against released source.
- Adopt SigNoz v0.142.0+ and canonical changed contracts, notification v2
  `config.kind/spec`, and no v1 fallback, dual schema, legacy channel parameters,
  or older-backend compatibility matrix. Remove obsolete aliases only where
  the corresponding tool/schema is changed, with explicit migration notes.
  Existing body-only searchText remains a useful explicit convenience, not an
  old-client requirement. This replaces the earlier v1-retention decision.
- Thread takeaway retained: system dashboards are not a new MCP feature. Keep
  a brief explanation of source and denied writes in existing tools/resources;
  no system-specific tool. Do not add unsupported discovery or mutation paths.
- A reviewer checked released v2 handlers and found the earlier audit mixed in v1
  mutability rules. `DashboardV2.ErrIfNotMutable` blocks every non-user source;
  update/patch use that gate. System/integration cannot be modified, deleted,
  or lock/unlocked by users. The v1 public-sharing rule must not be presented
  as a released v2 MCP feature. The active approach now states these v2 rules.
- The referenced thread also claimed system dashboards appear in list results.
  Pinned `impldashboard/store.go` proves ListV2/ListForUser exclude SourceSystem
  (lines 185/122). Get-by-ID may still return a system dashboard and its source.
  Corrected that claim rather than copying it. Client-side source filtering
  cannot discover system rows omitted upstream.
- An agent is rewriting notification scope; an independent reviewer will assess the
  completed hard-cut revision. Prior review approval covers the old draft only.
  No implementation approval is inferred from this planning change.

### 2026-09-18 — Canonical v2 channel design and renewed review

-  rewrote channel scope against pinned released v2 source: all ten
  providers use config.kind/spec; create supports explicit name or generateName;
  id/name/displayName are separate; update replaces config; alert routing still
  uses displayName; list is config-free with upstream filtering/pagination.
- Opt-in test sends now default to false. This intentionally replaces the prior
  automatic-test contract. Upstream provider defaults own omitted sendResolved;
  canonical get-to-update preserves the materialized value and complete config.
- Parent review corrected a draft annotation mistake: update is non-idempotent
  because the accepted test:true path can resend. Post-write auth failures keep
  top-level coded errors plus committed-mutation identity, not silent notes or
  unsafe retries. Delivery/read-back advisories must not imply verified delivery.
- A fresh independent agent
  is reviewing the completed revision. The earlier source-audit agent did not
  perform this independent review. No implementation or live calls were made.

### 2026-09-18 — Independent review approved the hard-cut revision

- Independent reviewer approved the revised plan for
  user review with no remaining material findings. Review artifact:
  `/tmp/signoz-232-hard-cut-review.md`; this entry preserves the outcome.
- Reviewed v2 notification identity/config/read-write/list contracts, opt-in
  tests, non-idempotent annotation, committed-mutation auth errors, corrected
  v2 dashboard list/mutation/get behavior, and bounded alias-removal scope.
- Planning checks passed: current hard-cut policy, local links, whitespace,
  single-file structure, and unchanged tracked files. No build, Go tests,
  implementation, or live tenant verification was performed.
- User approval of implementation remains pending.

### 2026-09-18 — Implementation approved

- User approved starting implementation on 2026-09-18. Technical decisions
  and independent-review outcomes are retained in this plan.
- Work proceeds on `feat/released-feature-parity-232` with disjoint dashboard,
  query, channel, companion-guidance, and verification assignments.

### 2026-09-18 — Implementation baseline and schema migration

- Baseline `GOTOOLCHAIN=go1.26.0 go test ./...` passed before feature changes.
- Removed the two grandfathered wide-schema inventories for flat channel
  create/update inputs. Canonical v2 configs fit the existing property budget;
  this tightens policy and does not increase any limit.
- Intentional SCH-4 exception: the canonical channel config discriminated union
  is pinned to the released upstream provider schema, as with dashboard plugin
  schemas. It exposes known provider-specific required fields for valid first
  calls; unknown kinds/fields return a clear error rather than being dropped.
  Provenance and live/recorded contract checks track upstream evolution.
- Identified required alert-consumer change: fully paginate v2 channel summaries
  and match displayName rather than machine name. This is part of the channel
  migration, not a new alert API feature.

### 2026-09-18 — Integration and live-test setup

- Kept internal raw channel method signatures where they remain suitable;
  introduced a typed, paginated v2 list method for tool results and alert routing.
  Public channel payloads and routes still make a hard cut to v2.
- Updated the wire oracle's channel upstream fixture to the released v2
  `data.channels` / `data.total` response. Catalog updates will be restricted to
  the changed tools and resources; no full-catalog re-record is permitted.
- Query-package checkpoint passed: `GOTOOLCHAIN=go1.26.0 go test ./pkg/types ./pkg/querybuilder ./pkg/metricsrules`. This is not a full integration result.
- Initial live-test setup found Docker Desktop stopped. The verification agent
  was authorized to start it and retry. Docker and the pinned backend containers
  are now running. Ruff formatting, Ruff lint, and collection of 59 E2E tests
  passed. Runtime version verification and live execution remain pending while
  the channel migration is integrated; no live test has passed at this checkpoint.

### 2026-09-18 — Query implementation verification

- Query and guide package checks passed, including saved-view raw JSON checks.
- Any supplied legacy log `query` key now returns a correction to `filter`,
  including empty and non-string forms; unrelated signal aliases are unchanged.
- Pinned-source parity permits structurally valid bucket options on disabled
  non-heatmap inputs and normalizes null spec to its kind's zero value. Enabled
  semantic validation and explicit zero bucket-count preservation are tested.
- Heatmap output and ranking remain upstream-owned; MCP preserves the response
  and bounded request rather than implementing a second ranking algorithm.
- Independent review found one correctness issue: combining a caller filter
  containing OR with convenience predicates could change its meaning. The fix
  groups the caller filter only when composing it with other predicates; a
  standalone raw filter stays unchanged. A focused precedence test is required.
  No other query/log findings were reported. The fix is implemented and the
  focused heatmap, log-filter, and channel-schema drift tests passed. Corrected
  two new bounds-note assertions to match the existing quoted query-name format.

### 2026-09-18 — Source-backed channel review corrections

- Separate response validation from authored-input validation: upstream unset
  optional template strings serialize as empty strings, so valid reads must not
  fail. Normalize those documented unset fields for full-config write-back.
- The released UUID parser accepts every valid UUID version even though new
  IDs are generated as v7. Do not reject valid existing IDs at the MCP boundary.
- Preserve unmodelled list kinds with a detectable signal rather than failing
  the entire page; alert routing still uses each returned displayName.
- Channel mutations must make one attempt, including PUT and DELETE. Retain
  strict test-body decoding, committed-write identity, and drift signals.
- Add pagination metadata alongside canonical channels/total so effective
  bounds and the next offset are explicit (OUT-3).

### 2026-09-18 — Dashboard and alert review

- Focused dashboard/schema tests and alert-consumer tests passed. Alert lookup
  fully paginates and uses displayName; later-page authorization failures
  prevent writes. Independent review found no alert-consumer defects.
- Dashboard review found one missing import-path normalization for TextPanels
  with omitted/null queries. The import path must apply the same normalization
  as create, with a request-body regression test. That fix is implemented; all
  dashboard handler tests and the targeted import test passed. No other
  dashboard findings.
- Shared guardrails initially failed only on wire-fixture serialization; the
  targeted fixtures now use Go-compatible HTML escaping. They will be checked
  again after final channel metadata changes.

### 2026-09-18 — Companion contract review and validation

- Updated five skills, both identical dashboard translation references, and
  their evaluation cases. Preserved unrelated alert/query workflows,
  frontmatter, prepared-operation reuse, and existing assertion coverage.
- Independent review found and confirmed fixes for the dashboard update
  argument shape, missing query-tool wrapper, contradictory old dashboard
  assertions, and a scoped-search expectation that included an extra scope.
- JSON parsing, local links, file-size conventions, unique evaluation IDs,
  reference parity, and whitespace checks passed. Evaluation case counts
  increased from 23/15/10/6/6 to 26/18/13/9/9.
- These checks are static validation; an offline selection/recovery exercise
  is running separately and is not a hosted client integration test.
- Shared guardrails passed and the sorted inventory matches all 16 tests.
  Workflow lint passed for guardrails and E2E.

### 2026-09-18 — PR CI and main synchronization follow-up

- User reported failing CI on server PR #313 and conflicts on companion PR
  #98. Fetched both base branches: server main remains `d227e4d`; companion
  main advanced from `70746a2` to `4cdc848`, requiring a semantic merge.
- CI found two staticcheck violations, `required: null` in the notification
  list input schema (rejected by Inspector and conformance), and the local
  webhook sink unreachable in the Linux E2E job. Previous local verification
  did not establish that these GitHub checks passed.
- Corrected the schema and lint issues without loosening guardrails, made local
  capture portable across host platforms, merged the companion's current main,
  and verified both updated PR heads in GitHub CI before marking Done again.
- Notification roots now omit an empty `required` array instead of serializing
  it as null. A serialization regression test covers all five channel tools;
  only that invalid field was removed from the wire fixture. Fixed the two
  staticcheck naming/style violations without changing behavior.
- Replaced the host webhook listener with a small capture container on the
  foundry SigNoz network. Docker DNS works for both Linux and Docker Desktop;
  capture reads are exposed on host loopback. The live test asserts the
  successful opt-in outcome, exactly one delivery, and confirmed deletion.
- Exact CI golangci-lint v2.12.2 passed locally. Ruff format/lint and the focused
  live webhook test passed; the channel, session key, MCP container, and capture
  container were cleaned up, with resource absence confirmed.
- Independent review of the CI fixes found no blockers. The full uncached Go
  suite and exact Inspector script passed in Linux with Go 1.26.0, along with
  focused guardrails, build, formatting, and workflow lint.
- Server fix commit `7da6a0e` passes all 11 GitHub checks, including Inspector,
  selected official conformance, Go lint/tests/build, guardrails, Python style,
  and Linux E2E (59 passed in 132.86s). E2E setup and teardown also passed.
  Runs: [Go](https://github.com/SigNoz/signoz-mcp-server/actions/runs/35313931594),
  [protocol](https://github.com/SigNoz/signoz-mcp-server/actions/runs/35313930698),
  [guardrails](https://github.com/SigNoz/signoz-mcp-server/actions/runs/35313930699),
  [E2E](https://github.com/SigNoz/signoz-mcp-server/actions/runs/35313931521).
- Companion merge `d98669b` includes current main `4cdc848`. Resolved all 11
  conflicts while retaining main's policy routing, v2 saved-view guidance,
  writing cleanup, portable packaging, and client config isolation. Independent
  review found three inconsistencies (policy routing guardrail, scoped search
  argument order, and stale v6 fixture expectations); all were fixed and the
  reviewer confirmed the corrections.
- Companion validation passed: version/config consistency, portable package
  generation and boundaries, plugin/MCP schemas, every packaged skill's
  `skills-ref` validation, JSON/Python syntax, eval ID/name uniqueness, reference
  parity, writing style, and diff checks. No plugin versions were bumped.
  GitHub [Validate Agent Plugin](https://github.com/SigNoz/agent-skills/actions/runs/35314547445)
  passed on `d98669b`; GitHub reports the PR mergeable with no conflicts.

### 2026-09-23 — Merge main `a31a6df` (make ci, alert guidance)

- Server main advanced to `a31a6df` (#314 alert percentile/anomaly guidance,
  #315 `make ci` and repo-docs checks). Merged it per the repository's
  merge-main convention.
- Only the `signoz://alert/instructions` wire-catalog entries conflicted. Both
  sides' resource text merged cleanly, so only that entry's size, serialized
  length, and hash were updated to the combined content; no other catalog
  entry was re-recorded.
- `GOTOOLCHAIN=go1.26.0 make ci` passed: formatting, golangci-lint v2.12.2,
  module verification, build, race tests, guardrails, protocol, conformance,
  e2e Python style, and repo docs (including `READY=1`).

## Reference Links

- [Issue #232](https://github.com/SigNoz/nerve-pod/issues/232)
- [Released upstream v0.142.0](https://github.com/SigNoz/signoz/tree/57268e50b4907194cf4a6628143df3d6aaa1424e)
- [Separate release-sync process #233](https://github.com/SigNoz/nerve-pod/issues/233)
- [Existing search guidance issue #149](https://github.com/SigNoz/nerve-pod/issues/149)
- [Existing host-list PR #67](https://github.com/SigNoz/signoz-mcp-server/pull/67)
- [Existing key CRUD issue #223](https://github.com/SigNoz/nerve-pod/issues/223)
- Local standards: `docs/mcp-best-practices.md`, `docs/client-visible-writing-style.md`, `guardrails/README.md`, `tests/README.md`.
- [Single-file planning convention PR #307](https://github.com/SigNoz/signoz-mcp-server/pull/307)
- [Current plan lifecycle](README.md)

## Verification

### Planning verification completed

- 2026-09-17: three delegated source audits and independent review;
  both review findings resolved and confirmed. No live verification performed.
- 2026-09-18: checked pulled main and affected-file diffs, preserved all prior
  decision entries, and validated plan structure, whitespace, and local links.
  No production code changed; Go/build/E2E tests were not run for this update.


### Implementation verification (2026-09-18)

- Go 1.26.0: `make fmt goimports`, `go test -count=1 ./...`, and the server
  build passed on the final production changes. The focused guardrail suite,
  exact sorted inventory, and guardrails/E2E workflow lint also passed.
- Independent implementation reviews covered dashboards, query/search,
  notification schemas/client/handlers, alert routing, telemetry redaction,
  and companion skills. Reported findings were fixed and re-reviewed.
- Notification arguments, copied user text, and upstream credential-bearing
  errors are excluded from log/span telemetry. Canary tests preserve safe
  classification and ordinary unrelated-tool logging.
- Every committed notification-write outcome includes test status. A test
  aborted before dispatch is skipped with accurate requested state; transport
  failures have unknown outcome, while definitive HTTP errors are failed.
  Authorization remains a coded top-level error with committed identity.
- Only affected wire-catalog entries changed. Removed legacy channel property
  exceptions tighten policy; no guardrail budget was increased.
- README, manifest, migration guidance, and five companion skills are updated.
  Unrelated trace-filter aliases remain supported.

### Delegated live verification (2026-09-18)

- The agent verified running SigNoz v0.142.0 and both pinned backend/collector
  image digests. API keys remain in memory and are revoked after each run.
- Intermediate runs exposed harness errors in canonical payloads, response
  traversal, and credential-preservation assertions. These were corrected
  against actual responses and pinned source, without removing contract checks.
- Live investigation found a real `searchText` issue: upstream CONTAINS passes
  decoded text into ILIKE without escaping pattern metacharacters. MCP now
  escapes backslash, percent, and underscore before grammar quoting. Explicit
  caller filters remain untouched. Live escaped convenience and explicit
  scoped/unscoped search checks passed after the fix.
- Heatmap tests retain bucket/count/overflow checks. Pinned SigNoz trims returned
  boundaries to the observed range, floors query end to the step, and nests
  both time-series and heatmap series under aggregations. The harness now uses
  source-backed bounds, older samples, and canonical response traversal.
- Final isolated heatmap check: **1 passed in 4.93 seconds**.
- Final full E2E suite against the rebuilt current MCP: **59 passed in
  126.34 seconds**. Ruff formatting/lint and diff checks passed.
- Verified running images: SigNoz
  `sha256:041de69ac78a2bd302aade6a11f619c96014e86973754fee024e397ca0eafd55`;
  collector
  `sha256:f234365bfa563fadc1c185abd38ed1be060f64e2fc3a4e01bc339433f0481984`.
- Each completed run confirmed deletion and absence of created resources,
  stopped its MCP test container, and revoked its session key. Notification
  delivery tests targeted only the local capture sink. The pinned backend is
  retained as the cached local test environment. No pytest process or MCP
  E2E container remains; the credential cache contains only the endpoint.

### Offline companion behavior evaluation (2026-09-18)

- Fifteen new prompts across five skills were evaluated with frozen decisions
  before assertions were read. All 15 decisions conformed semantically.
- Two initially overstrict assertions were repaired against source: the service
  shortcut adds the same direct predicate; heatmaps reject `fillGaps: true`,
  while omission and false are equivalent. The frozen decisions were unchanged.
- Final grading: 2 offline passes, 13 requiring runtime evidence, no semantic
  failures. This is not hosted client integration or proof of live outcomes.
- Frozen artifact SHA-256:
  `e97f0b1773af36e5827955906700995b675e11c24cee5df29e59031188880cdc`.
  Skill validation, JSON parsing, and companion diff checks passed.

### Reproducible backend contract evidence

Foundry v0.2.17 is pinned in CI. Its `DefaultSigNoz()` uses floating images,
so `tests/casting.yaml` now overrides both backend and collector with the
v0.142.0 image digests recorded above. The delegated run verified the rendered
deployment, running version, and digests.
Keep version-specific expectations explicit; a missing target feature must
fail the target lane rather than silently skip it. This release requires
v0.142.0+; no older-backend lane or fallback behavior is promised.
Release-sync scheduling itself stays in #233.

Use existing E2E fixtures and add focused feature tests. Delegate every live
or credentialed multistep verification to a subagent, use an ephemeral backend,
clone existing resource shapes where practical, delete every created resource
in cleanup, verify absence, and report which fields round-tripped. Never print
or persist credentials. Routine notification tests use a local capture sink
and must not contact external notification services.

For every changed upstream parse/output transformation, use a live contract
test or a sanitized recorded real response with backend version/provenance.
Handcrafted fixtures alone cannot satisfy this requirement. Record unavailable
verification as a limitation rather than claiming it passed.

### Local and CI checks after implementation

- Run focused unit/contract tests for each changed subsystem.
- Run `make fmt goimports`, `go test ./...`, and `go build ./cmd/server`.
- Run `actionlint .github/workflows/guardrails.yaml`,
  `go test -count=1 -run '^TestGuardrail_' ./...`, and `go test -count=1 ./...`
  when guardrail fixtures/tests change; maintain the sorted inventory when needed.
- Run `uv run ruff format --check .` and `uv run ruff check .` from `tests/`
  after E2E edits; lint any modified workflow.
- Delegate `make test-e2e` (or cached-environment equivalent) with the pinned
  backend; run companion repository validation for changed skill files.
- Evaluate changed selection/recovery guidance with direct, indirect, and
  negative prompts, checking tool choice, argument shape, and error recovery.
- Run `git diff --check` and verify metadata/doc parity before handoff.

## Independent review

Current status: the hard-cut plan and substantive implementation changes passed
independent review with all reported findings resolved. User approval to
implement was received on 2026-09-18. Final verification results are recorded
above; no additional implementation approval is pending.

The following independent approval records the older 2026-09-17 draft only:

A fresh independent agent
reviewed the completed draft independently and returned conditional approval
with two required corrections: remove the heatmap saved-view rendering claim,
and make channel read-modify-write mappings/limitations and real update tests
explicit. Both corrections are incorporated above. The same independent reviewer
confirmed the revised plan is approved for user review, with no remaining
blocker from those findings. That approval preceded the later hard-cut revision.


## Outcome

Implemented the approved hard cut against SigNoz v0.142.0: Markdown TextPanels,
system-dashboard guidance, explicit log search and literal convenience filters,
raw heatmaps, and canonical v2 notification channels with displayName routing.
The recorded local checks and all 59 Linux CI E2E tests passed. The subsequent
CI corrections passed every server check; the synchronized companion also
passes CI and has no merge conflicts.

CMP-3 requires the coordinated companion change. It is implemented in
`SigNoz/agent-skills` on `feat/released-feature-parity-232`, merge commit `d98669b`.
Server README, manifest, migration documentation, resources, and targeted wire
fixtures are synchronized. Offline skill evaluation does not claim hosted
client integration. The server PR is [#313](https://github.com/SigNoz/signoz-mcp-server/pull/313)
and the required companion PR is [#98](https://github.com/SigNoz/agent-skills/pull/98).
Both branches are pushed and PRs are open; neither change has been merged or
released. Coordinate the server and skills releases.

Deferred scope remains under Broader product coverage with the existing
issue/PR links, including release synchronization in #233. No additional
follow-up issues were created.
