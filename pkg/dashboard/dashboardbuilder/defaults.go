package dashboardbuilder

import "github.com/google/uuid"

// Panel types (from frontend PANEL_TYPES enum).
const (
	PanelTypeGraph     = "graph"
	PanelTypeValue     = "value"
	PanelTypeTable     = "table"
	PanelTypeList      = "list"
	PanelTypeTrace     = "trace"
	PanelTypeBar       = "bar"
	PanelTypePie       = "pie"
	PanelTypeHistogram = "histogram"
	PanelTypeRow       = "row"
)

// Query types (from frontend EQueryType enum).
const (
	QueryTypeBuilder    = "builder"
	QueryTypeClickHouse = "clickhouse_sql"
	QueryTypePromQL     = "promql"
)

// Variable types.
const (
	VariableTypeQuery   = "QUERY"
	VariableTypeTextbox = "TEXTBOX"
	VariableTypeCustom  = "CUSTOM"
	VariableTypeDynamic = "DYNAMIC"
)

// Variable sort types.
const (
	SortDisabled = "DISABLED"
	SortASC      = "ASC"
	SortDESC     = "DESC"
)

// Data sources for builder queries.
const (
	DataSourceMetrics = "metrics"
	DataSourceTraces  = "traces"
	DataSourceLogs    = "logs"
)

// Dynamic variable sources.
const (
	DynamicSourceTraces     = "Traces"
	DynamicSourceLogs       = "Logs"
	DynamicSourceMetrics    = "Metrics"
	DynamicSourceAllSources = "All telemetry"
)

// Legend positions.
const (
	LegendBottom = "bottom"
	LegendRight  = "right"
)

// Line interpolation modes.
const (
	InterpolationLinear     = "linear"
	InterpolationSpline     = "spline"
	InterpolationStepAfter  = "stepAfter"
	InterpolationStepBefore = "stepBefore"
)

// Line styles.
const (
	LineStyleSolid  = "solid"
	LineStyleDashed = "dashed"
)

// Fill modes.
const (
	FillModeSolid    = "solid"
	FillModeGradient = "gradient"
	FillModeNone     = "none"
)

// Grid constants (from react-grid-layout configuration).
const (
	GridColumns         = 12
	DefaultWidgetWidth  = 6
	DefaultWidgetHeight = 6
	RowWidth            = 12
	RowHeight           = 1
)

// Default widget display values.
const (
	DefaultOpacity        = "1"
	DefaultNullZeroValues = "zero"
	DefaultTimePreferance = "GLOBAL_TIME" // Intentional typo — matches frontend.
)

// Dashboard version.
const DashboardVersion = "v5"

// Allowed value sets for validation.
var (
	ValidPanelTypes = []string{
		PanelTypeGraph, PanelTypeValue, PanelTypeTable, PanelTypeList,
		PanelTypeTrace, PanelTypeBar, PanelTypePie, PanelTypeHistogram,
	}

	ValidQueryTypes = []string{QueryTypeBuilder, QueryTypeClickHouse, QueryTypePromQL}

	ValidVariableTypes = []string{VariableTypeQuery, VariableTypeTextbox, VariableTypeCustom, VariableTypeDynamic}

	ValidSortTypes = []string{SortDisabled, SortASC, SortDESC}

	ValidDataSources = []string{DataSourceMetrics, DataSourceTraces, DataSourceLogs}

	ValidDynamicVariableSources = []string{DynamicSourceTraces, DynamicSourceLogs, DynamicSourceMetrics, DynamicSourceAllSources}

	ValidLegendPositions = []string{LegendBottom, LegendRight}

	ValidLineInterpolations = []string{InterpolationLinear, InterpolationSpline, InterpolationStepAfter, InterpolationStepBefore}

	ValidLineStyles = []string{LineStyleSolid, LineStyleDashed}

	ValidFillModes = []string{FillModeSolid, FillModeGradient, FillModeNone}
)

