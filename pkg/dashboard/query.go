package dashboard

const PromqlQuery = `
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
SUMMARY
================================================================================
Always use the new format for consistency and OpenTelemetry compatibility.
Key takeaway: Wrap everything in curly braces, quote anything with dots, metric name goes first.
`
const ClickhouseSqlQueryForMetrics = `
SigNoz Metrics ClickHouse Query Examples
Source: https://signoz.io/docs/userguide/write-a-metrics-clickhouse-query/

================================================================================
BASIC QUERIES
================================================================================
Fingerprints and Labels:
  SELECT DISTINCT fingerprint, labels
  FROM signoz_metrics.distributed_time_series_v4
  WHERE metric_name = 'signoz_calls_total'
    AND temporality = 'Cumulative'
  LIMIT 10

Extract Label Value:
  SELECT DISTINCT fingerprint,
         JSONExtractString(labels, 'service_name') AS service_name
  FROM signoz_metrics.distributed_time_series_v4_1day
  WHERE metric_name = 'signoz_calls_total'
    AND temporality = 'Cumulative'
  LIMIT 10

Raw Samples:
  SELECT timestamp_ms, value
  FROM signoz_metrics.distributed_samples_v4
  WHERE metric_name = 'signoz_calls_total'
  LIMIT 10

================================================================================
RATE CALCULATION PATTERN
================================================================================
Request Rate (Counter Metric):
  SELECT ts, sum(per_series_value) AS value
  FROM (
      SELECT ts,
             If((per_series_value - lagInFrame(per_series_value, 1, 0) OVER rate_window) < 0, nan,
                If((ts - lagInFrame(ts, 1, toDate('1970-01-01')) OVER rate_window) >= 86400, nan,
                   (per_series_value - lagInFrame(per_series_value, 1, 0) OVER rate_window) /
                   (ts - lagInFrame(ts, 1, toDate('1970-01-01')) OVER rate_window))) AS per_series_value
      FROM (
          SELECT fingerprint,
                 toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(60)) AS ts,
                 max(value) AS per_series_value
          FROM signoz_metrics.distributed_samples_v4
          INNER JOIN (
              SELECT DISTINCT fingerprint
              FROM signoz_metrics.time_series_v4_1day
              WHERE metric_name = 'signoz_calls_total'
                AND temporality = 'Cumulative'
                AND unix_milli >= intDiv({{.start_timestamp_ms}}, 86400000) * 86400000
                AND unix_milli < {{.end_timestamp_ms}}
                AND JSONExtractString(labels, 'service_name') = 'frontend'
          ) AS filtered_time_series USING (fingerprint)
          WHERE metric_name = 'signoz_calls_total'
            AND unix_milli >= {{.start_timestamp_ms}}
            AND unix_milli < {{.end_timestamp_ms}}
          GROUP BY fingerprint, ts
          ORDER BY fingerprint ASC, ts ASC
      )
      WINDOW rate_window AS (PARTITION BY fingerprint ORDER BY fingerprint ASC, ts ASC)
  )
  WHERE isNaN(per_series_value) = 0
  GROUP BY ts
  ORDER BY ts ASC

Key Pattern:
  - Use lagInFrame() to calculate difference between consecutive values
  - Handle counter resets (negative differences → nan)
  - Handle large time gaps (>= 86400s → nan)
  - Filter out nan values before aggregation

================================================================================
ERROR RATE (RATIO OF TWO METRICS)
================================================================================
  SELECT A.ts AS ts, (A.value * 100) / B.value AS error_percentage
  FROM (
      -- Error count rate (status_code = ERROR)
      SELECT ts, sum(value) AS value
      FROM (
          SELECT ts,
                 if(runningDifference(ts) <= 0, nan,
                    if(runningDifference(value) < 0, value / runningDifference(ts),
                       runningDifference(value) / runningDifference(ts))) AS value
          FROM (
              SELECT fingerprint,
                     toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(60)) AS ts,
                     max(value) AS value
              FROM signoz_metrics.distributed_samples_v4
              INNER JOIN (
                  SELECT DISTINCT fingerprint
                  FROM signoz_metrics.time_series_v4_1day
                  WHERE metric_name = 'signoz_calls_total'
                    AND temporality = 'Cumulative'
                    AND unix_milli >= intDiv({{.start_timestamp_ms}}, 86400000) * 86400000
                    AND unix_milli < {{.end_timestamp_ms}}
                    AND JSONExtractString(labels, 'service_name') = 'redis'
                    AND JSONExtractString(labels, 'status_code') IN ['STATUS_CODE_ERROR']
              ) AS filtered_time_series USING (fingerprint)
              WHERE metric_name = 'signoz_calls_total'
                AND unix_milli >= {{.start_timestamp_ms}}
                AND unix_milli <= {{.end_timestamp_ms}}
              GROUP BY fingerprint, ts
              ORDER BY fingerprint ASC, ts ASC
          )
          WHERE isNaN(value) = 0
      )
      GROUP BY ts
      ORDER BY ts ASC
  ) AS A
  INNER JOIN (
      -- Total count rate (all status codes)
      SELECT ts, sum(value) AS value
      FROM (
          SELECT ts,
                 if(runningDifference(ts) <= 0, nan,
                    if(runningDifference(value) < 0, value / runningDifference(ts),
                       runningDifference(value) / runningDifference(ts))) AS value
          FROM (
              SELECT fingerprint,
                     toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(60)) AS ts,
                     max(value) AS value
              FROM signoz_metrics.distributed_samples_v4
              INNER JOIN (
                  SELECT DISTINCT fingerprint
                  FROM signoz_metrics.time_series_v4_1day
                  WHERE metric_name = 'signoz_calls_total'
                    AND temporality = 'Cumulative'
                    AND unix_milli >= intDiv({{.start_timestamp_ms}}, 86400000) * 86400000
                    AND unix_milli < {{.end_timestamp_ms}}
                    AND JSONExtractString(labels, 'service_name') = 'redis'
              ) AS filtered_time_series USING (fingerprint)
              WHERE metric_name = 'signoz_calls_total'
                AND unix_milli >= {{.start_timestamp_ms}}
                AND unix_milli <= {{.end_timestamp_ms}}
              GROUP BY fingerprint, ts
              ORDER BY fingerprint ASC, ts ASC
          )
          WHERE isNaN(value) = 0
      )
      GROUP BY ts
      ORDER BY ts ASC
  ) AS B ON A.ts = B.ts

Key Pattern:
  - Calculate rate for numerator (errors)
  - Calculate rate for denominator (total)
  - Join on timestamp and compute ratio

================================================================================
HISTOGRAM QUANTILE (LATENCY PERCENTILES)
================================================================================
  SELECT ts, histogramQuantile(arrayMap(x -> toFloat64(x), groupArray(le)),
                                groupArray(value), 0.99) AS p99_latency
  FROM (
      SELECT le, ts, sum(value) AS value
      FROM (
          SELECT le, ts,
                 if(runningDifference(ts) <= 0, nan,
                    if(runningDifference(value) < 0, value / runningDifference(ts),
                       runningDifference(value) / runningDifference(ts))) AS value
          FROM (
              SELECT fingerprint, le,
                     toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(60)) AS ts,
                     max(value) AS value
              FROM signoz_metrics.distributed_samples_v4
              INNER JOIN (
                  SELECT DISTINCT JSONExtractString(labels, 'le') AS le, fingerprint
                  FROM signoz_metrics.time_series_v4_1day
                  WHERE metric_name = 'signoz_latency_bucket'
                    AND temporality = 'Cumulative'
                    AND unix_milli >= intDiv({{.start_timestamp_ms}}, 86400000) * 86400000
                    AND unix_milli < {{.end_timestamp_ms}}
                    AND JSONExtractString(labels, 'service_name') = 'frontend'
              ) AS filtered_time_series USING (fingerprint)
              WHERE metric_name = 'signoz_latency_bucket'
                AND unix_milli >= {{.start_timestamp_ms}}
                AND unix_milli <= {{.end_timestamp_ms}}
              GROUP BY fingerprint, le, ts
              ORDER BY fingerprint ASC, le ASC, ts ASC
          )
          WHERE isNaN(value) = 0
      )
      GROUP BY le, ts
      HAVING isNaN(value) = 0
      ORDER BY le ASC, ts ASC
  )
  GROUP BY ts
  ORDER BY ts ASC

Key Pattern:
  - Extract le (less than or equal) bucket labels
  - Calculate rate for each bucket
  - Use histogramQuantile() to compute percentile (0.99 = P99)
  - Group buckets by timestamp

================================================================================
KEY PATTERNS
================================================================================
Time Series Tables by Granularity:
  - distributed_time_series_v4       - 1 hour granularity
  - distributed_time_series_v4_6hrs  - 6 hour granularity
  - distributed_time_series_v4_1day  - 1 day granularity

Use appropriate table for time range:
  - Short queries (< 6h):    time_series_v4
  - Medium queries (6h-24h): time_series_v4_6hrs
  - Long queries (> 24h):    time_series_v4_1day

Timestamp Filtering:
  Time series table (unix_milli):
    unix_milli >= intDiv({{.start_timestamp_ms}}, 86400000) * 86400000
    AND unix_milli < {{.end_timestamp_ms}}

  Samples table (unix_milli):
    unix_milli >= {{.start_timestamp_ms}}
    AND unix_milli < {{.end_timestamp_ms}}

Label Filtering:
  JSONExtractString(labels, 'label_name') = 'value'
  JSONExtractString(labels, 'status_code') IN ['ERROR', 'WARN']

Rate Calculation Methods:

  Using lagInFrame() (Recommended):
    (per_series_value - lagInFrame(per_series_value, 1, 0) OVER rate_window) /
    (ts - lagInFrame(ts, 1, toDate('1970-01-01')) OVER rate_window)

  Using runningDifference():
    if(runningDifference(value) < 0, value / runningDifference(ts),
       runningDifference(value) / runningDifference(ts))

Time Interval Aggregation:
  toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(60))   -- 1 minute
  toStartOfInterval(toDateTime(intDiv(unix_milli, 1000)), toIntervalSecond(300))  -- 5 minutes

Variables:
  {{.start_timestamp_ms}} - Start time (milliseconds)
  {{.end_timestamp_ms}}   - End time (milliseconds)
  {{.service_name}}       - Custom variable (user-defined)
  {{.variable_name}}      - Any custom variable

Using Variables:
  JSONExtractString(labels, 'service_name') = {{.service_name}}

Common Metric Types:
  - Counter (cumulative):  signoz_calls_total, use rate calculation
  - Histogram (buckets):   signoz_latency_bucket, use histogramQuantile()
  - Gauge (point-in-time): Direct aggregation (avg, max, min)

Performance Tips:
  1. Use appropriate time_series table for query range
  2. Filter on metric_name and temporality in time_series table
  3. Use DISTINCT fingerprint in subquery for filtering
  4. Add time range filters to both tables
  5. Order by fingerprint and timestamp for window functions
  6. Filter isNaN() values before final aggregation
`

