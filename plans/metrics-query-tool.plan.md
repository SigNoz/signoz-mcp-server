# Plan: Add `signoz_query_metrics` tool with guided aggregation

## Status
In Progress

## Context
`signoz_execute_builder_query` can run any v5 query but requires the LLM to know the exact JSON payload. Metrics have complex aggregation rules that vary by type (gauge, sum/counter, histogram) — choosing wrong aggregations produces meaningless or empty data. This task adds a higher-level `signoz_query_metrics` tool that accepts human-readable inputs, validates aggregation choices against metric type metadata, builds the payload, executes it, and **communicates every decision made** back to the caller.

## API Field Names (from OpenAPI spec — critical)
- `timeAggregation` (not `temporalAggregation`)
- `spaceAggregation` (not `spatialAggregation`)

## Aggregation Rules

| metricType | isMonotonic | Valid timeAggregation | Default timeAgg | Valid spaceAggregation | Default spaceAgg |
|---|---|---|---|---|---|
| gauge | — | latest, sum, avg, min, max, count, count_distinct | **avg** | sum, avg, min, max, count | **sum** |
| sum | true (counter) | rate, increase | **rate** | sum, avg, min, max, count | **sum** |
| sum | false | avg, sum, min, max, count, count_distinct | **avg** | sum, avg, min, max, count | **sum** |
| histogram | — | _(empty — auto)_ | — | p50, p75, p90, p95, p99 | **p99** |
| exponential_histogram | — | _(empty — auto)_ | — | p50, p75, p90, p95, p99 | **p99** |

**Validation errors:**
- `rate`/`increase` on gauge → error with explanation + suggested fix
- `timeAggregation` set on histogram → warn, ignore it
- `sum`/`avg`/`min`/`max` spaceAggregation on histogram → error explaining valid options

**Step interval auto-calc:** `max(60, (end-start)/300/1000)` seconds

## Architecture

```
pkg/metricsrules/
├── rules.go    — pure validation & defaults functions (testable, no HTTP)
└── guide.go    — MetricsGuide markdown const (served as MCP resource)
```

Validation lives in code (not prompts) → correctness guaranteed regardless of LLM behavior.
MCP resource `signoz://metrics-aggregation-guide` keeps tool description concise.

## Payload Examples (embedded in guide.go)

**Gauge — CPU utilization:**
```json
{"requestType":"time_series","compositeQuery":{"queries":[{"type":"builder_query","spec":{"signal":"metrics","name":"A","stepInterval":60,"aggregations":[{"metricName":"container.cpu.utilization","temporality":"unspecified","timeAggregation":"avg","spaceAggregation":"sum"}],"groupBy":[{"name":"k8s.namespace.name","fieldContext":"resource"}]}}]}}
```

**Counter (cumulative) — request rate:**
```json
{"requestType":"time_series","compositeQuery":{"queries":[{"type":"builder_query","spec":{"signal":"metrics","name":"A","stepInterval":60,"aggregations":[{"metricName":"http_requests_total","temporality":"cumulative","timeAggregation":"rate","spaceAggregation":"sum"}],"groupBy":[{"name":"service_name","fieldContext":"attribute"}]}}]}}
```

**Histogram — latency p99:**
```json
{"requestType":"time_series","compositeQuery":{"queries":[{"type":"builder_query","spec":{"signal":"metrics","name":"A","stepInterval":60,"aggregations":[{"metricName":"http_request_duration_seconds","temporality":"delta","timeAggregation":"","spaceAggregation":"p99"}]}}]}}
```

**Formula — error rate %:**
```json
{"requestType":"time_series","compositeQuery":{"queries":[
  {"type":"builder_query","spec":{"signal":"metrics","name":"A","stepInterval":60,"aggregations":[{"metricName":"http_errors_total","temporality":"cumulative","timeAggregation":"rate","spaceAggregation":"sum"}]}},
  {"type":"builder_query","spec":{"signal":"metrics","name":"B","stepInterval":60,"aggregations":[{"metricName":"http_requests_total","temporality":"cumulative","timeAggregation":"rate","spaceAggregation":"sum"}]}},
  {"type":"builder_formula","spec":{"name":"C","expression":"A / B * 100","legend":"error_rate_%"}}
]}}
```

## Files to Modify

