package metricsrules

// MetricsGuide is the full markdown guide served as the
// signoz://metrics-aggregation-guide MCP resource.
const MetricsGuide = `# Metrics Aggregation Guide

## Pre-Query Checklist

If the exact metric name is known, call **signoz_query_metrics** directly. When ` + "`metricType`" + ` is
omitted, the tool looks up the metric and auto-fetches:
- **type**: gauge, sum, histogram, exponential_histogram, summary
- **temporality**: cumulative, delta, unspecified
- **isMonotonic**: true/false (only relevant for type=sum)

These fields pick the default aggregations for any you leave empty. Use
**signoz_list_metrics** first only when discovering a metric name, inspecting catalog metadata, or
supplying metadata manually.

---

## Aggregation Defaults

The tool fills only the aggregations you leave empty. SigNoz validates the combination you send.

### Time Aggregation (within each series, per time bucket)

Values: latest, sum, avg, min, max, count, count_distinct, rate, increase.

| Metric Type | Default |
|---|---|
| gauge | **avg** |
| sum (isMonotonic=true, i.e. counter) | **rate** |
| sum (isMonotonic=false) | **avg** |
| histogram, exponential_histogram | _(empty)_ for percentiles |
| summary | no default; set it |

### Space Aggregation (across series / label dimensions)

Values: sum, avg, min, max, count, and p50, p75, p90, p95, p99 for histograms.

| Metric Type | Default |
|---|---|
| gauge, sum | **sum** |
| histogram, exponential_histogram | **p99** |
| summary | no default; set it |

### ReduceTo (only for requestType=scalar — collapses time dimension to a single value)

| Metric Type | Default reduceTo |
|---|---|
| gauge | avg |
| sum (monotonic) | sum |
| sum (non-monotonic) | avg |
| histogram | avg |

Valid reduceTo values: sum, count, avg, min, max, last, median

### What SigNoz rejects

- A percentile spaceAggregation (p50 to p99) on anything but a histogram.
- An exponential histogram that is not delta temporality.

---

## Choosing Aggregations

1. **Counters**: rate gives per-second throughput and increase gives the count added per bucket. max or latest on a cumulative counter reads its running total, not activity in the window.
2. **Gauges**: avg, max, or latest. rate and increase run but measure how the value changed, which is rarely what a gauge question asks.
3. **Histograms**: SigNoz stores a histogram named X as X.bucket (type histogram) plus the counters X.count and X.sum; query X.bucket, not X. Leave timeAggregation empty and pick a percentile for latency. To read the buckets instead, use timeAggregation rate with spaceAggregation sum: the result is grouped by le, and the le="+Inf" series is the total observation rate.
4. **Summaries**: query the .quantile series with both aggregations set, for example avg and max, grouped by quantile; the .count and .sum series are counters.
5. **Providing only metricType for a counter** → This bypasses metadata auto-fetch and can leave temporality or monotonicity unspecified. Either omit metricType so all metadata is fetched, or pass the matching values from signoz_list_metrics.
6. **Setting temporality by hand** → Omit it. The backend resolves cumulative, delta or unspecified from the metric's metadata; a pinned value that does not match the exporter returns no data. Set it only to override the stored metadata.

---

## groupBy and Filter Keys

Metric labels hold resource attributes and data-point attributes in one set, so groupBy and filter
keys are plain label names with no context prefix: k8s.namespace.name, host.name, service_name,
http.method. Discover them with signoz_get_field_keys (signal="metrics", metricName). The tool
sends every groupBy key in attribute context, which reads the label.

---

## Formula Queries

You can combine multiple metric queries using formulas:

- Define multiple queries with names like "A", "B", "C"
- Reference them in a formula expression: "A / B * 100"
- Both queries in a formula should ideally use the same groupBy fields for compatible label sets
- Different metrics can have different types; each gets its own aggregation defaults

### Formula Functions
Supported: exp, log, ln, sqrt, sin, cos, tan, abs, ceil, floor

### Example: Error Rate %
Query A: http_errors_total (counter) → rate / sum
Query B: http_requests_total (counter) → rate / sum
Formula C: A / B * 100

## Result Bounds and Ordering

Every standalone ` + "`builder_query`" + ` and every ` + "`builder_formula`" + ` must include a positive limit and
` + "`order: [{\"key\":{\"name\":\"__result\"},\"direction\":\"desc\"}]`" + ` unless the task intentionally
needs another ordering. Standalone queries and formula results use ` + "`limit: 100`" + `. Omit limit and order
on queries a formula references: SigNoz applies each input's limit before it evaluates the formula, so an
input limit silently drops groups from the result. The wire field is ` + "`order`" + `; dashboard/editor payloads use a
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
        "limit": 100,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "aggregations": [{
          "metricName": "container.cpu.utilization",
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
        "limit": 100,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "aggregations": [{
          "metricName": "http_requests_total",
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
        "limit": 100,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "aggregations": [{
          "metricName": "http_request_duration_seconds",
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
          "aggregations": [{
            "metricName": "http_errors_total",
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
          "limit": 100,
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
telemetry ingestion volume (logs, spans, and metric data points, by count and by bytes),
and the set evolves (it may include other billable usage, such as AI credit consumption, in
future). They answer questions like "which services consume the most telemetry budget?" or
"ingestion volume by environment week-over-week". These metrics live in a separate store and
are **not** visible in the default metrics store.

To query them, set ` + "`source: \"meter\"`" + ` on the ` + "`builder_query`" + ` spec (a sibling of
` + "`name`" + ` and ` + "`signal`" + `), or pass ` + "`source=\"meter\"`" + ` to **signoz_query_metrics**. Omit
` + "`source`" + ` (or leave it empty) for ordinary metrics. Everything else (filters, groupBy,
aggregations, formulas) works exactly as for normal metrics.

Even log and span volume are queried this way, as metrics with ` + "`signal: \"metrics\"`" + ` and
` + "`source: \"meter\"`" + `, not through the logs or traces tools.

### Hourly aggregation — use ` + "`stepInterval: 3600`" + `

Meter data is aggregated in 1-hour buckets, so always set ` + "`stepInterval: 3600`" + ` (as in the
example below). A smaller step adds no resolution and a query window shorter than 1 hour yields at
most the current, still-incomplete hour (the build tested returns it flagged ` + "`partial: true`" + `;
some versions return no data). Use a window of at least a few hours. When **signoz_query_metrics**
is called without ` + "`stepInterval`" + ` it sends none and the backend auto-derives roughly
` + "`max(60, window/300)`" + ` seconds, below 3600 for any window shorter than ~12.5 days, so pass
` + "`stepInterval: 3600`" + ` explicitly for meter queries.

### Discover the current meter metrics

Don't assume a fixed list; the meter metric set evolves. Call **signoz_list_metrics** with
` + "`source=\"meter\"`" + ` for the authoritative, current set, with each metric's ` + "`type`" + `,
` + "`temporality`" + `, and ` + "`unit`" + `, then apply the normal per-type aggregation rules (see above).
As of this writing the set is telemetry-ingestion counters (delta monotonic sums), for
example ` + "`signoz.meter.log.size`" + ` (bytes), ` + "`signoz.meter.span.count`" + `, and
` + "`signoz.meter.metric.datapoint.size`" + `, for which ` + "`timeAggregation: rate`" + ` or ` + "`increase`" + `
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
        "limit": 100,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "aggregations": [{
          "metricName": "signoz.meter.log.size",
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