const ClickhouseSqlQueryForLogs = `
	SigNoz Logs ClickHouse Query Examples
Source: https://signoz.io/docs/userguide/logs_clickhouse_queries/

================================================================================
ESSENTIAL PATTERNS
================================================================================
Resource Filter Template (CTE):
  WITH __resource_filter AS (
      SELECT fingerprint
      FROM signoz_logs.distributed_logs_v2_resource
      WHERE (simpleJSONExtractString(labels, 'service.name') = 'myservice')
        AND seen_at_ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  )
  SELECT ...
  FROM signoz_logs.distributed_logs_v2
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp >= $start_timestamp_nano
    AND timestamp <= $end_timestamp_nano
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp

Multiple Resource Filters:
  WHERE (simpleJSONExtractString(labels, 'service.name') = 'demo')
    AND (simpleJSONExtractString(labels, 'environment') = 'production')

================================================================================
TIMESERIES QUERIES
================================================================================
Basic Count Per Minute:
  WITH __resource_filter AS (...)
  SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS ts,
         toFloat64(count()) AS value
  FROM signoz_logs.distributed_logs_v2
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp >= $start_timestamp_nano
    AND timestamp <= $end_timestamp_nano
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY ts
  ORDER BY ts ASC;

Group By Standard Attribute:
  SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS ts,
         attributes_string['container_name'] as container_name,
         toFloat64(count()) AS value
  FROM signoz_logs.distributed_logs_v2
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp >= $start_timestamp_nano
    AND timestamp <= $end_timestamp_nano
    AND mapContains(attributes_string, 'container_name')
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY container_name, ts
  ORDER BY ts ASC;

Multiple Filters (Severity + Attribute + Resource):
  WITH __resource_filter AS (
      SELECT fingerprint
      FROM signoz_logs.distributed_logs_v2_resource
      WHERE (simpleJSONExtractString(labels, 'service.name') = 'demo')
        AND seen_at_ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  )
  SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS ts,
         toFloat64(count()) AS value
  FROM signoz_logs.distributed_logs_v2
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp >= $start_timestamp_nano
    AND timestamp <= $end_timestamp_nano
    AND severity_text = 'INFO'
    AND attributes_string['method'] = 'GET'
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY ts
  ORDER BY ts ASC;

Using Indexed (Selected) Fields:
  Replace: attributes_string['method']
  With:    attribute_string_method

  WHERE severity_text = 'INFO'
    AND attribute_string_method = 'GET'

================================================================================
VALUE QUERIES (SINGLE AGGREGATED VALUE)
================================================================================
Average from Timeseries:
  WITH __resource_filter AS (...)
  SELECT avg(value) as value, any(ts) as ts
  FROM (
      SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS ts,
             toFloat64(count()) AS value
      FROM signoz_logs.distributed_logs_v2
      WHERE resource_fingerprint GLOBAL IN __resource_filter
        AND timestamp >= $start_timestamp_nano
        AND timestamp <= $end_timestamp_nano
        AND severity_text = 'INFO'
        AND attributes_string['method'] = 'GET'
        AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
      GROUP BY ts
      ORDER BY ts ASC
  )
================================================================================
COMMON USE CASES
================================================================================
Logs by Kubernetes Cluster:
  SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS ts,
         resource.k8s.cluster.name::String as k8s_cluster_name,
         toFloat64(count()) AS value
  FROM signoz_logs.distributed_logs_v2
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp >= $start_timestamp_nano
    AND timestamp <= $end_timestamp_nano
    AND k8s_cluster_name IS NOT NULL
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY k8s_cluster_name, ts
  ORDER BY ts ASC;

================================================================================
KEY PATTERNS
================================================================================
Timestamp Filtering (Always Include Both):
  ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  AND timestamp >= $start_timestamp_nano
  AND timestamp <= $end_timestamp_nano

Attribute Access:

  Resource Attributes (SELECT/GROUP BY):
    resource.<keyname>::String
    Examples:
      resource.service.name::String
      resource.k8s.cluster.name::String
      resource.k8s.namespace.name::String

  Resource Attributes (WHERE - Non-indexed):
    simpleJSONExtractString(labels, 'resource.name')

  Resource Attributes (WHERE - Indexed):
    resource_string_resource$$name
    Example: resource_string_service$$name

  Standard Attributes (Non-indexed):
    attributes_string['attribute_name']
    Check existence: mapContains(attributes_string, 'attribute_name')

  Standard Attributes (Indexed/Selected):
    attribute_string_attribute_name
    Example: attribute_string_method

Common Severity Filters:
  severity_text = 'ERROR'
  severity_text = 'WARN'
  severity_text = 'INFO'
  severity_text IN ('ERROR', 'FATAL')

Variables:
  $start_timestamp      - Unix timestamp (seconds)
  $end_timestamp        - Unix timestamp (seconds)
  $start_timestamp_nano - Unix timestamp (nanoseconds)
  $end_timestamp_nano   - Unix timestamp (nanoseconds)

Common Fields:
  - severity_text         - Log severity level
  - body                  - Log message body
  - trace_id              - Associated trace ID
  - span_id               - Associated span ID
  - resource_fingerprint  - Resource fingerprint for filtering

Performance Tips:
  1. Always use resource filter CTE for service filtering
  2. Include both timestamp filters (ts_bucket_start and timestamp)
  3. Use indexed/selected attributes when available
  4. Filter on IS NOT NULL for grouped fields
  5. Add 1800s buffer to ts_bucket_start for safety
  6. Use mapContains() before accessing non-indexed attributes

Indexed vs Non-Indexed Attributes:

  When to use indexed (selected) fields:
    - Frequently queried attributes
    - High cardinality fields
    - Performance-critical queries

  Syntax comparison:
    Non-indexed (slower):  attributes_string['method'] = 'GET'
    Indexed (faster):      attribute_string_method = 'GET'

  Check if attribute exists (non-indexed):
    mapContains(attributes_string, 'attribute_name')
`

