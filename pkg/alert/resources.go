package alert

// Instructions is the MCP resource content for signoz://alert/instructions.
const Instructions = `# SigNoz Alert Rule — Instructions

## Overview
An alert rule monitors a signal (metrics, logs, traces, or exceptions) and fires when a condition is met.
The alert is created via POST /api/v1/rules. All alerts use v2alpha1 schema with structured thresholds and evaluation.

## CRITICAL: Before Creating an Alert
1. ALWAYS read signoz://alert/examples for complete working payloads
2. Use signoz_get_alert on an existing alert to study the exact structure your SigNoz instance expects
3. Use signoz_get_field_keys to discover available attributes for filters and groupBy
4. NOTIFICATION CHANNELS: If the user explicitly names a channel, use it directly. Otherwise, do NOT guess channel names — call signoz_create_alert without channels first, it returns available channels. Present the list to the user, let them choose, then retry with their selection. If no suitable channel exists, use signoz_create_notification_channel to create one first

## Alert Types (alertType)
| Value | Signal | Use When |
|-------|--------|----------|
| METRIC_BASED_ALERT | metrics | Monitoring numeric metrics (CPU, memory, request rate, latency) |
| LOGS_BASED_ALERT | logs | Monitoring log patterns, error counts, log volume |
| TRACES_BASED_ALERT | traces | Monitoring span latency, error rates, throughput |
| EXCEPTIONS_BASED_ALERT | exceptions | Monitoring exception counts (typically uses clickhouse_sql) |

## Rule Types (ruleType)
| Value | Description | Constraints |
|-------|-------------|-------------|
| threshold_rule | Compare metric against a static threshold | Works with all alert types |
| promql_rule | Evaluate a PromQL expression | queryType must be "promql" |
| anomaly_rule | Detect anomalies using seasonal decomposition | Only with METRIC_BASED_ALERT |

## Composite Query Structure

### Query Format
The condition.compositeQuery uses the v5 query format:
- queryType: "builder" | "promql" | "clickhouse_sql"
- panelType: always "graph" for alerts (auto-set)
- queries: array of query objects

### Builder Query Format
Each query in the queries array has:
- type: "builder_query" (for data queries) or "builder_formula" (for formulas like A/B*100)
- spec: query specification

For builder_query spec:
- name: query identifier (A, B, C...)
- signal: "metrics" | "logs" | "traces" (must match alertType)
- aggregations: [{expression: "count()"}, {expression: "avg(duration)"}]
- filter: {expression: "service.name = 'frontend' AND http.status_code >= 500"}
- groupBy: [{name: "service.name", fieldContext: "resource", fieldDataType: "string"}]
- stepInterval: interval in seconds (use 60 for most alerts)

For promql/clickhouse_sql queries:
- name: query identifier
- query: the PromQL or SQL string
- legend: legend template
- disabled: false

### Aggregation Expressions
Common expressions for the aggregations field:
- count() — count of items
- avg(fieldName) — average of a field
- sum(fieldName) — sum of a field
- min(fieldName) / max(fieldName)
- p50(fieldName) / p75(fieldName) / p90(fieldName) / p95(fieldName) / p99(fieldName)
- count_distinct(fieldName) — count of unique values
- rate(fieldName) — rate of change

### Filter Expressions
The filter.expression field uses SigNoz query syntax:
- Equality: service.name = 'frontend'
- Comparison: http.status_code >= 500
- Pattern: body CONTAINS 'timeout'
- Case-insensitive: body ILIKE '%error%'
- Boolean: service.name = 'frontend' AND http.status_code >= 500
- IN: severity_text IN ('ERROR', 'WARN', 'FATAL')
- EXISTS: trace_id EXISTS

## Formulas (builder_formula)

Formulas combine multiple queries using math expressions. Add a builder_formula entry in the queries array:

` + "```" + `json
{
  "type": "builder_formula",
  "spec": {
    "name": "F1",
    "expression": "A * 100 / B"
  }
}
` + "```" + `

- name: formula identifier (F1, F2, etc.)
- expression: math expression referencing other query names (A, B, C). Supports +, -, *, /, and functions like abs(), sqrt(), log(), exp()
- Set selectedQueryName to the formula name (e.g. "F1") so the alert triggers on the formula result

## Units

Set compositeQuery.unit to specify the unit of the queried data (Y-axis). This is used for:
- Value formatting in alert messages ({{$value}})
- Unit conversion when targetUnit on a threshold differs from the query unit

Common units: percent, ms, s, ns, bytes, kbytes, mbytes, gbytes, reqps, ops, cps

When compositeQuery.unit and threshold targetUnit differ, SigNoz auto-converts during evaluation (e.g. query returns bytes but threshold is in gbytes).

## Threshold Configuration

Use condition.thresholds to define alert thresholds. Each threshold level can route to different channels:

` + "```" + `json
"thresholds": {
  "kind": "basic",
  "spec": [
    {
      "name": "critical",
      "target": 1000,
      "targetUnit": "",
      "recoveryTarget": null,
      "matchType": "1",
      "op": "1",
      "channels": ["pagerduty-oncall", "slack-alerts"]
    },
    {
      "name": "warning",
      "target": 100,
      "targetUnit": "",
      "recoveryTarget": null,
      "matchType": "1",
      "op": "1",
      "channels": ["slack-alerts"]
    }
  ]
}
` + "```" + `

### Threshold Fields
- name: severity level (critical, warning, or info)
- target: numeric threshold value
- targetUnit: unit of the target value (e.g. ms, percent, bytes). Auto-converted to compositeQuery.unit during evaluation
- recoveryTarget: value for recovery (null if not needed)
- matchType: 1=at_least_once, 2=all_the_times, 3=on_average, 4=in_total, 5=last
- op: 1=above, 2=below, 3=equal, 4=not_equal
- channels: notification channel names for this threshold level (use signoz_list_notification_channels to discover available names)

## Evaluation Configuration

Use the evaluation field to control how often the alert is checked:

` + "```" + `json
"evaluation": {
  "kind": "rolling",
  "spec": {
    "evalWindow": "5m0s",
    "frequency": "1m0s"
  }
}
` + "```" + `

- evalWindow: how long the condition must persist (5m0s, 10m0s, 15m0s, 1h0m0s, 4h0m0s, 24h0m0s)
- frequency: how often to evaluate (1m0s, 5m0s)
- Auto-generated with defaults (5m0s window, 1m0s frequency) if omitted

## Notification Settings

Controls grouping and re-notification:

` + "```" + `json
"notificationSettings": {
  "groupBy": ["service.name"],
  "renotify": {
    "enabled": true,
    "interval": "4h0m0s",
    "alertStates": ["firing", "nodata"]
  },
  "usePolicy": true
}
` + "```" + `

- groupBy: fields to group notifications by (reduces noise)
- renotify: re-send alerts at interval for specified states
- usePolicy: enable routing policy matching based on labels

## Labels & Routing
- labels.severity: MUST be set (info, warning, or critical) — auto-defaults to warning
- Additional labels like team, service, environment enable routing policies
- preferredChannels: fallback notification channel names (thresholds.channels takes priority). Use signoz_list_notification_channels to discover available names
- Set usePolicy: true in notificationSettings to enable label-based routing

## Annotations
- Use {{$value}} for the current metric value
- Use {{$threshold}} for the threshold value
- Use {{$labels.key}} for label values
- Common annotations: description, summary, runbook

## Anomaly Alerts (ruleType: anomaly_rule)
- Must use alertType: METRIC_BASED_ALERT
- Set condition.algorithm: "zscore"
- Set condition.seasonality: "hourly" | "daily" | "weekly"
- target in thresholds is the Z-score threshold (e.g., 3 for 3 standard deviations)
- op should be "1" (above) for standard anomaly detection

## Auto-Applied Defaults
- version → "v5"
- schemaVersion → "v2alpha1"
- evaluation → {kind: "rolling", spec: {evalWindow: "5m0s", frequency: "1m0s"}}
- notificationSettings → {renotify: {enabled: false, interval: "30m"}}
- panelType → "graph"
- selectedQueryName → first query name
- source → "mcp"
- labels.severity → "warning" (if not set)
- annotations → default description and summary templates
`

