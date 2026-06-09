# Feature: Metrics Stats & Attributes — Context & Discussion

## Original Prompt
> Add `signoz_get_metrics_stats` tool wrapping `POST /api/v2/metrics/stats`, which returns metrics ranked by ingested sample count — critical for telemetry cost optimization workflows.
> Also planned: `signoz_get_metric_attributes` wrapping `GET /api/v2/metrics/{name}/attributes`.
> (from GitHub issue #176)

## Reference Links
- [Issue #176](https://github.com/SigNoz/signoz-mcp-server/issues/176)
- `metrics.py` (telemetry-optimisation skill script) — shows exact API call

## Key Decisions & Discussion Log

### 2026-06-03 — API structure for GetMetricsStats
- `POST /api/v2/metrics/stats` accepts `{"start": ms, "end": ms, "limit": int, "orderBy": {"key": {"name": "samples"}, "direction": "desc"}}`
- Response: `{"data": {"metrics": [{"metricName": "...", "samples": 0, "type": "...", "unit": "...", "description": "..."}]}}`
- Ordering hardcoded to `samples desc` in client since that is the only meaningful use case for cost optimization; no need to expose orderBy as a tool parameter
- `resolveTimestamps` from `aggregate_helper.go` reused for time range resolution
- Default limit: 100 (higher than list_metrics default of 50 since cost analysis often needs top-N overview)
- Default timeRange: "1h" (consistent with other metrics tools)

### 2026-06-09 — Two-call fetch approach and timeRange/limit alignment with skill
- Initial default limit of 100 was wrong — the telemetry-optimisation skill always fetches with limit=2000 to compute accurate percentage denominators; a lower limit silently inflates per-metric percentages
- Hardcoding any limit is fragile: tenants with >N metrics would be silently truncated
- Decision: remove `limit` as a tool parameter entirely; handler always probes with `limit=1` first to read `data.total` from the response, then fetches all metrics in a second call using that total as the limit. Falls back to 2000 if the probe response cannot be parsed
- Default timeRange changed from "1h" to "7d" — the skill always uses a 7-day window; shorter windows miss volume patterns needed for accurate cost analysis
- Supported timeRange values narrowed to 24h, 3d, 7d — sub-day windows are not meaningful for this tool's purpose

## Open Questions
- [x] Should `orderBy` be a tool parameter? Decided: No — hardcode samples desc; the tool's purpose is cost ranking
- [x] Should `limit` be a tool parameter? Decided: No — always auto-fetch all metrics via two-call probe pattern
