# Feature: metric-cardinality — Context & Discussion

## Original Prompt
> Add a signoz_check_metric_cardinality tool that returns label/attribute keys for a single metric
> with their cardinality counts and sample values, sorted highest-cardinality first. This is used
> during telemetry cost investigations to understand whether high-cardinality labels are bounded or
> unbounded (e.g. UUIDs, pod IDs vs namespace names, status codes).

## Reference Links
- PR #208: feat(metrics): add signoz_check_metric_cardinality tool

## Key Decisions & Discussion Log

### 2026-06-22 — Tool design
- Tool takes a single required metricName and optional time range
- Sorted highest-cardinality first so the most impactful labels surface immediately
- Description must not reference companion tools (signoz_check_metric_usage) that may not be
  registered in the same deployment — Codex P2 finding on PR #208
- Uses GetArguments() + nil guard pattern (not bare type assertion) to avoid nil map panic

### 2026-06-23 — Codex P2 review findings on PR #208
- Contract check in `GetMetricCardinality` only warned when `probe.Data == nil`; if upstream renames
  `attributes` to `items` (returning `{"data":{"items":[...]}}`) the check silently passed. Fixed by
  also warning when `probe.Data.Attributes == nil`.
- README description referenced `signoz_check_metric_usage` (PR #205, not yet merged). Removed the
  direct tool reference; replaced with intent-based guidance: unused metrics are better dropped
  (PR #196 territory) rather than having labels trimmed — this tool is for metrics confirmed to be
  in active use in dashboards/alerts.
- Handler `mcp.WithDescription` and manifest.json were not updated with the revised workflow
  positioning (LLMs read the tool description, not the README). Updated both to match.
- Added `internal/client/metric_cardinality_test.go` covering all contract-check branches including
  the new `probe.Data.Attributes == nil` case.

### 2026-06-23 — Description wording iterations
- Removed "Use this only for..." framing — it read as a gate and blocked standalone use. Tool is a
  general-purpose primitive; workflow guidance is context, not a precondition.
- Removed "cost-optimisation workflow" from tool description — that's a skill-layer concept and
  the MCP tool layer should not reference it. Rephrased as a standalone note.
- Corrected "reduces cardinality more than trimming labels" — factually wrong. Dropping a metric
  eliminates sample ingestion entirely (different dimension from cardinality). Trimming labels
  reduces cardinality (unique series count). New wording: "dropping it outright eliminates its
  ingestion cost entirely — more impactful than trimming its labels."

### 2026-07-06 — Rebase onto main + align error handling for merge readiness
- Merged `origin/main` into the branch. Conflicts were all "both added a tool in the same spot"
  (main gained `signoz_get_top_metrics` #196 and `signoz_check_metric_usage` #205 while this branch
  was open): resolved by keeping all three metrics-cost tools, grouped in workflow order
  (top_metrics → check_metric_usage → check_metric_cardinality) in `README.md`, `manifest.json`, and
  `nil_arguments_test.go`.
- Brought the handler in line with the error-handling convention that landed in main after this
  branch forked (#227, `errs.go`): required-arg now uses `requireStringArg` (coded
  `VALIDATION_FAILED`), timestamp parsing uses `errorWithCode(CodeValidationFailed, …)`, and the
  upstream client call routes through `upstreamError(err)` so a SigNoz 401/403 surfaces the
  `UNAUTHORIZED`/`PERMISSION_DENIED` structured code (CLAUDE.md hard requirement: authz failures must
  propagate through the shared coded path so clients re-authenticate). Previously all three returned
  a bare `mcp.NewToolResultError(err.Error())`.
- Added tests mirroring the `check_metric_usage` sibling: validation errors carry `CodeValidationFailed`
  (missing metricName, malformed timestamp) and a 401/403 authz failure returns the classified upstream
  code. The authz test wraps the `HTTPStatusError` with `fmt.Errorf("…: %w")` exactly as the real
  client does, proving `errors.As` classification survives the client's wrapping.

### 2026-07-06 — Param consistency pass vs MCP best-practices
- Audited the tool's params against the metrics-tool siblings and the MCP Best-practices doc.
  Three divergences, all because this tool predated the shared `timeRangeDesc()` helper:
  1. `timeRange` description enumerated a closed-looking set ("30m, 1h, 6h, 24h, 7d") while the
     parser (`resolveTimestamps`→timeutil) accepts the full `<number><unit>` grammar — an under-
     advertised contract (the doc's "advertised contract is an executable promise / teach the
     grammar" point). Switched to the shared `timeRangeDesc("Defaults to '7d' (a cost-analysis
     window).")`, matching `list_metrics`/`query_metrics`/`get_top_metrics`.
  2. `timeRange` lacked `mcp.DefaultString("7d")`, so the advertised schema carried no default
     (the three siblings all set it). Added it. Handler default was already 7d, so no behavior change.
  3. `searchContext` used the short description; aligned it to the fuller sibling wording
     ("…Always include the user's raw query here for better results.").
- Synced the README `timeRange` line to describe the grammar instead of the closed list.
- No behavior change — schema/description only. `metricName` (singular) vs `metricNames` (array on
  check_metric_usage) is intentional and correct. Repo-wide latent gap noted but out of scope:
  `start`/`end` are advertised string-only across all time tools though the parser accepts numeric.

### 2026-07-06 — P1: wrong endpoint URL (Codex PR review r3530397545)
- The client built `GET /api/v2/metrics/{name}/attributes?start=&end=` (metricName as a PATH
  segment). Verified against SigNoz backend source: the route is `/api/v2/metrics/attributes` and
  `MetricAttributesRequest` binds `MetricName` from `req.URL.Query()` with `query:"metricName"
  required:"true"` (and the field description notes names may contain slashes). The path-based URL
  hits no registered route and omits the required query param → validation/route error against real
  SigNoz. Fixed to `/api/v2/metrics/attributes?metricName=...&start=...&end=...` via `url.Values`,
  matching the sibling `fetchMetricUsage` pattern (`/api/v2/metrics/dashboards?metricName=...`).
- Root cause of the miss: `TestGetMetricCardinality_ContractCheck` used an httptest server that
  replied identically regardless of URL, so it validated response-shape parsing but never the
  request path/query — exactly the fixture-vs-reality gap CLAUDE.md warns about. Added request-URL
  assertions to every contract-check case plus `TestGetMetricCardinality_MetricNameWithSlash` to pin
  the query-param contract and slash round-trip. Also verified live against staging (see below).
- Aligned the debug-log ctx with `ensureTenantContext` to match the treemap sibling.

## Open Questions
- (none)
