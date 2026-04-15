package dashboardbuilder

import (
	"strings"
	"testing"
)

func TestValidate_MissingTitle(t *testing.T) {
	d := &DashboardData{
		Variables: map[string]*DashboardVariable{},
		Version:   DashboardVersion,
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing title")
	}
	assertHasFieldError(t, verr, "title")
}

func TestValidate_InvalidVersion(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Variables: map[string]*DashboardVariable{},
		Version:   "v3",
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid version")
	}
	assertHasFieldError(t, verr, "version")
}

func TestValidate_InvalidVariableType(t *testing.T) {
	d := &DashboardData{
		Title:   "Test",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"v1": {ID: "id1", Type: "INVALID", Sort: SortDisabled},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid variable type")
	}
	assertHasFieldError(t, verr, "variables.v1.type")
}

func TestValidate_QueryVariableMissingQueryValue(t *testing.T) {
	d := &DashboardData{
		Title:   "Test",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"v1": {ID: "id1", Type: VariableTypeQuery, Sort: SortDisabled},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing queryValue")
	}
	assertHasFieldError(t, verr, "variables.v1.queryValue")
}

func TestValidate_CustomVariableMissingCustomValue(t *testing.T) {
	d := &DashboardData{
		Title:   "Test",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"v1": {ID: "id1", Type: VariableTypeCustom, Sort: SortDisabled},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing customValue")
	}
	assertHasFieldError(t, verr, "variables.v1.customValue")
}

func TestValidate_VariableKeyWithSpaces(t *testing.T) {
	d := &DashboardData{
		Title:   "Test",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"bad key": {ID: "id1", Type: VariableTypeDynamic, Sort: SortASC},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for variable key with spaces")
	}
	assertHasFieldError(t, verr, "variables.bad key")
}

func TestValidate_InvalidDynamicVariableSource(t *testing.T) {
	d := &DashboardData{
		Title:   "Test",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"v1": {
				ID:                     "id1",
				Type:                   VariableTypeDynamic,
				Sort:                   SortASC,
				DynamicVariablesSource: "invalid_source",
			},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid dynamic variable source")
	}
	assertHasFieldError(t, verr, "variables.v1.dynamicVariablesSource")
}

func TestValidate_DuplicateWidgetIDs(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "dup", PanelTypes: PanelTypeGraph, Title: "W1", Query: validBuilderQuery()},
			{ID: "dup", PanelTypes: PanelTypeGraph, Title: "W2", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "dup", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for duplicate widget IDs")
	}
	assertHasFieldError(t, verr, "widgets[1].id")
}

func TestValidate_InvalidPanelType(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: "invalid_panel", Title: "W1", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid panel type")
	}
	assertHasFieldError(t, verr, "widgets[0].panelTypes")
}

func TestValidate_MissingQueryForNonRowWidget(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1"},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing query")
	}
	assertHasFieldError(t, verr, "widgets[0].query")
}

func TestValidate_InvalidQueryType(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1", Query: &Query{QueryType: "bad"}},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid query type")
	}
	assertHasFieldError(t, verr, "widgets[0].query.queryType")
}

func TestValidate_BuilderQueryMissingQueryData(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{
				ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1",
				Query: &Query{
					QueryType: QueryTypeBuilder,
					Builder:   &BuilderData{QueryData: []map[string]interface{}{}},
				},
			},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for empty queryData")
	}
	assertHasFieldError(t, verr, "widgets[0].query.builder.queryData")
}

func TestValidate_BuilderQueryMissingDataSource(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{
				ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1",
				Query: &Query{
					QueryType: QueryTypeBuilder,
					Builder: &BuilderData{
						QueryData: []map[string]interface{}{
							{"queryName": "A", "expression": "A"},
						},
					},
				},
			},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing dataSource")
	}
	assertHasFieldError(t, verr, "widgets[0].query.builder.queryData[0].dataSource")
}

func TestValidate_ClickHouseQueryEmpty(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{
				ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1",
				Query: &Query{QueryType: QueryTypeClickHouse, ClickhouseSQL: []ClickHouseQuery{}},
			},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for empty clickhouse_sql")
	}
	assertHasFieldError(t, verr, "widgets[0].query.clickhouse_sql")
}

func TestValidate_PromQLQueryEmpty(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{
				ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1",
				Query: &Query{QueryType: QueryTypePromQL, PromQL: []PromQLQuery{}},
			},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for empty promql")
	}
	assertHasFieldError(t, verr, "widgets[0].query.promql")
}

func TestValidate_LayoutExceedsGrid(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 8, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for layout exceeding grid")
	}
	assertHasFieldError(t, verr, "layout[0]")
}

func TestValidate_LayoutReferencesUnknownWidget(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
			{I: "unknown", X: 6, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for unknown widget reference")
	}
	assertHasFieldError(t, verr, "layout[1].i")
}

func TestValidate_MissingLayoutForWidget(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1", Query: validBuilderQuery()},
			{ID: "w2", PanelTypes: PanelTypeGraph, Title: "W2", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for missing layout entry")
	}
	assertHasFieldError(t, verr, "layout")
}

func TestValidate_RowWidgetNoQueryRequired(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{ID: "row-1", PanelTypes: PanelTypeRow, Title: "Row"},
		},
		Layout: []LayoutItem{
			{I: "row-1", X: 0, Y: 0, W: 12, H: 1},
		},
	}
	verr := Validate(d)
	if verr != nil {
		t.Fatalf("expected no errors for valid row widget, got: %v", verr)
	}
}

func TestValidate_ValidDashboard(t *testing.T) {
	d := &DashboardData{
		Title:   "Valid Dashboard",
		Version: DashboardVersion,
		Variables: map[string]*DashboardVariable{
			"svc": {
				ID:                        "v1",
				Type:                      VariableTypeDynamic,
				Sort:                      SortASC,
				DynamicVariablesAttribute: "service.name",
				DynamicVariablesSource:    DynamicSourceTraces,
			},
		},
		Widgets: []WidgetOrRow{
			{ID: "w1", PanelTypes: PanelTypeGraph, Title: "Latency", Query: validBuilderQuery()},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
		PanelMap: map[string]*PanelMapEntry{},
	}
	verr := Validate(d)
	if verr != nil {
		t.Fatalf("expected no errors, got: %v", verr)
	}
}

func TestValidate_InvalidLegendPosition(t *testing.T) {
	d := &DashboardData{
		Title:     "Test",
		Version:   DashboardVersion,
		Variables: map[string]*DashboardVariable{},
		Widgets: []WidgetOrRow{
			{
				ID: "w1", PanelTypes: PanelTypeGraph, Title: "W1",
				Query: validBuilderQuery(), LegendPosition: "top",
			},
		},
		Layout: []LayoutItem{
			{I: "w1", X: 0, Y: 0, W: 6, H: 6},
		},
	}
	verr := Validate(d)
	if verr == nil {
		t.Fatal("expected validation error for invalid legend position")
	}
	assertHasFieldError(t, verr, "widgets[0].legendPosition")
}

// --- helpers ---

func validBuilderQuery() *Query {
	return &Query{
		QueryType: QueryTypeBuilder,
		Builder: &BuilderData{
			QueryData: []map[string]interface{}{
				{"queryName": "A", "dataSource": "metrics", "expression": "A"},
			},
		},
	}
}

func assertHasFieldError(t *testing.T, verr *ValidationError, field string) {
	t.Helper()
	for _, fe := range verr.Errors {
		if strings.Contains(fe.Field, field) {
			return
		}
	}
	t.Errorf("expected error on field %q, but got errors: %v", field, verr)
}
