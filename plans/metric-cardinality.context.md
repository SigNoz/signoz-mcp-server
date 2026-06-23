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

## Open Questions
- (none)