const ClickhouseSqlQueryForTraces = `
	SigNoz Traces ClickHouse Query Examples
Source: https://signoz.io/docs/userguide/writing-clickhouse-traces-query/

================================================================================
ESSENTIAL PATTERNS
================================================================================
Resource Filter Template (CTE):
  WITH __resource_filter AS (
      SELECT fingerprint
      FROM signoz_traces.distributed_traces_v3_resource
      WHERE (simpleJSONExtractString(labels, 'service.name') = 'myservice')
        AND seen_at_ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  )
  SELECT ...
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp

Multiple Resource Filters:
  WHERE (simpleJSONExtractString(labels, 'service.name') = 'frontend')
    AND (simpleJSONExtractString(labels, 'environment') = 'production')

================================================================================
TIMESERIES QUERIES
================================================================================
Basic Count Per Minute:
  WITH __resource_filter AS (...)
  SELECT toStartOfInterval(timestamp, INTERVAL 1 MINUTE) AS ts,
         toFloat64(count()) AS value
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY ts
  ORDER BY ts ASC;

Custom Interval (100ms):
  SELECT fromUnixTimestamp64Milli(intDiv(toUnixTimestamp64Milli(timestamp), 100) * 100) AS interval,
         toFloat64(count()) AS count
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE resource_string_service$$name='frontend'
    AND duration_nano >= 50*exp10(6)  -- 50ms
    AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  GROUP BY interval
  ORDER BY interval ASC;

Group By Standard Attribute:
  SELECT toStartOfInterval(timestamp, INTERVAL 1 MINUTE) AS interval,
         attributes_string['http.method'] AS method,
         toFloat64(avg(durationNano)) AS value
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE attributes_string['http.method'] != ''
    AND timestamp > now() - INTERVAL 30 MINUTE
    AND ts_bucket_start >= toUInt64(toUnixTimestamp(now() - toIntervalMinute(30))) - 1800
  GROUP BY (method, interval)
  ORDER BY (method, interval) ASC;

================================================================================
VALUE QUERIES (SINGLE AGGREGATED VALUE)
================================================================================
Average from Timeseries:
  SELECT avg(value) as value, any(ts) as ts
  FROM (
      SELECT toStartOfInterval(timestamp, INTERVAL 1 MINUTE) AS ts,
             toFloat64(avg(duration_nano)) AS value
      FROM signoz_traces.distributed_signoz_index_v3
      WHERE resource_fingerprint GLOBAL IN __resource_filter
        AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
        AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
        AND http_method = 'GET'
      GROUP BY ts
      ORDER BY ts ASC
  )

================================================================================
TABLE QUERIES
================================================================================
Group By with Table Format:
  SELECT now() as ts,
         http_method,
         toFloat64(avg(duration_nano)) AS avg_duration_nano
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE resource_fingerprint GLOBAL IN __resource_filter
    AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
    AND ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
    AND http_method IS NOT NULL
    AND http_method != ''
  GROUP BY http_method, ts
  ORDER BY avg_duration_nano DESC;

================================================================================
ADVANCED USE CASES
================================================================================
Extract Span Event Attributes:
  WITH arrayFilter(x -> JSONExtractString(x, 'name')='Getting customer', events) AS filteredEvents
  SELECT toStartOfInterval(timestamp, INTERVAL 1 MINUTE) AS interval,
         toFloat64(count()) AS count,
         arrayJoin(arrayMap(x -> JSONExtractString(JSONExtractString(x, 'attributeMap'), 'customer_id'),
                            filteredEvents)) AS customer_id
  FROM signoz_traces.distributed_signoz_index_v3
  WHERE not empty(filteredEvents)
    AND timestamp > toUnixTimestamp(now() - INTERVAL 30 MINUTE)
    AND ts_bucket_start >= toUInt64(toUnixTimestamp(now() - toIntervalMinute(30))) - 1800
  GROUP BY (customer_id, interval)
  ORDER BY (customer_id, interval) ASC;

Latency Between Spans in Trace:
  SELECT interval, round(avg(time_diff), 2) AS avg_latency_ms
  FROM (
      SELECT interval, traceID,
             if(startTime1 != 0 AND startTime2 != 0,
                (toUnixTimestamp64Nano(startTime2) - toUnixTimestamp64Nano(startTime1)) / 1000000,
                nan) AS time_diff
      FROM (
          SELECT toStartOfInterval(timestamp, toIntervalMinute(1)) AS interval,
                 traceID,
                 minIf(timestamp, resource_string_service$$name='driver' AND name='/driver.DriverService/FindNearest') AS startTime1,
                 minIf(timestamp, resource_string_service$$name='route' AND name='HTTP GET /route') AS startTime2
          FROM signoz_traces.distributed_signoz_index_v3
          WHERE timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}
            AND ts_bucket_start BETWEEN {{.start_timestamp}} - 1800 AND {{.end_timestamp}}
            AND resource_string_service$$name IN ('driver', 'route')
          GROUP BY (interval, traceID)
      )
  )
  WHERE isNaN(time_diff) = 0
  GROUP BY interval
  ORDER BY interval ASC;

Cross-Signal Query (Logs):
  SELECT toStartOfInterval(fromUnixTimestamp64Nano(timestamp), INTERVAL 1 MINUTE) AS interval,
         toFloat64(count()) AS value
  FROM signoz_logs.distributed_logs_v2
  WHERE timestamp > toUnixTimestamp64Nano(now64() - INTERVAL 30 MINUTE)
    AND ts_bucket_start >= toUInt64(toUnixTimestamp(now() - toIntervalMinute(30))) - 1800
  GROUP BY interval
  ORDER BY interval ASC;

================================================================================
KEY PATTERNS
================================================================================
Timestamp Filtering (Always Include Both):
  ts_bucket_start BETWEEN $start_timestamp - 1800 AND $end_timestamp
  AND timestamp BETWEEN {{.start_datetime}} AND {{.end_datetime}}

Attribute Access:

  Resource Attributes (SELECT/GROUP BY):
    resource.<keyname>::String
    Example: resource.service.name::String

  Resource Attributes (WHERE - Non-indexed):
    simpleJSONExtractString(labels, 'resource.name')

  Resource Attributes (WHERE - Indexed):
    resource_string_resource$$name

  Standard Attributes (Non-indexed):
    attributes_string['attribute_name']

  Standard Attributes (Indexed):
    attribute_string_attribute_name

Duration Conversions:
  duration_nano >= 50*exp10(6)      -- 50ms to nanoseconds
  duration_nano / 1000000            -- nanoseconds to milliseconds

Variables:
  $start_timestamp    - Unix timestamp (seconds)
  $end_timestamp      - Unix timestamp (seconds)
  {{.start_datetime}} - DateTime format
  {{.end_datetime}}   - DateTime format

Common Fields:
  - has_error             - Boolean (span error status)
  - http_method           - String (GET, POST, etc.)
  - duration_nano         - UInt64 (span duration)
  - traceID               - String (trace identifier)
  - resource_fingerprint  - UInt64 (for filtering)
  - events                - Array (span events)

Performance Tips:
  1. Always use resource filter CTE for service filtering
  2. Include both timestamp filters
  3. Use indexed attributes when available
  4. Filter on IS NOT NULL and != '' for string fields
  5. Add 1800s buffer to ts_bucket_start for safety
`

