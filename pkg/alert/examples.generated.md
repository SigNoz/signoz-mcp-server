# Alert rule request examples

Source: request examples published by the SigNoz OpenAPI document at 09bba8960dfc3e0a43dc54d6d7cf203aae9841f4.

## Create a rule with signoz_create_alert (or replace one with signoz_update_alert)

### 1. logs_error_rate_formula — Logs error rate error count / total count × 100

Two disabled log count queries (A = errors, B = total) combined via a builder_formula into a percentage. Classic service-level error-rate alert pattern for log-based signals.

```json
{
  "alert": "Payments-api error log rate above 1%",
  "alertType": "LOGS_BASED_ALERT",
  "annotations": {
    "description": "Error log rate in {{$deployment.environment}} is {{$value}}%",
    "summary": "Payments-api error rate above {{$threshold}}%"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "expression": "count()"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "service.name = 'payments-api' AND severity_text IN ['ERROR', 'FATAL']"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "name": "A",
            "signal": "logs",
            "stepInterval": 60
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
            "disabled": true,
            "filter": {
              "expression": "service.name = 'payments-api'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "name": "B",
            "signal": "logs",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "expression": "(A / B) * 100",
            "legend": "{{deployment.environment}}",
            "name": "F1"
          },
          "type": "builder_formula"
        }
      ],
      "queryType": "builder",
      "unit": "percent"
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-payments"
          ],
          "matchType": "at_least_once",
          "name": "critical",
          "op": "above",
          "target": 1
        }
      ]
    }
  },
  "description": "Error log ratio as a percentage of total logs for payments-api",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "critical",
    "team": "payments"
  },
  "notificationSettings": {
    "groupBy": [
      "deployment.environment"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "30m"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 2. logs_threshold — Logs threshold count() over filter

Counts matching log records (ERROR severity + body contains) over a rolling window. Fires at least once per evaluation when the count exceeds zero.

```json
{
  "alert": "Payments service panic logs",
  "alertType": "LOGS_BASED_ALERT",
  "annotations": {
    "description": "{{$k8s.pod.name}} emitted {{$value}} panic log(s) in {{$deployment.environment}}.",
    "summary": "Payments service panic"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "expression": "count()"
              }
            ],
            "filter": {
              "expression": "service.name = 'payments-api' AND severity_text = 'ERROR' AND body CONTAINS 'panic'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.pod.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "legend": "{{k8s.pod.name}} ({{deployment.environment}})",
            "name": "A",
            "signal": "logs",
            "stepInterval": 60
          },
          "type": "builder_query"
        }
      ],
      "queryType": "builder"
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-payments",
            "pagerduty-payments"
          ],
          "matchType": "at_least_once",
          "name": "critical",
          "op": "above",
          "target": 0
        }
      ]
    }
  },
  "description": "Any panic log line emitted by the payments service",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "critical",
    "team": "payments"
  },
  "notificationSettings": {
    "groupBy": [
      "k8s.pod.name",
      "deployment.environment"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "15m"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 3. metric_anomaly — Metric anomaly rule (v1 only)

Anomaly rules are not yet supported under schemaVersion v2alpha1, so this example uses the v1 shape. Wraps a builder query in the `anomaly` function with daily seasonality SigNoz compares each point against the forecast for that time of day. Fires when the anomaly score stays below the threshold for the entire window; `requireMinPoints` guards against noisy intervals.

```json
{
  "alert": "Anomalous drop in ingested spans",
  "alertType": "METRIC_BASED_ALERT",
  "annotations": {
    "description": "Ingestion rate for tenant {{$tenant_id}} is anomalously low (z-score {{$value}}).",
    "summary": "Span ingestion anomaly"
  },
  "condition": {
    "algorithm": "standard",
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "otelcol_receiver_accepted_spans",
                "spaceAggregation": "sum",
                "timeAggregation": "rate"
              }
            ],
            "filter": {
              "expression": "tenant_tier = 'premium'"
            },
            "functions": [
              {
                "args": [
                  {
                    "name": "z_score_threshold",
                    "value": 2
                  }
                ],
                "name": "anomaly"
              }
            ],
            "groupBy": [
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "tenant_id"
              }
            ],
            "legend": "{{tenant_id}}",
            "name": "A",
            "signal": "metrics",
            "stepInterval": 21600
          },
          "type": "builder_query"
        }
      ],
      "queryType": "builder"
    },
    "matchType": "all_the_times",
    "op": "below",
    "requireMinPoints": true,
    "requiredNumPoints": 3,
    "seasonality": "daily",
    "selectedQueryName": "A",
    "target": 2
  },
  "description": "Detect an abrupt drop in span ingestion using a z-score anomaly function",
  "evalWindow": "24h",
  "frequency": "3h",
  "labels": {
    "severity": "warning"
  },
  "preferredChannels": [
    "slack-ingestion"
  ],
  "ruleType": "anomaly_rule",
  "version": "v5"
}
```

### 4. metric_promql — Metric threshold PromQL rule

PromQL expression instead of the builder. Dotted OTEL resource attributes are quoted ("deployment.environment"). Useful for queries that combine series with group_right or other Prom operators.

```json
{
  "alert": "Kafka consumer group lag above 1000",
  "alertType": "METRIC_BASED_ALERT",
  "annotations": {
    "description": "Consumer group {{$group}} is {{$value}} messages behind on {{$topic}}/{{$partition}}.",
    "summary": "Kafka consumer lag high"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "legend": "{{topic}}/{{partition}} ({{group}})",
            "name": "A",
            "query": "(max by(topic, partition, \"deployment.environment\")(kafka_log_end_offset) - on(topic, partition, \"deployment.environment\") group_right max by(group, topic, partition, \"deployment.environment\")(kafka_consumer_committed_offset)) > 0"
          },
          "type": "promql"
        }
      ],
      "queryType": "promql"
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-data-platform",
            "pagerduty-data"
          ],
          "matchType": "all_the_times",
          "name": "critical",
          "op": "above",
          "target": 1000
        }
      ]
    }
  },
  "description": "Consumer group lag computed via PromQL",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "10m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "critical"
  },
  "notificationSettings": {
    "groupBy": [
      "group",
      "topic"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "1h"
    }
  },
  "ruleType": "promql_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 5. metric_threshold_formula — Metric threshold multi-query formula

Computes disk utilization as (1 - available/capacity) * 100 by combining two disabled base queries with a builder_formula. The formula emits 0–100, so compositeQuery.unit is set to "percent" and the target is a bare number.

```json
{
  "alert": "PersistentVolume above 80% utilization",
  "alertType": "METRIC_BASED_ALERT",
  "annotations": {
    "description": "Volume {{$k8s.persistentvolumeclaim.name}} in {{$k8s.namespace.name}} is {{$value}}% full.",
    "summary": "Disk utilization above {{$threshold}}%"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "k8s.volume.available",
                "spaceAggregation": "max",
                "timeAggregation": "max"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "k8s.volume.type = 'persistentVolumeClaim'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.persistentvolumeclaim.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.namespace.name"
              }
            ],
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "k8s.volume.capacity",
                "spaceAggregation": "max",
                "timeAggregation": "max"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "k8s.volume.type = 'persistentVolumeClaim'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.persistentvolumeclaim.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.namespace.name"
              }
            ],
            "name": "B",
            "signal": "metrics",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "expression": "(1 - A/B) * 100",
            "legend": "{{k8s.persistentvolumeclaim.name}} ({{k8s.namespace.name}})",
            "name": "F1"
          },
          "type": "builder_formula"
        }
      ],
      "queryType": "builder",
      "unit": "percent"
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-storage"
          ],
          "matchType": "at_least_once",
          "name": "critical",
          "op": "above",
          "target": 80
        }
      ]
    }
  },
  "description": "Disk utilization for a persistent volume is above 80%",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "30m",
      "frequency": "5m"
    }
  },
  "labels": {
    "severity": "critical"
  },
  "notificationSettings": {
    "groupBy": [
      "k8s.namespace.name",
      "k8s.persistentvolumeclaim.name"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "2h"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 6. metric_threshold_single — Metric threshold single builder query

Fires when a pod consumes more than 80% of its requested CPU for the whole evaluation window. Uses `k8s.pod.cpu_request_utilization`.

```json
{
  "alert": "Pod CPU above 80% of request",
  "alertType": "METRIC_BASED_ALERT",
  "annotations": {
    "description": "Pod {{$k8s.pod.name}} CPU is at {{$value}} of request in {{$deployment.environment}}.",
    "summary": "Pod CPU above {{$threshold}} of request"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "k8s.pod.cpu_request_utilization",
                "spaceAggregation": "max",
                "timeAggregation": "avg"
              }
            ],
            "filter": {
              "expression": "k8s.deployment.name = 'api-service'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "k8s.pod.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "legend": "{{k8s.pod.name}} ({{deployment.environment}})",
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60
          },
          "type": "builder_query"
        }
      ],
      "queryType": "builder",
      "unit": "percentunit"
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-platform",
            "pagerduty-oncall"
          ],
          "matchType": "all_the_times",
          "name": "critical",
          "op": "above",
          "target": 0.8
        }
      ]
    }
  },
  "description": "CPU usage for api-service pods exceeds 80% of the requested CPU",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "15m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "critical",
    "team": "platform"
  },
  "notificationSettings": {
    "groupBy": [
      "k8s.pod.name",
      "deployment.environment"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "4h"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 7. notification_settings — Full notification settings (grouping, nodata renotify, grace period)

Demonstrates the full notificationSettings surface: `groupBy` merges alerts across labels to cut noise, `newGroupEvalDelay` gives newly-appearing series a grace period before firing, `renotify` re-alerts every 30m while firing OR while the alert is in nodata (missing data is treated as actionable), and `usePolicy: false` means channels come from the threshold entries rather than global routing policies. Set `usePolicy: true` to skip per-threshold channels and route via the org-level notification policy instead.

```json
{
  "alert": "API 5xx error rate above 1%",
  "alertType": "TRACES_BASED_ALERT",
  "annotations": {
    "description": "{{$service.name}} 5xx rate in {{$deployment.environment}} is {{$value}}%.",
    "summary": "API service error rate elevated"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "expression": "count()"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "service.name CONTAINS 'api' AND http.status_code >= 500"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "service.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "name": "A",
            "signal": "traces",
            "stepInterval": 60
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
            "disabled": true,
            "filter": {
              "expression": "service.name CONTAINS 'api'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "service.name"
              },
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "deployment.environment"
              }
            ],
            "name": "B",
            "signal": "traces",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "expression": "(A / B) * 100",
            "legend": "{{service.name}} ({{deployment.environment}})",
            "name": "F1"
          },
          "type": "builder_formula"
        }
      ],
      "queryType": "builder",
      "unit": "percent"
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-api-alerts",
            "pagerduty-oncall"
          ],
          "matchType": "at_least_once",
          "name": "critical",
          "op": "above",
          "target": 1
        }
      ]
    }
  },
  "description": "Noise-controlled 5xx error rate alert with renotify on gaps",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "team": "platform"
  },
  "notificationSettings": {
    "groupBy": [
      "service.name",
      "deployment.environment"
    ],
    "newGroupEvalDelay": "2m",
    "renotify": {
      "alertStates": [
        "firing",
        "nodata"
      ],
      "enabled": true,
      "interval": "30m"
    },
    "usePolicy": false
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 8. tiered_thresholds — Tiered thresholds with per-tier channels

Two tiers (warning and critical) in a single rule, each with its own target, op, matchType, and channels so warnings and pages route to different receivers. `alertOnAbsent` + `absentFor` fires a no-data alert when the query returns no series for 15 consecutive evaluations.

```json
{
  "alert": "Kafka consumer lag warn / critical",
  "alertType": "METRIC_BASED_ALERT",
  "annotations": {
    "description": "Consumer lag for {{$topic}} partition {{$partition}} is {{$value}}.",
    "summary": "Kafka consumer lag"
  },
  "condition": {
    "absentFor": 15,
    "alertOnAbsent": true,
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "kafka_log_end_offset",
                "spaceAggregation": "max",
                "timeAggregation": "max"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "topic != '__consumer_offsets'"
            },
            "groupBy": [
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "topic"
              },
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "partition"
              }
            ],
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "aggregations": [
              {
                "metricName": "kafka_consumer_committed_offset",
                "spaceAggregation": "max",
                "timeAggregation": "max"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "topic != '__consumer_offsets'"
            },
            "groupBy": [
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "topic"
              },
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "partition"
              }
            ],
            "name": "B",
            "signal": "metrics",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "expression": "A - B",
            "legend": "{{topic}}/{{partition}}",
            "name": "F1"
          },
          "type": "builder_formula"
        }
      ],
      "queryType": "builder"
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-kafka-info"
          ],
          "matchType": "all_the_times",
          "name": "warning",
          "op": "above",
          "target": 50
        },
        {
          "channels": [
            "slack-kafka-alerts",
            "pagerduty-kafka"
          ],
          "matchType": "all_the_times",
          "name": "critical",
          "op": "above",
          "target": 200
        }
      ]
    }
  },
  "description": "Warn at lag ≥ 50 and page at ≥ 200, tiered via thresholds.spec.",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "team": "data-platform"
  },
  "notificationSettings": {
    "groupBy": [
      "topic"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "15m"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 9. traces_error_rate_formula — Traces error rate error spans / total spans × 100

Two disabled trace count queries (A = error spans where hasError=true, B = total spans) combined via a builder_formula into a percentage. Mirrors the common request-error-rate dashboard shape.

```json
{
  "alert": "Search-api error rate above 5%",
  "alertType": "TRACES_BASED_ALERT",
  "annotations": {
    "description": "Error rate on {{$service.name}} {{$http.route}} is {{$value}}%",
    "summary": "Search-api error rate above {{$threshold}}%"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "expression": "count()"
              }
            ],
            "disabled": true,
            "filter": {
              "expression": "service.name = 'search-api' AND hasError = true"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "service.name"
              },
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "http.route"
              }
            ],
            "name": "A",
            "signal": "traces",
            "stepInterval": 60
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
            "disabled": true,
            "filter": {
              "expression": "service.name = 'search-api'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "service.name"
              },
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "http.route"
              }
            ],
            "name": "B",
            "signal": "traces",
            "stepInterval": 60
          },
          "type": "builder_query"
        },
        {
          "spec": {
            "expression": "(A / B) * 100",
            "legend": "{{service.name}} {{http.route}}",
            "name": "F1"
          },
          "type": "builder_formula"
        }
      ],
      "queryType": "builder",
      "unit": "percent"
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-search",
            "pagerduty-search"
          ],
          "matchType": "at_least_once",
          "name": "critical",
          "op": "above",
          "target": 5
        }
      ]
    }
  },
  "description": "Request error rate for search-api, grouped by route",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "critical",
    "team": "search"
  },
  "notificationSettings": {
    "groupBy": [
      "service.name",
      "http.route"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "15m"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```

### 10. traces_threshold_latency — Traces threshold p99 latency (ns → s conversion)

Builder query against the traces signal with p99(duration_nano). The series unit is ns (compositeQuery.unit), the target is in seconds (threshold.targetUnit) SigNoz converts before comparing. Canonical shape when series and target live in different units.

```json
{
  "alert": "Search API p99 latency above 5s",
  "alertType": "TRACES_BASED_ALERT",
  "annotations": {
    "description": "p99 latency for {{$service.name}} on {{$http.route}} crossed {{$threshold}}s.",
    "summary": "Search-api latency degraded"
  },
  "condition": {
    "compositeQuery": {
      "panelType": "graph",
      "queries": [
        {
          "spec": {
            "aggregations": [
              {
                "expression": "p99(duration_nano)"
              }
            ],
            "filter": {
              "expression": "service.name = 'search-api' AND name = 'GET /api/v1/search'"
            },
            "groupBy": [
              {
                "fieldContext": "resource",
                "fieldDataType": "string",
                "name": "service.name"
              },
              {
                "fieldContext": "attribute",
                "fieldDataType": "string",
                "name": "http.route"
              }
            ],
            "legend": "{{service.name}} {{http.route}}",
            "name": "A",
            "signal": "traces",
            "stepInterval": 60
          },
          "type": "builder_query"
        }
      ],
      "queryType": "builder",
      "unit": "ns"
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "channels": [
            "slack-search"
          ],
          "matchType": "at_least_once",
          "name": "warning",
          "op": "above",
          "target": 5,
          "targetUnit": "s"
        }
      ]
    }
  },
  "description": "p99 duration of the search endpoint exceeds 5s",
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m",
      "frequency": "1m"
    }
  },
  "labels": {
    "severity": "warning",
    "team": "search"
  },
  "notificationSettings": {
    "groupBy": [
      "service.name",
      "http.route"
    ],
    "renotify": {
      "alertStates": [
        "firing"
      ],
      "enabled": true,
      "interval": "30m"
    }
  },
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "version": "v5"
}
```
