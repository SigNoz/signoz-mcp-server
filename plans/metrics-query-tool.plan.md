# Plan: Add `signoz_query_metrics` tool with guided aggregation

## Status
Done

## Context
`signoz_execute_builder_query` can run any v5 query but requires the LLM to know the exact JSON payload. Metrics have complex aggregation rules that vary by type (gauge, sum/counter, histogram) — choosing wrong aggregations produces meaningless or empty data. This tool accepts human-readable inputs, validates aggregation choices, applies smart defaults, builds the payload, executes it, and **communicates every decision made** back to the caller.

## API Field Names (from OpenAPI spec)
- `timeAggregation` (not `temporalAggregation`)
- `spaceAggregation` (not `spatialAggregation`)
- Source: https://raw.githubusercontent.com/SigNoz/signoz/main/docs/api/openapi.yml

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

**Step interval auto-calc:** `max(60, (end-start)/300/1000)` seconds (~300 data points)

## Architecture

```
pkg/metricsrules/
├── rules.go    — pure validation & defaults functions (testable, no HTTP)
└── guide.go    — MetricsGuide markdown const (served as MCP resource)
```

Validation lives in code (not prompts) → correctness guaranteed regardless of LLM behavior.
MCP resource `signoz://metrics-aggregation-guide` keeps tool description concise.

## Payload Examples (embedded in guide.go)

### Gauge — CPU utilization
```json
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics", "name": "A", "stepInterval": 60,
        "aggregations": [{
          "metricName": "container.cpu.utilization",
          "temporality": "unspecified",
          "timeAggregation": "avg",
          "spaceAggregation": "sum"
        }],
        "groupBy": [{"name": "k8s.namespace.name", "fieldContext": "resource", "signal": "metrics"}],
        "filter": {"expression": "k8s.cluster.name = 'prod'"}
      }
    }]
  }
}
```

### Counter (cumulative monotonic) — request rate
```json
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics", "name": "A", "stepInterval": 60,
        "aggregations": [{
          "metricName": "http_requests_total",
          "temporality": "cumulative",
          "timeAggregation": "rate",
          "spaceAggregation": "sum"
        }],
        "groupBy": [{"name": "service_name", "fieldContext": "attribute", "signal": "metrics"}]
      }
    }]
  }
}
```

### Histogram — latency p99
```json
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics", "name": "A", "stepInterval": 60,
        "aggregations": [{
          "metricName": "http_request_duration_seconds",
          "temporality": "delta",
          "timeAggregation": "",
          "spaceAggregation": "p99"
        }]
      }
    }]
  }
}
```

### Formula — error rate percentage (A/B*100)
```json
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [
      {"type": "builder_query", "spec": {"signal": "metrics", "name": "A", "stepInterval": 60,
        "aggregations": [{"metricName": "http_errors_total", "temporality": "cumulative", "timeAggregation": "rate", "spaceAggregation": "sum"}]}},
      {"type": "builder_query", "spec": {"signal": "metrics", "name": "B", "stepInterval": 60,
        "aggregations": [{"metricName": "http_requests_total", "temporality": "cumulative", "timeAggregation": "rate", "spaceAggregation": "sum"}]}},
      {"type": "builder_formula", "spec": {"name": "C", "expression": "A / B * 100", "legend": "error_rate_%"}}
    ]
  }
}
```

## Design Decisions

1. **Formula sub-queries**: Each sub-query carries full params (name, metricName, metricType, isMonotonic, temporality, timeAggregation, spaceAggregation, groupBy, filter). Handles mixed-type formulas.
2. **requestType**: Both `time_series` (default) and `scalar`. For scalar, `reduceTo` param (sum/count/avg/min/max/last/median) with type-based defaults.
3. **Post-aggregation functions** (timeshift, anomaly, ewma): Deferred to v2.
4. **Unknown metricType**: Auto-call `client.ListMetrics()` internally to fetch type/temporality/isMonotonic. Report in decisions block.
5. **groupBy fieldContext**: Auto-detect by known resource prefixes (`k8s.`, `container.`, `host.`, `cloud.`, `deployment.`, `process.`) → `resource`. Everything else → `attribute`.

## Files Modified/Created

