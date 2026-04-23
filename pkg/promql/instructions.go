// Package promql exposes the canonical PromQL writing guide served as the
// signoz://promql/instructions MCP resource. The guide is consumed by both
// promql_rule alerts and PromQL dashboard widgets, so it lives in its own
// package rather than under pkg/dashboard.
package promql

// Instructions is the text served as the signoz://promql/instructions MCP
// resource. Cross-referenced from signoz://alert/instructions, the
// signoz_create_alert / signoz_update_alert / signoz_execute_builder_query
// tool descriptions, and the dashboard widgets-instructions resource.
const Instructions = `
SigNoz PromQL Guide
Source: https://signoz.io/docs/userguide/write-a-prom-query-with-new-format/
================================================================================
CRITICAL RULES
================================================================================
When writing PromQL queries in SigNoz, you MUST follow these rules:
1. Metric names with dots MUST be quoted and wrapped in curly braces
2. Label names with dots MUST be quoted
3. Metric name MUST be the first parameter inside curly braces (without a label operator)
4. Use double quotes for all quoted strings
5. Traditional metric names (letters, numbers, underscores only) can use old or new format, but prefer new format for consistency
================================================================================
SYNTAX PATTERN
================================================================================
New format syntax:
  {<metric_name>, <label_filters>}

With dots in metric name:
  {"metric.name.with.dots", label="value"}

With dots in label name:
  {"metric.name", "label.name.with.dots"="value"}
================================================================================
KEY EXAMPLES
================================================================================
Example 1: Container CPU Utilization
  sum by ("k8s.pod.name") (rate({"container.cpu.utilization","k8s.namespace.name"="ns"}[5m]))

Example 2: Histogram Quantile
  histogram_quantile(0.95, sum by (le) (rate({"request.duration.bucket",job="api"}[5m])))

Example 3: Complex Query with Functions
  max_over_time({"foo.bar.total",env=~"prod|stag"}[1h] offset 30m) / ignoring ("instance") group_left () sum without ("pod") (rate({"other.metric"}[5m]))

Example 4: Metrics Without Dots (Both Work)
  sum by ("foo_attr") (rate({"foo_bar_total", env=~"prod|stage"}[1h] offset 30m))
================================================================================
EXAMPLES BY METRIC TYPE
================================================================================
COUNTER:
  rate({"http_requests_total", job="api", status!~"5.."}[5m])
GAUGE:
  avg_over_time({"kube_pod_container_resource_requests_cpu_cores",namespace="prod"}[10m])
HISTOGRAM:
  histogram_quantile(0.95, sum by (le) (rate({"http_request_duration_seconds.bucket",job="api"}[5m])))
  Note: Histogram metrics use .min, .max, .count, .bucket, .sum suffixes (not _ underscore)
SUMMARY:
  sum(rate({"http_request_duration_seconds.sum"}[5m])) / sum(rate({"http_request_duration_seconds.count"}[5m]))
  Note: Summary metrics use .count, .quantile, .sum suffixes (not _ underscore)
================================================================================
COMMON PATTERNS
================================================================================
Rate with Filters:
  rate({"http.requests.total", job="api", status!~"5.."}[5m])
Aggregation with Grouping:
  sum by ("service.name", "environment") (rate({"http.requests"}[5m]))
Multiple Label Filters:
  {"metric.name", "label.one"="value1", "label.two"=~"value.*", label_three="value3"}
Using ignoring/without:
  sum({"metric.a"}) / ignoring ("instance.id") sum({"metric.b"})
================================================================================
QUICK REFERENCE
================================================================================
Element              | Has Dots? | Format
---------------------|-----------|----------------------------------------
Metric name          | Yes       | {"metric.name"}
Metric name          | No        | {"metric_name"} or metric_name
Label name           | Yes       | "label.name"="value"
Label name           | No        | label_name="value" or "label_name"="value"
Grouping label       | Yes       | by ("label.name")
Grouping label       | No        | by ("label_name") or by (label_name)
================================================================================
VALIDATION CHECKLIST
================================================================================
Before submitting a PromQL query, verify:
  [ ] Metric name is wrapped in curly braces {}
  [ ] Metric name is the first parameter inside {}
  [ ] Metric name with dots is quoted: {"metric.name"}
  [ ] Label names with dots are quoted: "label.name"="value"
  [ ] Grouping labels with dots are quoted: by ("label.name")
  [ ] All quotes are double quotes ", not single quotes '
  [ ] Histogram/Summary metrics use dot suffixes (.bucket, .sum, .count)
  [ ] For grouped or multi-series charts, legend uses {{label_name}} syntax matching query labels
================================================================================
ERROR PREVENTION
================================================================================
Common Mistakes:
  WRONG: rate("http.requests.total"[5m])                                    // Missing curly braces
  WRONG: rate({job="api", "http.requests.total"}[5m])                      // Metric name not first
  WRONG: rate({'http.requests.total'}[5m])                                 // Single quotes
  WRONG: rate({"http.requests.total", service.name="api"}[5m])            // Missing quotes on label with dots

Correct Versions:
  RIGHT: rate({"http.requests.total"}[5m])                                 // Curly braces present
  RIGHT: rate({"http.requests.total", job="api"}[5m])                     // Metric name first
  RIGHT: rate({"http.requests.total"}[5m])                                // Double quotes
  RIGHT: rate({"http.requests.total", "service.name"="api"}[5m])         // Quoted labels with dots
================================================================================
PROMETHEUS 3.X UTF-8 QUOTED SELECTOR (OTEL METRIC NAMES WITH DOTS)
================================================================================
OTel metrics in SigNoz are stored with their original dotted names (e.g. payment_latency_ms.bucket,
http.server.duration.bucket). Standard Prometheus name forms do NOT resolve these in SigNoz's PromQL
layer. The canonical way to reference dotted names is the Prometheus 3.x UTF-8 quoted selector
inside curly braces: {"metric.name.with.dots"}.

Use this:
  histogram_quantile(0.9, sum(rate({"payment_latency_ms.bucket"}[5m])) by (le))

The quoted-string-inside-braces form works everywhere a metric selector is valid (range vectors,
instant vectors, rate(), sum(), histogram_quantile(), etc).

Forms that DO NOT work for dotted OTel names:
  Form                    | Example                                              | Result
  ------------------------|------------------------------------------------------|-----------------------------------------------
  Underscored conversion  | rate(payment_latency_ms_bucket[5m])                  | no data — SigNoz does not rename to underscores
  __name__ selector       | rate({__name__="payment_latency_ms.bucket"}[5m])     | no data — dots rejected inside the value
  Bare dotted name        | rate(payment_latency_ms.bucket[5m])                  | no data — dot is not a legal identifier char

================================================================================
COMBINING WITH DOTTED LABEL FILTERS
================================================================================
Dotted resource attributes (e.g. deployment.environment, service.name) follow the same rule —
quote them both in by(...) and in label matchers:

  histogram_quantile(
    0.9,
    sum by (le, "service.name") (
      rate({"payment_latency_ms.bucket", "deployment.environment"="prod"}[5m])
    )
  )

================================================================================
MIXING QUOTED AND UNQUOTED LABELS IN VECTOR MATCHING
================================================================================
Inside by(), without(), on(), and ignoring(), only labels containing dots or other
non-identifier characters need quoting. Plain identifier labels stay bare. Mix them
freely within the same call:

  sum by (topic, partition, "deployment.environment") (rate({"kafka.log.end.offset"}[5m]))
  sum without ("k8s.pod.name") (rate({"container.cpu.utilization"}[5m]))

Vector matching operators follow the same rule:

  # from SigNoz PR #11023 — consumer-group lag (max lag end offset - committed offset)
  (
    max by(topic, partition, "deployment.environment") (kafka_log_end_offset)
    - on(topic, partition, "deployment.environment") group_right
      max by(group, topic, partition, "deployment.environment") (kafka_consumer_committed_offset)
  ) > 0

Notes:
  - on(...)     — join the two sides by the listed labels; quote dotted ones.
  - group_left  — the LEFT side may have many rows per matched key.
  - group_right — the RIGHT side may have many rows per matched key.
  - Empty tuples are legal, e.g.  / ignoring ("instance") group_left () sum without ("pod") (rate(...)) .
  - Backward compatibility: pre-existing queries that use only non-dotted names continue to work unchanged; the quoted form is additive, not a replacement.

================================================================================
PRE-FLIGHT CHECKLIST FOR A PROMQL HISTOGRAM ALERT
================================================================================
Before creating a promql_rule alert on a histogram-derived query, run these checks so the rule
actually resolves against data:

  1. Confirm the metric exists and has data via signoz_list_metrics + signoz_query_metrics
     (a builder query on the same metric).
  2. Use signoz_get_field_keys with metricName set, to verify the bucket-boundary label is named
     le (it usually is, but custom pipelines can rename it).
  3. Write the PromQL with the UTF-8 quoted selector form: {"metric.name.bucket"}.
  4. After creating the alert, call signoz_get_alert and check state — inactive under a
     metric that you know is breaching the threshold is a strong signal the query isn't resolving
     (typically a name/selector-form mistake from the table above).

================================================================================
SUMMARY
================================================================================
Always use the new format for consistency and OpenTelemetry compatibility.
Key takeaway: Wrap everything in curly braces, quote anything with dots, metric name goes first.
For dotted OTel names use the Prometheus 3.x UTF-8 quoted selector form {"metric.name.with.dots"} —
no other form resolves in SigNoz's PromQL layer.
`
