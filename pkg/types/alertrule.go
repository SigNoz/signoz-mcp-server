package types

// AlertType identifies the signal an alert monitors.
type AlertType string

const (
	AlertTypeMetric     AlertType = "METRIC_BASED_ALERT"
	AlertTypeLogs       AlertType = "LOGS_BASED_ALERT"
	AlertTypeTraces     AlertType = "TRACES_BASED_ALERT"
	AlertTypeExceptions AlertType = "EXCEPTIONS_BASED_ALERT"
)

// RuleType identifies how the alert condition is evaluated.
type RuleType string

const (
	RuleTypeThreshold RuleType = "threshold_rule"
	RuleTypePromQL    RuleType = "promql_rule"
	RuleTypeAnomaly   RuleType = "anomaly_rule"
)

type CreateAlertInput struct {
	AlertRule
	SearchContext string `json:"searchContext,omitempty" jsonschema:"The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results."`
}

type UpdateAlertInput struct {
	// id and ruleId are both optional properties (json ",omitempty", neither
	// required) so additionalProperties:false accepts either key; the handler
	// requires exactly one via readResourceID (canonical "id" wins).
	ID           string `json:"id,omitempty" jsonschema:"UUIDv7 of the alert rule to update (required). Obtain it from signoz_list_alert_rules or signoz_get_alert."`
	LegacyRuleID string `json:"ruleId,omitempty" jsonschema:"Deprecated alias for 'id'."`
	AlertRule
	SearchContext string `json:"searchContext,omitempty" jsonschema:"The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results."`
}

// AlertRule is the payload for creating an alert rule via POST /api/v2/rules.
// It matches the SigNoz PostableRule structure. Threshold and PromQL rules use
// the v2alpha1 schema (structured thresholds + evaluation + notificationSettings).
// Anomaly rules use the v1 schema: top-level evalWindow/frequency with
// condition.op/matchType/target/algorithm/seasonality (no thresholds block).
type AlertRule struct {
	Alert             string            `json:"alert" jsonschema:"Name of the alert rule. Must be unique and descriptive."`
	AlertType         AlertType         `json:"alertType" jsonschema:"Signal type: METRIC_BASED_ALERT or LOGS_BASED_ALERT or TRACES_BASED_ALERT or EXCEPTIONS_BASED_ALERT."`
	RuleType          RuleType          `json:"ruleType" jsonschema:"Evaluation type: threshold_rule (compare against value) or promql_rule (PromQL expression) or anomaly_rule (anomaly detection on metrics)."`
	Description       string            `json:"description,omitempty" jsonschema:"Human-readable description of what this alert monitors."`
	Condition         AlertCondition    `json:"condition" jsonschema:"Alert condition containing the query and threshold configuration."`
	Labels            map[string]string `json:"labels,omitempty" jsonschema:"Labels for the alert rule. MUST include severity (one of critical, error, warning, info). When thresholds is used, threshold.name (e.g. critical) acts as the routing tier - set labels.severity to match the highest tier you want this rule to carry. Additional labels like team/service/environment enable routing policies."`
	Annotations       map[string]string `json:"annotations,omitempty" jsonschema:"Annotations like description and summary. Supports template variables: {{$value}} for current metric value and {{$threshold}} for the threshold and {{$labels.key}} for label values."`
	Disabled          bool              `json:"disabled,omitempty" jsonschema:"Whether the alert rule is disabled. Defaults to false (enabled)."`
	Source            string            `json:"source,omitempty" jsonschema:"Source URL for the alert. Set automatically."`
	PreferredChannels []string          `json:"preferredChannels,omitempty" jsonschema:"Notification channel names to send alerts to. Use signoz_list_notification_channels to discover available channel names."`
	Version           string            `json:"version,omitempty" jsonschema:"API version. Always v5. Set automatically if omitted."`

	// v1-schema fields (used only when ruleType=anomaly_rule).
	EvalWindow string `json:"evalWindow,omitempty" jsonschema:"v1 schema only (anomaly_rule). Evaluation window as a Go duration string (e.g. 5m, 15m, 1h, 24h). For threshold/promql rules, use evaluation.spec.evalWindow instead."`
	Frequency  string `json:"frequency,omitempty" jsonschema:"v1 schema only (anomaly_rule). Evaluation frequency as a Go duration string (e.g. 1m, 5m, 3h). For threshold/promql rules, use evaluation.spec.frequency instead."`

	// v2alpha1 schema fields (used for threshold_rule and promql_rule).
	Evaluation           *AlertEvaluation      `json:"evaluation,omitempty" jsonschema:"v2alpha1 only. Evaluation configuration. kind=rolling (sliding window) auto-generated with defaults (5m/1m) if omitted; kind=cumulative (daily/monthly reset) for period-total alerts such as daily error counts or Cost Meter spend budgets. Skipped entirely for anomaly_rule which uses top-level evalWindow/frequency instead."`
	SchemaVersion        string                `json:"schemaVersion,omitempty" jsonschema:"Schema version. Set to v2alpha1 automatically for threshold_rule/promql_rule. Must be omitted (or empty) for anomaly_rule."`
	NotificationSettings *NotificationSettings `json:"notificationSettings,omitempty" jsonschema:"v2alpha1 only. Notification settings - controls grouping and re-notification behavior. Auto-generated with defaults if omitted."`
}

