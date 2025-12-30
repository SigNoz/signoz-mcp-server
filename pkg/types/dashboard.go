package types

type UpdateDashboardInput struct {
	UUID      string    `json:"uuid" jsonschema:"required" jsonschema_extras:"description=Dashboard UUID to update."`
	Dashboard Dashboard `json:"dashboard" jsonschema:"required" jsonschema_extras:"description=Full dashboard configuration representing the complete post-update state."`
}

type Dashboard struct {
	Title       string              `json:"title" jsonschema:"required" jsonschema_extras:"description=The display name of the dashboard."`
	Description string              `json:"description,omitempty" jsonschema_extras:"description=A brief explanation of what the dashboard shows."`
	Tags        []string            `json:"tags" jsonschema_extras:"description=Keywords for categorization e.g performance latency."`
	Layout      []LayoutItem        `json:"layout" jsonschema:"required" jsonschema_extras:"description=Defines the grid positioning and size for each widget."`
	Variables   map[string]Variable `json:"variables,omitempty" jsonschema_extras:"description=Key-value map of template variables available for queries."`
	Widgets     []Widget            `json:"widgets" jsonschema:"required" jsonschema_extras:"description=The list of all graphical components displayed on the dashboard."`
}

type LayoutItem struct {
	X           int    `json:"x" jsonschema:"required" jsonschema_extras:"description=X coordinate for the layout"`
	Y           int    `json:"y" jsonschema:"required" jsonschema_extras:"description=Y coordinate for the layout"`
	W           int    `json:"w" jsonschema:"required" jsonschema_extras:"description=Width for the layout"`
	H           int    `json:"h" jsonschema:"required" jsonschema_extras:"description=Height for the layout"`
	I           string `json:"i" jsonschema:"required" jsonschema_extras:"description=ID for the layout"`
	Moved       bool   `json:"moved,omitempty" jsonschema_extras:"description=Whether the layout is moved or not"`
	Static      bool   `json:"static,omitempty" jsonschema_extras:"description=Whether the layout is static or not"`
	MaxH        int    `json:"maxH,omitempty"`
	MinH        int    `json:"minH,omitempty"`
	MinW        int    `json:"minW,omitempty"`
	IsDraggable bool   `json:"isDraggable,omitempty"`
}

type Variable struct {
	ID                        string       `json:"id,omitempty" jsonschema_extras:"description=ID for the variable"`
	Name                      string       `json:"name,omitempty" jsonschema_extras:"description=Name for the variable"`
	Description               string       `json:"description,omitempty" jsonschema_extras:"description=Description for the variable"`
	Key                       string       `json:"key,omitempty" jsonschema_extras:"description=Key for the variable, use same as name"`
	Type                      VariableType `json:"type,omitempty" jsonschema_extras:"description=Type for the variable"`
	QueryValue                string       `json:"queryValue,omitempty" jsonschema_extras:"description=Query for the variable"`
	AllSelected               bool         `json:"allSelected,omitempty" jsonschema_extras:"description=Whether all the values are selected or not"`
	CustomValue               string       `json:"customValue,omitempty" jsonschema_extras:"description=Custom value for the variable"`
	MultiSelect               bool         `json:"multiSelect,omitempty" jsonschema_extras:"description=Whether the variable is multi select or not"`
	Order                     int          `json:"order,omitempty" jsonschema_extras:"description=Order for the variable"`
	ShowALLOption             bool         `json:"showALLOption,omitempty" jsonschema_extras:"description=Whether to show all option or not"`
	Sort                      VariableSort `json:"sort,omitempty" jsonschema_extras:"description=Sort for the variable"`
	TextboxValue              string       `json:"textboxValue,omitempty" jsonschema_extras:"description=Textbox value for the variable"`
	ModificationUUID          string       `json:"modificationUUID,omitempty"`
	SelectedValue             interface{}  `json:"selectedValue,omitempty"`
	DefaultValue              string       `json:"defaultValue,omitempty"`
	DynamicVariablesAttribute string       `json:"dynamicVariablesAttribute,omitempty"`
	DynamicVariablesSource    string       `json:"dynamicVariablesSource,omitempty"`
	HaveCustomValuesSelected  bool         `json:"haveCustomValuesSelected,omitempty"`
}

