// Package panelvalidator provides schema validation for SigNoz dashboard panels.
// It validates the frontend JSON format used by the dashboard API (POST/PUT /api/v1/dashboards).
// This package is standalone with zero external dependencies - copy it into any Go project.
package panelvalidator

import (
	"fmt"
	"regexp"
	"strings"
)

// =============================================================================
// Section 1: Constants
// =============================================================================

// Panel types
const (
	PanelTypeTimeSeries = "graph"
	PanelTypeValue      = "value"
	PanelTypeTable      = "table"
	PanelTypeList       = "list"
	PanelTypeBar        = "bar"
	PanelTypePie        = "pie"
	PanelTypeHistogram  = "histogram"
)

var validPanelTypes = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeValue:      true,
	PanelTypeTable:      true,
	PanelTypeList:       true,
	PanelTypeBar:        true,
	PanelTypePie:        true,
	PanelTypeHistogram:  true,
}

// Data sources
const (
	DataSourceMetrics = "metrics"
	DataSourceLogs    = "logs"
	DataSourceTraces  = "traces"
)

var validDataSources = map[string]bool{
	DataSourceMetrics: true,
	DataSourceLogs:    true,
	DataSourceTraces:  true,
}

// Query types
const (
	QueryTypeBuilder      = "builder"
	QueryTypeClickHouse   = "clickhouse_sql"
	QueryTypePromQL       = "promql"
)

var validQueryTypes = map[string]bool{
	QueryTypeBuilder:    true,
	QueryTypeClickHouse: true,
	QueryTypePromQL:     true,
}

// ReduceTo operators
var validReduceToOperators = map[string]bool{
	"last": true, "sum": true, "avg": true, "max": true, "min": true,
}

// Metric aggregate operators
var validMetricAggregateOperators = map[string]bool{
	"":               true, // empty is valid for some metric types
	"noop":           true,
	"count":          true,
	"count_distinct": true,
	"sum":            true,
	"avg":            true,
	"max":            true,
	"min":            true,
	"p05":            true,
	"p10":            true,
	"p20":            true,
	"p25":            true,
	"p50":            true,
	"p75":            true,
	"p90":            true,
	"p95":            true,
	"p99":            true,
	"rate":           true,
	"sum_rate":       true,
	"avg_rate":       true,
	"max_rate":       true,
	"min_rate":       true,
	"rate_sum":       true,
	"rate_avg":       true,
	"rate_min":       true,
	"rate_max":       true,
	"hist_quantile_50": true,
	"hist_quantile_75": true,
	"hist_quantile_90": true,
	"hist_quantile_95": true,
	"hist_quantile_99": true,
	"increase":         true,
	"latest":           true,
}

// Logs aggregate operators
var validLogsAggregateOperators = map[string]bool{
	"noop":           true,
	"count":          true,
	"count_distinct": true,
	"sum":            true,
	"avg":            true,
	"max":            true,
	"min":            true,
	"p05":            true,
	"p10":            true,
	"p20":            true,
	"p25":            true,
	"p50":            true,
	"p75":            true,
	"p90":            true,
	"p95":            true,
	"p99":            true,
	"rate":           true,
	"rate_sum":       true,
	"rate_avg":       true,
	"rate_min":       true,
	"rate_max":       true,
}

// Traces aggregate operators
var validTracesAggregateOperators = map[string]bool{
	"noop":           true,
	"count":          true,
	"count_distinct": true,
	"sum":            true,
	"avg":            true,
	"max":            true,
	"min":            true,
	"p05":            true,
	"p10":            true,
	"p20":            true,
	"p25":            true,
	"p50":            true,
	"p75":            true,
	"p90":            true,
	"p95":            true,
	"p99":            true,
	"rate":           true,
	"rate_sum":       true,
	"rate_avg":       true,
	"rate_min":       true,
	"rate_max":       true,
}

// Time aggregation values (metrics only)
var validTimeAggregations = map[string]bool{
	"":               true,
	"latest":         true,
	"sum":            true,
	"avg":            true,
	"min":            true,
	"max":            true,
	"count":          true,
	"count_distinct": true,
	"rate":           true,
	"increase":       true,
}

// Space aggregation values (metrics only)
var validSpaceAggregations = map[string]bool{
	"":      true,
	"sum":   true,
	"avg":   true,
	"min":   true,
	"max":   true,
	"count": true,
	"p50":   true,
	"p75":   true,
	"p90":   true,
	"p95":   true,
	"p99":   true,
}

// Query function names
var validFunctionNames = map[string]bool{
	"cutOffMin":     true,
	"cutOffMax":     true,
	"clampMin":      true,
	"clampMax":      true,
	"absolute":      true,
	"runningDiff":   true,
	"log2":          true,
	"log10":         true,
	"cumulativeSum": true,
	"ewma3":         true,
	"ewma5":         true,
	"ewma7":         true,
	"median3":       true,
	"median5":       true,
	"median7":       true,
	"timeShift":     true,
	"anomaly":       true,
	"fillZero":      true,
}