// Examples is the MCP resource content for signoz://alert/examples.
const Examples = `# SigNoz Alert Rule — Examples

All examples use v2alpha1 schema. Fields like schemaVersion, evaluation, and notificationSettings
are auto-generated if omitted, but shown here for clarity.

## Example 1: Metrics Threshold Alert
Alert when average CPU usage exceeds 80%.

` + "```" + `json
{
  "alert": "High CPU Usage",
  "alertType": "METRIC_BASED_ALERT",
  "ruleType": "threshold_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60,
            "aggregations": [
              {
                "metricName": "system.cpu.utilization",
                "timeAggregation": "avg",
                "spaceAggregation": "avg"
              }
            ],
            "filter": {"expression": ""},
            "groupBy": [
              {"name": "host.name", "fieldContext": "resource", "fieldDataType": "string"}
            ],
            "having": {"expression": ""}
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "critical",
          "target": 90,
          "targetUnit": "percent",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1",
          "channels": ["pagerduty-infra", "slack-infra"]
        },
        {
          "name": "warning",
          "target": 80,
          "targetUnit": "percent",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1",
          "channels": ["slack-infra"]
        }
      ]
    }
  },
  "labels": {
    "severity": "critical",
    "team": "infrastructure"
  },
  "annotations": {
    "description": "CPU usage is {{$value}}% on host {{$labels.host_name}}, exceeding threshold of {{$threshold}}%",
    "summary": "High CPU usage detected"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  },
  "notificationSettings": {
    "groupBy": ["host.name"],
    "renotify": {
      "enabled": true,
      "interval": "4h0m0s",
      "alertStates": ["firing"]
    }
  }
}
` + "```" + `

## Example 2: Logs Alert with Multi-Threshold Routing
Alert on high error log volume with different thresholds routing to different channels.

` + "```" + `json
{
  "alert": "High Error Log Volume",
  "alertType": "LOGS_BASED_ALERT",
  "ruleType": "threshold_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "logs",
            "aggregations": [
              {"expression": "count()"}
            ],
            "filter": {"expression": "severity_text IN ('ERROR', 'FATAL')"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"}
            ],
            "having": {"expression": ""}
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "critical",
          "target": 1000,
          "targetUnit": "",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1",
          "channels": ["pagerduty-oncall", "slack-alerts"]
        },
        {
          "name": "warning",
          "target": 100,
          "targetUnit": "",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1",
          "channels": ["slack-alerts"]
        }
      ]
    }
  },
  "labels": {
    "severity": "critical",
    "team": "backend"
  },
  "annotations": {
    "description": "Error log count for {{$labels.service_name}} is {{$value}}, threshold: {{$threshold}}",
    "summary": "High error log volume"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  },
  "notificationSettings": {
    "groupBy": ["service.name"],
    "renotify": {
      "enabled": true,
      "interval": "4h0m0s",
      "alertStates": ["firing"]
    },
    "usePolicy": true
  }
}
` + "```" + `

## Example 3: Traces Alert (Latency Monitoring)
Alert when p99 latency exceeds 2 seconds for any service.

` + "```" + `json
{
  "alert": "High P99 Latency",
  "alertType": "TRACES_BASED_ALERT",
  "ruleType": "threshold_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "traces",
            "stepInterval": 60,
            "aggregations": [
              {"expression": "p99(durationNano)"}
            ],
            "filter": {"expression": ""},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "name", "fieldDataType": "string"}
            ],
            "having": {"expression": ""}
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "warning",
          "target": 2000000000,
          "targetUnit": "ns",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1"
        }
      ]
    }
  },
  "labels": {
    "severity": "warning"
  },
  "annotations": {
    "description": "P99 latency for {{$labels.service_name}} {{$labels.name}} is {{$value}}ns (threshold: {{$threshold}}ns)",
    "summary": "High latency detected"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  }
}
` + "```" + `

## Example 4: PromQL Alert
Alert using a PromQL expression for request error rate.

` + "```" + `json
{
  "alert": "High Error Rate",
  "alertType": "METRIC_BASED_ALERT",
  "ruleType": "promql_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "promql",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "query": "sum(rate(signoz_calls_total{status_code='STATUS_CODE_ERROR'}[5m])) / sum(rate(signoz_calls_total[5m])) * 100",
            "legend": "",
            "disabled": false
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "critical",
          "target": 5,
          "targetUnit": "percent",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1"
        }
      ]
    }
  },
  "labels": {
    "severity": "critical"
  },
  "annotations": {
    "description": "Error rate is {{$value}}% (threshold: {{$threshold}}%)",
    "summary": "High error rate detected"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  }
}
` + "```" + `

## Example 5: ClickHouse SQL Alert (Exceptions)
Alert on exception count using ClickHouse SQL.

` + "```" + `json
{
  "alert": "High Exception Count",
  "alertType": "EXCEPTIONS_BASED_ALERT",
  "ruleType": "threshold_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "clickhouse_sql",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "query": "SELECT toStartOfInterval(timestamp, INTERVAL 1 MINUTE) AS ts, count() AS value FROM signoz_traces.distributed_signoz_error_index_v2 WHERE timestamp >= now() - INTERVAL 5 MINUTE AND exceptionType != '' GROUP BY ts ORDER BY ts",
            "legend": "",
            "disabled": false
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "warning",
          "target": 50,
          "targetUnit": "",
          "recoveryTarget": null,
          "matchType": "4",
          "op": "1"
        }
      ]
    }
  },
  "labels": {
    "severity": "warning"
  },
  "annotations": {
    "description": "Exception count is {{$value}} (threshold: {{$threshold}})",
    "summary": "High exception count"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  }
}
` + "```" + `

## Example 6: Anomaly Detection Alert
Detect anomalous metric behavior using seasonal decomposition.

` + "```" + `json
{
  "alert": "Anomalous Request Rate",
  "alertType": "METRIC_BASED_ALERT",
  "ruleType": "anomaly_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60,
            "aggregations": [
              {
                "metricName": "signoz_calls_total",
                "timeAggregation": "rate",
                "spaceAggregation": "sum"
              }
            ],
            "filter": {"expression": ""},
            "groupBy": [],
            "having": {"expression": ""}
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "algorithm": "zscore",
    "seasonality": "daily",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "warning",
          "target": 3,
          "targetUnit": "",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1"
        }
      ]
    }
  },
  "labels": {
    "severity": "warning"
  },
  "annotations": {
    "description": "Anomalous request rate detected: Z-score is {{$value}} (threshold: {{$threshold}})",
    "summary": "Request rate anomaly"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  }
}
` + "```" + `

## Example 7: Logs Alert with Formula (Error Rate %)
Use two queries and a formula to calculate error rate percentage.

` + "```" + `json
{
  "alert": "Log Error Rate High",
  "alertType": "LOGS_BASED_ALERT",
  "ruleType": "threshold_rule",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percent",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "logs",
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "severity_text IN ('ERROR', 'FATAL')"},
            "groupBy": [],
            "having": {"expression": ""}
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B",
            "signal": "logs",
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": ""},
            "groupBy": [],
            "having": {"expression": ""}
          }
        },
        {
          "type": "builder_formula",
          "spec": {
            "name": "F1",
            "expression": "A * 100 / B"
          }
        }
      ]
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {
          "name": "warning",
          "target": 10,
          "targetUnit": "percent",
          "recoveryTarget": null,
          "matchType": "1",
          "op": "1"
        }
      ]
    }
  },
  "labels": {
    "severity": "warning"
  },
  "annotations": {
    "description": "Log error rate is {{$value}}% (threshold: {{$threshold}}%)",
    "summary": "High log error rate"
  },
  "evaluation": {
    "kind": "rolling",
    "spec": {
      "evalWindow": "5m0s",
      "frequency": "1m0s"
    }
  }
}
` + "```" + `

## Key Notes
1. For metrics alerts, aggregations use the object format with metricName, timeAggregation, spaceAggregation
2. For logs/traces alerts, aggregations use the expression format: {expression: "count()"} or {expression: "avg(duration)"}
3. The selectedQueryName should reference the query or formula that determines the alert condition
4. Use signoz_get_alert to inspect existing alerts for the exact format your SigNoz version expects
5. Channel names in thresholds.spec[].channels must match exactly the names from signoz_list_notification_channels
6. schemaVersion, evaluation, and notificationSettings are auto-generated if omitted
`