### `pkg/metricsrules/rules.go` _(new)_
- `ApplyDefaults(p MetricQueryParams, requestType string) (ResolvedAggregation, error)` — returns resolved aggs + Decisions/Warnings slices
- `ValidateAggregation(p MetricQueryParams) error` — descriptive errors with suggested fixes

### `pkg/metricsrules/guide.go` _(new)_
- `MetricsGuide` const — markdown with rules table, 4 payload examples, groupBy guide, formula syntax

### `pkg/metricsrules/rules_test.go` _(new)_
- 16 test cases covering all type/aggregation combos, overrides, scalar reduceTo, invalid combos

### `pkg/types/querybuilder.go`
- `MetricAggregation` struct (metricName, temporality, timeAggregation, spaceAggregation, reduceTo)
- `MetricsQuerySpec` struct (Name, Aggregation, Filter, GroupBy, IsFormula, Expression, Legend)
- `BuildMetricsQueryPayload()` — creates QueryPayload for metrics
- `BuildMetricsQueryPayloadJSON()` — handles formula specs with different shape
- `FormulaSpec` struct
- Updated `Validate()` to allow `scalar` requestType for metrics

### `internal/handler/tools/metrics_helper.go` _(new)_
- `parseMetricsQueryArgs()` — validates/normalizes all tool inputs
- `buildGroupByFields()` — auto-detects fieldContext by prefix
- `autoStepInterval()` — `max(60, (end-start)/300/1000)`
- `detectFieldContext()` — resource vs attribute prefix matching

### `internal/handler/tools/metrics_query.go` _(new)_
- `handleQueryMetrics()` — full handler: auto-fetch metadata, apply defaults, validate, build payload, execute, prepend decisions block
- `fetchMetricMetadata()` — calls ListMetrics internally, parses response
- `resolveFormulaSubQuery()` — per-sub-query defaults + auto-fetch
- `parseMetricMetadataFromResponse()` — handles multiple response formats

### `internal/handler/tools/handler.go`
- `signoz_query_metrics` tool registration with all 16 parameters
- `signoz://metrics-aggregation-guide` MCP resource registration

### `manifest.json`
- Added `signoz_query_metrics` entry

### `README.md`
- Metrics Exploration examples + Tool Reference section

## Tool Parameters

| Param | Type | Required | Notes |
|---|---|---|---|
| metricName | string | yes | |
| metricType | string | no | auto-fetched if absent |
| isMonotonic | string | no | auto-fetched if absent |
| temporality | string | no | auto-fetched if absent |
| timeAggregation | string | no | auto-defaulted by type |
| spaceAggregation | string | no | auto-defaulted by type |
| groupBy | string | no | comma-separated, fieldContext auto-detected |
| filter | string | no | e.g. "k8s.cluster.name = 'prod'" |
| timeRange | string | no | default "1h" |
| start / end | string | no | unix ms, overrides timeRange |
| stepInterval | string | no | auto-calc if omitted |
| requestType | string | no | time_series (default) or scalar |
| reduceTo | string | no | for scalar: sum/count/avg/min/max/last/median |
| formula | string | no | e.g. "A/B*100" |
| formulaQueries | string | no | JSON array of sub-query objects |

## Decisions Response Format

Every successful call prepends:
```
[Decisions applied]
  metricName: container.cpu.utilization
  metricType: gauge (auto-fetched via signoz_list_metrics)
  temporality: unspecified (auto-fetched)
  timeAggregation: avg (default for gauge — averages samples within each time bucket)
  spaceAggregation: sum (default for gauge — sums across series/dimensions)
  stepInterval: 60s (auto-calculated)
  requestType: time_series
---
```

On validation error:
```
[Validation Error]
timeAggregation "rate" is not valid for metric type "gauge".
Valid values: latest, sum, avg, min, max, count, count_distinct
```

## Verification
1. `go build ./...` — compiles
2. `go vet ./...` — clean
3. `go test ./pkg/metricsrules/...` — all type/aggregation combos pass
4. `go test ./...` — full suite passes
5. MCP: `signoz_query_metrics` with metricName="container.cpu.utilization", metricType="gauge" → decisions block + time series
6. MCP: with metricType="gauge", timeAggregation="rate" → descriptive validation error
7. MCP: with metricType="sum", isMonotonic="true" → defaults to rate/sum
8. MCP: with formula="A/B*100" + formulaQueries → formula result
9. Read `signoz://metrics-aggregation-guide` → full guide renders