// Threshold operators
var validThresholdOperators = map[string]bool{
	">": true, "<": true, ">=": true, "<=": true, "=": true,
}

// Threshold formats
var validThresholdFormats = map[string]bool{
	"Text": true, "Background": true,
}

// Filter operators
var validFilterOperators = map[string]bool{
	"IN": true, "NOT_IN": true,
	"LIKE": true, "NOT_LIKE": true,
	"REGEX": true, "NOT_REGEX": true,
	"=": true, "!=": true,
	"EXISTS": true, "NOT_EXISTS": true,
	"CONTAINS": true, "NOT_CONTAINS": true,
	">=": true, ">": true, "<=": true, "<": true,
	"HAS": true, "NHAS": true,
	"ILIKE": true, "NOT_ILIKE": true,
}

// Having operators (subset of filter operators)
var validHavingOperators = map[string]bool{
	"=": true, "!=": true,
	"IN": true, "NOT_IN": true,
	">=": true, ">": true, "<=": true, "<": true,
}

// =============================================================================
// Section 2: Panel-Type Feature Maps
// =============================================================================

// panelSupportsThreshold indicates which panel types support thresholds.
var panelSupportsThreshold = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeValue:      true,
	PanelTypeTable:      true,
	PanelTypeBar:        true,
}

// panelSupportsSoftMinMax indicates which panel types support soft min/max on Y axis.
var panelSupportsSoftMinMax = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeBar:        true,
}

// panelSupportsFillSpan indicates which panel types support fill spans.
var panelSupportsFillSpan = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelSupportsLogScale indicates which panel types support log scale.
var panelSupportsLogScale = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeBar:        true,
}

// panelSupportsYAxisUnit indicates which panel types support Y axis unit.
var panelSupportsYAxisUnit = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeValue:      true,
	PanelTypePie:        true,
	PanelTypeBar:        true,
}

// panelSupportsBucketConfig indicates which panel types support bucket configuration.
var panelSupportsBucketConfig = map[string]bool{
	PanelTypeHistogram: true,
}

// panelSupportsColumnUnits indicates which panel types support per-column units.
var panelSupportsColumnUnits = map[string]bool{
	PanelTypeTable: true,
}

// panelSupportsStacking indicates which panel types support stacked bar chart.
var panelSupportsStacking = map[string]bool{
	PanelTypeBar: true,
}

// panelSupportsLegendPosition indicates which panel types support legend position.
var panelSupportsLegendPosition = map[string]bool{
	PanelTypeTimeSeries: true,
	PanelTypeBar:        true,
}

// panelSupportsLineInterpolation indicates which panel types support line interpolation.
var panelSupportsLineInterpolation = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelSupportsLineStyle indicates which panel types support line style.
var panelSupportsLineStyle = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelSupportsFillMode indicates which panel types support fill mode.
var panelSupportsFillMode = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelSupportsShowPoints indicates which panel types support show points.
var panelSupportsShowPoints = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelSupportsSpanGaps indicates which panel types support span gaps.
var panelSupportsSpanGaps = map[string]bool{
	PanelTypeTimeSeries: true,
}

// panelTypeQueryTypes maps each panel type to its supported query types.
var panelTypeQueryTypes = map[string]map[string]bool{
	PanelTypeTimeSeries: {QueryTypeBuilder: true, QueryTypeClickHouse: true, QueryTypePromQL: true},
	PanelTypeValue:      {QueryTypeBuilder: true, QueryTypeClickHouse: true, QueryTypePromQL: true},
	PanelTypeTable:      {QueryTypeBuilder: true, QueryTypeClickHouse: true},
	PanelTypeList:       {QueryTypeBuilder: true},
	PanelTypeBar:        {QueryTypeBuilder: true, QueryTypeClickHouse: true, QueryTypePromQL: true},
	PanelTypePie:        {QueryTypeBuilder: true, QueryTypeClickHouse: true},
	PanelTypeHistogram:  {QueryTypeBuilder: true, QueryTypeClickHouse: true, QueryTypePromQL: true},
}

// =============================================================================
// Section 3: Go Structs (matching frontend JSON format)
// =============================================================================