// ApplyDefaults fills in missing fields with sensible defaults.
// It should be called before Validate.
// The multiSelectSet/showALLSet maps track which variables had these bool fields
// explicitly provided in JSON (see convert.go). For variables not in these maps,
// DYNAMIC variables get multiSelect=true and showALLOption=true.
func ApplyDefaults(d *DashboardData, multiSelectSet, showALLSet map[string]bool) {
	if d.Variables == nil {
		d.Variables = make(map[string]*DashboardVariable)
	}
	if d.Version == "" {
		d.Version = DashboardVersion
	}

	// Variable defaults.
	order := 0
	for key, v := range d.Variables {
		if v.ID == "" {
			v.ID = uuid.NewString()
		}
		if v.Name == "" {
			v.Name = key
		}
		if v.Type == "" {
			v.Type = VariableTypeDynamic
		}
		if v.Sort == "" {
			if v.Type == VariableTypeDynamic {
				v.Sort = SortASC
			} else {
				v.Sort = SortDisabled
			}
		}
		if v.Order == nil {
			o := order
			v.Order = &o
			order++
		} else {
			if *v.Order >= order {
				order = *v.Order + 1
			}
		}

		// For DYNAMIC type, default multiSelect and showALLOption to true
		// only if they were not explicitly set in JSON.
		if v.Type == VariableTypeDynamic {
			if multiSelectSet != nil {
				if _, wasSet := multiSelectSet[key]; !wasSet {
					v.MultiSelect = true
				}
			}
			if showALLSet != nil {
				if _, wasSet := showALLSet[key]; !wasSet {
					v.ShowALLOption = true
				}
			}
		}
	}

	// Widget defaults.
	for i := range d.Widgets {
		w := &d.Widgets[i]
		if w.ID == "" {
			w.ID = uuid.NewString()
		}
		if w.PanelTypes == PanelTypeRow {
			continue
		}

		if w.Opacity == "" {
			w.Opacity = DefaultOpacity
		}
		if w.NullZeroValues == "" {
			w.NullZeroValues = DefaultNullZeroValues
		}
		if w.TimePreferance == "" {
			w.TimePreferance = DefaultTimePreferance
		}
		if w.SoftMin == nil {
			zero := 0.0
			w.SoftMin = &zero
		}
		if w.SoftMax == nil {
			zero := 0.0
			w.SoftMax = &zero
		}
		if w.SelectedLogFields == nil {
			w.SelectedLogFields = []byte("null")
		}
		if w.SelectedTracesFields == nil {
			w.SelectedTracesFields = []byte("null")
		}

		if w.Query != nil && w.Query.ID == "" {
			w.Query.ID = uuid.NewString()
		}
		// Ensure builder sub-fields are not nil.
		if w.Query != nil && w.Query.QueryType == QueryTypeBuilder && w.Query.Builder != nil {
			if w.Query.Builder.QueryData == nil {
				w.Query.Builder.QueryData = []map[string]any{}
			}
			if w.Query.Builder.QueryFormulas == nil {
				w.Query.Builder.QueryFormulas = []map[string]any{}
			}
			coerceHavingInQueryMaps(w.Query.Builder.QueryData)
			coerceHavingInQueryMaps(w.Query.Builder.QueryFormulas)
			uppercaseFilterOpsInQueryMaps(w.Query.Builder.QueryData)
			uppercaseFilterOpsInQueryMaps(w.Query.Builder.QueryFormulas)
			normalizeFilterItemsInQueryMaps(w.Query.Builder.QueryData)
			normalizeFilterItemsInQueryMaps(w.Query.Builder.QueryFormulas)
		}
		// Ensure clickhouse_sql and promql are not nil.
		if w.Query != nil {
			if w.Query.ClickhouseSQL == nil {
				w.Query.ClickhouseSQL = []ClickHouseQuery{}
			}
			if w.Query.PromQL == nil {
				w.Query.PromQL = []PromQLQuery{}
			}
		}
	}

	// Auto-compute layout if missing.
	if len(d.Layout) == 0 && len(d.Widgets) > 0 {
		d.Layout = ComputeAutoLayout(d.Widgets)
	}

	// Ensure panelMap is initialized.
	if d.PanelMap == nil {
		d.PanelMap = make(map[string]*PanelMapEntry)
	}
}

// applyBuilderDefaults is the simpler path used by the fluent builder API
// where we don't need JSON-level bool tracking.
func applyBuilderDefaults(d *DashboardData) {
	if d.Variables == nil {
		d.Variables = make(map[string]*DashboardVariable)
	}
	if d.Version == "" {
		d.Version = DashboardVersion
	}

	order := 0
	for key, v := range d.Variables {
		if v.ID == "" {
			v.ID = uuid.NewString()
		}
		if v.Name == "" {
			v.Name = key
		}
		if v.Type == "" {
			v.Type = VariableTypeDynamic
		}
		if v.Sort == "" {
			if v.Type == VariableTypeDynamic {
				v.Sort = SortASC
			} else {
				v.Sort = SortDisabled
			}
		}
		if v.Order == nil {
			o := order
			v.Order = &o
			order++
		} else {
			if *v.Order >= order {
				order = *v.Order + 1
			}
		}
	}

	for i := range d.Widgets {
		w := &d.Widgets[i]
		if w.ID == "" {
			w.ID = uuid.NewString()
		}
		if w.PanelTypes == PanelTypeRow {
			continue
		}
		if w.Opacity == "" {
			w.Opacity = DefaultOpacity
		}
		if w.NullZeroValues == "" {
			w.NullZeroValues = DefaultNullZeroValues
		}
		if w.TimePreferance == "" {
			w.TimePreferance = DefaultTimePreferance
		}
		if w.SoftMin == nil {
			zero := 0.0
			w.SoftMin = &zero
		}
		if w.SoftMax == nil {
			zero := 0.0
			w.SoftMax = &zero
		}
		if w.SelectedLogFields == nil {
			w.SelectedLogFields = []byte("null")
		}
		if w.SelectedTracesFields == nil {
			w.SelectedTracesFields = []byte("null")
		}
		if w.Query != nil && w.Query.ID == "" {
			w.Query.ID = uuid.NewString()
		}
		if w.Query != nil && w.Query.QueryType == QueryTypeBuilder && w.Query.Builder != nil {
			if w.Query.Builder.QueryData == nil {
				w.Query.Builder.QueryData = []map[string]any{}
			}
			if w.Query.Builder.QueryFormulas == nil {
				w.Query.Builder.QueryFormulas = []map[string]any{}
			}
			coerceHavingInQueryMaps(w.Query.Builder.QueryData)
			coerceHavingInQueryMaps(w.Query.Builder.QueryFormulas)
			uppercaseFilterOpsInQueryMaps(w.Query.Builder.QueryData)
			uppercaseFilterOpsInQueryMaps(w.Query.Builder.QueryFormulas)
			normalizeFilterItemsInQueryMaps(w.Query.Builder.QueryData)
			normalizeFilterItemsInQueryMaps(w.Query.Builder.QueryFormulas)
		}
		if w.Query != nil {
			if w.Query.ClickhouseSQL == nil {
				w.Query.ClickhouseSQL = []ClickHouseQuery{}
			}
			if w.Query.PromQL == nil {
				w.Query.PromQL = []PromQLQuery{}
			}
		}
	}

	if len(d.Layout) == 0 && len(d.Widgets) > 0 {
		d.Layout = ComputeAutoLayout(d.Widgets)
	}

	if d.PanelMap == nil {
		d.PanelMap = make(map[string]*PanelMapEntry)
	}
}
