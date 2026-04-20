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

// AlertRule is the payload for creating an alert rule via POST /api/v1/rules.
// It matches the SigNoz PostableRule structure.
type AlertRule struct {
	Alert             string            `json:"alert" jsonschema:"required" jsonschema_extras:"description=Name of the alert rule. Must be unique and descriptive."`
	AlertType         AlertType         `json:"alertType" jsonschema:"required" jsonschema_extras:"description=Signal type: METRIC_BASED_ALERT or LOGS_BASED_ALERT or TRACES_BASED_ALERT or EXCEPTIONS_BASED_ALERT."`
	RuleType          RuleType          `json:"ruleType" jsonschema:"required" jsonschema_extras:"description=Evaluation type: threshold_rule (compare against value) or promql_rule (PromQL expression) or anomaly_rule (anomaly detection on metrics)."`
	Description       string            `json:"description,omitempty" jsonschema_extras:"description=Human-readable description of what this alert monitors."`
	Condition         AlertCondition    `json:"condition" jsonschema:"required" jsonschema_extras:"description=Alert condition containing the query and threshold configuration."`
	Labels            map[string]string `json:"labels,omitempty" jsonschema_extras:"description=Labels for the alert rule. MUST include severity (info or warning or critical). Labels enable routing policies - use labels like team or service or environment for routing."`
	Annotations       map[string]string `json:"annotations,omitempty" jsonschema_extras:"description=Annotations like description and summary. Supports template variables: {{$value}} for current metric value and {{$threshold}} for the threshold and {{$labels.key}} for label values."`
	Disabled          bool              `json:"disabled,omitempty" jsonschema_extras:"description=Whether the alert rule is disabled. Defaults to false (enabled)."`
	Source            string            `json:"source,omitempty" jsonschema_extras:"description=Source URL for the alert. Set automatically."`
	PreferredChannels []string          `json:"preferredChannels,omitempty" jsonschema_extras:"description=Notification channel names to send alerts to. Use signoz_list_notification_channels to discover available channel names."`
	Version           string            `json:"version,omitempty" jsonschema_extras:"description=API version. Always v5. Set automatically if omitted."`

	// v2 schema fields
	Evaluation           *AlertEvaluation      `json:"evaluation,omitempty" jsonschema_extras:"description=Evaluation configuration. Specifies eval window and frequency. Auto-generated with defaults (5m0s window and 1m0s frequency) if omitted."`
	SchemaVersion        string                `json:"schemaVersion,omitempty" jsonschema_extras:"description=Schema version. Always set to v2alpha1 automatically."`
	NotificationSettings *NotificationSettings `json:"notificationSettings,omitempty" jsonschema_extras:"description=Notification settings. Controls grouping and re-notification behavior. Auto-generated with defaults if omitted."`
}

// AlertCondition defines when an alert should fire.
type AlertCondition struct {
	CompositeQuery AlertCompositeQuery `json:"compositeQuery" jsonschema:"required" jsonschema_extras:"description=The composite query defining what data to monitor."`

	SelectedQuery string `json:"selectedQueryName,omitempty" jsonschema_extras:"description=Which query name triggers the alert (e.g. A or B or F1). Required when multiple queries exist. Defaults to the first query name."`

	// Absent data alerting
	AlertOnAbsent     bool   `json:"alertOnAbsent,omitempty" jsonschema_extras:"description=Alert when no data is received within the evaluation window."`
	AbsentFor         uint64 `json:"absentFor,omitempty" jsonschema_extras:"description=Duration in milliseconds to wait before firing an absent-data alert."`
	RequireMinPoints  bool   `json:"requireMinPoints,omitempty" jsonschema_extras:"description=Require a minimum number of data points before evaluating the condition."`
	RequiredNumPoints int    `json:"requiredNumPoints,omitempty" jsonschema_extras:"description=Minimum number of data points required when requireMinPoints is true."`

	// Anomaly detection fields (for ruleType=anomaly_rule)
	Algorithm   string `json:"algorithm,omitempty" jsonschema_extras:"description=Anomaly detection algorithm. Currently only zscore is supported."`
	Seasonality string `json:"seasonality,omitempty" jsonschema_extras:"description=Seasonality pattern for anomaly detection: hourly or daily or weekly."`

	// Threshold configuration (v2alpha1 schema).
	Thresholds *AlertThresholds `json:"thresholds,omitempty" jsonschema_extras:"description=Threshold configuration (v2alpha1 schema). Each threshold level (critical or warning or info) can route to different notification channels. Required unless alertOnAbsent is true."`
}

