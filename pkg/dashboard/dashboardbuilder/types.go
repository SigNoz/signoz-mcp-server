package dashboardbuilder

import "encoding/json"

// DashboardData is the structured representation of a SigNoz dashboard's "data" field.
// It corresponds to the TypeScript DashboardData interface on the frontend.
// Public dashboard fields (timeRangeEnabled, defaultTimeRange, publicPath) are
// deliberately excluded — dashboards default to private.
type DashboardData struct {
	Title       string                        `json:"title"`
	Description string                        `json:"description,omitempty"`
	Tags        []string                      `json:"tags,omitempty"`
	Name        string                        `json:"name,omitempty"`
	Version     string                        `json:"version,omitempty"`
	Variables   map[string]*DashboardVariable `json:"variables"`
	Widgets     []WidgetOrRow                 `json:"widgets,omitempty"`
	Layout      []LayoutItem                  `json:"layout,omitempty"`
	PanelMap    map[string]*PanelMapEntry     `json:"panelMap,omitempty"`
}

// DashboardVariable represents a dashboard variable.
// When Type is empty, it defaults to "DYNAMIC" during ApplyDefaults.
type DashboardVariable struct {
	ID                        string      `json:"id"`
	Name                      string      `json:"name,omitempty"`
	Description               string      `json:"description"`
	Type                      string      `json:"type"`
	QueryValue                string      `json:"queryValue,omitempty"`
	CustomValue               string      `json:"customValue,omitempty"`
	TextboxValue              string      `json:"textboxValue,omitempty"`
	Sort                      string      `json:"sort"`
	MultiSelect               bool        `json:"multiSelect"`
	ShowALLOption             bool        `json:"showALLOption"`
	SelectedValue             any `json:"selectedValue,omitempty"`
	DefaultValue              string      `json:"defaultValue,omitempty"`
	DynamicVariablesAttribute string      `json:"dynamicVariablesAttribute,omitempty"`
	DynamicVariablesSource    string      `json:"dynamicVariablesSource,omitempty"`
	Order                     *int        `json:"order,omitempty"`
}

// WidgetOrRow is a union type representing either a widget panel or a row separator.
// When PanelTypes is "row", only ID, PanelTypes, Title, and Description are used.
// Otherwise, Query and display options are relevant.
type WidgetOrRow struct {
	ID          string `json:"id"`
	PanelTypes  string `json:"panelTypes"`
	Title       string `json:"title"`
	Description string `json:"description"`

	// Query — nil for row widgets.
	Query *Query `json:"query,omitempty"`

	// Display options (only for non-row widgets).
	Opacity        string   `json:"opacity,omitempty"`
	NullZeroValues string   `json:"nullZeroValues,omitempty"`
	TimePreferance string   `json:"timePreferance,omitempty"` // Intentional typo — matches frontend field name.
	StepSize       *int     `json:"stepSize,omitempty"`
	YAxisUnit      string   `json:"yAxisUnit,omitempty"`
	SoftMin        *float64 `json:"softMin"`
	SoftMax        *float64 `json:"softMax"`
	FillSpans      *bool    `json:"fillSpans,omitempty"`
	IsLogScale     *bool    `json:"isLogScale,omitempty"`

	// Thresholds
	Thresholds []Threshold `json:"thresholds,omitempty"`

	// Table/list specific
	ColumnUnits  map[string]string `json:"columnUnits,omitempty"`
	ColumnWidths map[string]int    `json:"columnWidths,omitempty"`

	// Fields kept as raw JSON for flexibility (schema evolves frequently).
	SelectedLogFields    json.RawMessage `json:"selectedLogFields"`
	SelectedTracesFields json.RawMessage `json:"selectedTracesFields"`

	// Chart display options
	LegendPosition     string            `json:"legendPosition,omitempty"`
	CustomLegendColors map[string]string  `json:"customLegendColors,omitempty"`
	ContextLinks       *ContextLinksData  `json:"contextLinks,omitempty"`
	LineInterpolation  string             `json:"lineInterpolation,omitempty"`
	ShowPoints         *bool              `json:"showPoints,omitempty"`
	LineStyle          string             `json:"lineStyle,omitempty"`
	FillMode           string             `json:"fillMode,omitempty"`
	SpanGaps           *json.RawMessage   `json:"spanGaps,omitempty"` // bool | number

	// Bar/histogram specific
	StackedBarChart    *bool `json:"stackedBarChart,omitempty"`
	BucketCount        *int  `json:"bucketCount,omitempty"`
	BucketWidth        *int  `json:"bucketWidth,omitempty"`

	// Precision
	DecimalPrecision *json.RawMessage `json:"decimalPrecision,omitempty"` // number | "full precision"

	// Merge
	MergeAllActiveQueries *bool `json:"mergeAllActiveQueries,omitempty"`

	// Custom table columns
	CustomColTitles map[string]string `json:"customColTitles,omitempty"`
	HiddenColumns   []string          `json:"hiddenColumns,omitempty"`
}

