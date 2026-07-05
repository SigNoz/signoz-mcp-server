# Plan: signoz_check_metric_usage

## Status
Done

## Context
The telemetry cost optimisation workflow (`signoz_get_top_metrics`) identifies expensive metrics but
cannot determine whether they are safe to drop without knowing if they appear in any dashboard or alert.
SigNoz exposes purpose-built per-metric lookup APIs used by the Metrics Explorer UI. This plan wraps
those APIs as one MCP tool.

## Tools

### Tool 1: `signoz_check_metric_usage`

**Purpose:** Given a list of metric names, return which dashboards and alerts reference each one.
The agent decides whether a metric is safe to drop based on this data combined with its own
reasoning (infra metric exclusions, skill rules, business context).

**Input:**
- `metricNames` (required) — JSON array of metric name strings. Soft cap of 50 per call — callers with more names should split into batches of 50 and merge results. Bounded concurrency via `errgroup.SetLimit(10)` also controls in-flight requests.
- `searchContext` — user's original question

**Output:** Compact map — raw reference data, no server-side drop verdict:
```json
{
  "system.disk.io": {
    "dashboards": ["Host Metrics", "Host Metrics (k8s)"],
    "alerts": []
  },
  "k8s.node.condition": {
    "dashboards": [],
    "alerts": []
  }
}
```
No IDs, no widget names — those are navigation aids, not decision inputs. Keeps agent context compact.

**Internal API calls (per metric, all in parallel):**
- `GET /api/v2/metrics/dashboards?metricName={name}`
- `GET /api/v2/metrics/alerts?metricName={name}`

These query-param routes require SigNoz v0.131.0 or newer.

**Why per-metric API over bulk fetch:**
- Server handles both builder-query and PromQL dashboard matching — no custom regex logic in handler
- Response is trimmed to just names before returning — context window stays small
- Bulk approach (`GET /api/v1/dashboards`) would require us to maintain PromQL negative-lookahead
  regex to avoid substring false positives (e.g. `cluster.upstream_rq` matching `cluster.upstream_rq_xx`)

**Key implementation notes:**
- Soft cap: reject batches > `MaxMetricUsageNames` (50) with a clear error directing callers to batch
- Overall deadline: 30s (`metricUsageTotalTimeout`) derived from ctx — returns partial results on expiry; metrics that didn't finish surface with a per-metric error
- Use `errgroup` with `g.SetLimit(10)` — bounded concurrency
  - At most 10 goroutines in-flight at once; each goroutine fetches dashboards then alerts sequentially
  - Protects SigNoz from thundering herd
  - Consistent with `golang.org/x/sync/errgroup` already used in `internal/docs/refresh.go`
- Deduplicate dashboard names (one metric can appear in multiple widgets of the same dashboard)
- A metric-not-found response from the usage API is treated as empty `{dashboards:[], alerts:[]}`; route-level 404s from unsupported SigNoz versions remain lookup errors
- `url.Values` / query encoding is required for metric names because cloud provider metrics can contain `/`
- No start/end query params on either endpoint — start/end are silently ignored by the server
- Alerts endpoint returns only METRIC_BASED_ALERT rules — no client-side filtering needed
- Handler returns structuredContent for successful synthesized JSON and VALIDATION_FAILED codes for local validation errors

**Go structs (internal to client, not exported in response):**
```go
type metricAlertRef struct {
    AlertName string `json:"alertName"`
    AlertID   string `json:"alertId"`
}
type metricDashboardRef struct {
    DashboardName string `json:"dashboardName"`
    DashboardID   string `json:"dashboardId"`
    WidgetID      string `json:"widgetId"`
    WidgetName    string `json:"widgetName"`
}
```

---

### Related Tool: `signoz_check_metric_cardinality` (shipped on PR #208, not this PR)

**Purpose:** Return label/attribute keys for a single metric with their cardinality counts, sorted
highest-first. Called AFTER `signoz_check_metric_usage` confirms the metric is in use — attribute
analysis is only useful for in-use metrics; drop candidates don't need it.

**Input:**
- `metricName` (required) — single metric name
- `timeRange` / `start` / `end` — same pattern as other tools, default 7d
- `searchContext` — user's original question

**Output:** Raw response from `GET /api/v2/metrics/attributes?metricName=...&start=...&end=...`
Response shape: `{data: {attributes: [{key: "...", valueCount: 42}]}}`

**Implementation:**
- New client method for the cardinality PR
- Query-param encoding for the metric name
- Pass through raw response — handler does not classify labels (agent uses the cardinality reference)

---

## Files to Modify/Create

### New files
- `internal/handler/tools/metric_usage.go` — `RegisterMetricUsageHandlers`, `handleCheckMetricUsage`
- `internal/handler/tools/metric_usage_test.go`

### Modified files
- `internal/client/interface.go` — add `CheckMetricUsage`
- `internal/client/metric_usage.go` — implement metric usage lookups
- `internal/client/mock.go` — add mock fn fields
- `internal/mcp-server/server.go` — register metric usage handlers
- `README.md` — tool table + parameter reference
- `manifest.json` — tool entries
- `docs/architecture.md` — add MetricUsage to registered handler list in Mermaid diagram

## Verification
1. Unit tests: mock returns known dashboards/alerts for one metric, empty for another — verify map output
2. Unit tests: mock returns the usage API's metric-not-found response — verify treated as empty result, not error
3. Unit tests: verify dashboard name deduplication (same dashboard, two widgets → one name in output)
4. Unit tests: verify empty string and duplicate names are filtered before API calls
5. Live test (subagent): call with top 5 metrics from `signoz_get_top_metrics`, verify non-empty dashboards on at least one
