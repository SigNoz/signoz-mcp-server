package types

type UpdateDashboardInput struct {
	// id and uuid are both optional properties (json ",omitempty", neither
	// required) so additionalProperties:false accepts either key; the handler
	// requires exactly one via readResourceID (canonical "id" wins).
	ID            string    `json:"id,omitempty" jsonschema:"Dashboard UUID to update (required)."`
	LegacyUUID    string    `json:"uuid,omitempty" jsonschema:"Deprecated alias for 'id'."`
	Dashboard     Dashboard `json:"dashboard" jsonschema:"Complete dashboard definition representing the post-update state. Start from signoz_get_dashboard and preserve every field the user did not ask to change."`
	SearchContext string    `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

type CreateDashboardInput struct {
	Dashboard
	SearchContext string `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

type Dashboard struct {
	Title       string              `json:"title" jsonschema:"The display name of the dashboard."`
	Description string              `json:"description,omitempty" jsonschema:"Concise explanation of the operational questions this dashboard answers."`
	Tags        []string            `json:"tags,omitempty" jsonschema:"Free-form categorization tags, for example performance or latency."`
	Layout      []LayoutItem        `json:"layout" jsonschema:"Grid positions for widgets on a 12-column layout. Each non-row widget ID must have one matching layout item; the server auto-generates layout only when this array is empty."`
	Variables   map[string]Variable `json:"variables,omitempty" jsonschema:"Map keyed by variable name. Query widgets reference variables with a dollar-sign prefix, for example $service_name."`
	Widgets     []Widget            `json:"widgets" jsonschema:"Dashboard panels. Each typed widget needs a unique ID, title, panelTypes value, and query. The current MCP schema requires the query envelope even for row separators."`
}

type LayoutItem struct {
	X           int    `json:"x" jsonschema:"Zero-based horizontal grid coordinate; x + w cannot exceed 12."`
	Y           int    `json:"y" jsonschema:"Zero-based vertical grid coordinate."`
	W           int    `json:"w" jsonschema:"Widget width in grid columns; the full grid is 12 columns."`
	H           int    `json:"h" jsonschema:"Widget height in grid rows."`
	I           string `json:"i" jsonschema:"Widget ID positioned by this item. Must exactly match one widgets[].id and be unique in the layout."`
	Moved       bool   `json:"moved,omitempty" jsonschema:"Frontend layout state; normally false or omitted."`
	Static      bool   `json:"static,omitempty" jsonschema:"Whether the widget is fixed in the grid. Default false."`
	MaxH        int    `json:"maxH,omitempty" jsonschema:"Frontend-only maximum-height hint; the current dashboard write normalizer does not persist it."`
	MinH        int    `json:"minH,omitempty" jsonschema:"Frontend-only minimum-height hint; the current dashboard write normalizer does not persist it."`
	MinW        int    `json:"minW,omitempty" jsonschema:"Frontend-only minimum-width hint; the current dashboard write normalizer does not persist it."`
	IsDraggable bool   `json:"isDraggable,omitempty" jsonschema:"Frontend-only drag-state hint; the current dashboard write normalizer does not persist it."`
}

type Variable struct {
	ID                        string       `json:"id,omitempty" jsonschema:"Stable variable UUID. The server generates one when omitted."`
	Name                      string       `json:"name,omitempty" jsonschema:"Variable name shown in the UI. Defaults to the variables map key."`
	Description               string       `json:"description,omitempty" jsonschema:"Concise statement of what this variable controls."`
	Key                       string       `json:"key,omitempty" jsonschema:"Frontend-only variable-key alias; the current write normalizer derives identity from the variables map key and does not persist this field."`
	Type                      VariableType `json:"type,omitempty" jsonschema:"Variable type: QUERY, TEXTBOX, DYNAMIC, or CUSTOM. Defaults to DYNAMIC when omitted."`
	QueryValue                string       `json:"queryValue,omitempty" jsonschema:"Query expression used by a QUERY variable."`
	AllSelected               bool         `json:"allSelected,omitempty" jsonschema:"Frontend-only all-values selection state; the current dashboard write normalizer does not persist it."`
	CustomValue               string       `json:"customValue,omitempty" jsonschema:"Comma-separated or UI-encoded values for a CUSTOM variable."`
	MultiSelect               bool         `json:"multiSelect,omitempty" jsonschema:"Whether multiple values may be selected. Defaults to true for DYNAMIC variables when omitted."`
	Order                     int          `json:"order,omitempty" jsonschema:"Zero-based display order. Generated from map iteration order when omitted, so set it explicitly for deterministic ordering."`
	ShowALLOption             bool         `json:"showALLOption,omitempty" jsonschema:"Whether to expose an all-values choice. Defaults to true for DYNAMIC variables when omitted."`
	Sort                      VariableSort `json:"sort,omitempty" jsonschema:"Value sorting: ASC, DESC, or DISABLED. Defaults to ASC for DYNAMIC variables and DISABLED otherwise."`
	TextboxValue              string       `json:"textboxValue,omitempty" jsonschema:"Current text for a TEXTBOX variable."`
	ModificationUUID          string       `json:"modificationUUID,omitempty" jsonschema:"Frontend-only modification token; the current dashboard write normalizer does not persist it."`
	SelectedValue             interface{}  `json:"selectedValue,omitempty" jsonschema:"Current selected value or values; preserve the shape returned by signoz_get_dashboard on update."`
	DefaultValue              string       `json:"defaultValue,omitempty" jsonschema:"Default value applied when the variable has no explicit selection."`
	DynamicVariablesAttribute string       `json:"dynamicVariablesAttribute,omitempty" jsonschema:"Attribute name populated by a DYNAMIC variable, for example service.name."`
	DynamicVariablesSource    string       `json:"dynamicVariablesSource,omitempty" jsonschema:"Signal source for a DYNAMIC variable: Traces, Logs, Metrics, or All telemetry. Legacy casing and all sources are normalized on write."`
	HaveCustomValuesSelected  bool         `json:"haveCustomValuesSelected,omitempty" jsonschema:"Frontend-only custom-value selection state; the current dashboard write normalizer does not persist it."`
}

type Widget struct {
	ID                    string             `json:"id" jsonschema:"Unique widget ID. The matching layout item uses this value in layout[].i."`
	Description           string             `json:"description,omitempty" jsonschema:"Concise explanation of what the widget measures or lists."`
	IsStacked             bool               `json:"isStacked,omitempty" jsonschema:"Frontend-only stacking flag that the current write normalizer does not persist. Use stackedBarChart for bar panels."`
	NullZeroValues        string             `json:"nullZeroValues,omitempty" jsonschema:"How absent numeric points are rendered. Defaults to zero when omitted."`
	Opacity               string             `json:"opacity,omitempty" jsonschema:"Numeric opacity encoded as a string. Defaults to 1."`
	PanelTypes            PanelType          `json:"panelTypes" jsonschema:"Panel type: graph, value, table, list, trace, bar, pie, histogram, or row. Runtime treats row as a separator, but the current MCP input schema still requires its query envelope."`
	TimePreferance        TimePreferance     `json:"timePreferance,omitempty" jsonschema:"Time range mode. Use GLOBAL_TIME; this intentionally matches the frontend's timePreferance spelling and defaults to GLOBAL_TIME."`
	Title                 string             `json:"title" jsonschema:"Widget title displayed on the dashboard."`
	YAxisUnit             string             `json:"yAxisUnit,omitempty" jsonschema:"SigNoz unit identifier for values and the y-axis, for example ms, s, bytes, percentunit, or none."`
	Query                 WidgetQuery        `json:"query" jsonschema:"Complete widget query. Choose exactly one queryType and populate its matching builder, clickhouse_sql, or promql envelope."`
	BucketCount           int                `json:"bucketCount,omitempty" jsonschema:"Number of histogram buckets; histogram default is 30."`
	BucketWidth           int                `json:"bucketWidth,omitempty" jsonschema:"Optional fixed histogram bucket width in the widget's yAxisUnit."`
	ColumnUnits           map[string]string  `json:"columnUnits,omitempty" jsonschema:"Table column-name to SigNoz unit mapping."`
	FillSpans             bool               `json:"fillSpans,omitempty" jsonschema:"Whether a timeseries fills gaps between data points. Default false."`
	MergeAllActiveQueries bool               `json:"mergeAllActiveQueries,omitempty" jsonschema:"Whether histogram results from all active queries are merged. Default false."`
	SelectedLogFields     []SelectedLogField `json:"selectedLogFields" jsonschema:"Columns shown by a logs list panel. Leave empty when not applicable."`
	SelectedTracesFields  []AttributeKey     `json:"selectedTracesFields" jsonschema:"Columns shown by a traces list panel. Leave empty when not applicable."`
	SoftMax               interface{}        `json:"softMax,omitempty" jsonschema:"Optional soft upper display bound in yAxisUnit; it does not filter data."`
	SoftMin               interface{}        `json:"softMin,omitempty" jsonschema:"Optional soft lower display bound in yAxisUnit; it does not filter data."`
	StackedBarChart       bool               `json:"stackedBarChart,omitempty" jsonschema:"Whether a bar panel stacks grouped series. Default false."`
	Thresholds            []Threshold        `json:"thresholds" jsonschema:"Visual thresholds. These color a widget; they do not create alert rules."`
	IsLogScale            bool               `json:"isLogScale,omitempty" jsonschema:"Whether supported charts use a logarithmic y-axis. Default false."`
	ColumnWidths          map[string]int     `json:"columnWidths,omitempty" jsonschema:"Table column-name to pixel-width mapping."`
	CustomLegendColors    map[string]string  `json:"customLegendColors,omitempty" jsonschema:"Series/query name to hex color mapping, for example A to #3366FF."`
	LegendPosition        string             `json:"legendPosition,omitempty" jsonschema:"Legend position: bottom or right. Omit when the panel type has no legend."`
	ContextLinks          ContextLinks       `json:"contextLinks" jsonschema:"Links shown from this widget to related SigNoz or external context."`
	DecimalPrecision      int                `json:"decimalPrecision,omitempty" jsonschema:"Number of decimal places to display. Omit to use the frontend default."`
	QueryData             interface{}        `json:"queryData,omitempty" jsonschema:"Frontend-only query state; the current dashboard write normalizer does not persist it. Use query for authored widgets."`
	QueryType             interface{}        `json:"queryType,omitempty" jsonschema:"Frontend-only query-type state; the current dashboard write normalizer does not persist it. Use query.queryType for authored widgets."`
}

type ContextLinks struct {
	LinksData []interface{} `json:"linksData" jsonschema:"Context-link definitions. Preserve entries returned by signoz_get_dashboard; use an empty list when no links are configured."`
}

type Threshold struct {
	Index                 string      `json:"index,omitempty" jsonschema:"Stable identifier for this threshold within the widget."`
	IsEditEnabled         bool        `json:"isEditEnabled,omitempty" jsonschema:"Frontend edit state; normally false or omitted."`
	KeyIndex              int         `json:"keyIndex,omitempty" jsonschema:"Frontend ordering index for this threshold."`
	SelectedGraph         string      `json:"selectedGraph,omitempty" jsonschema:"Query or series the threshold applies to; preserve it when updating an existing threshold."`
	ThresholdColor        string      `json:"thresholdColor,omitempty" jsonschema:"Hex color for the threshold (e.g. #FF0000)."`
	ThresholdFormat       string      `json:"thresholdFormat,omitempty" jsonschema:"How the threshold is rendered. Allowed values: 'Text' or 'Background'. SigNoz does NOT support a Grafana-style 'Line' marker; do not use 'Line'. 'Background' tints the panel area when the operator+value condition holds; 'Text' colors the threshold value label only."`
	ThresholdLabel        string      `json:"thresholdLabel,omitempty" jsonschema:"Optional display label for the threshold."`
	ThresholdOperator     string      `json:"thresholdOperator,omitempty" jsonschema:"Comparison operator. Allowed values: '>', '<', '>=', '<=', '='."`
	ThresholdTableOptions string      `json:"thresholdTableOptions,omitempty" jsonschema:"Table threshold display option; preserve the value returned by signoz_get_dashboard."`
	ThresholdUnit         string      `json:"thresholdUnit,omitempty" jsonschema:"Unit for the threshold value (should match the panel's yAxisUnit)."`
	ThresholdValue        interface{} `json:"thresholdValue,omitempty" jsonschema:"Numeric value the operator is compared against."`
}

type SelectedLogField struct {
	DataType      string `json:"dataType,omitempty" jsonschema:"Underlying field data type, for example string, int64, or bool."`
	Name          string `json:"name,omitempty" jsonschema:"Log field name displayed as a list column."`
	Type          string `json:"type,omitempty" jsonschema:"Attribute type, such as resource, tag, or log."`
	FieldContext  string `json:"fieldContext,omitempty" jsonschema:"Field namespace, such as resource or log."`
	FieldDataType string `json:"fieldDataType,omitempty" jsonschema:"Frontend field data type when it differs from dataType."`
	IsIndexed     bool   `json:"isIndexed,omitempty" jsonschema:"Whether the target tenant indexes this field."`
	Signal        string `json:"signal,omitempty" jsonschema:"Signal owning the field. Use logs for selectedLogFields."`
	IsColumn      bool   `json:"isColumn,omitempty" jsonschema:"Whether the field is a materialized column."`
	IsJSON        bool   `json:"isJSON,omitempty" jsonschema:"Whether the field contains JSON values."`
}

type WidgetQuery struct {
	QueryType     QueryType             `json:"queryType" jsonschema:"Query engine: builder, clickhouse_sql, or promql. Populate the matching sibling field and leave the other query arrays empty."`
	PromQL        []PromQL              `json:"promql" jsonschema:"PromQL queries when queryType is promql. Read signoz://promql/instructions before composing dotted OTel metric names."`
	ClickHouseSQL []ClickHouseSQL       `json:"clickhouse_sql" jsonschema:"Raw ClickHouse SQL queries when queryType is clickhouse_sql. Read the signal-specific schema and examples resources first."`
	Builder       BuilderQueryDashboard `json:"builder" jsonschema:"Query Builder queries and formulas when queryType is builder. Read signoz://dashboard/query-builder-example first."`
	ID            string                `json:"id,omitempty" jsonschema:"Stable frontend query UUID. The server generates one when omitted."`
}

type PromQL struct {
	Query    string `json:"query" jsonschema:"PromQL query expression. For OTel metrics with dots in the name use the Prometheus 3.x UTF-8 quoted-selector form: {\"metric.name.with.dots\"}. Underscored / __name__ / bare-dotted forms return no data in SigNoz. Read signoz://promql/instructions for the full guide."`
	Name     string `json:"name" jsonschema:"Query reference name, conventionally A, B, and so on."`
	Disabled bool   `json:"disabled" jsonschema:"Whether this base query is hidden from panel output. Set true when it only feeds a formula."`
	Legend   string `json:"legend,omitempty" jsonschema:"Legend template for naming PromQL series. Use {{label_name}} placeholders matching labels returned by the query. REQUIRED for grouped or multi-series charts. Example: {{service_name}} or {{service_name}} - {{instance}}. Without legend charts show generic series names."`
}

type ClickHouseSQL struct {
	Query    string `json:"query" jsonschema:"Raw ClickHouse SQL. Return a timestamp and value for timeseries panels and use the exact bundled-or-tenant schema column names."`
	Name     string `json:"name" jsonschema:"Query reference name, conventionally A, B, and so on."`
	Disabled bool   `json:"disabled" jsonschema:"Whether this base query is hidden from panel output. Set true when it only feeds a formula."`
	Legend   string `json:"legend,omitempty" jsonschema:"Legend template for naming ClickHouse query series. Use {{column_name}} placeholders for label columns returned by the query result. REQUIRED for grouped or multi-series charts. Example: {{service_name}} or {{service_name}} - {{http_method}}. Only columns present in the result can be used in the legend."`
}

type BuilderQueryDashboard struct {
	QueryData          []BuilderQuery `json:"queryData" jsonschema:"Base Query Builder queries. Include at least one when queryType is builder; formulas refer to their queryName values."`
	QueryFormulas      []BuilderQuery `json:"queryFormulas" jsonschema:"Derived formula queries, for example A/B. Their expression references base queryName values; normally set their result limit to 100."`
	QueryTraceOperator []interface{}  `json:"queryTraceOperator,omitempty" jsonschema:"Trace-operator definitions used by specialized trace queries. Preserve returned entries on update; otherwise omit."`
}

type BuilderQuery struct {
	QueryName          string            `json:"queryName" jsonschema:"Unique query reference, conventionally A, B, and so on. Formulas reference this name."`
	StepInterval       *int64            `json:"stepInterval" jsonschema:"Time bucket width in seconds. Use 0 for raw list queries; choose a positive interval for timeseries queries."`
	DataSource         DataSource        `json:"dataSource" jsonschema:"Signal queried by this builder entry: metrics, logs, or traces."`
	AggregateOperator  AggregateOperator `json:"aggregateOperator,omitempty" jsonschema:"Aggregation applied to aggregateAttribute. Stable common values include noop, count, count_distinct, sum, avg, min, max, p50, p75, p90, p95, p99, rate, rate_sum, rate_avg, rate_min, and rate_max; valid values depend on dataSource."`
	AggregateAttribute AttributeKey      `json:"aggregateAttribute,omitempty" jsonschema:"Field aggregated by aggregateOperator. Leave empty for count() or when aggregations supplies the v5 metric shape."`
	Temporality        Temporality       `json:"temporality,omitempty" jsonschema:"Metric temporality: Unspecified, Delta, or Cumulative. Omit for logs and traces."`
	Filters            FilterSet         `json:"filters,omitempty" jsonschema:"Structured filter tree. When filter.expression is also set, both representations must contain the same field predicates."`
	GroupBy            []AttributeKey    `json:"groupBy" jsonschema:"Attributes that split results into series or rows. Add a legend with matching placeholders for grouped chart queries."`
	Expression         string            `json:"expression" jsonschema:"Query reference or formula expression. Base queries conventionally use their queryName, such as A; formulas use expressions such as A/B."`
	Disabled           bool              `json:"disabled,omitempty" jsonschema:"Whether this query is hidden from panel output. Disable base queries that only feed a formula."`
	Having             interface{}       `json:"having,omitempty" jsonschema:"Post-aggregation predicate. For writes use an array of clauses, or an empty array when no having filter is needed; the server normalizes the empty object shape returned by some GET responses."`
	Legend             string            `json:"legend,omitempty" jsonschema:"Legend template for labeling grouped chart series. Use {{attribute_name}} placeholders that exactly match groupBy keys. REQUIRED when this query uses groupBy and is rendered as a multi-series chart for timeseries/graph or bar or pie or histogram. Example: if groupBy includes service.name then set legend to {{service.name}}. For multiple keys use {{service.name}} - {{http.method}}. Without legend SigNoz shows raw query identifiers such as A."`
	Limit              uint64            `json:"limit,omitempty" jsonschema:"Maximum result groups. Use 100 for displayed aggregate/formula results and 10000 for base queries feeding a formula."`
	Offset             uint64            `json:"offset,omitempty" jsonschema:"Zero-based row offset for list pagination. Default 0."`
	PageSize           uint64            `json:"pageSize,omitempty" jsonschema:"Rows requested per list-panel page; normally 100."`
	OrderBy            []OrderBy         `json:"orderBy" jsonschema:"Dashboard/editor ordering entries. Each item names a result column and uses asc or desc."`
	ReduceTo           ReduceToOperator  `json:"reduceTo,omitempty" jsonschema:"Single-value reduction: last, sum, avg, min, or max. Set it for value and pie queries (avg is the usual default); omit for raw list queries."`
	SelectColumns      []AttributeKey    `json:"selectColumns" jsonschema:"Fields displayed by a list panel. Each entry should include name/key, fieldContext, fieldDataType, and signal."`
	TimeAggregation    TimeAggregation   `json:"timeAggregation,omitempty" jsonschema:"Metric time aggregation: latest, sum, avg, min, max, count, count_distinct, rate, or increase. Omit for logs and traces."`
	SpaceAggregation   SpaceAggregation  `json:"spaceAggregation,omitempty" jsonschema:"Metric space aggregation across series: sum, avg, min, max, count, p50, p75, p90, p95, or p99. Omit for logs and traces."`
	SeriesAggregation  string            `json:"seriesAggregation,omitempty" jsonschema:"Optional aggregation across grouped metric series. Preserve server-returned values when updating."`
	Functions          []Function        `json:"functions" jsonschema:"Ordered post-query function pipeline. Use function names and arguments documented in the Query Builder resource."`
	Aggregations       []Aggregation     `json:"aggregations" jsonschema:"Query Builder v5 aggregation definitions. Metrics use metricName/timeAggregation/spaceAggregation; logs and traces use expression."`
	Filter             *QueryFilter      `json:"filter,omitempty" jsonschema:"Query Builder v5 filter expression. Use an empty expression for no filter."`
	Source             string            `json:"source,omitempty" jsonschema:"Storage source. Usually empty; use meter only for Cost Meter metric queries."`
}

type Aggregation struct {
	Expression       string           `json:"expression,omitempty" jsonschema:"Logs/traces aggregation expression, for example count() or p95(duration_nano). Leave empty for metric aggregations."`
	MetricName       string           `json:"metricName,omitempty" jsonschema:"Exact metric name for a metrics aggregation. Discover it with signoz_list_metrics when unknown."`
	ReduceTo         ReduceToOperator `json:"reduceTo,omitempty" jsonschema:"Optional reduction for this aggregation: last, sum, avg, min, or max."`
	SpaceAggregation SpaceAggregation `json:"spaceAggregation,omitempty" jsonschema:"Required metrics space aggregation: sum, avg, min, max, count, p50, p75, p90, p95, or p99."`
	Temporality      *Temporality     `json:"temporality,omitempty" jsonschema:"Optional metric temporality: Unspecified, Delta, or Cumulative."`
	TimeAggregation  TimeAggregation  `json:"timeAggregation,omitempty" jsonschema:"Required metrics time aggregation: latest, sum, avg, min, max, count, count_distinct, rate, or increase."`
}

type QueryFilter struct {
	Expression string `json:"expression,omitempty" jsonschema:"SigNoz filter expression, for example service.name = 'frontend' AND http.status_code >= 500. Use an empty string for no filter."`
}

type AttributeKey struct {
	Key           string `json:"key,omitempty" jsonschema:"Attribute key used by groupBy and filters, for example service.name. Prefer key in these contexts."`
	Name          string `json:"name,omitempty" jsonschema:"Field name used by selectColumns and order entries. Prefer name in those contexts."`
	DataType      string `json:"dataType,omitempty" jsonschema:"Field data type reported by SigNoz, for example string, int64, float64, or bool."`
	Type          string `json:"type,omitempty" jsonschema:"Attribute namespace reported by SigNoz, such as resource, tag, span, or log."`
	IsColumn      bool   `json:"isColumn,omitempty" jsonschema:"Whether the field is a materialized storage column."`
	IsJSON        bool   `json:"isJSON,omitempty" jsonschema:"Whether the field contains JSON values."`
	ID            string `json:"id,omitempty" jsonschema:"Frontend attribute identifier; preserve it on update when present."`
	FieldContext  string `json:"fieldContext,omitempty" jsonschema:"Field namespace required by selectColumns, such as resource, span, or log."`
	FieldDataType string `json:"fieldDataType,omitempty" jsonschema:"Frontend field type required by selectColumns, for example string or int64."`
	Signal        string `json:"signal,omitempty" jsonschema:"Owning signal required by selectColumns: traces, logs, or metrics."`
}

type FilterSet struct {
	Items []FilterItem `json:"items" jsonschema:"Structured field predicates. Keep these consistent with filter.expression when both forms are present."`
	Op    string       `json:"op" jsonschema:"Boolean operator combining items: AND or OR."`
}

type FilterItem struct {
	Key   AttributeKey `json:"key" jsonschema:"Field matched by this predicate. Use key plus its dataType/type metadata."`
	Value interface{}  `json:"value" jsonschema:"Scalar, array, or variable reference compared by op; preserve the type expected by the field."`
	Op    string       `json:"op" jsonschema:"SigNoz filter operator, for example =, !=, IN, NOT_IN, CONTAINS, EXISTS, >, or >=."`
	ID    string       `json:"id,omitempty" jsonschema:"Frontend predicate identifier; preserve it on update when present."`
}

type HavingClause struct {
	ColumnName string      `json:"columnName" jsonschema:"Aggregation result column filtered by this having clause, for example count()."`
	Op         string      `json:"op" jsonschema:"Comparison operator for the aggregation result, for example >, >=, <, <=, =, or !=."`
	Value      interface{} `json:"value" jsonschema:"Threshold compared with the aggregation result."`
	Expression string      `json:"expression,omitempty" jsonschema:"Query Builder v5 having expression. Use an empty string when no post-aggregation filter is needed."`
}

type OrderBy struct {
	ColumnName string `json:"columnName" jsonschema:"Result column used for ordering, such as timestamp, count(), or __result."`
	Order      string `json:"order" jsonschema:"Sort direction: asc or desc."`
}

type Function struct {
	Name      string                 `json:"name" jsonschema:"Query Builder function name. Use only functions documented by signoz://dashboard/query-builder-example."`
	Args      []interface{}          `json:"args" jsonschema:"Ordered positional arguments for the function."`
	NamedArgs map[string]interface{} `json:"namedArgs,omitempty" jsonschema:"Named function arguments keyed by parameter name."`
}

type VariableType string

const (
	VariableTypeQuery    VariableType = "QUERY"
	VariableTypeConstant VariableType = "CONSTANT"
	VariableTypeTextbox  VariableType = "TEXTBOX"
	VariableTypeDynamic  VariableType = "DYNAMIC"
	VariableTypeCustom   VariableType = "CUSTOM"
)

type VariableSort string

const (
	VariableSortAsc      VariableSort = "ASC"
	VariableSortDesc     VariableSort = "DESC"
	VariableSortDisabled VariableSort = "DISABLED"
)

type PanelType string

const (
	PanelTypeGraph     PanelType = "graph"
	PanelTypeTable     PanelType = "table"
	PanelTypeValue     PanelType = "value"
	PanelTypeList      PanelType = "list"
	PanelTypeTrace     PanelType = "trace"
	PanelTypePie       PanelType = "pie"
	PanelTypeRow       PanelType = "row"
	PanelTypeBar       PanelType = "bar"
	PanelTypeHistogram PanelType = "histogram"
)

type TimePreferance string

const (
	TimePreferanceGlobal TimePreferance = "GLOBAL_TIME"
)

type QueryType string

const (
	QueryTypeBuilder       QueryType = "builder"
	QueryTypeClickHouseSQL QueryType = "clickhouse_sql"
	QueryTypePromQL        QueryType = "promql"
)

type DataSource string

const (
	DataSourceMetrics DataSource = "metrics"
	DataSourceLogs    DataSource = "logs"
	DataSourceTraces  DataSource = "traces"
)

type AggregateOperator string

const (
	AggregateOperatorNoop          AggregateOperator = "noop"
	AggregateOperatorCount         AggregateOperator = "count"
	AggregateOperatorCountDistinct AggregateOperator = "count_distinct"
	AggregateOperatorSum           AggregateOperator = "sum"
	AggregateOperatorAvg           AggregateOperator = "avg"
	AggregateOperatorMin           AggregateOperator = "min"
	AggregateOperatorMax           AggregateOperator = "max"
	AggregateOperatorP05           AggregateOperator = "p05"
	AggregateOperatorP10           AggregateOperator = "p10"
	AggregateOperatorP20           AggregateOperator = "p20"
	AggregateOperatorP25           AggregateOperator = "p25"
	AggregateOperatorP50           AggregateOperator = "p50"
	AggregateOperatorP75           AggregateOperator = "p75"
	AggregateOperatorP90           AggregateOperator = "p90"
	AggregateOperatorP95           AggregateOperator = "p95"
	AggregateOperatorP99           AggregateOperator = "p99"
	AggregateOperatorRate          AggregateOperator = "rate"
	AggregateOperatorRateSum       AggregateOperator = "rate_sum"
	AggregateOperatorRateAvg       AggregateOperator = "rate_avg"
	AggregateOperatorRateMin       AggregateOperator = "rate_min"
	AggregateOperatorRateMax       AggregateOperator = "rate_max"
)

type Temporality string

const (
	TemporalityUnspecified Temporality = "Unspecified"
	TemporalityDelta       Temporality = "Delta"
	TemporalityCumulative  Temporality = "Cumulative"
)

type ReduceToOperator string

const (
	ReduceToLast ReduceToOperator = "last"
	ReduceToSum  ReduceToOperator = "sum"
	ReduceToAvg  ReduceToOperator = "avg"
	ReduceToMin  ReduceToOperator = "min"
	ReduceToMax  ReduceToOperator = "max"
)

type TimeAggregation string

const (
	TimeAggregationLatest        TimeAggregation = "latest"
	TimeAggregationSum           TimeAggregation = "sum"
	TimeAggregationAvg           TimeAggregation = "avg"
	TimeAggregationMin           TimeAggregation = "min"
	TimeAggregationMax           TimeAggregation = "max"
	TimeAggregationCount         TimeAggregation = "count"
	TimeAggregationCountDistinct TimeAggregation = "count_distinct"
	TimeAggregationRate          TimeAggregation = "rate"
	TimeAggregationIncrease      TimeAggregation = "increase"
)

type SpaceAggregation string

const (
	SpaceAggregationSum   SpaceAggregation = "sum"
	SpaceAggregationAvg   SpaceAggregation = "avg"
	SpaceAggregationMin   SpaceAggregation = "min"
	SpaceAggregationMax   SpaceAggregation = "max"
	SpaceAggregationCount SpaceAggregation = "count"
	SpaceAggregationP50   SpaceAggregation = "p50"
	SpaceAggregationP75   SpaceAggregation = "p75"
	SpaceAggregationP90   SpaceAggregation = "p90"
	SpaceAggregationP95   SpaceAggregation = "p95"
	SpaceAggregationP99   SpaceAggregation = "p99"
)