type Widget struct {
	ID                    string             `json:"id" jsonschema:"required" jsonschema_extras:"description=ID for the widget"`
	Description           string             `json:"description,omitempty" jsonschema_extras:"description=Description for the widget"`
	IsStacked             bool               `json:"isStacked,omitempty" jsonschema_extras:"description=Whether the widget is stacked or not"`
	NullZeroValues        string             `json:"nullZeroValues,omitempty" jsonschema_extras:"description=Whether the widget has null zero values or not"`
	Opacity               string             `json:"opacity,omitempty" jsonschema_extras:"description=Opacity for the widget"`
	PanelTypes            PanelType          `json:"panelTypes" jsonschema:"required" jsonschema_extras:"description=Panel type for the widget."`
	TimePreferance        TimePreferance     `json:"timePreferance,omitempty" jsonschema_extras:"description=Time preferance for the widget"`
	Title                 string             `json:"title" jsonschema:"required" jsonschema_extras:"description=Title for the widget"`
	YAxisUnit             string             `json:"yAxisUnit,omitempty" jsonschema_extras:"description=Y axis unit for the widget"`
	Query                 WidgetQuery        `json:"query" jsonschema:"required" jsonschema_extras:"description=Query for the widget."`
	BucketCount           int                `json:"bucketCount,omitempty"`
	BucketWidth           int                `json:"bucketWidth,omitempty"`
	ColumnUnits           map[string]string  `json:"columnUnits,omitempty"`
	FillSpans             bool               `json:"fillSpans,omitempty"`
	MergeAllActiveQueries bool               `json:"mergeAllActiveQueries,omitempty"`
	SelectedLogFields     []SelectedLogField `json:"selectedLogFields"`
	SelectedTracesFields  []AttributeKey     `json:"selectedTracesFields"`
	SoftMax               interface{}        `json:"softMax,omitempty"`
	SoftMin               interface{}        `json:"softMin,omitempty"`
	StackedBarChart       bool               `json:"stackedBarChart,omitempty"`
	Thresholds            []Threshold        `json:"thresholds"`
	IsLogScale            bool               `json:"isLogScale,omitempty"`
	ColumnWidths          map[string]int     `json:"columnWidths,omitempty"`
	CustomLegendColors    map[string]string  `json:"customLegendColors,omitempty"`
	LegendPosition        string             `json:"legendPosition,omitempty"`
	ContextLinks          ContextLinks       `json:"contextLinks"`
	DecimalPrecision      int                `json:"decimalPrecision,omitempty"`
	QueryData             interface{}        `json:"queryData,omitempty"`
	QueryType             interface{}        `json:"queryType,omitempty"`
}

type ContextLinks struct {
	LinksData []interface{} `json:"linksData"`
}

type Threshold struct {
	Index                 string      `json:"index,omitempty"`
	IsEditEnabled         bool        `json:"isEditEnabled,omitempty"`
	KeyIndex              int         `json:"keyIndex,omitempty"`
	SelectedGraph         string      `json:"selectedGraph,omitempty"`
	ThresholdColor        string      `json:"thresholdColor,omitempty"`
	ThresholdFormat       string      `json:"thresholdFormat,omitempty"`
	ThresholdLabel        string      `json:"thresholdLabel,omitempty"`
	ThresholdOperator     string      `json:"thresholdOperator,omitempty"`
	ThresholdTableOptions string      `json:"thresholdTableOptions,omitempty"`
	ThresholdUnit         string      `json:"thresholdUnit,omitempty"`
	ThresholdValue        interface{} `json:"thresholdValue,omitempty"`
}

type SelectedLogField struct {
	DataType      string `json:"dataType,omitempty"`
	Name          string `json:"name,omitempty"`
	Type          string `json:"type,omitempty"`
	FieldContext  string `json:"fieldContext,omitempty"`
	FieldDataType string `json:"fieldDataType,omitempty"`
	IsIndexed     bool   `json:"isIndexed,omitempty"`
	Signal        string `json:"signal,omitempty"`
	IsColumn      bool   `json:"isColumn,omitempty"`
	IsJSON        bool   `json:"isJSON,omitempty"`
}

