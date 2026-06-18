# Feature: Metric Usage & Attributes — Context & Discussion

## Original Prompt
> Check the skill to see how /telemetry-optimisation skill fetches dashboard/alert usage for metrics, then create a plan. Concerned about agent making 100s of tool calls per metric.

## Reference Links
- `~/.claude/skills/telemetry-optimisation/scripts/metrics.py` — reference implementation (lines 122–162 for usage, 278–283 for attributes)
- Issue #176 (signoz_get_top_metrics PR context)

## Key Decisions & Discussion Log

### 2026-06-16 — Skill analysis and tool design
- Skill calls `GET /api/v1/dashboards` once (full panel content) + `GET /api/v1/rules?limit=100` once, then searches in-memory for each metric name
- The MCP `ListDashboards` handler already calls this same endpoint but strips content down to summaries before returning
- Attributes are fetched per-metric (`GET /api/v2/metrics/{name}/attributes`) but ONLY for metrics confirmed as in-use (not drop candidates)
- Agent concern: if tool takes one metric name, agent makes N tool calls for N metrics → N × 2 API calls, and dashboards payload is fetched N times
- Decision: `signoz_check_metric_usage` takes a **list** of metric names, fetches dashboards + rules once, scans all names in-memory, returns a map. Agent calls it once for all top metrics → 2 API calls total

### 2026-06-16 — Switched to per-metric v2 API approach; response design decision
- Per-metric APIs confirmed working (same APIs Metrics Explorer UI uses):
  - `GET /api/v2/metrics/{name}/dashboards` → `{dashboardName, dashboardId, widgetId, widgetName}` per widget
  - `GET /api/v2/metrics/{name}/alerts` → `{alertName, alertId}` per alert (METRIC_BASED_ALERT only)
- No query params on either endpoint; 404 = metric not tracked → treat as empty result
- Decided AGAINST bulk approach (`GET /api/v1/dashboards` + in-memory scan) — would require maintaining PromQL negative-lookahead regex for substring false positives (e.g. `cluster.upstream_rq` must not match `cluster.upstream_rq_xx`); server handles this correctly for free via per-metric APIs
- Response trimmed to just names + `safeToDrops` boolean — no IDs — keeps agent context window small. Key concern: Noz AI assistant credit usage when this is embedded in SigNoz product
- Cap at 20 metric names per call
- The earlier v1/rules question is now moot — per-metric alerts API replaces bulk rules fetch entirely

## Open Questions
- [ ] Should `signoz_get_metric_attributes` be a separate tool or combined with usage check?
  - Current thinking: separate — different intent, different invocation time (only called for in-use metrics)
- [x] Is `/api/v1/rules?limit=100` still available on current SigNoz versions?
  - Confirmed: v1 endpoint is alive, returns all 487 rules with full compositeQuery inline
  - Metric name at: `condition.compositeQuery.queries[].spec.aggregations[].metricName`
  - Only METRIC_BASED_ALERT rules have metricName; logs/traces alerts use `aggregations[].expression` with no metricName
  - Moot: switched to per-metric `/api/v2/metrics/{name}/alerts` — no bulk rules fetch needed

### 2026-06-16 — Concurrency design: bounded concurrency over hard input cap
- Removed the 20-metric input cap — no artificial limit on how many metric names the caller can pass
- Decision: use `errgroup.WithContext` + `g.SetLimit(10)` (bounded concurrency)
  - Hard input cap penalizes the caller and forces agent-side batching — wrong layer for this concern
  - Bounded concurrency (`SetLimit`) controls in-flight HTTP requests instead — protects SigNoz without restricting the caller
  - `g.SetLimit(10)` means at most 10 goroutines active at once; each does dashboards → alerts sequentially per metric
  - Primary consumer (telemetry-optimisation skill) works with top 100 metrics from treemap — all processed in one call
  - Pattern is consistent with `errgroup` already used in `internal/docs/refresh.go`