// Panel represents a SigNoz dashboard widget/panel.
type Panel struct {
	ID                    string            `json:"id"`
	PanelTypes            string            `json:"panelTypes"`
	Title                 string            `json:"title"`
	Description           string            `json:"description"`
	Query                 Query             `json:"query"`
	Opacity               string            `json:"opacity"`
	NullZeroValues        string            `json:"nullZeroValues"`
	TimePreferance        string            `json:"timePreferance,omitempty"`
	YAxisUnit             string            `json:"yAxisUnit,omitempty"`
	DecimalPrecision      *int              `json:"decimalPrecision,omitempty"`
	SoftMin               *float64          `json:"softMin"`
	SoftMax               *float64          `json:"softMax"`
	Thresholds            []Threshold       `json:"thresholds,omitempty"`
	FillSpans             *bool             `json:"fillSpans,omitempty"`
	StackedBarChart       *bool             `json:"stackedBarChart,omitempty"`
	BucketCount           *int              `json:"bucketCount,omitempty"`
	BucketWidth           *float64          `json:"bucketWidth,omitempty"`
	MergeAllActiveQueries *bool             `json:"mergeAllActiveQueries,omitempty"`
	ColumnUnits           map[string]string `json:"columnUnits,omitempty"`
	SelectedLogFields     any       `json:"selectedLogFields"`
	SelectedTracesFields  any       `json:"selectedTracesFields"`
	IsLogScale            *bool             `json:"isLogScale,omitempty"`
	LegendPosition        string            `json:"legendPosition,omitempty"`
	LineInterpolation     string            `json:"lineInterpolation,omitempty"`
	ShowPoints            *bool             `json:"showPoints,omitempty"`
	LineStyle             string            `json:"lineStyle,omitempty"`
	FillMode              string            `json:"fillMode,omitempty"`
	SpanGaps              any       `json:"spanGaps,omitempty"`
}

// Query represents the query configuration for a panel.
type Query struct {
	QueryType    string           `json:"queryType"`
	Builder      QueryBuilderData `json:"builder"`
	PromQL       []PromQLQuery    `json:"promql"`
	ClickHouseQL []CHQuery        `json:"clickhouse_sql"`
	ID           string           `json:"id"`
	Unit         string           `json:"unit,omitempty"`
}

// QueryBuilderData holds the builder query data and formulas.
type QueryBuilderData struct {
	QueryData    []BuilderQuery   `json:"queryData"`
	QueryFormulas []BuilderFormula `json:"queryFormulas"`
}

// BuilderQuery represents a single query in the query builder.
type BuilderQuery struct {
	QueryName          string             `json:"queryName"`
	DataSource         string             `json:"dataSource"`
	AggregateOperator  string             `json:"aggregateOperator,omitempty"`
	AggregateAttribute *AutocompleteData  `json:"aggregateAttribute,omitempty"`
	TimeAggregation    string             `json:"timeAggregation,omitempty"`
	SpaceAggregation   string             `json:"spaceAggregation,omitempty"`
	Functions          []QueryFunction    `json:"functions,omitempty"`
	Filters            *TagFilter         `json:"filters,omitempty"`
	GroupBy            []AutocompleteData `json:"groupBy,omitempty"`
	Expression         string             `json:"expression"`
	Disabled           bool               `json:"disabled"`
	Having             []HavingClause     `json:"having,omitempty"`
	Limit              *int               `json:"limit"`
	StepInterval       *int               `json:"stepInterval"`
	OrderBy            []OrderByPayload   `json:"orderBy,omitempty"`
	ReduceTo           string             `json:"reduceTo,omitempty"`
	Legend             string             `json:"legend,omitempty"`
	PageSize           *int               `json:"pageSize,omitempty"`
	Offset             *int               `json:"offset,omitempty"`
	SelectColumns      []AutocompleteData `json:"selectColumns,omitempty"`
	Source             string             `json:"source,omitempty"`
}

// BuilderFormula represents a formula in the query builder.
type BuilderFormula struct {
	QueryName  string           `json:"queryName"`
	Expression string           `json:"expression"`
	Disabled   bool             `json:"disabled"`
	Legend     string           `json:"legend,omitempty"`
	Limit      *int             `json:"limit,omitempty"`
	Having     []HavingClause   `json:"having,omitempty"`
	OrderBy    []OrderByPayload `json:"orderBy,omitempty"`
}

// AutocompleteData represents an attribute or field reference.
type AutocompleteData struct {
	Key      string `json:"key"`
	DataType string `json:"dataType,omitempty"`
	Type     string `json:"type,omitempty"`
	ID       string `json:"id,omitempty"`
	IsColumn *bool  `json:"isColumn,omitempty"`
	IsJSON   *bool  `json:"isJSON,omitempty"`
}

// TagFilter represents a filter with multiple items.
type TagFilter struct {
	Items []TagFilterItem `json:"items"`
	Op    string          `json:"op"`
}

// TagFilterItem represents a single filter condition.
type TagFilterItem struct {
	ID    string            `json:"id"`
	Key   *AutocompleteData `json:"key,omitempty"`
	Op    string            `json:"op"`
	Value any       `json:"value"`
}

// HavingClause represents a HAVING condition.
type HavingClause struct {
	ColumnName string      `json:"columnName"`
	Op         string      `json:"op"`
	Value      any `json:"value"`
}

