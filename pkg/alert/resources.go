package alert

// Instructions is the MCP resource content for signoz://alert/instructions.
const Instructions = `# SigNoz Alert Rule — Instructions

## Overview
An alert rule monitors a signal (metrics, logs, traces, or exceptions) and fires when a condition is met.
The alert is created via POST /api/v2/rules.

Schemas supported:
- **v2alpha1** for threshold_rule and promql_rule — structured thresholds + evaluation + notificationSettings. Applied automatically.
- **v1** for anomaly_rule — top-level evalWindow/frequency with condition.op/matchType/target/algorithm/seasonality. No thresholds block.

## CRITICAL: Before Creating an Alert
1. ALWAYS read signoz://alert/examples for complete working payloads (mirrors the ten canonical examples from SigNoz PR #11023).
2. Use signoz_get_alert on an existing alert to study the exact structure your SigNoz instance expects.
3. Use signoz_get_field_keys to discover available attributes for filters and groupBy.
4. NOTIFICATION CHANNELS: If the user explicitly names a channel, use it directly. Otherwise, do NOT guess channel names — call signoz_create_alert without channels first, it returns available channels. Present the list to the user, let them choose, then retry with their selection. If no suitable channel exists, use signoz_create_notification_channel to create one first.

## Alert Types (alertType)
| Value | Signal | Use When |
|-------|--------|----------|
| METRIC_BASED_ALERT | metrics | Monitoring numeric metrics (CPU, memory, request rate, latency) |
| LOGS_BASED_ALERT | logs | Monitoring log patterns, error counts, log volume |
| TRACES_BASED_ALERT | traces | Monitoring span latency, error rates, throughput |
| EXCEPTIONS_BASED_ALERT | exceptions | Monitoring exception counts (typically uses clickhouse_sql) |

## Rule Types (ruleType)
| Value | Schema | Description | Constraints |
|-------|--------|-------------|-------------|
| threshold_rule | v2alpha1 | Compare series against a static threshold | Works with all alert types |
| promql_rule | v2alpha1 | Evaluate a PromQL expression | queryType must be "promql" |
| anomaly_rule | **v1** | Detect anomalies via an anomaly function (e.g. z-score) | Only with METRIC_BASED_ALERT; omit thresholds, set top-level evalWindow/frequency, set condition.op/matchType/target/algorithm/seasonality |

## Composite Query Structure

condition.compositeQuery uses the v5 query format:
- queryType: "builder" | "promql" | "clickhouse_sql"
- panelType: always "graph" for alerts (auto-set)
- unit: series unit (used for value formatting and target-unit conversion)
- queries: array of query objects

### Query envelope type (queries[].type)
The envelope type must match compositeQuery.queryType:
| queryType | Accepted envelope types |
|-----------|-------------------------|
| builder | builder_query, builder_formula, builder_trace_operator |
| promql | promql (or builder_formula when composing with another promql query) |
| clickhouse_sql | clickhouse_sql (or builder_formula) |

### Builder query spec (builder_query)
- name: query identifier (A, B, C, …)
- signal: "metrics" | "logs" | "traces" (must match alertType)
- stepInterval: interval in seconds (60 for most alerts)
- aggregations: see "Aggregation shapes" below
- filter: {expression: "service.name = 'frontend' AND http.status_code >= 500"}
- groupBy: [{name, fieldContext: "resource" | "attribute", fieldDataType}]
- functions: post-query transforms. Required for anomaly_rule: [{name: "anomaly", args: [{name: "z_score_threshold", value: 2}]}]
- disabled: true when the query is used only as an input to a formula

### PromQL / ClickHouse query spec (envelope type=promql or clickhouse_sql)
- name: query identifier
- query: the PromQL or SQL string (required)
- legend: legend template
- disabled: false

### Formula query spec (envelope type=builder_formula)
- name: formula identifier (F1, F2, …)
- expression: math expression referencing other query names (e.g. "(A / B) * 100"). Supports +, -, *, /, and functions like abs(), sqrt(), log(), exp()
- legend: legend template
- Set selectedQueryName to the formula name (e.g. "F1") so the alert triggers on the formula result

## Aggregation shapes

### Metrics signal — object shape
Use this shape when spec.signal = "metrics":

` + "```" + `json
"aggregations": [
  {"metricName": "k8s.pod.cpu_request_utilization", "timeAggregation": "avg", "spaceAggregation": "max"}
]
` + "```" + `

- metricName: the metric you're querying.
- timeAggregation: per-series time aggregation. Common: avg, max, min, sum, rate, increase, count, count_distinct, latest. Default by metric type: gauge→avg, cumulative counter→rate, delta counter→sum.
- spaceAggregation: cross-series aggregation. Common: sum, avg, min, max, count. For histograms: p50, p75, p90, p95, p99.

### Logs / traces signal — expression shape
Use this shape when spec.signal = "logs" or "traces":

` + "```" + `json
"aggregations": [{"expression": "count()"}]
` + "```" + `

Common expressions: count(), count_distinct(user_id), avg(duration), sum(bytes), min(x), max(x), p50/p75/p90/p95/p99(duration_nano).

### Filter expressions
- Equality: service.name = 'frontend'
- Comparison: http.status_code >= 500
- Pattern: body CONTAINS 'timeout'
- Case-insensitive: body ILIKE '%error%'
- Boolean: service.name = 'frontend' AND http.status_code >= 500
- IN: severity_text IN ('ERROR', 'WARN', 'FATAL')
- EXISTS: trace_id EXISTS

## Units

Set compositeQuery.unit to specify the unit of the queried data (Y-axis). This drives value formatting in alert messages ({{$value}}) and unit conversion when threshold targetUnit differs.

Common units: percent, percentunit, ms, s, ns, bytes, kbytes, mbytes, gbytes, reqps, ops, cps.

## Threshold Configuration (v2alpha1 — threshold_rule & promql_rule)

condition.thresholds defines one or more routing tiers. Each tier can route to different channels:

` + "```" + `json
"thresholds": {
  "kind": "basic",
  "spec": [
    {
      "name": "critical",
      "target": 1000,
      "op": "above",
      "matchType": "all_the_times",
      "channels": ["pagerduty-oncall", "slack-alerts"]
    },
    {
      "name": "warning",
      "target": 100,
      "op": "above",
      "matchType": "at_least_once",
      "channels": ["slack-alerts"]
    }
  ]
}
` + "```" + `

### Threshold fields
- **name**: tier name — critical, error, warning, or info. Acts as the routing label: alerts carry threshold_name equal to this value. Set labels.severity to match your highest tier.
- **target**: numeric threshold value (required).
- **targetUnit**: unit of the target (e.g. ms, percent, s, bytes). Auto-converted to compositeQuery.unit during evaluation.
- **recoveryTarget**: hysteresis value to avoid flapping (e.g. target=80%, recoveryTarget=75%). null uses the target itself as the recovery point.
- **matchType**: canonical at_least_once, all_the_times, on_average, in_total, last. Aliases accepted: avg (=on_average), sum (=in_total).
- **op**: canonical above, below, equal, not_equal, above_or_equal, below_or_equal, outside_bounds. Short forms accepted: eq, not_eq, above_or_eq, below_or_eq. Symbolic accepted: >, <, =, !=, >=, <=.
- **channels**: notification channel names for this tier. Discover via signoz_list_notification_channels. Ignored when notificationSettings.usePolicy is true.

## Evaluation (v2alpha1)

evaluation controls how the rule is evaluated:

` + "```" + `json
"evaluation": {
  "kind": "rolling",
  "spec": {"evalWindow": "5m", "frequency": "1m"}
}
` + "```" + `

- evalWindow: how long the condition must persist (e.g. 5m, 15m, 30m, 1h, 4h, 24h).
- frequency: how often to evaluate (e.g. 1m, 5m, 15m).
- Auto-generated (5m window, 1m frequency) if omitted for threshold/promql rules.

## Notification Settings (v2alpha1)

` + "```" + `json
"notificationSettings": {
  "groupBy": ["service.name", "deployment.environment"],
  "newGroupEvalDelay": "2m",
  "renotify": {
    "enabled": true,
    "interval": "30m",
    "alertStates": ["firing", "nodata"]
  },
  "usePolicy": false
}
` + "```" + `

- **groupBy**: fields to group notifications by (reduces noise).
- **newGroupEvalDelay**: Go duration string. Grace period during which a newly-appearing label group is excluded from evaluation. Helps avoid flapping when new pods/services come online.
- **renotify.enabled**: whether to re-send alerts at interval.
- **renotify.interval**: re-notify interval (e.g. 15m, 30m, 1h, 4h).
- **renotify.alertStates**: accepted values are firing and nodata. Any other value is rejected.
- **usePolicy**: routing mode. false (default) = deliver to the channels listed in each threshold entry. true = ignore per-threshold channels and route via the org-level notification policy matching on labels.

## Labels & Routing

- labels.severity: MUST be one of critical, error, warning, info. When thresholds is used, threshold.name is the routing tier — set labels.severity to the highest tier the rule carries.
- Additional labels like team, service, environment enable routing policies.
- preferredChannels: fallback notification channel names (thresholds.channels takes priority).
- Set usePolicy: true in notificationSettings to delegate routing to org-level policies.

## Annotations
- Use {{$value}} for the current metric value.
- Use {{$threshold}} for the threshold value.
- Use {{$labels.key}} for label values (e.g. {{$labels.k8s_pod_name}}).
- Common annotations: description, summary, runbook.

## Anomaly Alerts (ruleType: anomaly_rule — v1 schema)
Anomaly rules use the **v1 schema** today. Do NOT set thresholds, evaluation, notificationSettings, or schemaVersion.

Required fields:
- alertType: METRIC_BASED_ALERT
- ruleType: anomaly_rule
- **evalWindow** (top-level): e.g. 24h
- **frequency** (top-level): e.g. 3h
- condition.compositeQuery: normal builder query with spec.functions carrying an anomaly function, e.g. [{name: "anomaly", args: [{name: "z_score_threshold", value: 2}]}]
- condition.op / condition.matchType / condition.target: same accepted values as threshold.op / matchType / target. Applied to the anomaly score.
- condition.algorithm: e.g. "standard" (z-score based).
- condition.seasonality: hourly, daily, or weekly.
- condition.requireMinPoints + condition.requiredNumPoints are recommended to guard against noisy intervals.

See signoz://alert/examples → "metric_anomaly" for a complete payload.

## Absent-data alerting
Set condition.alertOnAbsent=true to fire when no series is returned. condition.absentFor is in **minutes** (equivalent to consecutive evaluation cycles when frequency is 1m). Example: absentFor=15 fires after 15 minutes of no data.

## Auto-Applied Defaults (threshold_rule / promql_rule only)
- version → "v5"
- schemaVersion → "v2alpha1"
- evaluation → {kind: "rolling", spec: {evalWindow: "5m0s", frequency: "1m0s"}}
- notificationSettings → {renotify: {enabled: false, interval: "30m"}}
- panelType → "graph"
- selectedQueryName → first query name
- source → "mcp"
- labels.severity → "warning" (if not set)
- annotations → default description and summary templates

anomaly_rule: none of the above defaults are applied automatically — you must supply evalWindow, frequency, and the condition fields yourself.
`