// AlertCompositeQuery contains the queries that define what data to monitor.
type AlertCompositeQuery struct {
	QueryType QueryType    `json:"queryType" jsonschema:"required" jsonschema_extras:"description=Query type: builder for Query Builder or promql for PromQL or clickhouse_sql for ClickHouse SQL."`
	PanelType string       `json:"panelType,omitempty" jsonschema_extras:"description=Panel type. Use graph for alerts. Defaults to graph."`
	Unit      string       `json:"unit,omitempty" jsonschema_extras:"description=Unit of the queried data (Y-axis unit). Used for value formatting in alert messages and for unit conversion with targetUnit in thresholds. Common values: percent, ms, s, bytes, ns, reqps, ops."`
	Queries   []AlertQuery `json:"queries" jsonschema:"required" jsonschema_extras:"description=Array of queries. At least one query is required."`
}

// AlertQuery wraps a single query within the composite query.
type AlertQuery struct {
	Type string         `json:"type" jsonschema:"required" jsonschema_extras:"description=Query type: builder_query for data queries or builder_formula for formulas combining queries."`
	Spec AlertQuerySpec `json:"spec" jsonschema:"required" jsonschema_extras:"description=Query specification."`
}

// AlertQuerySpec is the specification for a single query within an alert.
// For builder_query type this uses the v5 query builder format.
// For promql/clickhouse_sql types only name query legend and disabled are used.
type AlertQuerySpec struct {
	Name         string              `json:"name" jsonschema:"required" jsonschema_extras:"description=Query name (e.g. A or B or C). Used as reference in formulas and selectedQueryName."`
	Signal       string              `json:"signal,omitempty" jsonschema_extras:"description=Signal type for builder queries: metrics or logs or traces. Required for builder_query type."`
	StepInterval *int64              `json:"stepInterval,omitempty" jsonschema_extras:"description=Step interval in seconds for time aggregation. Use 60 for metrics alerts."`
	Disabled     bool                `json:"disabled,omitempty" jsonschema_extras:"description=Whether this query is disabled."`
	Source       string              `json:"source,omitempty"`
	Aggregations []AlertAggregation  `json:"aggregations,omitempty" jsonschema_extras:"description=Aggregation expressions for builder queries. Example: [{expression: count()}] or [{expression: avg(duration)}]."`
	Filter       *AlertQueryFilter   `json:"filter,omitempty" jsonschema_extras:"description=Filter expression for builder queries. Example: {expression: service.name = frontend AND http.status_code >= 500}."`
	GroupBy      []AlertGroupByField `json:"groupBy,omitempty" jsonschema_extras:"description=Fields to group by. Grouped dimensions appear as labels in alert notifications."`
	Order        []AlertOrderField   `json:"order,omitempty" jsonschema_extras:"description=Order by specification."`
	Having       *AlertQueryFilter   `json:"having,omitempty" jsonschema_extras:"description=Having clause to filter aggregation results."`

	// For promql / clickhouse_sql query types
	Query  string `json:"query,omitempty" jsonschema_extras:"description=PromQL or ClickHouse SQL query string. Used when queryType is promql or clickhouse_sql."`
	Legend string `json:"legend,omitempty" jsonschema_extras:"description=Legend template for the query."`

	// For builder_formula type
	Expression string `json:"expression,omitempty" jsonschema_extras:"description=Formula expression referencing other query names (e.g. A / B * 100). Used for builder_formula type."`
}

// AlertAggregation represents an aggregation expression in a builder query.
type AlertAggregation struct {
	Expression string `json:"expression" jsonschema:"required" jsonschema_extras:"description=Aggregation expression. Examples: count() or avg(duration) or p99(durationNano) or count_distinct(user_id) or sum(bytes)."`
}

// AlertQueryFilter holds a filter or having expression.
type AlertQueryFilter struct {
	Expression string `json:"expression" jsonschema_extras:"description=Filter expression using field operators. Example: service.name = frontend AND http.status_code >= 500. Use empty string for no filter."`
}

