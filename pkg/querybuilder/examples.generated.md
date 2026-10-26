# Query range request examples

Source: the request examples published by the SigNoz OpenAPI spec.

## Run a query with signoz_execute_builder_query

### 1. clickhouse_sql_logs_raw — ClickHouse SQL: raw logs with resource filter

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "name": "recent_errors",
          "query": "WITH __resource_filter AS ( SELECT fingerprint FROM signoz_logs.distributed_logs_v2_resource WHERE seen_at_ts_bucket_start >= $start_timestamp - 1800 AND seen_at_ts_bucket_start <= $end_timestamp ) SELECT timestamp, body FROM signoz_logs.distributed_logs_v2 WHERE resource_fingerprint GLOBAL IN (SELECT fingerprint FROM __resource_filter) AND timestamp >= $start_timestamp_nano AND timestamp <= $end_timestamp_nano AND ts_bucket_start >= $start_timestamp - 1800 AND ts_bucket_start <= $end_timestamp AND severity_text = 'ERROR' ORDER BY timestamp DESC LIMIT 100"
        },
        "type": "clickhouse_sql"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "raw",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 2. clickhouse_sql_traces_scalar — ClickHouse SQL: scalar aggregate with resource filter

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "name": "total_spans",
          "query": "WITH __resource_filter AS ( SELECT fingerprint FROM signoz_traces.distributed_traces_v3_resource WHERE seen_at_ts_bucket_start >= $start_timestamp - 1800 AND seen_at_ts_bucket_start <= $end_timestamp ) SELECT count() AS value FROM signoz_traces.distributed_signoz_index_v3 WHERE resource_fingerprint GLOBAL IN (SELECT fingerprint FROM __resource_filter) AND timestamp >= $start_datetime AND timestamp <= $end_datetime AND ts_bucket_start >= $start_timestamp - 1800 AND ts_bucket_start <= $end_timestamp"
        },
        "type": "clickhouse_sql"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "scalar",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 3. clickhouse_sql_traces_time_series — ClickHouse SQL: traces time series with resource filter

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "name": "span_rate",
          "query": "WITH __resource_filter AS ( SELECT fingerprint FROM signoz_traces.distributed_traces_v3_resource WHERE seen_at_ts_bucket_start >= $start_timestamp - 1800 AND seen_at_ts_bucket_start <= $end_timestamp ) SELECT toStartOfInterval(timestamp, INTERVAL 60 SECOND) AS ts, count() AS value FROM signoz_traces.distributed_signoz_index_v3 WHERE resource_fingerprint GLOBAL IN (SELECT fingerprint FROM __resource_filter) AND timestamp >= $start_datetime AND timestamp <= $end_datetime AND ts_bucket_start >= $start_timestamp - 1800 AND ts_bucket_start <= $end_timestamp GROUP BY ts ORDER BY ts"
        },
        "type": "clickhouse_sql"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 4. formula — Formula: error rate from two trace queries

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "expression": "countIf(has_error = true)"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "name": "A",
          "signal": "traces",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      },
      {
        "spec": {
          "aggregations": [
            {
              "expression": "count()"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "name": "B",
          "signal": "traces",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      },
      {
        "spec": {
          "expression": "A / B * 100",
          "name": "error_rate"
        },
        "type": "builder_formula"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 5. logs_raw — Raw: fetch raw log records

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "filter": {
            "expression": "severity_text = 'ERROR'"
          },
          "limit": 50,
          "name": "A",
          "offset": 0,
          "order": [
            {
              "direction": "desc",
              "key": {
                "fieldContext": "log",
                "name": "timestamp"
              }
            },
            {
              "direction": "desc",
              "key": {
                "name": "id"
              }
            }
          ],
          "selectFields": [
            {
              "fieldContext": "log",
              "name": "body"
            },
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "signal": "logs"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "raw",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 6. logs_scalar — Scalar: total error log count

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "error_count",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "severity_text = 'ERROR'"
          },
          "name": "A",
          "signal": "logs"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "scalar",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 7. logs_time_series — Time series: count error logs grouped by service

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "log_count",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "severity_text = 'ERROR'"
          },
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "limit": 10,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "name": "log_count"
              }
            }
          ],
          "signal": "logs",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 8. metrics_gauge_time_series — Time series: latest gauge value averaged across series

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "system.cpu.utilization",
              "spaceAggregation": "avg",
              "timeAggregation": "latest"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "host.name"
            }
          ],
          "name": "A",
          "signal": "metrics",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 9. metrics_histogram_time_series — Time series: p99 latency from histogram

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "http.server.duration.bucket",
              "spaceAggregation": "p99"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "name": "A",
          "signal": "metrics",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 10. metrics_rate_time_series — Time series: rate of cumulative counter

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "http.server.duration.count",
              "spaceAggregation": "sum",
              "timeAggregation": "rate"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "name": "A",
          "signal": "metrics",
          "stepInterval": 120
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 11. metrics_scalar — Scalar: single reduced metric value

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "http.server.duration.count",
              "reduceTo": "sum",
              "spaceAggregation": "sum",
              "timeAggregation": "rate"
            }
          ],
          "name": "A",
          "signal": "metrics",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "scalar",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 12. promql — PromQL: request rate with UTF-8 metric name

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "name": "request_rate",
          "query": "sum(rate({\"http.server.duration.count\"}[5m])) by (\"service.name\")",
          "step": 60
        },
        "type": "promql"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 13. traces_raw — Raw: fetch raw span records

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "filter": {
            "expression": "service.name = 'frontend' AND has_error = true"
          },
          "limit": 100,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "fieldContext": "span",
                "name": "timestamp"
              }
            }
          ],
          "selectFields": [
            {
              "fieldContext": "span",
              "name": "name"
            },
            {
              "fieldContext": "span",
              "name": "duration_nano"
            }
          ],
          "signal": "traces"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "raw",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 14. traces_scalar — Scalar: total span count

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "span_count",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "service.name = 'frontend'"
          },
          "name": "A",
          "signal": "traces"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "scalar",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 15. traces_time_series — Time series: count spans grouped by service

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "span_count",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "service.name = 'frontend'"
          },
          "groupBy": [
            {
              "fieldContext": "resource",
              "name": "service.name"
            }
          ],
          "limit": 10,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "name": "span_count"
              }
            }
          ],
          "signal": "traces",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```