### `pkg/metricsrules/rules.go` _(new)_
```go
type MetricQueryParams struct {
    MetricType       string // gauge, sum, histogram, exponential_histogram
    IsMonotonic      bool
    Temporality      string // cumulative, delta, unspecified
    TimeAggregation  string // user-provided or ""
    SpaceAggregation string // user-provided or ""
}

type ResolvedAggregation struct {
    TimeAggregation  string
    SpaceAggregation string
    Decisions        []string // "timeAggregation set to 'rate' (default for monotonic counter)"
    Warnings         []string // non-fatal issues
}

func ApplyDefaults(p MetricQueryParams) (ResolvedAggregation, error)
func ValidateAggregation(p MetricQueryParams) error
```

### `pkg/metricsrules/guide.go` _(new)_
Exports `MetricsGuide` const (markdown) — aggregation rules table, all 4 examples, groupBy fieldContext guide, formula syntax, pre-query checklist.

### `pkg/types/querybuilder.go`
Add:
```go
type MetricAggregation struct {
    MetricName       string `json:"metricName"`
    Temporality      string `json:"temporality,omitempty"`
    TimeAggregation  string `json:"timeAggregation,omitempty"`
    SpaceAggregation string `json:"spaceAggregation"`
}

type MetricsQuerySpec struct {
    Name         string
    Aggregation  MetricAggregation
    Filter       string
    GroupBy      []GroupByKey
    StepInterval int64
    IsFormula    bool
    Expression   string // formula: "A / B * 100"
    Legend       string
}

func BuildMetricsQueryPayload(start, end, stepInterval int64, queries []MetricsQuerySpec, requestType string) *QueryPayload
```

### `internal/handler/tools/metrics_helper.go` _(new)_
- `parseMetricsQueryArgs(args map[string]any) (*metricsQueryRequest, error)`
- `buildGroupByFields(names []string) []types.GroupByKey` — auto-detects fieldContext by prefix
- `autoStepInterval(startMs, endMs int64) int64`

groupBy fieldContext: prefixes `k8s.`, `container.`, `host.`, `cloud.`, `deployment.`, `process.` → `resource`; else → `attribute`

### `internal/handler/tools/handler.go`
Add `signoz_query_metrics` in `RegisterMetricsHandlers`.

**Parameters:**
| Param | Type | Required | Notes |
|---|---|---|---|
| metricName | string | yes | |
| metricType | string | no | from signoz_list_metrics |
| isMonotonic | bool | no | from signoz_list_metrics |
| temporality | string | no | from signoz_list_metrics |
| timeAggregation | string | no | auto-defaulted |
| spaceAggregation | string | no | auto-defaulted |
| groupBy | []string | no | fieldContext auto-detected |
| filter | string | no | e.g. "k8s.cluster.name = 'prod'" |
| timeRange | string | no | default "1h" |
| start / end | int64 | no | unix ms, overrides timeRange |
| stepInterval | int64 | no | auto-calc if omitted |
| formula | string | no | e.g. "A/B*100" |
| formulaQueries | []object | no | additional named queries for formula |

**Handler flow:**
1. `parseMetricsQueryArgs()`
2. `metricsrules.ApplyDefaults()` → resolved agg + decisions
3. Validation error → return with suggested fix
4. `autoStepInterval()` if not provided
5. `BuildMetricsQueryPayload()`
6. `client.QueryBuilderV5()`
7. Prepend `[Decisions applied]` block to response

**Decisions block format:**
```
[Decisions applied]
• metricName: container.cpu.utilization
• metricType: gauge (caller-provided)
• timeAggregation: avg (default for gauge)
• spaceAggregation: sum (default for gauge)
• stepInterval: 60s (auto: 1h ÷ 300 points)
• requestType: time_series
⚠ metricType not provided — conservative defaults used. Call signoz_list_metrics first for accuracy.
---
```

### `internal/mcp-server/server.go`
Register `signoz://metrics-aggregation-guide` → `metricsrules.MetricsGuide`

### `manifest.json`
Add `signoz_query_metrics` entry.

### `README.md`
Add to Features section and Tool Reference.

## Verification
1. `go build ./...` — compiles
2. `go test ./pkg/metricsrules/...` — all type/aggregation combos pass
3. `go test ./...` — full suite passes
4. MCP `signoz_list_metrics` searchText="container.cpu" → type=gauge
5. MCP `signoz_query_metrics` metricName="container.cpu.utilization", metricType="gauge", groupBy=["k8s.namespace.name"] → decisions block + time series
6. MCP with metricType="gauge", timeAggregation="rate" → descriptive validation error
7. MCP with metricType="sum", isMonotonic=true, no aggregations → defaults to rate/sum with decisions
8. MCP with formula="A/B*100" + two queries → formula result
9. Read `signoz://metrics-aggregation-guide` → full guide renders
