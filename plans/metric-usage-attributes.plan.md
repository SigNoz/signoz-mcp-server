# Plan: signoz_check_metric_usage + signoz_get_metric_attributes

## Status
Done (Tool 1 — signoz_check_metric_usage); Tool 2 shipped separately as signoz_check_metric_cardinality on PR #208

## Context
The telemetry cost optimisation workflow (`signoz_get_top_metrics`) identifies expensive metrics but
cannot determine whether they are safe to drop without knowing if they appear in any dashboard or alert.
SigNoz exposes purpose-built per-metric lookup APIs used by the Metrics Explorer UI. This plan wraps
those as two MCP tools.

## Tools

### Tool 1: `signoz_check_metric_usage`

**Purpose:** Given a list of metric names, return which dashboards and alerts reference each one.
The agent decides whether a metric is safe to drop based on this data combined with its own
reasoning (infra metric exclusions, skill rules, business context).

**Input:**
- `metricNames` (required) — JSON array of metric name strings. No hard cap — bounded concurrency via `errgroup.SetLimit(10)` controls load on SigNoz.
- `searchContext` (optional) — user's original question

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
- `GET /api/v2/metrics/{name}/dashboards`
- `GET /api/v2/metrics/{name}/alerts`

**Why per-metric API over bulk fetch:**
- Server handles both builder-query and PromQL dashboard matching — no custom regex logic in handler
- Response is trimmed to just names before returning — context window stays small
- Bulk approach (`GET /api/v1/dashboards`) would require us to maintain PromQL negative-lookahead
  regex to avoid substring false positives (e.g. `cluster.upstream_rq` matching `cluster.upstream_rq_xx`)

**Key implementation notes:**
- Use `errgroup` with `g.SetLimit(10)` — bounded concurrency, not a hard input cap
  - No limit on number of metric names the caller can pass
  - At most 10 goroutines in-flight at once; each goroutine fetches dashboards then alerts sequentially
  - Protects SigNoz from thundering herd; self-regulates without forcing the agent to batch
  - Consistent with `golang.org/x/sync/errgroup` already used in `internal/docs/refresh.go`
- Deduplicate dashboard names (one metric can appear in multiple widgets of the same dashboard)
- HTTP 404 = metric not tracked → treat as empty result `{dashboards:[], alerts:[]}`, not an error
- `url.PathEscape` is safe for metric names — dots are unreserved chars, won't be encoded; %2E returns 404
- No query params on either endpoint — start/end are silently ignored by the server
- Alerts endpoint returns only METRIC_BASED_ALERT rules — no client-side filtering needed

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

### Tool 2: `signoz_get_metric_attributes`

**Purpose:** Return label/attribute keys for a single metric with their cardinality counts, sorted
highest-first. Called AFTER `signoz_check_metric_usage` confirms the metric is in use — attribute
analysis is only useful for in-use metrics; drop candidates don't need it.

**Input:**
- `metricName` (required) — single metric name
- `timeRange` / `start` / `end` — same pattern as other tools, default 7d
- `searchContext` (optional)

**Output:** Raw response from `GET /api/v2/metrics/{name}/attributes?start=...&end=...`
Response shape: `{data: {attributes: [{key: "...", valueCount: 42}]}}`

**Implementation:**
- New client method `GetMetricAttributes(ctx, name string, start, end int64)`
- `url.PathEscape` for the metric name in the path
- Pass through raw response — handler does not classify labels (agent uses the cardinality reference)

---

## Files to Modify/Create

### New files
- `internal/handler/tools/metric_usage.go` — `RegisterMetricUsageHandlers`, `handleCheckMetricUsage`
- `internal/handler/tools/metric_usage_test.go`
- `internal/handler/tools/metric_attributes.go` — `RegisterMetricAttributesHandlers`, `handleGetMetricAttributes`
- `internal/handler/tools/metric_attributes_test.go`

### Modified files
- `internal/client/interface.go` — add `CheckMetricUsage` and `GetMetricAttributes`
- `internal/client/client.go` — implement both methods
- `internal/client/mock.go` — add mock fn fields
- `internal/mcp-server/server.go` — register both handler sets
- `README.md` — tool table + parameter reference
- `manifest.json` — tool entries
- `docs/architecture.md` — add MetricUsage and MetricAttributes to registered handler list in Mermaid diagram

## Verification
1. Unit tests: mock returns known dashboards/alerts for one metric, empty for another — verify map output
2. Unit tests: mock returns 404 — verify treated as empty result, not error
3. Unit tests: verify dashboard name deduplication (same dashboard, two widgets → one name in output)
4. Unit tests: verify empty string and duplicate names are filtered before API calls
5. Live test (subagent): call with top 5 metrics from `signoz_get_top_metrics`, verify non-empty dashboards on at least one