// OrderByPayload represents an ORDER BY clause.
type OrderByPayload struct {
	ColumnName string `json:"columnName"`
	Order      string `json:"order"`
}

// QueryFunction represents a post-processing function applied to query results.
type QueryFunction struct {
	Name      string                 `json:"name"`
	Args      []any          `json:"args,omitempty"`
	NamedArgs map[string]any `json:"namedArgs,omitempty"`
}

// PromQLQuery represents a PromQL query.
type PromQLQuery struct {
	Name     string `json:"name"`
	Query    string `json:"query"`
	Legend   string `json:"legend,omitempty"`
	Disabled bool   `json:"disabled"`
}

// CHQuery represents a ClickHouse SQL query.
type CHQuery struct {
	Name     string `json:"name"`
	Legend   string `json:"legend,omitempty"`
	Disabled bool   `json:"disabled"`
	Query    string `json:"query"`
}

// Threshold represents a threshold configuration for a panel.
type Threshold struct {
	Index                 string   `json:"index"`
	KeyIndex              int      `json:"keyIndex"`
	ThresholdOperator     string   `json:"thresholdOperator,omitempty"`
	ThresholdValue        *float64 `json:"thresholdValue,omitempty"`
	ThresholdUnit         string   `json:"thresholdUnit,omitempty"`
	ThresholdColor        string   `json:"thresholdColor,omitempty"`
	ThresholdFormat       string   `json:"thresholdFormat,omitempty"`
	ThresholdLabel        string   `json:"thresholdLabel,omitempty"`
	ThresholdTableOptions string   `json:"thresholdTableOptions,omitempty"`
}

// =============================================================================
// Section 4: ValidationResult
// =============================================================================

// ValidationResult contains the result of panel validation.
type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func newResult() *ValidationResult {
	return &ValidationResult{
		Valid:    true,
		Errors:   []string{},
		Warnings: []string{},
	}
}

func (r *ValidationResult) addError(msg string) {
	r.Errors = append(r.Errors, msg)
	r.Valid = false
}

func (r *ValidationResult) addWarning(msg string) {
	r.Warnings = append(r.Warnings, msg)
}

// =============================================================================
// Section 5: Component Validators
// =============================================================================

func validateTagFilter(filter *TagFilter, path string, r *ValidationResult) {
	if filter == nil {
		return
	}
	if filter.Op != "AND" && filter.Op != "OR" {
		r.addError(fmt.Sprintf("%s.op: must be 'AND' or 'OR', got '%s'", path, filter.Op))
	}
	for i, item := range filter.Items {
		itemPath := fmt.Sprintf("%s.items[%d]", path, i)
		if item.ID == "" {
			r.addWarning(fmt.Sprintf("%s.id: filter item ID is empty", itemPath))
		}
		if item.Op == "" {
			r.addError(fmt.Sprintf("%s.op: filter operator is required", itemPath))
		} else if !validFilterOperators[item.Op] {
			r.addError(fmt.Sprintf("%s.op: invalid filter operator '%s'", itemPath, item.Op))
		}
	}
}

func validateHaving(having []HavingClause, path string, r *ValidationResult) {
	for i, h := range having {
		hPath := fmt.Sprintf("%s[%d]", path, i)
		if h.ColumnName == "" {
			r.addError(fmt.Sprintf("%s.columnName: must not be empty", hPath))
		}
		if h.Op == "" {
			r.addError(fmt.Sprintf("%s.op: having operator is required", hPath))
		} else if !validHavingOperators[h.Op] {
			r.addError(fmt.Sprintf("%s.op: invalid having operator '%s'. Valid: =, !=, IN, NOT_IN, >=, >, <=, <", hPath, h.Op))
		}
	}
}

func validateOrderBy(orderBy []OrderByPayload, path string, r *ValidationResult) {
	for i, o := range orderBy {
		oPath := fmt.Sprintf("%s[%d]", path, i)
		if o.ColumnName == "" {
			r.addError(fmt.Sprintf("%s.columnName: must not be empty", oPath))
		}
		order := strings.ToLower(o.Order)
		if order != "asc" && order != "desc" {
			r.addError(fmt.Sprintf("%s.order: must be 'asc' or 'desc', got '%s'", oPath, o.Order))
		}
	}
}

func validateFunctions(functions []QueryFunction, path string, r *ValidationResult) {
	for i, f := range functions {
		fPath := fmt.Sprintf("%s[%d]", path, i)
		if f.Name == "" {
			r.addError(fmt.Sprintf("%s.name: function name is required", fPath))
		} else if !validFunctionNames[f.Name] {
			r.addError(fmt.Sprintf("%s.name: invalid function '%s'", fPath, f.Name))
		}
	}
}

func getValidOperatorsForDataSource(dataSource string) map[string]bool {
	switch dataSource {
	case DataSourceMetrics:
		return validMetricAggregateOperators
	case DataSourceLogs:
		return validLogsAggregateOperators
	case DataSourceTraces:
		return validTracesAggregateOperators
	default:
		return nil
	}
}

