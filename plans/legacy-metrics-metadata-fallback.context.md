# Feature: Legacy Metrics Metadata Fallback - Context & Discussion

## Original Prompt
> OK, let's fix Signoz MCP now.

## Reference Links
- Xylem SigNoz: `https://signoz.xylem.10upmanaged.com`

## Key Decisions & Discussion Log
### 2026-07-14 - Xylem v0.108 compatibility
- Xylem SigNoz reports version `v0.108.0`.
- `/api/v2/metrics` and `/api/v2/metrics/attributes` return the SigNoz frontend HTML on this deployment.
- `/api/v2/metrics/metadata?metricName=...` returns exact metric metadata with `type`, `temporality`, and `isMonotonic`.
- Added a narrow fallback for exact metric lookups so `signoz_query_metrics` can auto-fetch metadata on older SigNoz deployments without changing the public MCP tool contract.

## Open Questions
- [ ] Should broad metric catalog listing on pre-v0.131 SigNoz be supported through another route, or should those deployments be upgraded?