// AlertCondition defines when an alert should fire.
type AlertCondition struct {
	CompositeQuery AlertCompositeQuery `json:"compositeQuery" jsonschema:"The composite query defining what data to monitor."`

	SelectedQuery string `json:"selectedQueryName,omitempty" jsonschema:"Which query name triggers the alert (e.g. A or B or F1). Required when multiple queries exist. Defaults to the first query name."`

	// Absent data alerting
	AlertOnAbsent     bool   `json:"alertOnAbsent,omitempty" jsonschema:"Alert when no data is received within the evaluation window."`
	AbsentFor         uint64 `json:"absentFor,omitempty" jsonschema:"Minutes (equivalent to consecutive evaluation cycles when frequency is 1m) to wait with no data before firing an absent-data alert. Example: absentFor=15 with frequency=1m fires after 15 evaluations return no series."`
	RequireMinPoints  bool   `json:"requireMinPoints,omitempty" jsonschema:"Require a minimum number of data points before evaluating the condition."`
	RequiredNumPoints int    `json:"requiredNumPoints,omitempty" jsonschema:"Minimum number of data points required when requireMinPoints is true."`

	// v1-schema anomaly-rule fields. Used only when the parent AlertRule has
	// ruleType=anomaly_rule. All six are required together in that case and
	// replace the v2alpha1 thresholds block.
	Op          string      `json:"op,omitempty" jsonschema:"v1 (anomaly_rule) only. Comparison operator applied to the anomaly score - same accepted values as threshold.op (above, below, equal, not_equal, above_or_equal, below_or_equal, outside_bounds)."`
	MatchType   string      `json:"matchType,omitempty" jsonschema:"v1 (anomaly_rule) only. Match type - same accepted values as threshold.matchType (at_least_once, all_the_times, on_average/avg, in_total/sum, last)."`
	Target      interface{} `json:"target,omitempty" jsonschema:"v1 (anomaly_rule) only. Threshold value compared against the anomaly score."`
	Algorithm   string      `json:"algorithm,omitempty" jsonschema:"v1 (anomaly_rule) only. Anomaly detection algorithm. Accepted values include standard (z-score based). Used only when ruleType=anomaly_rule."`
	Seasonality string      `json:"seasonality,omitempty" jsonschema:"v1 (anomaly_rule) only. Seasonality pattern for anomaly detection: hourly, daily, or weekly."`

	// Threshold configuration (v2alpha1 schema). Required for threshold_rule
	// and promql_rule unless alertOnAbsent is true. Omit for anomaly_rule.
	Thresholds *AlertThresholds `json:"thresholds,omitempty" jsonschema:"v2alpha1 only (threshold_rule, promql_rule). Each threshold level (critical, error, warning, info) can route to different notification channels. Required unless alertOnAbsent is true. Omit entirely for anomaly_rule - use condition.op/matchType/target there instead."`
}

