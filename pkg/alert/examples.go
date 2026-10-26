package alert

import _ "embed"

// generatedExamples is rendered by tools/specgen/specgen.py from the request
// examples SigNoz publishes on POST /api/v2/rules in the pinned OpenAPI spec.
//
//go:embed examples.generated.md
var generatedExamples string

// Examples is the MCP resource content for signoz://alert/examples: the
// upstream examples between a hand-written routing preamble and the Cost
// Meter budget example upstream does not publish yet.
var Examples = examplesPreamble + generatedExamples + examplesAddendum

const examplesPreamble = `# SigNoz Alert Rule Examples

Threshold and PromQL rules use schemaVersion v2alpha1; the anomaly example uses the v1 shape. Adapt each example to the target workspace: discover filter and groupBy keys with signoz_get_field_keys instead of copying attribute names.

**Before using any example:** channel displayName values are illustrative. Reuse a fully paginated signoz_list_notification_channels result only from the same still-current prepared operation; otherwise call it and replace every direct-routing value with an exact returned displayName. If none fits, ask the user or offer signoz_create_notification_channel with settings the user provides; never create automatically. Channel creation defaults test to false. Confirmed v2 policy routing may remove tier channels and set notificationSettings.usePolicy=true. Anomaly examples require direct top-level preferredChannels and cannot use policy routing.

`

const examplesAddendum = `
## Cost Meter budget alert (cumulative window, source "meter")

Not published upstream yet. Fires when today's total log ingestion exceeds 10 GiB. The query targets Cost Meter (source "meter") with timeAggregation increase; the threshold uses matchType in_total over a cumulative daily window. For a currency budget, add a builder_formula converting bytes to cost (for example "(A / 1e9) * cost_per_gb") and set selectedQueryName to the formula.

` + "```" + `json
{
  "alert": "Daily log ingestion budget",
  "alertType": "METRIC_BASED_ALERT",
  "ruleType": "threshold_rule",
  "schemaVersion": "v2alpha1",
  "labels": {"severity": "critical"},
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
            "source": "meter",
            "stepInterval": 3600,
            "aggregations": [
              {"metricName": "signoz.meter.log.size", "timeAggregation": "increase", "spaceAggregation": "sum"}
            ]
          }
        }
      ]
    },
    "selectedQueryName": "A",
    "thresholds": {
      "kind": "basic",
      "spec": [
        {"name": "critical", "target": 10737418240, "op": "above", "matchType": "in_total", "channels": ["my-channel"]}
      ]
    }
  },
  "evaluation": {
    "kind": "cumulative",
    "spec": {"schedule": {"type": "daily", "minute": 0, "hour": 0}, "frequency": "1m", "timezone": "UTC"}
  },
  "notificationSettings": {"renotify": {"enabled": false, "interval": "30m"}}
}
` + "```" + `

## Key Notes
1. Metrics signal uses the object aggregation shape ({metricName, timeAggregation, spaceAggregation}); logs and traces use the expression shape ({expression: "count()"}).
2. selectedQueryName references the query or formula that determines the alert.
3. Direct routing uses exact displayName values from a same-operation, still-current signoz_list_notification_channels result, or calls the fully paginated tool when absent or stale. V2 needs a displayName on every tier; confirmed policy routing may omit them. V1 anomaly routing uses exact displayName values in top-level preferredChannels.
4. For threshold_rule and promql_rule, set schemaVersion="v2alpha1" and include evaluation and notificationSettings; SigNoz requires both and treats an omitted schemaVersion as v1. For anomaly_rule, supply evalWindow, frequency, and the condition fields op, matchType, target, algorithm, and seasonality. That schema has no thresholds, evaluation, notificationSettings, or policy routing.
`