func validateBuilderQuery(q BuilderQuery, idx int, panelType string, r *ValidationResult) {
	path := fmt.Sprintf("query.builder.queryData[%d]", idx)

	// queryName required
	if q.QueryName == "" {
		r.addError(fmt.Sprintf("%s.queryName: is required", path))
	}

	// dataSource validation
	if !validDataSources[q.DataSource] {
		r.addError(fmt.Sprintf("%s.dataSource: invalid data source '%s'. Valid: metrics, logs, traces", path, q.DataSource))
		return // can't do further validation without a valid data source
	}

	// aggregateOperator validation per data source
	if q.AggregateOperator != "" {
		operators := getValidOperatorsForDataSource(q.DataSource)
		if operators != nil && !operators[q.AggregateOperator] {
			r.addError(fmt.Sprintf("%s.aggregateOperator: '%s' is not valid for %s data source", path, q.AggregateOperator, q.DataSource))
		}
	}

	// List panel constraints
	if panelType == PanelTypeList {
		if q.DataSource == DataSourceMetrics {
			r.addError(fmt.Sprintf("%s.dataSource: list panel does not support 'metrics' data source. Use 'logs' or 'traces'", path))
		}
		if q.AggregateOperator != "" && q.AggregateOperator != "noop" {
			r.addError(fmt.Sprintf("%s.aggregateOperator: list panel requires 'noop' operator, got '%s'", path, q.AggregateOperator))
		}
		if len(q.OrderBy) == 0 {
			r.addWarning(fmt.Sprintf("%s: list panel should have orderBy (typically [{columnName: 'timestamp', order: 'desc'}])", path))
		}
		if q.PageSize == nil {
			r.addWarning(fmt.Sprintf("%s: list panel should have pageSize set (typically 100)", path))
		}
	}

	// Value/Pie panel: reduceTo is critical
	if panelType == PanelTypeValue || panelType == PanelTypePie {
		if !q.Disabled && q.ReduceTo == "" {
			r.addWarning(fmt.Sprintf("%s.reduceTo: %s panel should have reduceTo set (defaults to 'avg'). Without it, the panel may show unexpected values", path, panelType))
		}
	}

	// Table panel: groupBy recommended
	if panelType == PanelTypeTable {
		if !q.Disabled && len(q.GroupBy) == 0 {
			r.addWarning(fmt.Sprintf("%s.groupBy: table panel without groupBy may produce a less useful single-row table", path))
		}
	}

	// Pie panel: groupBy recommended for multiple slices
	if panelType == PanelTypePie {
		if !q.Disabled && len(q.GroupBy) == 0 {
			r.addWarning(fmt.Sprintf("%s.groupBy: pie panel without groupBy will show a single slice", path))
		}
	}

	// reduceTo validation
	if q.ReduceTo != "" && !validReduceToOperators[q.ReduceTo] {
		r.addError(fmt.Sprintf("%s.reduceTo: invalid value '%s'. Valid: last, sum, avg, max, min", path, q.ReduceTo))
	}

	// Metrics-specific: timeAggregation and spaceAggregation
	if q.DataSource == DataSourceMetrics {
		if q.TimeAggregation != "" && !validTimeAggregations[q.TimeAggregation] {
			r.addError(fmt.Sprintf("%s.timeAggregation: invalid value '%s'", path, q.TimeAggregation))
		}
		if q.SpaceAggregation != "" && !validSpaceAggregations[q.SpaceAggregation] {
			r.addError(fmt.Sprintf("%s.spaceAggregation: invalid value '%s'", path, q.SpaceAggregation))
		}
	}

	// Source validation
	if q.Source != "" && q.Source != "meter" {
		r.addError(fmt.Sprintf("%s.source: must be '' or 'meter', got '%s'", path, q.Source))
	}

	// Sub-component validation
	validateTagFilter(q.Filters, path+".filters", r)
	validateHaving(q.Having, path+".having", r)
	validateOrderBy(q.OrderBy, path+".orderBy", r)
	validateFunctions(q.Functions, path+".functions", r)
}

// formulaRefPattern matches single uppercase letters that reference queries (A, B, C, etc.)
var formulaRefPattern = regexp.MustCompile(`\b([A-Z])\b`)

func validateBuilderFormula(f BuilderFormula, idx int, queryNames map[string]bool, r *ValidationResult) {
	path := fmt.Sprintf("query.builder.queryFormulas[%d]", idx)

	if f.QueryName == "" {
		r.addError(fmt.Sprintf("%s.queryName: is required", path))
	}

	if f.Expression == "" {
		r.addError(fmt.Sprintf("%s.expression: formula expression is required", path))
		return
	}

	// Check that referenced query names exist
	refs := formulaRefPattern.FindAllString(f.Expression, -1)
	for _, ref := range refs {
		if !queryNames[ref] {
			r.addError(fmt.Sprintf("%s.expression: references query '%s' which does not exist in queryData. Available: %s",
				path, ref, joinKeys(queryNames)))
		}
	}

	// Sub-component validation
	validateHaving(f.Having, path+".having", r)
	validateOrderBy(f.OrderBy, path+".orderBy", r)
}