type WidgetQuery struct {
	QueryType     QueryType             `json:"queryType" jsonschema:"required" jsonschema_extras:"description=Query type for the widget"`
	PromQL        []PromQL              `json:"promql" jsonschema_extras:"description=PromQL for the widget"`
	ClickHouseSQL []ClickHouseSQL       `json:"clickhouse_sql" jsonschema_extras:"description=Clickhouse SQL for the widget"`
	Builder       BuilderQueryDashboard `json:"builder" jsonschema_extras:"description=Builder query for the widget"`
	ID            string                `json:"id,omitempty"`
}

type PromQL struct {
	Query    string `json:"query" jsonschema:"required" jsonschema_extras:"description=PromQL query expression"`
	Name     string `json:"name" jsonschema:"required" jsonschema_extras:"description=Name for the query"`
	Disabled bool   `json:"disabled" jsonschema:"required" jsonschema_extras:"description=Whether the PromQL query is disabled or not"`
	Legend   string `json:"legend,omitempty" jsonschema_extras:"description=Legend for the query"`
}

type ClickHouseSQL struct {
	Query    string `json:"query" jsonschema:"required" jsonschema_extras:"description=Clickhouse SQL query for the widget"`
	Name     string `json:"name" jsonschema:"required" jsonschema_extras:"description=Name for the query"`
	Disabled bool   `json:"disabled" jsonschema:"required" jsonschema_extras:"description=Whether the Clickhouse SQL is disabled or not"`
	Legend   string `json:"legend,omitempty" jsonschema_extras:"description=Legend for the query"`
}

type BuilderQueryDashboard struct {
	QueryData          []BuilderQuery `json:"queryData" jsonschema_extras:"description=Query data for the widget. Populate with non-formula queries."`
	QueryFormulas      []BuilderQuery `json:"queryFormulas" jsonschema_extras:"description=Query formulas for the widget. Populate with formula queries."`
	QueryTraceOperator []interface{}  `json:"queryTraceOperator,omitempty"`
}

type BuilderQuery struct {
	QueryName          string            `json:"queryName" jsonschema:"required" jsonschema_extras:"description=Name of the query"`
	StepInterval       *int64            `json:"stepInterval" jsonschema:"required" jsonschema_extras:"description=Step/Aggregation interval for the query in seconds."`
	DataSource         DataSource        `json:"dataSource" jsonschema:"required" jsonschema_extras:"description=Data source for the query"`
	AggregateOperator  AggregateOperator `json:"aggregateOperator,omitempty" jsonschema_extras:"description=Aggregate operator for the query"`
	AggregateAttribute AttributeKey      `json:"aggregateAttribute,omitempty"`
	Temporality        Temporality       `json:"temporality,omitempty" jsonschema_extras:"description=Temporality for metrics data"`
	Filters            FilterSet         `json:"filters,omitempty"`
	GroupBy            []AttributeKey    `json:"groupBy" jsonschema_extras:"description=Group by attributes for the query"`
	Expression         string            `json:"expression" jsonschema:"required" jsonschema_extras:"description=Expression for the query"`
	Disabled           bool              `json:"disabled,omitempty" jsonschema_extras:"description=Whether the query is disabled"`
	Having             interface{}       `json:"having,omitempty" jsonschema_extras:"description=Having clauses for the query"`
	Legend             string            `json:"legend,omitempty" jsonschema_extras:"description=Legend for the query"`
	Limit              uint64            `json:"limit,omitempty" jsonschema_extras:"description=Limit for the query"`
	Offset             uint64            `json:"offset,omitempty" jsonschema_extras:"description=Offset for the query"`
	PageSize           uint64            `json:"pageSize,omitempty" jsonschema_extras:"description=Page size for the query"`
	OrderBy            []OrderBy         `json:"orderBy" jsonschema_extras:"description=Order by for the query"`
	ReduceTo           ReduceToOperator  `json:"reduceTo,omitempty" jsonschema_extras:"description=Reduce to operator for the query"`
	SelectColumns      []AttributeKey    `json:"selectColumns" jsonschema_extras:"description=Select columns for the query. Required for list panel types."`
	TimeAggregation    TimeAggregation   `json:"timeAggregation,omitempty" jsonschema_extras:"description=Time aggregation for metrics queries"`
	SpaceAggregation   SpaceAggregation  `json:"spaceAggregation,omitempty" jsonschema_extras:"description=Space aggregation for metrics queries"`
	SeriesAggregation  string            `json:"seriesAggregation,omitempty" jsonschema_extras:"description=Series aggregation for metrics queries with group by"`
	Functions          []Function        `json:"functions" jsonschema_extras:"description=Functions to apply to the query result"`
	Aggregations       []Aggregation     `json:"aggregations"`
	Filter             *QueryFilter      `json:"filter,omitempty"`
	Source             string            `json:"source,omitempty"`
}