// AlertCompositeQuery contains the queries that define what data to monitor.
type AlertCompositeQuery struct {
	QueryType QueryType    `json:"queryType" jsonschema:"Query type: builder for Query Builder or promql for PromQL or clickhouse_sql for ClickHouse SQL."`
	PanelType string       `json:"panelType,omitempty" jsonschema:"Panel type. Use graph for alerts. Defaults to graph."`
	Unit      string       `json:"unit,omitempty" jsonschema:"Unit of the queried data (Y-axis unit). Used for value formatting in alert messages and for unit conversion with targetUnit in thresholds. Common values: percent, ms, s, bytes, ns, reqps, ops."`
	Queries   []AlertQuery `json:"queries" jsonschema:"Array of queries. At least one query is required."`
}

// AlertQuery wraps a single query within the composite query.
type AlertQuery struct {
	Type string         `json:"type" jsonschema:"Query envelope type. Must match compositeQuery.queryType: builder → builder_query or builder_formula; promql → promql; clickhouse_sql → clickhouse_sql. Also accepted: builder_trace_operator for trace operator queries."`
	Spec AlertQuerySpec `json:"spec" jsonschema:"Query specification."`
}

// AlertQuerySpec is the specification for a single query within an alert.
// For builder_query type this uses the v5 query builder format.
// For promql/clickhouse_sql types only name query legend and disabled are used.
type AlertQuerySpec struct {
	Name         string               `json:"name" jsonschema:"Query name (e.g. A or B or C). Used as reference in formulas and selectedQueryName."`
	Signal       string               `json:"signal,omitempty" jsonschema:"Signal type for builder queries: metrics or logs or traces. Required for builder_query type."`
	StepInterval *int64               `json:"stepInterval,omitempty" jsonschema:"Step interval in seconds for time aggregation. Use 60 for metrics alerts."`
	Disabled     bool                 `json:"disabled,omitempty" jsonschema:"Whether this query is disabled."`
	Source       string               `json:"source,omitempty" jsonschema:"Data-source filter for metrics builder_query only. Set to meter to alert on Cost Meter (usage/billing) metrics such as signoz.meter.log.size; omit otherwise."`
	Aggregations []AlertAggregation   `json:"aggregations,omitempty" jsonschema:"Aggregation expressions for builder queries. For metrics signal use the object shape: [{metricName: k8s.pod.cpu_request_utilization, timeAggregation: avg, spaceAggregation: max}]. For logs/traces use the expression shape: [{expression: count()}] or [{expression: p99(duration_nano)}]."`
	Filter       *AlertQueryFilter    `json:"filter,omitempty" jsonschema:"Filter expression for builder queries. Example: {expression: service.name = frontend AND http.status_code >= 500}."`
	GroupBy      []AlertGroupByField  `json:"groupBy,omitempty" jsonschema:"Fields to group by. Grouped dimensions appear as labels in alert notifications."`
	Limit        int                  `json:"limit,omitempty" jsonschema:"Positive maximum number of result groups. Use 100 for standalone alert queries and formula results. Use 10000 for each builder query referenced by a formula because input limits are applied before formula evaluation."`
	Order        []AlertOrderField    `json:"order,omitempty" jsonschema:"Query Builder v5 result order. Use __result desc for metrics and formulas; use the primary aggregation descending for logs and traces. This is the wire field order, not dashboard editor orderBy."`
	Having       *AlertQueryFilter    `json:"having,omitempty" jsonschema:"Having clause to filter aggregation results."`
	Functions    []AlertQueryFunction `json:"functions,omitempty" jsonschema:"Post-query functions applied to the series. Required for anomaly_rule: wrap with {name: anomaly, args: [{name: z_score_threshold, value: 2}]}."`

	// For promql / clickhouse_sql query types
	Query  string `json:"query,omitempty" jsonschema:"PromQL or ClickHouse SQL query string. Used when queryType is promql or clickhouse_sql. PromQL with OTel dotted metric names MUST use the Prometheus 3.x UTF-8 quoted-selector form: {\"metric.name.with.dots\"}. Underscored / __name__ / bare-dotted forms return no data. Read signoz://promql/instructions for the full guide (histogram patterns dotted labels pre-flight checklist)."`
	Legend string `json:"legend,omitempty" jsonschema:"Legend template for the query."`

	// For builder_formula type
	Expression string `json:"expression,omitempty" jsonschema:"Formula expression referencing other query names (e.g. A / B * 100). Used for builder_formula type."`
}

