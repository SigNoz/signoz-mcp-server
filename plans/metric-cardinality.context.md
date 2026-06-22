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

## Open Questions
- (none)