func validatePromQLQuery(q PromQLQuery, idx int, r *ValidationResult) {
	path := fmt.Sprintf("query.promql[%d]", idx)
	if q.Name == "" {
		r.addError(fmt.Sprintf("%s.name: is required", path))
	}
	if q.Query == "" && !q.Disabled {
		r.addError(fmt.Sprintf("%s.query: PromQL query string is required", path))
	}
}

func validateClickHouseQuery(q CHQuery, idx int, r *ValidationResult) {
	path := fmt.Sprintf("query.clickhouse_sql[%d]", idx)
	if q.Name == "" {
		r.addError(fmt.Sprintf("%s.name: is required", path))
	}
	if q.Query == "" && !q.Disabled {
		r.addError(fmt.Sprintf("%s.query: ClickHouse SQL query string is required", path))
	}
}

func validateQuery(q Query, panelType string, r *ValidationResult) {
	// queryType validation
	if q.QueryType == "" {
		r.addError("query.queryType: is required. Valid: builder, clickhouse_sql, promql")
		return
	}
	if !validQueryTypes[q.QueryType] {
		r.addError(fmt.Sprintf("query.queryType: invalid value '%s'. Valid: builder, clickhouse_sql, promql", q.QueryType))
		return
	}

	// queryType must be supported for the panel type
	supportedTypes := panelTypeQueryTypes[panelType]
	if supportedTypes != nil && !supportedTypes[q.QueryType] {
		supported := joinKeys(supportedTypes)
		r.addError(fmt.Sprintf("query.queryType: '%s' is not supported for %s panel. Supported: %s", q.QueryType, panelType, supported))
	}

	// Validate based on queryType
	switch q.QueryType {
	case QueryTypeBuilder:
		if len(q.Builder.QueryData) == 0 {
			r.addError("query.builder.queryData: must have at least one query")
		}

		// Collect query names for formula validation
		queryNames := make(map[string]bool)
		for _, qd := range q.Builder.QueryData {
			if qd.QueryName != "" {
				queryNames[qd.QueryName] = true
			}
			// Also index by expression since formulas reference the expression name
			if qd.Expression != "" {
				queryNames[qd.Expression] = true
			}
		}

		for i, qd := range q.Builder.QueryData {
			validateBuilderQuery(qd, i, panelType, r)
		}
		for i, f := range q.Builder.QueryFormulas {
			validateBuilderFormula(f, i, queryNames, r)
		}

	case QueryTypePromQL:
		if len(q.PromQL) == 0 {
			r.addError("query.promql: must have at least one PromQL query")
		}
		for i, pq := range q.PromQL {
			validatePromQLQuery(pq, i, r)
		}

	case QueryTypeClickHouse:
		if len(q.ClickHouseQL) == 0 {
			r.addError("query.clickhouse_sql: must have at least one ClickHouse query")
		}
		for i, ch := range q.ClickHouseQL {
			validateClickHouseQuery(ch, i, r)
		}
	}
}

func validateThresholds(thresholds []Threshold, panelType string, r *ValidationResult) {
	if len(thresholds) == 0 {
		return
	}

	if !panelSupportsThreshold[panelType] {
		r.addWarning(fmt.Sprintf("thresholds: %s panel does not support thresholds; they will be ignored", panelType))
	}

	for i, t := range thresholds {
		tPath := fmt.Sprintf("thresholds[%d]", i)

		if t.Index == "" {
			r.addWarning(fmt.Sprintf("%s.index: threshold index is empty", tPath))
		}

		if t.ThresholdOperator != "" && !validThresholdOperators[t.ThresholdOperator] {
			r.addError(fmt.Sprintf("%s.thresholdOperator: invalid operator '%s'. Valid: >, <, >=, <=, =", tPath, t.ThresholdOperator))
		}

		if t.ThresholdFormat != "" && !validThresholdFormats[t.ThresholdFormat] {
			r.addError(fmt.Sprintf("%s.thresholdFormat: invalid format '%s'. Valid: Text, Background", tPath, t.ThresholdFormat))
		}

		if t.ThresholdColor != "" && !isHexColor(t.ThresholdColor) {
			r.addWarning(fmt.Sprintf("%s.thresholdColor: '%s' does not look like a valid hex color (expected #RGB or #RRGGBB)", tPath, t.ThresholdColor))
		}
	}
}

// =============================================================================
// Section 6: Panel-Type Specific Validators
// =============================================================================