// Examples is the MCP resource content for signoz://alert/examples.
// The ten examples below mirror the canonical payloads in SigNoz PR #11023
// (pkg/apiserver/signozapiserver/ruler_examples.go). Keep this list in sync
// with upstream when that file changes.
const Examples = `# SigNoz Alert Rule — Examples (mirrors SigNoz PR #11023)

These examples mirror the canonical payloads in SigNoz PR #11023
(pkg/apiserver/signozapiserver/ruler_examples.go). Threshold and PromQL rules
use v2alpha1; the anomaly example uses the v1 shape.

## 1. metric_threshold_single — metric threshold, single builder query
Fires when a pod consumes more than 80% of its requested CPU for the whole evaluation window.

` + "```" + `json
{
  "alert": "Pod CPU above 80% of request",
  "alertType": "METRIC_BASED_ALERT",
  "description": "CPU usage for api-service pods exceeds 80% of the requested CPU",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percentunit",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "stepInterval": 60,
            "aggregations": [{"metricName": "k8s.pod.cpu_request_utilization", "timeAggregation": "avg", "spaceAggregation": "max"}],
            "filter": {"expression": "k8s.deployment.name = 'api-service'"},
            "groupBy": [
              {"name": "k8s.pod.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}
            ],
            "legend": "{{k8s.pod.name}} ({{deployment.environment}})"
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "all_the_times", "target": 0.8, "channels": ["slack-platform", "pagerduty-oncall"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "15m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["k8s.pod.name", "deployment.environment"],
    "renotify": {"enabled": true, "interval": "4h", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical", "team": "platform"},
  "annotations": {
    "description": "Pod {{$k8s.pod.name}} CPU is at {{$value}} of request in {{$deployment.environment}}.",
    "summary": "Pod CPU above {{$threshold}} of request"
  }
}
` + "```" + `

## 2. metric_threshold_formula — metric threshold with a builder_formula
Computes disk utilization as (1 - available/capacity) * 100 by combining two disabled base queries with a builder_formula. The formula emits 0-100, so compositeQuery.unit is set to "percent" and the target is a bare number.

` + "```" + `json
{
  "alert": "PersistentVolume above 80% utilization",
  "alertType": "METRIC_BASED_ALERT",
  "description": "Disk utilization for a persistent volume is above 80%",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percent",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "metrics", "stepInterval": 60, "disabled": true,
            "aggregations": [{"metricName": "k8s.volume.available", "timeAggregation": "max", "spaceAggregation": "max"}],
            "filter": {"expression": "k8s.volume.type = 'persistentVolumeClaim'"},
            "groupBy": [
              {"name": "k8s.persistentvolumeclaim.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "k8s.namespace.name", "fieldContext": "resource", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B", "signal": "metrics", "stepInterval": 60, "disabled": true,
            "aggregations": [{"metricName": "k8s.volume.capacity", "timeAggregation": "max", "spaceAggregation": "max"}],
            "filter": {"expression": "k8s.volume.type = 'persistentVolumeClaim'"},
            "groupBy": [
              {"name": "k8s.persistentvolumeclaim.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "k8s.namespace.name", "fieldContext": "resource", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_formula",
          "spec": {
            "name": "F1",
            "expression": "(1 - A/B) * 100",
            "legend": "{{k8s.persistentvolumeclaim.name}} ({{k8s.namespace.name}})"
          }
        }
      ]
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "at_least_once", "target": 80, "channels": ["slack-storage"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "30m", "frequency": "5m"}},
  "notificationSettings": {
    "groupBy": ["k8s.namespace.name", "k8s.persistentvolumeclaim.name"],
    "renotify": {"enabled": true, "interval": "2h", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical"},
  "annotations": {
    "description": "Volume {{$k8s.persistentvolumeclaim.name}} in {{$k8s.namespace.name}} is {{$value}}% full.",
    "summary": "Disk utilization above {{$threshold}}%"
  }
}
` + "```" + `

## 3. metric_promql — PromQL rule
PromQL expression instead of the builder. Dotted OTEL resource attributes are quoted ("deployment.environment"). The envelope type is "promql" — not "builder_query".

` + "```" + `json
{
  "alert": "Kafka consumer group lag above 1000",
  "alertType": "METRIC_BASED_ALERT",
  "description": "Consumer group lag computed via PromQL",
  "ruleType": "promql_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "promql",
      "panelType": "graph",
      "queries": [
        {
          "type": "promql",
          "spec": {
            "name": "A",
            "query": "(max by(topic, partition, \"deployment.environment\")(kafka_log_end_offset) - on(topic, partition, \"deployment.environment\") group_right max by(group, topic, partition, \"deployment.environment\")(kafka_consumer_committed_offset)) > 0",
            "legend": "{{topic}}/{{partition}} ({{group}})"
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "all_the_times", "target": 1000, "channels": ["slack-data-platform", "pagerduty-data"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "10m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["group", "topic"],
    "renotify": {"enabled": true, "interval": "1h", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical"},
  "annotations": {
    "description": "Consumer group {{$group}} is {{$value}} messages behind on {{$topic}}/{{$partition}}.",
    "summary": "Kafka consumer lag high"
  }
}
` + "```" + `

## 4. metric_anomaly — anomaly rule (v1 schema)
Anomaly rules are not yet supported under schemaVersion v2alpha1, so this example uses the **v1 shape**: top-level evalWindow/frequency, condition.op/matchType/target/algorithm/seasonality at the condition level, and an anomaly function inside the builder-query spec. No thresholds block.

` + "```" + `json
{
  "alert": "Anomalous drop in ingested spans",
  "alertType": "METRIC_BASED_ALERT",
  "description": "Detect an abrupt drop in span ingestion using a z-score anomaly function",
  "ruleType": "anomaly_rule",
  "version": "v5",
  "evalWindow": "24h",
  "frequency": "3h",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "metrics", "stepInterval": 21600,
            "aggregations": [{"metricName": "otelcol_receiver_accepted_spans", "timeAggregation": "rate", "spaceAggregation": "sum"}],
            "filter": {"expression": "tenant_tier = 'premium'"},
            "groupBy": [{"name": "tenant_id", "fieldContext": "attribute", "fieldDataType": "string"}],
            "functions": [
              {"name": "anomaly", "args": [{"name": "z_score_threshold", "value": 2}]}
            ],
            "legend": "{{tenant_id}}"
          }
        }
      ]
    },
    "op": "below",
    "matchType": "all_the_times",
    "target": 2,
    "algorithm": "standard",
    "seasonality": "daily",
    "selectedQueryName": "A",
    "requireMinPoints": true,
    "requiredNumPoints": 3
  },
  "labels": {"severity": "warning"},
  "preferredChannels": ["slack-ingestion"],
  "annotations": {
    "description": "Ingestion rate for tenant {{$tenant_id}} is anomalously low (z-score {{$value}}).",
    "summary": "Span ingestion anomaly"
  }
}
` + "```" + `

## 5. logs_threshold — logs threshold count()
Counts matching log records (ERROR severity + body contains) over a rolling window.

` + "```" + `json
{
  "alert": "Payments service panic logs",
  "alertType": "LOGS_BASED_ALERT",
  "description": "Any panic log line emitted by the payments service",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "logs", "stepInterval": 60,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name = 'payments-api' AND severity_text = 'ERROR' AND body CONTAINS 'panic'"},
            "groupBy": [
              {"name": "k8s.pod.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}
            ],
            "legend": "{{k8s.pod.name}} ({{deployment.environment}})"
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "at_least_once", "target": 0, "channels": ["slack-payments", "pagerduty-payments"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["k8s.pod.name", "deployment.environment"],
    "renotify": {"enabled": true, "interval": "15m", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical", "team": "payments"},
  "annotations": {
    "description": "{{$k8s.pod.name}} emitted {{$value}} panic log(s) in {{$deployment.environment}}.",
    "summary": "Payments service panic"
  }
}
` + "```" + `

## 6. logs_error_rate_formula — logs error rate (error count / total count × 100)
Two disabled log count queries (A = errors, B = total) combined via a builder_formula.

` + "```" + `json
{
  "alert": "Payments-api error log rate above 1%",
  "alertType": "LOGS_BASED_ALERT",
  "description": "Error log ratio as a percentage of total logs for payments-api",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percent",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "logs", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name = 'payments-api' AND severity_text IN ['ERROR', 'FATAL']"},
            "groupBy": [{"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}]
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B", "signal": "logs", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name = 'payments-api'"},
            "groupBy": [{"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}]
          }
        },
        {
          "type": "builder_formula",
          "spec": {"name": "F1", "expression": "(A / B) * 100", "legend": "{{deployment.environment}}"}
        }
      ]
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "at_least_once", "target": 1, "channels": ["slack-payments"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["deployment.environment"],
    "renotify": {"enabled": true, "interval": "30m", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical", "team": "payments"},
  "annotations": {
    "description": "Error log rate in {{$deployment.environment}} is {{$value}}%",
    "summary": "Payments-api error rate above {{$threshold}}%"
  }
}
` + "```" + `

## 7. traces_threshold_latency — traces p99 with unit conversion (ns → s)
Builder query against the traces signal with p99(duration_nano). The series unit is ns, but the threshold target is in seconds (targetUnit: "s") — SigNoz converts during evaluation.

` + "```" + `json
{
  "alert": "Search API p99 latency above 5s",
  "alertType": "TRACES_BASED_ALERT",
  "description": "p99 duration of the search endpoint exceeds 5s",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "ns",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "traces", "stepInterval": 60,
            "aggregations": [{"expression": "p99(duration_nano)"}],
            "filter": {"expression": "service.name = 'search-api' AND name = 'GET /api/v1/search'"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "http.route", "fieldContext": "attribute", "fieldDataType": "string"}
            ],
            "legend": "{{service.name}} {{http.route}}"
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "warning", "op": "above", "matchType": "at_least_once", "target": 5, "targetUnit": "s", "channels": ["slack-search"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["service.name", "http.route"],
    "renotify": {"enabled": true, "interval": "30m", "alertStates": ["firing"]}
  },
  "labels": {"severity": "warning", "team": "search"},
  "annotations": {
    "description": "p99 latency for {{$service.name}} on {{$http.route}} crossed {{$threshold}}s.",
    "summary": "Search-api latency degraded"
  }
}
` + "```" + `

## 8. traces_error_rate_formula — traces error rate (error spans / total spans × 100)
Two disabled trace count queries (A = error spans, B = total spans) combined via a builder_formula.

` + "```" + `json
{
  "alert": "Search-api error rate above 5%",
  "alertType": "TRACES_BASED_ALERT",
  "description": "Request error rate for search-api, grouped by route",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percent",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "traces", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name = 'search-api' AND hasError = true"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "http.route", "fieldContext": "attribute", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B", "signal": "traces", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name = 'search-api'"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "http.route", "fieldContext": "attribute", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_formula",
          "spec": {"name": "F1", "expression": "(A / B) * 100", "legend": "{{service.name}} {{http.route}}"}
        }
      ]
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "at_least_once", "target": 5, "channels": ["slack-search", "pagerduty-search"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["service.name", "http.route"],
    "renotify": {"enabled": true, "interval": "15m", "alertStates": ["firing"]}
  },
  "labels": {"severity": "critical", "team": "search"},
  "annotations": {
    "description": "Error rate on {{$service.name}} {{$http.route}} is {{$value}}%",
    "summary": "Search-api error rate above {{$threshold}}%"
  }
}
` + "```" + `

## 9. tiered_thresholds — tiered thresholds with per-tier channels and alertOnAbsent
Two tiers (warning and critical) in a single rule, each with its own target, op, matchType, and channels. alertOnAbsent + absentFor fires a no-data alert when the query returns no series for 15 consecutive evaluations (15 minutes when frequency=1m).

` + "```" + `json
{
  "alert": "Kafka consumer lag warn / critical",
  "alertType": "METRIC_BASED_ALERT",
  "description": "Warn at lag >= 50 and page at >= 200, tiered via thresholds.spec.",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "metrics", "stepInterval": 60, "disabled": true,
            "aggregations": [{"metricName": "kafka_log_end_offset", "timeAggregation": "max", "spaceAggregation": "max"}],
            "filter": {"expression": "topic != '__consumer_offsets'"},
            "groupBy": [
              {"name": "topic", "fieldContext": "attribute", "fieldDataType": "string"},
              {"name": "partition", "fieldContext": "attribute", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B", "signal": "metrics", "stepInterval": 60, "disabled": true,
            "aggregations": [{"metricName": "kafka_consumer_committed_offset", "timeAggregation": "max", "spaceAggregation": "max"}],
            "filter": {"expression": "topic != '__consumer_offsets'"},
            "groupBy": [
              {"name": "topic", "fieldContext": "attribute", "fieldDataType": "string"},
              {"name": "partition", "fieldContext": "attribute", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_formula",
          "spec": {"name": "F1", "expression": "A - B", "legend": "{{topic}}/{{partition}}"}
        }
      ]
    },
    "alertOnAbsent": true,
    "absentFor": 15,
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "warning", "op": "above", "matchType": "all_the_times", "target": 50, "channels": ["slack-kafka-info"]},
        {"name": "critical", "op": "above", "matchType": "all_the_times", "target": 200, "channels": ["slack-kafka-alerts", "pagerduty-kafka"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["topic"],
    "renotify": {"enabled": true, "interval": "15m", "alertStates": ["firing"]}
  },
  "labels": {"team": "data-platform"},
  "annotations": {
    "description": "Consumer lag for {{$topic}} partition {{$partition}} is {{$value}}.",
    "summary": "Kafka consumer lag"
  }
}
` + "```" + `

## 10. notification_settings — full notificationSettings surface
Demonstrates groupBy (noise control), newGroupEvalDelay (grace period for new series), renotify on both firing and nodata states, and usePolicy: false (per-threshold channels rather than org-level routing).

` + "```" + `json
{
  "alert": "API 5xx error rate above 1%",
  "alertType": "TRACES_BASED_ALERT",
  "description": "Noise-controlled 5xx error rate alert with renotify on gaps",
  "ruleType": "threshold_rule",
  "version": "v5",
  "schemaVersion": "v2alpha1",
  "condition": {
    "compositeQuery": {
      "queryType": "builder",
      "panelType": "graph",
      "unit": "percent",
      "queries": [
        {
          "type": "builder_query",
          "spec": {
            "name": "A", "signal": "traces", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name CONTAINS 'api' AND http.status_code >= 500"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_query",
          "spec": {
            "name": "B", "signal": "traces", "stepInterval": 60, "disabled": true,
            "aggregations": [{"expression": "count()"}],
            "filter": {"expression": "service.name CONTAINS 'api'"},
            "groupBy": [
              {"name": "service.name", "fieldContext": "resource", "fieldDataType": "string"},
              {"name": "deployment.environment", "fieldContext": "resource", "fieldDataType": "string"}
            ]
          }
        },
        {
          "type": "builder_formula",
          "spec": {"name": "F1", "expression": "(A / B) * 100", "legend": "{{service.name}} ({{deployment.environment}})"}
        }
      ]
    },
    "selectedQueryName": "F1",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "op": "above", "matchType": "at_least_once", "target": 1, "channels": ["slack-api-alerts", "pagerduty-oncall"]}
      ]
    }
  },
  "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
  "notificationSettings": {
    "groupBy": ["service.name", "deployment.environment"],
    "newGroupEvalDelay": "2m",
    "usePolicy": false,
    "renotify": {"enabled": true, "interval": "30m", "alertStates": ["firing", "nodata"]}
  },
  "labels": {"team": "platform"},
  "annotations": {
    "description": "{{$service.name}} 5xx rate in {{$deployment.environment}} is {{$value}}%.",
    "summary": "API service error rate elevated"
  }
}
` + "```" + `

## Key Notes
1. Metrics signal → object aggregation shape ({metricName, timeAggregation, spaceAggregation}). Logs/traces → expression shape ({expression: "count()"}).
2. selectedQueryName should reference the query or formula that determines the alert.
3. Use signoz_get_alert to inspect existing alerts for the exact format your SigNoz version expects.
4. Channel names in thresholds.spec[].channels must match exactly the names from signoz_list_notification_channels.
5. For threshold_rule/promql_rule, schemaVersion/evaluation/notificationSettings are auto-generated if omitted. For anomaly_rule, supply evalWindow/frequency at the top level and op/matchType/target/algorithm/seasonality under condition — no thresholds block, no auto-generated evaluation.
6. absentFor is in minutes (= consecutive evaluation cycles when frequency is 1m).
`