type Aggregation struct {
	Expression       string           `json:"expression,omitempty"`
	MetricName       string           `json:"metricName,omitempty"`
	ReduceTo         ReduceToOperator `json:"reduceTo,omitempty"`
	SpaceAggregation SpaceAggregation `json:"spaceAggregation,omitempty"`
	Temporality      *Temporality     `json:"temporality,omitempty"`
	TimeAggregation  TimeAggregation  `json:"timeAggregation,omitempty"`
}

type QueryFilter struct {
	Expression string `json:"expression,omitempty"`
}

type AttributeKey struct {
	Key           string `json:"key,omitempty" jsonschema_extras:"description=Key for the attribute"`
	Name          string `json:"name,omitempty" jsonschema_extras:"description=Name for the attribute (alternative to key)"`
	DataType      string `json:"dataType,omitempty" jsonschema_extras:"description=Data type of the attribute"`
	Type          string `json:"type,omitempty" jsonschema_extras:"description=Type of the attribute (tag, resource, log, etc.)"`
	IsColumn      bool   `json:"isColumn,omitempty" jsonschema_extras:"description=Whether the attribute is a materialized column or not"`
	IsJSON        bool   `json:"isJSON,omitempty" jsonschema_extras:"description=Whether the attribute is a JSON or not"`
	ID            string `json:"id,omitempty"`
	FieldContext  string `json:"fieldContext,omitempty" jsonschema_extras:"description=Field context (resource, span, log, etc.) - Required for selectColumns"`
	FieldDataType string `json:"fieldDataType,omitempty" jsonschema_extras:"description=Field data type"`
	Signal        string `json:"signal,omitempty" jsonschema_extras:"description=Signal type (traces, logs, metrics) - Required for selectColumns"`
}

type FilterSet struct {
	Items []FilterItem `json:"items" jsonschema:"required"`
	Op    string       `json:"op" jsonschema:"required" jsonschema_extras:"description=Operator for combining filter items."`
}

type FilterItem struct {
	Key   AttributeKey `json:"key" jsonschema:"required" jsonschema_extras:"description=Key for the filter"`
	Value interface{}  `json:"value" jsonschema:"required" jsonschema_extras:"description=Value for the filter"`
	Op    string       `json:"op" jsonschema:"required" jsonschema_extras:"description=Filter operator"`
	ID    string       `json:"id,omitempty"`
}

type HavingClause struct {
	ColumnName string      `json:"columnName" jsonschema:"required" jsonschema_extras:"description=Column name for the having clause."`
	Op         string      `json:"op" jsonschema:"required" jsonschema_extras:"description=Operator for the having clause"`
	Value      interface{} `json:"value" jsonschema:"required" jsonschema_extras:"description=Value for the having clause."`
	Expression string      `json:"expression,omitempty"`
}

type OrderBy struct {
	ColumnName string `json:"columnName" jsonschema:"required" jsonschema_extras:"description=Column name for the order by."`
	Order      string `json:"order" jsonschema:"required" jsonschema_extras:"description=Order direction"`
}

type Function struct {
	Name      string                 `json:"name" jsonschema:"required" jsonschema_extras:"description=Function name"`
	Args      []interface{}          `json:"args" jsonschema_extras:"description=Function arguments"`
	NamedArgs map[string]interface{} `json:"namedArgs,omitempty" jsonschema_extras:"description=Named arguments for the function"`
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
