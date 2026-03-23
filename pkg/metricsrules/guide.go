package metricsrules

// MetricsGuide is the full markdown guide served as the
// signoz://metrics-aggregation-guide MCP resource.
const MetricsGuide = `# Metrics Aggregation Guide

## Pre-Query Checklist

Before querying any metric, call **signoz_list_metrics** with the metric name to get:
- **type**: gauge, sum, histogram, exponential_histogram
- **temporality**: cumulative, delta, unspecified
- **isMonotonic**: true/false (only relevant for type=sum)

These fields determine which aggregations are valid and which defaults are applied.

---

## Aggregation Rules

### Time Aggregation (within a single time series, across time buckets)

| Metric Type | Valid timeAggregation | Default |
|---|---|---|
| gauge | latest, sum, avg, min, max, count, count_distinct | **avg** |
| sum (isMonotonic=true, i.e. counter) | rate, increase | **rate** |
| sum (isMonotonic=false) | avg, sum, min, max, count, count_distinct | **avg** |
| histogram | _(empty — automatic)_ | — |
| exponential_histogram | _(empty — automatic)_ | — |

### Space Aggregation (across multiple time series / label dimensions)

| Metric Type | Valid spaceAggregation | Default |
|---|---|---|
| gauge | sum, avg, min, max, count | **sum** |
| sum (monotonic counter) | sum, avg, min, max, count | **sum** |
| sum (non-monotonic) | sum, avg, min, max, count | **sum** |
| histogram | p50, p75, p90, p95, p99 | **p99** |
| exponential_histogram | p50, p75, p90, p95, p99 | **p99** |

### ReduceTo (only for requestType=scalar — collapses time dimension to a single value)

| Metric Type | Default reduceTo |
|---|---|
| gauge | avg |
| sum (monotonic) | sum |
| sum (non-monotonic) | avg |
| histogram | avg |

Valid reduceTo values: sum, count, avg, min, max, last, median

---

## Common Pitfalls

1. **rate/increase on a gauge** → Invalid. Rate measures change over time, but gauges represent instantaneous values. Use avg, latest, or sum instead.
2. **timeAggregation on histogram** → Ignored. Histogram time aggregation is handled automatically by the backend. Only set spaceAggregation (p50-p99).
3. **sum/avg/min/max spaceAggregation on histogram** → Invalid. Histograms must use percentile aggregations (p50, p75, p90, p95, p99).
4. **Missing temporality for counters** → Always pass temporality from signoz_list_metrics. Cumulative vs delta affects how rate is computed.

---

## groupBy Field Context

When specifying groupBy fields, the fieldContext is auto-detected:

**Resource attributes** (fieldContext="resource"):
Prefixes: k8s., container., host., cloud., deployment., process.
Examples: k8s.namespace.name, k8s.pod.name, host.name, container.id

**Metric attributes** (fieldContext="attribute"):
Everything else. Examples: service_name, http.method, status_code

---

## Formula Queries

You can combine multiple metric queries using formulas:

- Define multiple queries with names like "A", "B", "C"
- Reference them in a formula expression: "A / B * 100"
- Both queries in a formula should ideally use the same groupBy fields for compatible label sets
- Different metrics can have different types — each gets independent aggregation validation

### Formula Functions
Supported: exp, log, ln, sqrt, sin, cos, tan, abs, ceil, floor

### Example: Error Rate %
Query A: http_errors_total (counter) → rate / sum
Query B: http_requests_total (counter) → rate / sum
Formula C: A / B * 100

---

## Payload Examples

### Example 1: Gauge — CPU Utilization Over Time
` + "```json" + `
{
  "start": 1711123200000,
  "end": 1711209600000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics",
        "name": "A",
        "stepInterval": 60,
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
` + "```" + `

### Example 2: Counter — HTTP Request Rate
` + "```json" + `
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics",
        "name": "A",
        "stepInterval": 60,
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
` + "```" + `

### Example 3: Histogram — Latency P99
` + "```json" + `
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "signal": "metrics",
        "name": "A",
        "stepInterval": 60,
        "aggregations": [{
          "metricName": "http_request_duration_seconds",
          "temporality": "delta",
          "timeAggregation": "",
          "spaceAggregation": "p99"
        }],
        "groupBy": [{"name": "service_name", "fieldContext": "attribute", "signal": "metrics"}]
      }
    }]
  }
}
` + "```" + `

### Example 4: Formula — Error Rate Percentage
` + "```json" + `
{
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "signal": "metrics",
          "name": "A",
          "stepInterval": 60,
          "aggregations": [{
            "metricName": "http_errors_total",
            "temporality": "cumulative",
            "timeAggregation": "rate",
            "spaceAggregation": "sum"
          }]
        }
      },
      {
        "type": "builder_query",
        "spec": {
          "signal": "metrics",
          "name": "B",
          "stepInterval": 60,
          "aggregations": [{
            "metricName": "http_requests_total",
            "temporality": "cumulative",
            "timeAggregation": "rate",
            "spaceAggregation": "sum"
          }]
        }
      },
      {
        "type": "builder_formula",
        "spec": {
          "name": "C",
          "expression": "A / B * 100",
          "legend": "error_rate_%"
        }
      }
    ]
  }
}
` + "```" + `

---

## Step Interval

- Auto-calculated as: max(60, (end - start) / 300 / 1000) seconds
- Targets approximately 300 data points per series
- Minimum: 60 seconds
- Can be overridden with the stepInterval parameter
`
