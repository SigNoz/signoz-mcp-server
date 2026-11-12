# Query range request examples

Source: the request examples published by the SigNoz OpenAPI spec.

## Run a query with signoz_execute_builder_query

### 1. ai_traces_raw — Raw: LLM, tool, and agent spans of one service's AI traces with many tool calls

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "filter": {
            "expression": "service.name = 'ai-agent' AND trace.tool_call_count > 10"
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
          "signal": "traces"
        },
        "type": "builder_ai_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "raw",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 2. ai_traces_raw_select_fields — Raw: the same spans with the span name and gen_ai model, tool, and token fields selected

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "filter": {
            "expression": "service.name = 'ai-agent' AND trace.tool_call_count > 10"
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
              "fieldContext": "attribute",
              "name": "gen_ai.request.model"
            },
            {
              "fieldContext": "attribute",
              "name": "gen_ai.tool.name"
            },
            {
              "fieldContext": "attribute",
              "name": "gen_ai.usage.input_tokens"
            },
            {
              "fieldContext": "attribute",
              "name": "gen_ai.usage.output_tokens"
            }
          ],
          "signal": "traces"
        },
        "type": "builder_ai_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "raw",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 3. ai_traces_scalar — Scalar: average per-trace AI cost by model

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "expression": "avg(trace.estimated_total_cost)"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "attribute",
              "name": "gen_ai.request.model"
            }
          ],
          "limit": 10,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "name": "avg(trace.estimated_total_cost)"
              }
            }
          ],
          "signal": "traces"
        },
        "type": "builder_ai_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "scalar",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 4. ai_traces_time_series — Time series: per-trace AI cost by model

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "expression": "sum(trace.estimated_total_cost)"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "attribute",
              "name": "gen_ai.request.model"
            }
          ],
          "limit": 10,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "name": "sum(trace.estimated_total_cost)"
              }
            }
          ],
          "signal": "traces",
          "stepInterval": "60s"
        },
        "type": "builder_ai_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 5. ai_traces_trace — Trace list: AI traces with many tool calls, most expensive first

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "filter": {
            "expression": "trace.tool_call_count > 10 AND trace.distinct_tool_count < 3"
          },
          "limit": 10,
          "name": "A",
          "order": [
            {
              "direction": "desc",
              "key": {
                "name": "trace.estimated_total_cost"
              }
            }
          ],
          "signal": "traces"
        },
        "type": "builder_ai_query"
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "trace",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 6. clickhouse_sql_logs_raw — ClickHouse SQL: raw logs with resource filter

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

### 7. clickhouse_sql_traces_scalar — ClickHouse SQL: scalar aggregate with resource filter

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

### 8. clickhouse_sql_traces_time_series — ClickHouse SQL: traces time series with resource filter

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

### 9. formula — Formula: error rate from two trace queries

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

### 10. function_anomaly_request_rate — anomaly: request rate scored against its daily pattern

