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

## Result Bounds and Ordering

Every ` + "`builder_query`" + ` and ` + "`builder_formula`" + ` must include ` + "`limit: 1000`" + ` and
` + "`order: [{\"key\":{\"name\":\"__result\"},\"direction\":\"desc\"}]`" + ` unless the task intentionally
needs another positive limit or ordering. The wire field is ` + "`order`" + `; dashboard/editor payloads use a
different ` + "`orderBy`" + ` shape.

For time_series queries with groupBy, limit selects top groups using the ordering across the entire time
range, not each time bucket. A short-lived spike can therefore fall outside the selected groups.

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
        "limit": 1000,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
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
        "limit": 1000,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
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
        "limit": 1000,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
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
  "start": 1711123200000,
  "end": 1711209600000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "signal": "metrics",
          "name": "A",
          "stepInterval": 60,
          "limit": 1000,
          "order": [{"key": {"name": "__result"}, "direction": "desc"}],
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
          "limit": 1000,
          "order": [{"key": {"name": "__result"}, "direction": "desc"}],
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
          "legend": "error_rate_%",
          "limit": 1000,
          "order": [{"key": {"name": "__result"}, "direction": "desc"}]
        }
      }
    ]
  }
}
` + "```" + `

---

## Cost Meter (SigNoz usage / billing metrics)

Cost Meter is the data store for the metrics SigNoz meters and bills on. Today these are
telemetry ingestion volume — logs, spans, and metric data points, by count and by bytes —
and the set evolves (it may include other billable usage, such as AI credit consumption, in
future). They answer questions like "which services consume the most telemetry budget?" or
"ingestion volume by environment week-over-week". These metrics live in a separate store and
are **not** visible in the default metrics store.

To query them, set ` + "`source: \"meter\"`" + ` on the ` + "`builder_query`" + ` spec (a sibling of
` + "`name`" + ` and ` + "`signal`" + `), or pass ` + "`source=\"meter\"`" + ` to **signoz_query_metrics**. Omit
` + "`source`" + ` (or leave it empty) for ordinary metrics. Everything else — filters, groupBy,
aggregations, formulas — works exactly as for normal metrics.

Even log and span volume are queried this way — as metrics with ` + "`signal: \"metrics\"`" + ` and
` + "`source: \"meter\"`" + `, not through the logs or traces tools.

### Hourly aggregation — use ` + "`stepInterval: 3600`" + `

Meter data is aggregated in 1-hour buckets, so always set ` + "`stepInterval: 3600`" + ` (as in the
example below). A smaller step adds no resolution and a query window shorter than 1 hour yields at
most the current, still-incomplete hour (the build tested returns it flagged ` + "`partial: true`" + `;
some versions return no data). Use a window of at least a few hours. When **signoz_query_metrics**
is called without ` + "`stepInterval`" + ` it sends none and the backend auto-derives roughly
` + "`max(60, window/300)`" + ` seconds — below 3600 for any window shorter than ~12.5 days — so pass
` + "`stepInterval: 3600`" + ` explicitly for meter queries.

### Discover the current meter metrics

Don't assume a fixed list — the meter metric set evolves. Call **signoz_list_metrics** with
` + "`source=\"meter\"`" + ` for the authoritative, current set, with each metric's ` + "`type`" + `,
` + "`temporality`" + `, and ` + "`unit`" + `, then apply the normal per-type aggregation rules (see above).
As of this writing the set is telemetry-ingestion counters (delta monotonic sums) — for
example ` + "`signoz.meter.log.size`" + ` (bytes), ` + "`signoz.meter.span.count`" + `, and
` + "`signoz.meter.metric.datapoint.size`" + ` — for which ` + "`timeAggregation: rate`" + ` or ` + "`increase`" + `
with ` + "`spaceAggregation: sum`" + ` is correct. Verify type/unit per metric via signoz_list_metrics
rather than relying on this example list.

### Example: Log Bytes Ingested Over Time

Add a ` + "`groupBy`" + ` (e.g. a service or environment attribute) to break the volume down by
dimension, just like any other metric.
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
        "source": "meter",
        "name": "A",
        "stepInterval": 3600,
        "limit": 1000,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "aggregations": [{
          "metricName": "signoz.meter.log.size",
          "temporality": "delta",
          "timeAggregation": "increase",
          "spaceAggregation": "sum"
        }]
      }
    }]
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