// AlertQueryFunction applies a post-query transform to a builder query series.
// Most commonly used for anomaly detection on metrics.
type AlertQueryFunction struct {
	Name string                  `json:"name" jsonschema:"Function name (e.g. anomaly for ruleType=anomaly_rule)."`
	Args []AlertQueryFunctionArg `json:"args,omitempty" jsonschema:"Function arguments. Example for anomaly: [{name: z_score_threshold, value: 2}]."`
}

// AlertQueryFunctionArg is a single argument to an AlertQueryFunction.
type AlertQueryFunctionArg struct {
	Name  string      `json:"name" jsonschema:"Argument name (e.g. z_score_threshold)."`
	Value interface{} `json:"value,omitempty" jsonschema:"Argument value. Can be number, string, or bool depending on the function."`
}

// AlertAggregation represents an aggregation in a builder query.
// Use one of two shapes:
//   - Metrics signal: set MetricName, TimeAggregation, SpaceAggregation (Expression empty).
//   - Logs/traces signal: set Expression (metric fields empty).
type AlertAggregation struct {
	// For metrics signal.
	MetricName       string `json:"metricName,omitempty" jsonschema:"Metric name (metrics signal only). Example: k8s.pod.cpu_request_utilization. Use alongside timeAggregation and spaceAggregation. Do not set expression when using this shape."`
	TimeAggregation  string `json:"timeAggregation,omitempty" jsonschema:"Per-series time aggregation (metrics signal only). Common values: avg, max, min, sum, rate, increase, count, count_distinct, latest. Default by metric type: gauge→avg, cumulative counter→rate, delta counter→sum."`
	SpaceAggregation string `json:"spaceAggregation,omitempty" jsonschema:"Cross-series space aggregation (metrics signal only). Common values: sum, avg, min, max, count. For histograms use percentiles: p50, p75, p90, p95, p99."`

	// For logs/traces signal.
	Expression string `json:"expression,omitempty" jsonschema:"Aggregation expression (logs/traces signal). Examples: count(), avg(duration), p99(duration_nano), count_distinct(user_id), sum(bytes). Do not set metricName/timeAggregation/spaceAggregation when using this shape."`
}

// AlertQueryFilter holds a filter or having expression.
type AlertQueryFilter struct {
	Expression string `json:"expression" jsonschema:"Filter expression using field operators. Example: service.name = frontend AND http.status_code >= 500. Use empty string for no filter."`
}

// AlertGroupByField identifies a field to group by.
type AlertGroupByField struct {
	Name          string `json:"name" jsonschema:"Field name to group by (e.g. service.name or http.method or severity_text)."`
	FieldContext  string `json:"fieldContext,omitempty" jsonschema:"Field context: resource for resource attributes or tag for span/log attributes. Required for non-top-level fields."`
	FieldDataType string `json:"fieldDataType,omitempty" jsonschema:"Data type of the field: string or int64 or float64 or bool."`
}

// AlertOrderField specifies ordering for query results.
type AlertOrderField struct {
	Key       AlertOrderKey `json:"key"`
	Direction string        `json:"direction" jsonschema:"Sort direction: asc or desc."`
}

// AlertOrderKey identifies the field to order by.
type AlertOrderKey struct {
	Name string `json:"name" jsonschema:"Field or aggregation expression to order by (e.g. timestamp or count())."`
}

// AlertThresholds holds multi-threshold configuration for v2 schema alerts.
type AlertThresholds struct {
	Kind string           `json:"kind" jsonschema:"Threshold kind. Currently only basic is supported."`
	Spec []BasicThreshold `json:"spec" jsonschema:"Array of threshold specifications. Each threshold can route to different channels."`
}