// AlertGroupByField identifies a field to group by.
type AlertGroupByField struct {
	Name          string `json:"name" jsonschema:"required" jsonschema_extras:"description=Field name to group by (e.g. service.name or http.method or severity_text)."`
	FieldContext  string `json:"fieldContext,omitempty" jsonschema_extras:"description=Field context: resource for resource attributes or tag for span/log attributes. Required for non-top-level fields."`
	FieldDataType string `json:"fieldDataType,omitempty" jsonschema_extras:"description=Data type of the field: string or int64 or float64 or bool."`
}

// AlertOrderField specifies ordering for query results.
type AlertOrderField struct {
	Key       AlertOrderKey `json:"key" jsonschema:"required"`
	Direction string        `json:"direction" jsonschema:"required" jsonschema_extras:"description=Sort direction: asc or desc."`
}

// AlertOrderKey identifies the field to order by.
type AlertOrderKey struct {
	Name string `json:"name" jsonschema:"required" jsonschema_extras:"description=Field or aggregation expression to order by (e.g. timestamp or count())."`
}

// AlertThresholds holds multi-threshold configuration for v2 schema alerts.
type AlertThresholds struct {
	Kind string           `json:"kind" jsonschema:"required" jsonschema_extras:"description=Threshold kind. Currently only basic is supported."`
	Spec []BasicThreshold `json:"spec" jsonschema:"required" jsonschema_extras:"description=Array of threshold specifications. Each threshold can route to different channels."`
}

// BasicThreshold defines a single threshold level with routing.
type BasicThreshold struct {
	Name           string   `json:"name" jsonschema:"required" jsonschema_extras:"description=Threshold name: critical or warning or info."`
	Target         *float64 `json:"target" jsonschema:"required" jsonschema_extras:"description=Threshold value to compare against."`
	TargetUnit     string   `json:"targetUnit,omitempty" jsonschema_extras:"description=Unit of the threshold target value. If different from compositeQuery.unit the backend converts between them during evaluation. Common values: percent, ms, s, bytes, ns."`
	RecoveryTarget *float64 `json:"recoveryTarget" jsonschema_extras:"description=Value below which the alert is considered recovered. Use null if not needed."`
	MatchType      string   `json:"matchType" jsonschema:"required" jsonschema_extras:"description=How to evaluate: 1=at_least_once 2=all_the_times 3=on_average 4=in_total 5=last."`
	CompareOp      string   `json:"op" jsonschema:"required" jsonschema_extras:"description=Comparison operator: 1=above 2=below 3=equal 4=not_equal."`
	Channels       []string `json:"channels,omitempty" jsonschema_extras:"description=Notification channel names for this threshold level. Use signoz_list_notification_channels to discover available names."`
}

// AlertEvaluation holds the evaluation schedule for v2 schema alerts.
type AlertEvaluation struct {
	Kind string              `json:"kind" jsonschema:"required" jsonschema_extras:"description=Evaluation kind. Currently only rolling is supported."`
	Spec AlertEvaluationSpec `json:"spec" jsonschema:"required" jsonschema_extras:"description=Evaluation specification."`
}

// AlertEvaluationSpec defines the evaluation window and frequency.
type AlertEvaluationSpec struct {
	EvalWindow string `json:"evalWindow" jsonschema:"required" jsonschema_extras:"description=Evaluation window as a Go duration string (e.g. 5m0s 10m0s 15m0s 1h0m0s)."`
	Frequency  string `json:"frequency" jsonschema:"required" jsonschema_extras:"description=Evaluation frequency as a Go duration string (e.g. 1m0s 5m0s)."`
}

// NotificationSettings controls alert notification behavior for v2 schema.
type NotificationSettings struct {
	GroupBy   []string  `json:"groupBy,omitempty" jsonschema_extras:"description=Fields to group alert notifications by (e.g. service.name). Reduces notification noise by batching alerts with the same group key."`
	Renotify  *Renotify `json:"renotify,omitempty" jsonschema_extras:"description=Re-notification configuration."`
	UsePolicy bool      `json:"usePolicy,omitempty" jsonschema_extras:"description=Whether to use routing policies for notification routing."`
}

// Renotify controls re-notification behavior.
type Renotify struct {
	Enabled     bool     `json:"enabled" jsonschema_extras:"description=Whether re-notification is enabled."`
	Interval    string   `json:"interval,omitempty" jsonschema_extras:"description=Re-notification interval as a Go duration string (e.g. 30m 4h0m0s)."`
	AlertStates []string `json:"alertStates,omitempty" jsonschema_extras:"description=Alert states that trigger re-notification: firing or nodata."`
}