func validateTimeSeriesPanel(p Panel, r *ValidationResult) {
	if p.LegendPosition != "" && p.LegendPosition != "bottom" && p.LegendPosition != "right" {
		r.addWarning(fmt.Sprintf("legendPosition: invalid value '%s'. Valid: bottom, right", p.LegendPosition))
	}
}

func validateValuePanel(p Panel, r *ValidationResult) {
	if p.Query.QueryType == QueryTypeBuilder {
		for i, q := range p.Query.Builder.QueryData {
			if !q.Disabled && q.ReduceTo == "" {
				r.addWarning(fmt.Sprintf("query.builder.queryData[%d].reduceTo: value panel needs reduceTo to collapse time series to a single number. Defaulting to 'avg'", i))
			}
		}
	}
}

func validateTablePanel(p Panel, r *ValidationResult) {
	// columnUnits values don't need strict validation - they're user-specified unit strings
}

func validateListPanel(p Panel, r *ValidationResult) {
	if p.Query.QueryType != QueryTypeBuilder {
		r.addError(fmt.Sprintf("query.queryType: list panel only supports 'builder' query type, got '%s'", p.Query.QueryType))
	}
}

func validateBarPanel(p Panel, r *ValidationResult) {
	if p.LegendPosition != "" && p.LegendPosition != "bottom" && p.LegendPosition != "right" {
		r.addWarning(fmt.Sprintf("legendPosition: invalid value '%s'. Valid: bottom, right", p.LegendPosition))
	}
}

func validatePiePanel(p Panel, r *ValidationResult) {
	if p.Query.QueryType == QueryTypeBuilder {
		for i, q := range p.Query.Builder.QueryData {
			if !q.Disabled && q.ReduceTo == "" {
				r.addWarning(fmt.Sprintf("query.builder.queryData[%d].reduceTo: pie panel needs reduceTo to collapse time series. Defaulting to 'avg'", i))
			}
		}
	}
}

func validateHistogramPanel(p Panel, r *ValidationResult) {
	if p.BucketCount != nil && *p.BucketCount <= 0 {
		r.addError("bucketCount: must be a positive integer")
	}
	if p.BucketWidth != nil && *p.BucketWidth < 0 {
		r.addError("bucketWidth: must be non-negative")
	}

	// Check if any query uses non-metrics data source
	if p.Query.QueryType == QueryTypeBuilder {
		for i, q := range p.Query.Builder.QueryData {
			if !q.Disabled && q.DataSource != DataSourceMetrics {
				r.addWarning(fmt.Sprintf("query.builder.queryData[%d].dataSource: histogram panel works best with 'metrics' data source, got '%s'", i, q.DataSource))
			}
		}
	}
}

// =============================================================================
// Section 7: Main ValidatePanel Function
// =============================================================================

// ValidatePanel validates a SigNoz dashboard panel and returns errors and warnings.
// Errors indicate the panel will not render correctly.
// Warnings indicate the panel will work but may produce unexpected results.
func ValidatePanel(panel Panel) ValidationResult {
	r := newResult()

	// Required fields
	if panel.ID == "" {
		r.addError("id: panel ID is required")
	}
	if panel.PanelTypes == "" {
		r.addError("panelTypes: is required")
		return *r
	}
	if !validPanelTypes[panel.PanelTypes] {
		r.addError(fmt.Sprintf("panelTypes: invalid panel type '%s'. Valid: graph, value, table, list, bar, pie, histogram", panel.PanelTypes))
		return *r
	}

	// Validate query
	validateQuery(panel.Query, panel.PanelTypes, r)

	// Validate thresholds
	validateThresholds(panel.Thresholds, panel.PanelTypes, r)

	// Warn about visual fields on unsupported panel types
	warnIfUnsupported := func(fieldName string, isSet bool, supportMap map[string]bool) {
		if isSet && !supportMap[panel.PanelTypes] {
			r.addWarning(fmt.Sprintf("%s: not supported on %s panel; it will be ignored", fieldName, panel.PanelTypes))
		}
	}

	warnIfUnsupported("yAxisUnit", panel.YAxisUnit != "", panelSupportsYAxisUnit)
	warnIfUnsupported("softMin", panel.SoftMin != nil, panelSupportsSoftMinMax)
	warnIfUnsupported("softMax", panel.SoftMax != nil, panelSupportsSoftMinMax)
	warnIfUnsupported("fillSpans", panel.FillSpans != nil, panelSupportsFillSpan)
	warnIfUnsupported("isLogScale", panel.IsLogScale != nil, panelSupportsLogScale)
	warnIfUnsupported("stackedBarChart", panel.StackedBarChart != nil, panelSupportsStacking)
	warnIfUnsupported("bucketCount", panel.BucketCount != nil, panelSupportsBucketConfig)
	warnIfUnsupported("bucketWidth", panel.BucketWidth != nil, panelSupportsBucketConfig)
	warnIfUnsupported("columnUnits", len(panel.ColumnUnits) > 0, panelSupportsColumnUnits)
	warnIfUnsupported("legendPosition", panel.LegendPosition != "", panelSupportsLegendPosition)
	warnIfUnsupported("lineInterpolation", panel.LineInterpolation != "", panelSupportsLineInterpolation)
	warnIfUnsupported("lineStyle", panel.LineStyle != "", panelSupportsLineStyle)
	warnIfUnsupported("fillMode", panel.FillMode != "", panelSupportsFillMode)
	warnIfUnsupported("showPoints", panel.ShowPoints != nil, panelSupportsShowPoints)
	warnIfUnsupported("spanGaps", panel.SpanGaps != nil, panelSupportsSpanGaps)

	// Panel-type-specific validation
	switch panel.PanelTypes {
	case PanelTypeTimeSeries:
		validateTimeSeriesPanel(panel, r)
	case PanelTypeValue:
		validateValuePanel(panel, r)
	case PanelTypeTable:
		validateTablePanel(panel, r)
	case PanelTypeList:
		validateListPanel(panel, r)
	case PanelTypeBar:
		validateBarPanel(panel, r)
	case PanelTypePie:
		validatePiePanel(panel, r)
	case PanelTypeHistogram:
		validateHistogramPanel(panel, r)
	}

	return *r
}