const Querybuilder = `
SigNoz Query Builder Reference
Consolidated guide for SigNoz Query Builder, search syntax, operators, and advanced querying.

================================================================================
METRICS NAMING CONVENTIONS (CRITICAL FOR QUERY BUILDER)
================================================================================

When querying OpenTelemetry metrics in Query Builder, you MUST use the correct metric name format:

CRITICAL RULE: OpenTelemetry histogram and summary metrics use DOT NOTATION for suffixes, NOT underscores.

Correct Format (with dots):
  - otelcol_connector_received_items.sum
  - otelcol_processor_batch_batch_send_size.sum
  - signoz_latency.sum
  - signoz_latency.count
  - signoz_latency.bucket
  - http_request_duration_seconds.sum
  - http_request_duration_seconds.count

WRONG Format (with underscores - DO NOT USE):
  - otelcol_connector_received_items_sum        ❌ WRONG
  - otelcol_connector_received_items_count      ❌ WRONG
  - signoz_latency_sum                          ❌ WRONG
  - http_request_duration_seconds_count         ❌ WRONG

Common Metric Suffixes (use dots):
  .sum       - Sum of all values (histogram/summary)
  .count     - Count of observations (histogram/summary)
  .bucket    - Histogram bucket (for percentile calculations)
  .min       - Minimum value
  .max       - Maximum value

Examples by Metric Type:

  Counter Metrics (no suffix needed):
    - http_requests_total
    - signoz_calls_total
    - otelcol_receiver_accepted_spans

  Gauge Metrics (no suffix needed):
    - system_memory_usage
    - process_cpu_seconds_total

  Histogram Metrics (use .sum, .count, .bucket):
    - signoz_latency.sum          (total latency)
    - signoz_latency.count        (number of requests)
    - signoz_latency.bucket       (for percentiles)
    - http_request_duration.sum
    - http_request_duration.count

  Summary Metrics (use .sum, .count, .quantile):
    - rpc_duration_seconds.sum
    - rpc_duration_seconds.count

When selecting metrics in Query Builder:
  1. Search for the base metric name (e.g., "otelcol_connector_received_items")
  2. Select the appropriate suffix variant (e.g., "otelcol_connector_received_items.sum")
  3. NEVER manually append _sum, _count, etc. with underscores

Common OpenTelemetry Collector Metrics:
  - otelcol_receiver_accepted_spans.sum
  - otelcol_receiver_refused_spans.sum
  - otelcol_processor_batch_batch_send_size.sum
  - otelcol_processor_batch_batch_send_size.count
  - otelcol_exporter_sent_spans.sum
  - otelcol_exporter_send_failed_spans.sum
  - otelcol_connector_received_items.sum
  - otelcol_connector_produced_items.sum

Common SigNoz Metrics:
  - signoz_latency.sum
  - signoz_latency.count
  - signoz_latency.bucket
  - signoz_calls_total

================================================================================
QUERY BUILDER CORE FEATURES
================================================================================

Filtering:
  Apply filters using operators: =, !=, >, >=, <, <=, IN, NOT IN, LIKE, ILIKE, CONTAINS, EXISTS
  Example: service.name = 'api' AND http.status_code >= 400

Aggregation Functions:
  Statistical: count(), count_distinct(), sum(), avg(), min(), max()
  Percentiles: p50(), p95(), p99()
  Rate: rate(), rate_sum(), rate_avg()

Grouping & Result Manipulation:
  Group By: Segment data by attributes
  Order By: Sort ascending/descending
  Limit: Top N results (for time series: limits number of series, not data points)
  Having: Filter aggregated results - count() > 1000 AND count() < 5000
  Legend Format: Customize labels with {{attribute}}
  Missing Values: Records without grouping key → grouped as "". Use field EXISTS to exclude.

Multi-Query Analysis:
  Combine queries with formulas:
    - Error Rate: (errors / total) * 100
    - SLA: (requests_under_200ms / total) * 100
  Math Functions: +, -, *, /, log, ln, sqrt, exp, sin, cos, abs, now

Time Aggregation:
  Auto Step Interval:
    - Traces/Logs: Min 5s, auto (time_range / 300) rounded to 5s
    - Metrics: Min 60s, varies by time range
    - Max: 1,500 data points per series
  Examples:
    - 1 hour → 10s steps (360 points)
    - 24 hours → 5min steps (300 points)

Trace Operators (V5):
  Analyze parent-child relationships:
    - => Direct descendant
    - -> Indirect descendant
    - && AND within trace
    - || OR
    - NOT Negation
  Example: service.name = 'frontend' => service.name = 'customer'

Metrics-Specific Features:
  Temporal Aggregation: Rate, Increase
  Spatial Aggregation: Across dimensions (service, region, etc.)
  Advanced Functions:
    - Exclusion: Cut Off Min/Max
    - Arithmetic: Absolute, Log2, Log10
    - Smoothing: EWMA 3/5/7
    - Time Shift: Time Shift 3600 (1 hour)

================================================================================
SEARCH SYNTAX
================================================================================

Basic Structure:
  field operator value
  Combine: condition1 AND condition2 OR condition3
  Precedence: NOT > AND > OR (use parentheses!)

Operators Reference:

  Comparison:
    =, !=, <>, <, <=, >, >=

  String Matching:
    - LIKE '%pattern%' - Case-sensitive, wildcards: % (any chars), _ (single char)
    - ILIKE '%pattern%' - Case-insensitive
    - CONTAINS 'text' - Contains text
    - REGEXP 'pattern' - RE2 regex

  Range & Set:
    - BETWEEN min AND max - Inclusive range
    - IN ('val1', 'val2') - Value in list

  Existence:
    - EXISTS - Field exists
    - NOT EXISTS - Field missing

  Boolean:
    - AND, OR, NOT

Field Existence Behavior:

  CRITICAL: Operators behave differently with missing fields!

  Positive Operators (Check Existence):
    =, >, <, LIKE, ILIKE, BETWEEN, IN, CONTAINS, REGEXP
    Example: response_time > 500 only matches records WITH the field.

  Negative Operators (Don't Check):
    !=, <>, NOT LIKE, NOT ILIKE, NOT BETWEEN, NOT IN, NOT CONTAINS, NOT REGEXP
    Example: service.name != 'redis' matches records WITHOUT the field too!

  Best Practice:
    Ambiguous:
      service.name != 'redis'  // Includes records without service.name!
    Explicit:
      service.name EXISTS AND service.name != 'redis'
    Or use positive operators:
      service.name IN ('api', 'web', 'auth')  // Automatically checks existence

Common Pitfalls:
  1. Missing quotes: status = 'active' not status = active
  2. LIKE wildcards: message LIKE '%error%' not message LIKE 'error'
  3. Array syntax: region IN ('us', 'eu') not region IN 'us', 'eu'
  4. Precedence: (a OR b) AND c not a OR b AND c
  5. Case: LIKE is case-sensitive, ILIKE is case-insensitive

Regular Expression (REGEXP):
  RE2 Syntax - Fast, safe, linear time
  Common Patterns:
    . - Any character
    * - Zero or more
    + - One or more
    ? - Zero or one
    [abc] - Character set
    \d - Digit
    \w - Word character
    \s - Whitespace
  Examples:
    email REGEXP '^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$'
    ip_address REGEXP '^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$'
    url REGEXP '^https://'
    level REGEXP '^(ERROR|WARN|FATAL)$'
  Note: RE2 doesn't support lookahead/lookbehind or backreferences.

================================================================================
FULL-TEXT SEARCH (LOGS ONLY)
================================================================================

Searches log body field only.

Quoted vs Unquoted:
  Quoted (Exact Phrase):
    'failed to connect to database'
  Unquoted (All Words):
    error database connection  // Finds logs with ALL three words

Escaping:
  Only escape these two:
    - Single quote: \'
    - Backslash: \\
  Examples:
    'user\'s email'
    'C:\\Program Files\\App'
    'user\'s path: C:\\Users\\John'
  No escaping needed for: ", [, ], $, etc.

Regex in Full-Text:
  '[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}'  // Email
  '^\[SnapshotGenerator id=\d+\]'  // Structured logs

Combining with Fields:
  'payment failed' AND service.name = 'payment-service' AND severity_text = 'ERROR'

Performance Tips:
  1. Narrow time range
  2. Add field filters (service.name, severity_text)
  3. Avoid leading wildcards
  4. Start simple, add complexity incrementally

================================================================================
FUNCTIONS REFERENCE
================================================================================

For JSON body fields only.

hasToken(field, "value"):
  Find whole-word token in string (not substring).
  Examples:
    hasToken(body, "john@test.com")
    hasToken(body, "uuid-123")
  Matches: uuid123 in "The uuid123 was generated"
  Doesn't match: uuid123 in "abcuuid123xyz"

has(field, value):
  Check if array contains value.
  Examples:
    has(body.tags, 'production')
    has(body.user_ids, 12345)
    has(body.regions, 'us-east')
  Requires: Field must be an array in JSON body.

Combining Functions:
  has(body.tags, 'production') AND status_code IN (500, 502, 503)
  has(body.user_ids, 12345) AND severity_text = 'ERROR'
  NOT has(body.tags, 'debug')

Common Errors:
  - "unknown function" - Check spelling/case
  - "expects key and value" - Provide both parameters
  - "supports only body JSON" - Use body. prefix (except hasToken)

================================================================================
ADVANCED SEARCH EXAMPLES
================================================================================

Complex Boolean Logic:
  (service.name = 'auth' OR service.name = 'user') AND status_code >= 400

Nested Conditions:
  region = 'us-east' AND (
    (status = 'error' AND retry_count > 3) OR
    (status = 'timeout' AND response_time_ms > 5000)
  )

Production Incident Investigation:
  deployment.environment = 'production' AND
  severity_text IN ('ERROR', 'FATAL') AND (
    (error.type = 'DatabaseError' AND body CONTAINS 'connection') OR
    (error.type = 'TimeoutError' AND response_time_ms > 30000) OR
    (http.status_code >= 500 AND http.url LIKE '%/critical/%')
  )

User Experience Monitoring:
  user.tier = 'premium' AND
  service.name = 'api-gateway' AND (
    (http.method = 'GET' AND response_time_ms > 2000) OR
    (http.method = 'POST' AND response_time_ms > 5000)
  ) AND
  k8s.namespace.name = 'production'

Security Event Detection:
  (
    (body CONTAINS 'authentication failed' AND client.ip NOT LIKE '10.%') OR
    (http.status_code = 403 AND request_count > 10) OR
    (body REGEXP '(sql injection|xss|csrf)' AND severity_text = 'WARNING')
  )

Performance Degradation:
  service.name = 'database-service' AND (
    (query.type = 'SELECT' AND execution_time_ms > 1000 AND
     table_name IN ('users', 'orders', 'payments')) OR
    (query.type IN ('INSERT', 'UPDATE') AND execution_time_ms > 500 AND
     affected_rows > 1000)
  ) AND deployment.environment = 'production'

Kubernetes Pod Issues:
  k8s.pod.name LIKE 'payment-service-%' AND
  k8s.namespace.name = 'production' AND (
    (severity_text = 'ERROR' AND body CONTAINS 'OOMKilled') OR
    (restart_count > 5) OR
    (body CONTAINS 'CrashLoopBackOff')
  )

Cross-Service Transaction Tracing:
  (trace_id = '123e4567-e89b-12d3-a456-426614174000' OR
   correlation_id = 'ORDER-2024-001') AND
  service.name IN ('order-service', 'payment-service', 'inventory-service')

Feature Flag Analysis:
  has(body.feature_flags, 'new-checkout-flow') AND (
    (conversion.success = true AND response_time_ms < 3000) OR
    (severity_text = 'ERROR' AND body CONTAINS 'feature flag evaluation failed')
  ) AND deployment.environment = 'production'

================================================================================
BEST PRACTICES
================================================================================

Query Building Workflow:
  1. Start broad: service.name = 'payment' AND deployment.environment = 'production'
  2. Add severity: ... AND severity_text IN ('ERROR', 'FATAL')
  3. Add conditions: ... AND (http.status_code >= 500 OR response_time_ms > 5000)
  4. Add context: ... AND user.tier = 'premium'

Performance Optimization:

  Query Level:
    1. Filter early - Apply restrictive filters first
    2. Limit scope - Use specific time ranges
    3. Resource attributes - Leverage indexed fields (service.name, severity_text)
    4. Aggregation efficiency - Choose appropriate time windows

  Dashboard Level:
    1. Adjust refresh frequency
    2. Optimize time ranges
    3. Limit concurrent queries

Common Patterns:
  Pattern                | Example                              | Use Case
  -----------------------|--------------------------------------|---------------------------
  Multi-service          | service IN ('a', 'b')                | Cross-service analysis
  Nested OR              | (cond1 OR cond2) AND cond3           | Complex filtering
  Range + Pattern        | status >= 400 AND url LIKE '%api%'   | API error tracking
  Regex + Field          | body REGEXP 'pattern' AND service='x'| Pattern detection
  Array + Field          | has(body.tags, 'x') AND status = 'y' | Tag-based filtering

Troubleshooting:
  Slow Queries:
    - Check time range
    - Use resource attributes in WHERE
    - Reduce GROUP BY cardinality
    - Increase aggregation intervals
  No Results:
    - Verify filter syntax
    - Check time range includes data
    - Confirm field existence
  Unexpected Results:
    - Review AND vs OR logic
    - Check operator precedence (use parentheses!)
    - Validate field existence behavior

================================================================================
QUICK REFERENCE TABLES
================================================================================

Operators Summary:
  Category     | Positive (Checks Exists)        | Negative (Doesn't Check)
  -------------|----------------------------------|----------------------------------
  Equality     | =                                | !=, <>
  Comparison   | <, <=, >, >=                     | -
  Pattern      | LIKE, ILIKE                      | NOT LIKE, NOT ILIKE
  Range        | BETWEEN                          | NOT BETWEEN
  Set          | IN                               | NOT IN
  Text         | CONTAINS                         | NOT CONTAINS
  Regex        | REGEXP                           | NOT REGEXP
  Existence    | EXISTS                           | NOT EXISTS

Aggregation Functions:
  Function         | Syntax                  | Description
  -----------------|-------------------------|--------------------------------
  Count            | count()                 | Total occurrences
  Count Distinct   | count_distinct(field)   | Unique values
  Sum              | sum(field)              | Total of values
  Average          | avg(field)              | Mean value
  Min/Max          | min(field), max(field)  | Range boundaries
  Percentiles      | p50(), p95(), p99()     | Distribution analysis
  Rate             | rate()                  | Occurrences per time unit
  Rate Sum         | rate_sum(field)         | Change in totals
  Rate Avg         | rate_avg(field)         | Change in averages

Common Query Patterns:
  Operation        | Syntax                      | Example
  -----------------|-----------------------------|---------------------------------
  Equality         | field = 'value'             | service.name = 'api'
  Comparison       | field > value               | duration > 1000
  Pattern          | field LIKE '%pattern%'      | message LIKE '%error%'
  Case-insensitive | field ILIKE '%pattern%'     | path ILIKE '%/api/%'
  In list          | field IN (val1, val2)       | region IN ('us', 'eu')
  Exists           | field EXISTS                | user_id EXISTS
  Full-text        | 'exact phrase'              | 'connection timeout'
  Regex            | field REGEXP 'pattern'      | email REGEXP '^[a-z]+@'
  Array check      | has(field, value)           | has(body.tags, 'prod')
  Token search     | hasToken(field, "token")    | hasToken(body, "uuid123")
`