// BasicThreshold defines a single threshold level with routing.
type BasicThreshold struct {
	Name           string   `json:"name" jsonschema:"Threshold tier: critical, error, warning, or info. Also used as the routing label - alerts carry threshold_name equal to this value."`
	Target         *float64 `json:"target" jsonschema:"Threshold value to compare against."`
	TargetUnit     string   `json:"targetUnit,omitempty" jsonschema:"Unit of the threshold target value. If different from compositeQuery.unit the backend converts between them during evaluation. Common values: percent, ms, s, bytes, ns."`
	RecoveryTarget *float64 `json:"recoveryTarget,omitempty" jsonschema:"Hysteresis - value at which a firing alert is considered resolved. Useful to avoid flapping near the threshold (e.g. target=80 percent, recoveryTarget=75 percent). Use null to use the threshold target itself as the recovery point."`
	MatchType      string   `json:"matchType" jsonschema:"How to evaluate the threshold. Canonical: at_least_once, all_the_times, on_average, in_total, last. Aliases accepted: avg (=on_average), sum (=in_total). Numeric 1-5 also accepted but discouraged."`
	CompareOp      string   `json:"op" jsonschema:"Comparison operator. Canonical literals: above, below, equal, not_equal, above_or_equal, below_or_equal, outside_bounds. Short forms accepted: eq, not_eq, above_or_eq, below_or_eq. Symbolic accepted: >, <, =, !=, >=, <=. Numeric 1-7 also accepted but discouraged."`
	Channels       []string `json:"channels,omitempty" jsonschema:"Notification channel names for this threshold tier. Use signoz_list_notification_channels to discover available names. Ignored when notificationSettings.usePolicy is true."`
}

// AlertEvaluation holds the evaluation schedule for v2 schema alerts.
type AlertEvaluation struct {
	Kind string              `json:"kind" jsonschema:"Evaluation kind: rolling (sliding lookback window) or cumulative (accumulates from a fixed daily/monthly reset boundary). Cumulative works for any period-total alert (e.g. daily error counts, monthly request budgets); Cost Meter spend budgets are one common use."`
	Spec AlertEvaluationSpec `json:"spec" jsonschema:"Evaluation specification. For kind=rolling set evalWindow + frequency; for kind=cumulative set schedule + frequency + timezone."`
}

// AlertEvaluationSpec defines the evaluation window. Rolling uses evalWindow+frequency;
// cumulative uses schedule+frequency+timezone.
type AlertEvaluationSpec struct {
	EvalWindow string                   `json:"evalWindow,omitempty" jsonschema:"Rolling kind only. Evaluation window as a Go duration string (e.g. 5m, 15m, 30m, 1h, 4h, 24h)."`
	Frequency  string                   `json:"frequency" jsonschema:"Evaluation frequency as a Go duration string (e.g. 1m, 5m, 15m)."`
	Schedule   *AlertEvaluationSchedule `json:"schedule,omitempty" jsonschema:"Cumulative kind only. Fixed reset boundary the accumulation window starts from."`
	Timezone   string                   `json:"timezone,omitempty" jsonschema:"Cumulative kind only. IANA timezone for the schedule boundary (e.g. UTC)."`
}

// AlertEvaluationSchedule is the reset schedule for a cumulative evaluation window.
type AlertEvaluationSchedule struct {
	Type   string `json:"type" jsonschema:"Reset cadence: daily or monthly."`
	Minute int    `json:"minute" jsonschema:"Minute of the reset boundary (0-59); e.g. 0 for the top of the hour."`
	Hour   int    `json:"hour" jsonschema:"Hour of the reset boundary (0-23); e.g. 0 for midnight."`
}

// NotificationSettings controls alert notification behavior for v2alpha1 rules.
type NotificationSettings struct {
	GroupBy           []string  `json:"groupBy,omitempty" jsonschema:"Fields to group alert notifications by (e.g. service.name, k8s.namespace.name). Reduces notification noise by batching alerts with the same group key."`
	NewGroupEvalDelay string    `json:"newGroupEvalDelay,omitempty" jsonschema:"Grace period (Go duration string, e.g. 2m) during which a newly-appearing label group is excluded from evaluation. Helps avoid flapping when new pods/services come online."`
	Renotify          *Renotify `json:"renotify,omitempty" jsonschema:"Re-notification configuration."`
	UsePolicy         bool      `json:"usePolicy,omitempty" jsonschema:"Routing mode. false (default) = deliver to channels listed in each threshold entry. true = ignore per-threshold channels and route via the org-level notification policy matching on labels."`
}

// Renotify controls re-notification behavior.
type Renotify struct {
	Enabled     bool     `json:"enabled" jsonschema:"Whether re-notification is enabled."`
	Interval    string   `json:"interval,omitempty" jsonschema:"Re-notification interval as a Go duration string (e.g. 15m, 30m, 1h, 4h)."`
	AlertStates []string `json:"alertStates,omitempty" jsonschema:"Alert states that trigger re-notification. Accepted values: firing, nodata. Other values are rejected."`
}