// =============================================================================
// Section 8: CreateDefaultPanel Helper
// =============================================================================

// CreateDefaultPanel creates a fully valid default panel for the given panel type and data source.
// If dataSource is empty, it defaults to "metrics" (or "logs" for list panels).
// The returned panel passes ValidatePanel with zero errors.
func CreateDefaultPanel(panelType string, dataSource string) Panel {
	if dataSource == "" {
		switch panelType {
		case PanelTypeList:
			dataSource = DataSourceLogs
		default:
			dataSource = DataSourceMetrics
		}
	}

	// Default aggregate operator per data source
	defaultAggOp := "count"
	if panelType == PanelTypeList {
		defaultAggOp = "noop"
	}

	// Default query
	bq := BuilderQuery{
		QueryName:         "A",
		DataSource:        dataSource,
		AggregateOperator: defaultAggOp,
		AggregateAttribute: &AutocompleteData{
			Key:      "",
			DataType: "",
			Type:     "",
		},
		Filters:    &TagFilter{Items: []TagFilterItem{}, Op: "AND"},
		GroupBy:    []AutocompleteData{},
		Expression: "A",
		Disabled:   false,
		Having:     []HavingClause{},
		OrderBy:    []OrderByPayload{},
		Functions:  []QueryFunction{},
		Legend:     "",
		ReduceTo:   "avg",
	}

	// Metrics-specific defaults
	if dataSource == DataSourceMetrics {
		bq.TimeAggregation = "rate"
		bq.SpaceAggregation = "sum"
	}

	// List-specific defaults
	if panelType == PanelTypeList {
		bq.ReduceTo = ""
		bq.OrderBy = []OrderByPayload{{ColumnName: "timestamp", Order: "desc"}}
		pageSize := 100
		bq.PageSize = &pageSize
		offset := 0
		bq.Offset = &offset
	}

	query := Query{
		QueryType: QueryTypeBuilder,
		Builder: QueryBuilderData{
			QueryData:     []BuilderQuery{bq},
			QueryFormulas: []BuilderFormula{},
		},
		PromQL:       []PromQLQuery{},
		ClickHouseQL: []CHQuery{},
		ID:           generateID(),
		Unit:         "",
	}

	panel := Panel{
		ID:                    generateID(),
		PanelTypes:            panelType,
		Title:                 "",
		Description:           "",
		Query:                 query,
		Opacity:               "1",
		NullZeroValues:        "zero",
		SelectedLogFields:     nil,
		SelectedTracesFields:  nil,
	}

	// Panel-type-specific defaults
	switch panelType {
	case PanelTypeHistogram:
		bc := 30
		panel.BucketCount = &bc
		bw := 0.0
		panel.BucketWidth = &bw
		maq := false
		panel.MergeAllActiveQueries = &maq
	case PanelTypeBar:
		stacked := false
		panel.StackedBarChart = &stacked
	}

	return panel
}

// =============================================================================
// Section 9: Utility Functions
// =============================================================================

var hexColorPattern = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

func isHexColor(s string) bool {
	return hexColorPattern.MatchString(s)
}

func joinKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			keys = append(keys, k)
		}
	}
	return strings.Join(keys, ", ")
}

// generateID creates a simple unique ID. Replace with uuid.New().String() if you have a UUID library.
func generateID() string {
	// Simple implementation using timestamp - replace with proper UUID in production
	return fmt.Sprintf("%d", uniqueCounter())
}

var idCounter int64

func uniqueCounter() int64 {
	idCounter++
	return idCounter
}
