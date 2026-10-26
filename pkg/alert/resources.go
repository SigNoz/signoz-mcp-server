package alert

// Instructions is the MCP resource content for signoz://alert/instructions.
const Instructions = `# SigNoz Alert Rule — Instructions

## Overview
An alert rule monitors a signal (metrics, logs, traces, or exceptions) and fires when a condition is met.
The alert is created via POST /api/v2/rules.

Schemas supported:
- **v2alpha1** for threshold_rule and promql_rule: structured thresholds + evaluation + notificationSettings. Always set schemaVersion="v2alpha1" explicitly: SigNoz treats an omitted schemaVersion as v1 and then requires the v1 condition fields instead.
- **v1** for anomaly_rule: top-level evalWindow/frequency with condition.op/matchType/target/algorithm/seasonality. No thresholds block.

## Before Creating or Updating an Alert
1. Read signoz://alert/examples for complete payloads unless already read for the same prepared operation.
2. Update is a full replacement. Reuse signoz_get_alert only from the same still-current prepared operation; refresh if state may have changed, then preserve unchanged fields.
3. Use signoz_get_field_keys to discover filter/groupBy attributes, reusing results from that operation.
4. NOTIFICATION ROUTING: Reuse a fully paginated signoz_list_notification_channels result only from the same still-current prepared operation; otherwise call it, refreshing only if state may have changed. The list is config-free, defaults to 20 rows, and accepts at most 200 per page; follow its filtered total. The immutable routing identity is displayName, not machine name. For v2 direct routing, every threshold tier needs an exact returned displayName: SigNoz rejects a tier with no channels and a channel name that does not exist. Top-level preferredChannels plays no part in v2 routing (SigNoz stores it unused), so omit it. V1 anomaly rules use exact returned displayName values in top-level preferredChannels and cannot use policy routing. If no direct channel fits, show the returned choices and ask; if none exists, offer signoz_create_notification_channel with settings the user provides. Never guess or create automatically. Channel create and update default test to false; set test=true only when the user asks to send a test. Confirmed v2 policy routing sets notificationSettings.usePolicy=true and may omit tier channels.

## Quick Workflow: From User Intent to Payload
A repeatable mental model for going from a user request ("alert me when login p99 > 2s") to a valid payload:
1. **Signal -> alertType.** CPU, memory, latency histograms, request rate -> METRIC_BASED_ALERT. Log lines or log volume -> LOGS_BASED_ALERT. Span latency or span error rate -> TRACES_BASED_ALERT. Exception counts -> EXCEPTIONS_BASED_ALERT. SigNoz validates the value.
2. **Pick ruleType.** Default to threshold_rule. Use promql_rule only if the user provided a PromQL expression. Use anomaly_rule only for metric deviation detection; it uses a different (v1) schema. See the Anomaly Alerts section.
3. **Pick compositeQuery.queryType + matching envelope type.** See the "Query envelope type" table.
4. **Pick the aggregation shape.** Metrics -> object {metricName, timeAggregation, spaceAggregation}. Logs/traces -> {expression: "count()" | "p99(duration_nano)" | ...}.
5. **Write the filter.** See "Filter & Having Expressions" for the operator set. Prefer resource attributes (service.name, deployment.environment, k8s.*); the backend indexes them.
6. **Configure thresholds.** Every threshold/PromQL rule requires tiers with name, op, matchType, and target. Direct routing uses exact displayName values from the notification-routing preflight above on every tier; confirmed policy routing sets notificationSettings.usePolicy=true and may omit tier channels.
7. **Evaluation.** Set evaluation explicitly (SigNoz requires it for v2alpha1): kind=rolling with evalWindow=5m and frequency=1m unless the user asked for a different window.
8. **Notification.** Set notificationSettings.groupBy on high-cardinality queries to reduce noise.

The tool's input schema describes every top-level and condition field (accepted values, defaults, which schemaVersion reads each). This resource covers what the schema cannot: the query envelopes, threshold tiers, and evaluation specs inside the union-typed fields, and the workflow around them.

## Composite Query Structure

condition.compositeQuery uses the v5 query format. Set panelType to "graph" for alerts; SigNoz stores an empty value when omitted.

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
- source (metrics only): "meter" to alert on Cost Meter usage/billing metrics (e.g. signoz.meter.log.size); omit otherwise. Works with either evaluation kind: cumulative for daily/monthly spend budgets, rolling for rate/over-time meter alerts.
- stepInterval: interval in seconds (60 for most alerts)
- aggregations: see "Aggregation shapes" below
- filter: {expression: "service.name = 'frontend' AND http.status_code >= 500"}
- groupBy: [{name, fieldContext: "resource" | "attribute", fieldDataType}]
- order and limit: omit both on alert queries. On a grouped query they cap evaluation to the top-N series (SigNoz builds a top-N CTE from them), so series beyond N are silently never evaluated; a "below" alert ordered descending can miss every breaching series. Set them only when top-N alerting is the explicit intent, using Query Builder v5 wire ordering (metrics: [{key: {name: "__result"}, direction: "desc"}]), never dashboard editor orderBy.
- functions: post-query transforms. Required for anomaly_rule: [{name: "anomaly", args: [{name: "z_score_threshold", value: 2}]}]
- disabled: true when the query is used only as an input to a formula

### PromQL / ClickHouse query spec (envelope type=promql or clickhouse_sql)
- name: query identifier
- query: the PromQL or SQL string (required)
- legend: legend template
- disabled: false

### PromQL for OTel dotted metric names
Alerts with ruleType=promql_rule on OpenTelemetry metrics must use the Prometheus 3.x UTF-8 quoted-selector form for any name containing a dot. The forms that look natural but return no data in SigNoz:

- Underscored conversion: rate(payment_latency_ms_bucket[5m]). SigNoz does NOT rename dots to underscores.
- Bare dotted name: rate(payment_latency_ms.bucket[5m]). A dot is not a legal identifier char.

Correct form (quote the name inside braces):

  histogram_quantile(0.9, sum(rate({"payment_latency_ms.bucket"}[5m])) by (le))

The same rule applies to dotted label names in by(...) and in matchers:

  sum by (le, "service.name") (
    rate({"payment_latency_ms.bucket", "deployment.environment"="prod"}[5m])
  )

For the full guide (syntax, examples by metric type, anti-pattern table, pre-flight checklist for histogram alerts) read signoz://promql/instructions. Always read it before writing a promql_rule query.

### Formula query spec (envelope type=builder_formula)
- name: formula identifier (F1, F2, …)
- expression: math expression referencing other query names (e.g. "(A / B) * 100"). Supports +, -, *, /, and functions like abs(), sqrt(), log(), exp()
- legend: legend template
- Set selectedQueryName to the formula name (e.g. "F1") so the alert triggers on the formula result

Omit limit and order on formula and input queries too: a limit ranks groups over the whole evaluation window and drops everything outside the top N, so a group with a short-lived spike can silently never be evaluated.

## Aggregation shapes

### Metrics signal — object shape
Use this shape when spec.signal = "metrics":

` + "```" + `json
"aggregations": [
  {"metricName": "k8s.pod.cpu_request_utilization", "timeAggregation": "avg", "spaceAggregation": "max"}
]
` + "```" + `

- metricName: the metric you're querying.
- timeAggregation: per-series time aggregation. Common: avg, max, min, sum, rate, increase, count, count_distinct, latest. Default by metric type: gauge -> avg, cumulative counter -> rate, delta counter -> rate.
- spaceAggregation: cross-series aggregation. Common: sum, avg, min, max, count. For histograms: p50, p75, p90, p95, p99.

### Logs / traces signal — expression shape
Use this shape when spec.signal = "logs" or "traces":

` + "```" + `json
"aggregations": [{"expression": "count()"}]
` + "```" + `

Common expressions: count(), count_distinct(user_id), avg(duration), sum(bytes), min(x), max(x), p50/p75/p90/p95/p99(duration_nano).

## Filter & Having Expressions

builder_query spec.filter.expression (pre-aggregation) and spec.having.expression (post-aggregation) use the same syntax. Prefer resource attributes in filters; they are the fastest path through the storage backend.

For logs/traces, HAVING aggregation expressions must be included in aggregations. For a percentile count guard, put the percentile first and count() second, then use count() > N in having.expression. Alerts evaluate the first aggregation.

### Operator reference
| Intent | Operator | Example |
|--------|----------|---------|
| Field exists | EXISTS | trace_id EXISTS |
| Field is absent | NOT EXISTS | k8s.pod.name NOT EXISTS |
| Exact match | = | service.name = 'frontend' |
| Not equal (field must exist) | EXISTS AND != | service.name EXISTS AND service.name != 'redis' |
| One of several values | IN | severity_text IN ('ERROR', 'WARN', 'FATAL') |
| Excluded from a set | NOT IN | deployment.environment NOT IN ('dev', 'staging') |
| Substring / pattern | LIKE | name LIKE '%payment%' |
| Case-insensitive pattern | ILIKE | body ILIKE '%timeout%' |
| Simple containment | CONTAINS | body CONTAINS 'timeout' |
| Regex | REGEXP | name REGEXP '^grpc\.' |
| Numeric comparison | >, >=, <, <= | http.status_code >= 500 |

### Data type guardrails
- **bool**: =, !=, EXISTS, NOT EXISTS
- **int / float**: =, !=, >, >=, <, <=, IN, EXISTS, NOT EXISTS
- **string**: =, !=, LIKE, ILIKE, CONTAINS, REGEXP, IN, NOT IN, EXISTS, NOT EXISTS

### Composition
Combine with AND / OR and parentheses for grouping:
- service.name = 'frontend' AND http.status_code >= 500
- (severity_text = 'ERROR' AND service.name = 'payments-api') OR service.name = 'billing-api'

Negative operators (!=, NOT IN, NOT LIKE) only match rows where the field is present. To also exclude rows that lack the field entirely, pair with EXISTS:
- k8s.namespace.name EXISTS AND k8s.namespace.name != 'kube-system'

Common compositeQuery.unit values: percent, percentunit, ms, s, ns, bytes, kbytes, mbytes, gbytes, reqps, ops, cps.

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
- **name**: tier name (critical, error, warning, or info). Acts as the routing label: alerts carry threshold.name equal to this value. Set labels.severity to match your highest tier.
- **target**: numeric threshold value (required).
- **targetUnit**: unit of the target (e.g. ms, percent, s, bytes). Auto-converted to compositeQuery.unit during evaluation.
- **recoveryTarget**: hysteresis value to avoid flapping (e.g. target=80%, recoveryTarget=75%). null uses the target itself as the recovery point.
- **matchType**: canonical at_least_once, all_the_times, on_average, in_total, last. Aliases accepted: avg (=on_average), sum (=in_total).
- **op**: canonical above, below, equal, not_equal, above_or_equal, below_or_equal, outside_bounds. Short forms accepted: eq, not_eq, above_or_eq, below_or_eq. Symbolic accepted: >, <, =, !=, >=, <=.
- **channels**: existing notification channel displayName values for this tier. Direct v2 routing requires at least one exact returned displayName from signoz_list_notification_channels on every tier; top-level preferredChannels is not a fallback. Reuse only a same-operation, still-current list result; otherwise call the fully paginated tool, refreshing if state may have changed. With notificationSettings.usePolicy=true, tier channels may be omitted, but supplied displayName values are still validated.

### Choosing targetUnit
- Set targetUnit when the threshold value is in a different unit from the query series. Example: the series emits nanoseconds (compositeQuery.unit="ns") but you want to threshold at "5 seconds". Set target=5, targetUnit="s". SigNoz converts during evaluation.
- If compositeQuery.unit is empty and targetUnit is set, the validator propagates targetUnit onto compositeQuery.unit, so you can omit compositeQuery.unit on single-threshold rules.
- Formulas that compute ratios (e.g. (A/B) * 100) already emit percent. Set compositeQuery.unit="percent" and give the threshold a bare numeric target (no targetUnit).

### Choosing recoveryTarget
- **null / unset**: the alert recovers as soon as the series crosses back through target. Simplest but can flap when the value oscillates at the threshold boundary.
- **non-null**: creates a dead-band between target and recoveryTarget. Example: fire at target=80, recover at recoveryTarget=70. The series must drop below 70 before the alert clears. Recommended for signals that oscillate near the threshold.

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
- Required by SigNoz for threshold/promql rules (schemaVersion v2alpha1); it rejects a rule without an evaluation block. Use a 5m window with 1m frequency unless asked otherwise.
- **Format**: Go duration strings. Both "5m" and "5m0s" are accepted; stick to one style per payload. Common values: 1m, 5m, 15m, 30m, 1h, 4h, 24h, 1d.
- **Sizing tips**: keep evalWindow ≥ frequency. Short windows (1-5m) catch spikes; long windows (30m-1h) smooth noise. For infrequent signals (hourly batch jobs) set frequency to 5-15m to reduce evaluation cost.

### Cumulative window (daily/monthly totals)

A general evaluation kind, independent of signal/source. Use it for any period-total alert (daily error budgets, monthly request counts, Cost Meter spend budgets, …). It accumulates from a fixed reset point instead of using a sliding window:

` + "```" + `json
"evaluation": {
  "kind": "cumulative",
  "spec": {"schedule": {"type": "daily", "minute": 0, "hour": 0}, "frequency": "1m", "timezone": "UTC"}
}
` + "```" + `

- kind "cumulative" sums since the schedule boundary (vs "rolling" sliding window); pair with threshold matchType "in_total".
- schedule.type: "daily" (shown; minute/hour set the reset, e.g. 00:00) or "monthly". timezone e.g. "UTC".

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

Set groupBy on high-cardinality queries to reduce noise. usePolicy=false (the default) requires existing channels on every threshold tier; usePolicy=true routes by the org-level policies so tier channels may be omitted. Anomaly rules omit notificationSettings.

## Labels & Routing

- labels.severity: MUST be one of critical, error, warning, info. When thresholds is used, threshold.name is the routing tier. Set labels.severity to the highest tier the rule carries.
- Additional labels like team, service, environment enable routing policies.

### Label sources available to routing policies
Routing policies evaluate expressions against three merged label sources:
1. **User static labels** from the labels object on the rule (severity, team, service, environment, …).
2. **Platform labels** auto-injected at fire time:
   - alertname: the rule's alert field
   - threshold.name: the tier that fired (critical | error | warning | info)
   - ruleSource, ruleId: rule metadata
3. **Dynamic labels** from groupBy fields in the query (service.name, k8s.pod.name, http.route, deployment.environment, topic, partition, …).

### Routing-policy matcher operators
Policy expressions use a reduced operator set (not identical to query filters):
- Comparison: =, !=
- Text matching: CONTAINS, REGEXP
- Set membership: IN, NOT IN
- Logical: AND, OR, parentheses

Example: deployment.environment = "production" AND threshold.name = "critical"

### V2 channel-routing modes
| notificationSettings.usePolicy | thresholds[].channels | Effective routing |
|--------------------------------|-----------------------|-------------------|
| false (default) | present on every tier | Send each tier to its listed channels |
| false | absent on any tier | Invalid; preferredChannels is not a v2 fallback |
| true | omit (any supplied names are still validated) | Match alert labels against the org-level routing policy; send to policy-matched channels |

## Anomaly Alerts (ruleType: anomaly_rule — v1 schema)
Anomaly rules use the **v1 schema** today and direct top-level preferredChannels. Do NOT set thresholds, evaluation, notificationSettings, or schemaVersion; policy routing is unsupported.

Required fields:
- alertType: METRIC_BASED_ALERT
- ruleType: anomaly_rule
- **evalWindow** and **frequency** (top-level): e.g. 24h and 3h
- condition.compositeQuery: normal builder query with spec.functions carrying an anomaly function, e.g. [{name: "anomaly", args: [{name: "z_score_threshold", value: 2}]}]
- condition.op, condition.matchType, condition.target: applied to the anomaly score.
- condition.algorithm and condition.seasonality.
- condition.requireMinPoints and condition.requiredNumPoints are recommended to guard against noisy intervals.

### Anomaly tuning
- **seasonality**: "hourly" for short-period patterns, "daily" for business-hours / off-hours patterns, "weekly" for weekend-vs-weekday traffic.
- **z_score_threshold** (in spec.functions args): controls the anomaly function's sensitivity.

  | Value | Sensitivity | Use case |
  |-------|-------------|----------|
  | 4.0 | Conservative | Only the strongest anomalies; minimal false positives |
  | 3.0 | Balanced (recommended) | Default choice for most series |
  | 2.5 | Sensitive | Catch moderate deviations |
  | 2.0 | Very sensitive | Catch small deviations; higher risk of false positives |

- **Scoring**: anomaly_score = |actual_value − predicted_value| / stddev(current_season). The alert fires when this score satisfies the condition.op / condition.target comparison (e.g. op="above", target=3 ≈ "score above 3 standard deviations").
- **evalWindow** selects the period being evaluated (e.g. 5m); seasonality controls separate historical lookbacks. Check data coverage across those lookbacks before choosing an anomaly rule. Increasing evalWindow does not supply missing history.

See signoz://alert/examples → "metric_anomaly" for a complete payload.

## Set These Explicitly (threshold_rule / promql_rule)
The payload is forwarded to SigNoz exactly as composed, and SigNoz fills in very little (version="v5" and notificationSettings.usePolicy=false). Everything else is on the author:
- schemaVersion: always "v2alpha1"; SigNoz treats an omitted schemaVersion as v1 and then requires the v1 condition fields.
- evaluation and notificationSettings: SigNoz rejects a v2alpha1 rule missing either block.
- panelType: set "graph"; SigNoz stores an empty value when omitted.
- selectedQueryName: set it whenever more than one query exists.
- labels.severity and annotations: SigNoz adds no defaults; include them so routing tiers and notification text work.
- Omit server-populated fields (createdAt, updatedAt, createdBy, updatedBy) and the injected webUrl, also when composing an update from a signoz_get_alert result.

anomaly_rule uses the v1 schema: supply evalWindow, frequency, and the condition fields yourself.

## Further Reading
User-facing docs. Cite these back to the user when they want to understand a concept in depth:
- Metrics alerts: https://signoz.io/docs/alerts-management/metrics-based-alerts
- Log alerts: https://signoz.io/docs/alerts-management/log-based-alerts
- Trace alerts: https://signoz.io/docs/alerts-management/trace-based-alerts
- Exception alerts: https://signoz.io/docs/alerts-management/exceptions-based-alerts
- Anomaly alerts: https://signoz.io/docs/alerts-management/anomaly-based-alerts
- Routing policies: https://signoz.io/docs/alerts-management/routing-policy
- Planned maintenance: https://signoz.io/docs/alerts-management/planned-maintenance
- Notification channel setup: https://signoz.io/docs/setup-alerts-notification
- Alerts history: https://signoz.io/docs/alerts-management/alerts-history
`