// Query represents the query configuration of a widget.
type Query struct {
	QueryType     string             `json:"queryType"`
	Builder       *BuilderData       `json:"builder,omitempty"`
	ClickhouseSQL []ClickHouseQuery  `json:"clickhouse_sql,omitempty"`
	PromQL        []PromQLQuery      `json:"promql,omitempty"`
	ID            string             `json:"id"`
	Unit          string             `json:"unit,omitempty"`
}

// BuilderData holds query builder queries, formulas, and trace operators.
// Fields use []map[string]any to tolerate schema evolution without
// tight coupling — the validator checks structural keys (queryName, dataSource,
// expression) without deep-typing every field.
type BuilderData struct {
	QueryData          []map[string]any `json:"queryData"`
	QueryFormulas      []map[string]any `json:"queryFormulas"`
	QueryTraceOperator []map[string]any `json:"queryTraceOperator,omitempty"`
}

// ClickHouseQuery represents a raw ClickHouse SQL query.
type ClickHouseQuery struct {
	Name     string `json:"name"`
	Legend   string `json:"legend"`
	Disabled bool   `json:"disabled"`
	Query    string `json:"query"`
}

// PromQLQuery represents a PromQL query.
type PromQLQuery struct {
	Name     string `json:"name"`
	Query    string `json:"query"`
	Legend   string `json:"legend"`
	Disabled bool   `json:"disabled"`
}

// LayoutItem represents a react-grid-layout item.
type LayoutItem struct {
	I      string `json:"i"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	Moved  bool   `json:"moved,omitempty"`
	Static bool   `json:"static,omitempty"`
}

// PanelMapEntry represents a collapsible row group and its child widget layouts.
type PanelMapEntry struct {
	Widgets   []LayoutItem `json:"widgets"`
	Collapsed bool         `json:"collapsed"`
}

// Threshold represents a visual threshold on a chart.
// Fields match the SigNoz frontend schema (types.Threshold).
type Threshold struct {
	Index                 string `json:"index,omitempty"`
	IsEditEnabled         bool   `json:"isEditEnabled,omitempty"`
	KeyIndex              int    `json:"keyIndex,omitempty"`
	SelectedGraph         string `json:"selectedGraph,omitempty"`
	ThresholdColor        string `json:"thresholdColor,omitempty"`
	ThresholdFormat       string `json:"thresholdFormat,omitempty"`
	ThresholdLabel        string `json:"thresholdLabel,omitempty"`
	ThresholdOperator     string `json:"thresholdOperator,omitempty"`
	ThresholdTableOptions string `json:"thresholdTableOptions,omitempty"`
	ThresholdUnit         string `json:"thresholdUnit,omitempty"`
	ThresholdValue        any    `json:"thresholdValue,omitempty"`
}

// ContextLinksData holds context link definitions for a widget.
type ContextLinksData struct {
	LinksData []ContextLinkProps `json:"linksData"`
}

// ContextLinkProps is a single context link.
type ContextLinkProps struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Label string `json:"label"`
}
