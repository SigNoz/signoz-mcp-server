# Plan: Add `signoz_query_metrics` tool with guided aggregation

## Context
The existing `signoz_execute_builder_query` tool can execute metrics queries but requires the LLM to know the exact v5 JSON payload. Metrics have complex aggregation rules that vary by type (gauge, sum/counter, histogram) — choosing wrong aggregations produces meaningless data. This task adds a higher-level `signoz_query_metrics` tool that accepts human-readable inputs, validates aggregation choices against metric type metadata (returned by `signoz_list_metrics`), builds the payload, and executes it. A companion MCP resource documents the full aggregation rules guide.

## Aggregation rules (from docs + k8s dashboard analysis)

| Metric type | Valid temporal agg | Default temporal | Valid spatial agg | Default spatial |
|---|---|---|---|---|
| gauge | avg, latest, sum, min, max, count, count_distinct | **avg** | sum, avg, min, max | **sum** |
| sum (isMonotonic=true, counter) | rate, increase | **rate** | sum, avg, min, max | **sum** |
| sum (isMonotonic=false) | avg, sum, min, max, count | **avg** | sum, avg, min, max | **sum** |
| histogram | _(leave empty — auto)_ | — | p50, p75, p90, p95, p99 | **p99** |
| exponential_histogram | _(leave empty — auto)_ | — | p50, p75, p90, p95, p99 | **p99** |

**Key pitfalls to validate:**
- `rate`/`increase` are invalid for `gauge` → return error with explanation
- Temporal aggregation is invalid for `histogram`/`exponential_histogram` → warn and ignore
- `sum`/`avg`/`min`/`max` spatial aggregations are invalid for histograms → return error with explanation
- `exponential_histogram` requires delta temporality → warn if `temporality=cumulative`

---

## Files to modify

### 1. `pkg/types/querybuilder.go`
Add `MetricAggregation` struct and `BuildMetricsQueryPayload` helper:

```go
type MetricAggregation struct {
    MetricName          string `json:"metricName"`
    TemporalAggregation string `json:"temporalAggregation,omitempty"`
    SpatialAggregation  string `json:"spatialAggregation"`
}

func BuildMetricsQueryPayload(startTime, endTime, stepInterval int64,
    metricAggs []MetricAggregation,
    filterExpression string,
    groupBy []SelectField) *QueryPayload
```

The builder sets `requestType="time_series"`, `signal="metrics"`, populates `aggregations` as `[]any{MetricAggregation{...}}`.

### 2. `pkg/metricsguide/guide.go` _(new file)_
Exports `MetricsAggregationGuide` const string covering:
- All metric types and their valid temporal/spatial aggregation options
- Default recommendations per type
- Common pitfalls (rate on gauge, sum on histogram)
- Example payloads for gauge (CPU), counter (network I/O), histogram (latency)
- How to read `signoz_list_metrics` metadata to pick the right aggregations

### 3. `internal/handler/tools/handler.go`
Add `signoz_query_metrics` tool inside `RegisterMetricsHandlers`. Register the metrics guide as an MCP resource.

**Tool parameters:**
- `metricName` (required) — metric to query
- `metricType` (optional) — "gauge", "sum", "histogram", "exponential_histogram" from `signoz_list_metrics`
- `temporality` (optional) — "cumulative", "delta", "unspecified" from `signoz_list_metrics`
- `isMonotonic` (optional) — "true"/"false" from `signoz_list_metrics`
- `temporalAggregation` (optional) — see table above; defaults applied by type
- `spatialAggregation` (optional) — see table above; defaults applied by type
- `groupBy` (optional) — comma-separated field names, e.g. "k8s.namespace.name,k8s.pod.name"
- `filter` (optional) — filter expression, e.g. "k8s.cluster.name = 'prod'"
- `timeRange` (optional) — "30m", "1h", "6h", "24h", "7d" (default: "1h")
- `start` / `end` (optional) — unix ms timestamps (override timeRange)
- `stepInterval` (optional) — step in seconds (default: 60)

**Handler logic:**
1. Parse and validate parameters
2. Apply type-based defaults for temporal/spatial aggregation when not provided
3. Validate aggregation compatibility with metricType — return descriptive errors on invalid combos
4. Parse `groupBy` string into `[]types.SelectField`
5. Parse timeRange using `timeutil` if start/end not provided
6. Call `BuildMetricsQueryPayload` and execute via `client.QueryBuilderV5`

**Tool description** includes:
> Always call `signoz_list_metrics` first to get the metric's type, temporality, and isMonotonic — these determine valid aggregations. Read the `signoz://metrics-aggregation-guide` resource for full rules.

### 4. `internal/mcp-server/server.go`
Register `signoz://metrics-aggregation-guide` MCP resource using `s.AddResource` (following same pattern as `signoz_execute_builder_query` docs resource from PR #79).

### 5. `manifest.json`
Add entry for `signoz_query_metrics`.

### 6. `README.md`
Add `signoz_query_metrics` to Features section, User Guide Metrics Exploration examples, and Tool Reference.

---

## Verification
1. `go build ./...` — compiles cleanly
2. `go test ./...` — all tests pass
3. Call `signoz_list_metrics` with searchText="container.cpu" → observe type=gauge, temporality=unspecified
4. Call `signoz_query_metrics` with metricName="container.cpu.utilization", metricType="gauge", groupBy="k8s.namespace.name" → should return time series
5. Call with metricType="gauge", temporalAggregation="rate" → should return validation error explaining rate is for counters
6. Call with metricType="sum", isMonotonic="true" (counter), no aggregations → should default to rate/sum and succeed

---

## Status: Planning — not yet implemented