anomaly is the one function with named arguments. seasonality says which past periods predict the current value: hourly, daily or weekly, daily when absent. z_score_threshold is how many standard deviations from the prediction count as an anomaly. It has no default: a request without it gets bounds of zero width, so send it. 3 is the value the UI sends. The response carries, next to the values, a predicted series, an upper and a lower bound series and an anomaly score series. The scoring runs in the enterprise build; the community build returns the series unchanged.

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
          "functions": [
            {
              "args": [
                {
                  "name": "seasonality",
                  "value": "daily"
                },
                {
                  "name": "z_score_threshold",
                  "value": 3
                }
              ],
              "name": "anomaly"
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
          "stepInterval": "300s"
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

### 11. function_cumulativesum_errors_so_far — cumulativeSum: 5xx responses since the start of the window

cumulativeSum replaces each point with the sum of every point up to and including it, so a per-step count becomes a running total whose first point is the first count. It takes no arguments. The total resets with the window, so a longer time range gives a larger final value; it is a view of the window, not a counter.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "server_errors",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "http.response.status_code >= 500"
          },
          "functions": [
            {
              "name": "cumulativeSum"
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
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 12. function_cutoffmin_hide_idle_hosts — cutOffMin: drop hosts below 5% CPU from the chart

The four threshold functions take one number. cutOffMin and cutOffMax remove the points below or above the threshold, so an idle host disappears from the chart instead of drawing a line along the bottom. clampMin and clampMax keep the points and replace the value with the threshold, which caps spikes without hiding them. system.cpu.utilization is a ratio, so 0.05 is 5%.

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
              "timeAggregation": "avg"
            }
          ],
          "functions": [
            {
              "args": [
                {
                  "value": 0.05
                }
              ],
              "name": "cutOffMin"
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

### 13. function_ewma_smoothed_latency — ewma5: smoothed p95 latency

ewma3, ewma5 and ewma7 replace each point with an exponentially weighted average of the point and the points before it. The number in the name is the window the default weight is derived from: alpha = 2 / (n + 1), so 0.5, 0.333 and 0.25. One optional argument overrides alpha; a value close to 1 follows the raw series, a value close to 0 smooths harder. 0.3 here smooths a little more than ewma5's default.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "http.server.duration.bucket",
              "spaceAggregation": "p95"
            }
          ],
          "functions": [
            {
              "args": [
                {
                  "value": 0.3
                }
              ],
              "name": "ewma5"
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

### 14. function_fillzero_sparse_errors — fillZero: a zero at every step with no failed spans

A count over sparse events returns a point only for steps that had a match, so a chart of failed spans has gaps where nothing failed. fillZero adds a zero at every missing step. With no arguments the server fills the request window at the query's step, which is the common case. The three optional arguments are start, end and step in milliseconds, for callers that want to fill a different grid.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "failed_spans",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "has_error = true"
          },
          "functions": [
            {
              "name": "fillZero"
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
      }
    ]
  },
  "end": 1640998800000,
  "requestType": "time_series",
  "schemaVersion": "v1",
  "start": 1640995200000
}
```

### 15. function_log10_network_bytes — log10: network throughput across devices that differ by orders of magnitude

log2 and log10 replace each value with its logarithm, so a loopback device moving kilobytes and an uplink moving gigabytes fit on one chart. They take no arguments. Zero and negative values have no logarithm and are not drawn. absolute, also without arguments, is the usual companion for a signed series before a logarithm.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "system.network.io",
              "spaceAggregation": "sum",
              "timeAggregation": "rate"
            }
          ],
          "filter": {
            "expression": "direction = 'transmit'"
          },
          "functions": [
            {
              "name": "log10"
            }
          ],
          "groupBy": [
            {
              "fieldContext": "attribute",
              "name": "device"
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

### 16. function_median_despiked_request_rate — median5: request rate without one-step spikes

median3, median5 and median7 replace each point with the median of the surrounding 3, 5 or 7 points. A spike that lasts one step vanishes, a shift that lasts longer than half the window stays. The first and last half window, 2 points each for median5, have no full window and keep their values, so the newest spike stays visible; a series shorter than the window is returned unchanged. They take no arguments. Use them when the raw series is noisy and the chart is for a trend, not for catching the spike.

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
          "functions": [
            {
              "name": "median5"
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

### 17. function_runningdiff_disk_growth — runningDiff then clampMin: bytes written to disk per step

runningDiff turns a gauge into the change between consecutive points, so a filesystem usage gauge becomes bytes written per step. It takes no arguments and drops the first point of the window, since that point has nothing to difference against; the server reads one step before the window to keep the first interval. Functions apply in order: clampMin with 0 runs on the differences and replaces the negative value a cleanup or a volume reset produces with zero.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "metricName": "system.filesystem.usage",
              "spaceAggregation": "sum",
              "timeAggregation": "latest"
            }
          ],
          "filter": {
            "expression": "state = 'used'"
          },
          "functions": [
            {
              "name": "runningDiff"
            },
            {
              "args": [
                {
                  "value": 0
                }
              ],
              "name": "clampMin"
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

### 18. function_timeshift_week_over_week — timeShift: this week's error logs next to last week's

timeShift moves every point of its query forward by the argument, in seconds, so a series read from the past lands on the current timestamps. B here reads the hour exactly seven days (604800 s) before the requested window and comes back on this week's timestamps, so it overlays A on a chart and the formula can subtract it point by point. The argument is positive; it says how far back to read.

```json
{
  "compositeQuery": {
    "queries": [
      {
        "spec": {
          "aggregations": [
            {
              "alias": "errors",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "severity_text = 'ERROR'"
          },
          "name": "A",
          "signal": "logs",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      },
      {
        "spec": {
          "aggregations": [
            {
              "alias": "errors",
              "expression": "count()"
            }
          ],
          "filter": {
            "expression": "severity_text = 'ERROR'"
          },
          "functions": [
            {
              "args": [
                {
                  "value": 604800
                }
              ],
              "name": "timeShift"
            }
          ],
          "name": "B",
          "signal": "logs",
          "stepInterval": "60s"
        },
        "type": "builder_query"
      },
      {
        "spec": {
          "expression": "A - B",
          "name": "F1"
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

### 19. logs_raw — Raw: fetch raw log records

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

### 20. logs_scalar — Scalar: total error log count

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

### 21. logs_time_series — Time series: count error logs grouped by service

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

### 22. metrics_gauge_time_series — Time series: latest gauge value averaged across series

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

### 23. metrics_histogram_time_series — Time series: p99 latency from histogram

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

### 24. metrics_rate_time_series — Time series: rate of cumulative counter

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

### 25. metrics_scalar — Scalar: single reduced metric value

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

### 26. promql — PromQL: request rate with UTF-8 metric name

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

### 27. traces_raw — Raw: fetch raw span records

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

### 28. traces_scalar — Scalar: total span count

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

### 29. traces_time_series — Time series: count spans grouped by service

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
