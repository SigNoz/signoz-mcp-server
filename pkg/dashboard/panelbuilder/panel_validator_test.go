package panelvalidator

import (
	"encoding/json"
	"testing"
)

func TestCreateDefaultPanel_AllTypes(t *testing.T) {
	panelTypes := []string{
		PanelTypeTimeSeries,
		PanelTypeValue,
		PanelTypeTable,
		PanelTypeList,
		PanelTypeBar,
		PanelTypePie,
		PanelTypeHistogram,
	}

	for _, pt := range panelTypes {
		t.Run(pt, func(t *testing.T) {
			panel := CreateDefaultPanel(pt, "")
			result := ValidatePanel(panel)
			if !result.Valid {
				t.Errorf("CreateDefaultPanel(%q) should produce a valid panel, got errors: %v", pt, result.Errors)
			}
			if len(result.Errors) > 0 {
				t.Errorf("CreateDefaultPanel(%q) errors: %v", pt, result.Errors)
			}
		})
	}
}

func TestCreateDefaultPanel_DataSources(t *testing.T) {
	for _, ds := range []string{DataSourceMetrics, DataSourceLogs, DataSourceTraces} {
		t.Run(ds, func(t *testing.T) {
			panel := CreateDefaultPanel(PanelTypeTimeSeries, ds)
			result := ValidatePanel(panel)
			if !result.Valid {
				t.Errorf("CreateDefaultPanel(graph, %q) should produce valid panel, got errors: %v", ds, result.Errors)
			}
		})
	}
}

func TestValidatePanel_InvalidPanelType(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, "")
	panel.PanelTypes = "invalid_type"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad panel type")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors for bad panel type")
	}
}

func TestValidatePanel_MissingID(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, "")
	panel.ID = ""
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for missing ID")
	}
}

func TestValidatePanel_ListPanelWithMetrics(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeList, DataSourceLogs)
	panel.Query.Builder.QueryData[0].DataSource = DataSourceMetrics
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for list panel with metrics data source")
	}
	found := false
	for _, e := range result.Errors {
		if contains(e, "list panel does not support 'metrics'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about metrics not supported for list, got: %v", result.Errors)
	}
}

func TestValidatePanel_ListPanelWithNonNoopOperator(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeList, DataSourceLogs)
	panel.Query.Builder.QueryData[0].AggregateOperator = "count"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for list panel with non-noop operator")
	}
}

func TestValidatePanel_ValuePanelWithoutReduceTo(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeValue, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].ReduceTo = ""
	result := ValidatePanel(panel)
	// Should still be valid but with warnings
	if !result.Valid {
		t.Errorf("value panel without reduceTo should be valid (just warned), got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warnings for value panel without reduceTo")
	}
}

func TestValidatePanel_FormulaReferencingMissingQuery(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryFormulas = []BuilderFormula{
		{
			QueryName:  "F1",
			Expression: "A + B",
			Disabled:   false,
		},
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for formula referencing missing query B")
	}
	found := false
	for _, e := range result.Errors {
		if contains(e, "references query 'B'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about missing query B, got: %v", result.Errors)
	}
}

func TestValidatePanel_FormulaReferencingExistingQueries(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	// Add a second query
	bq := panel.Query.Builder.QueryData[0]
	bq.QueryName = "B"
	bq.Expression = "B"
	panel.Query.Builder.QueryData = append(panel.Query.Builder.QueryData, bq)
	panel.Query.Builder.QueryFormulas = []BuilderFormula{
		{
			QueryName:  "F1",
			Expression: "A + B",
			Disabled:   false,
		},
	}
	result := ValidatePanel(panel)
	if !result.Valid {
		t.Errorf("expected valid result for formula referencing existing queries, got errors: %v", result.Errors)
	}
}

func TestValidatePanel_ThresholdOnListPanel(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeList, DataSourceLogs)
	val := 100.0
	panel.Thresholds = []Threshold{
		{
			Index:             "1",
			ThresholdOperator: ">",
			ThresholdValue:    &val,
			ThresholdFormat:   "Text",
			ThresholdColor:    "#FF0000",
		},
	}
	result := ValidatePanel(panel)
	// Should be valid but with a warning
	if !result.Valid {
		t.Errorf("threshold on list panel should be valid (just warned), got errors: %v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if contains(w, "does not support thresholds") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about thresholds not supported, got warnings: %v", result.Warnings)
	}
}

func TestValidatePanel_InvalidFilterOperator(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].Filters = &TagFilter{
		Items: []TagFilterItem{
			{
				ID: "1",
				Op: "INVALID_OP",
			},
		},
		Op: "AND",
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad filter operator")
	}
}

func TestValidatePanel_InvalidHavingOperator(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].Having = []HavingClause{
		{
			ColumnName: "count",
			Op:         "LIKE",
			Value:      10,
		},
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for LIKE as having operator")
	}
}

func TestValidatePanel_InvalidReduceTo(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeValue, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].ReduceTo = "median"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad reduceTo value")
	}
}

func TestValidatePanel_InvalidQueryType(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.QueryType = "graphql"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad queryType")
	}
}

func TestValidatePanel_PromQLOnListPanel(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeList, DataSourceLogs)
	panel.Query.QueryType = QueryTypePromQL
	panel.Query.PromQL = []PromQLQuery{{Name: "A", Query: "up"}}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for promql on list panel")
	}
}

func TestValidatePanel_TablePanelWithoutGroupBy(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTable, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].GroupBy = nil
	result := ValidatePanel(panel)
	if !result.Valid {
		t.Errorf("table without groupBy should be valid (just warned), got errors: %v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if contains(w, "groupBy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about missing groupBy, got warnings: %v", result.Warnings)
	}
}

func TestValidatePanel_HistogramWithNonMetrics(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeHistogram, DataSourceLogs)
	result := ValidatePanel(panel)
	found := false
	for _, w := range result.Warnings {
		if contains(w, "histogram panel works best with 'metrics'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about non-metrics histogram, got warnings: %v", result.Warnings)
	}
}

func TestValidatePanel_UnsupportedVisualFields(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeList, DataSourceLogs)
	panel.YAxisUnit = "ms"
	fs := true
	panel.FillSpans = &fs
	panel.LegendPosition = "right"

	result := ValidatePanel(panel)
	if len(result.Warnings) < 3 {
		t.Errorf("expected at least 3 warnings for unsupported visual fields on list panel, got %d: %v", len(result.Warnings), result.Warnings)
	}
}

func TestValidatePanel_InvalidAggregateOperatorForDataSource(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceLogs)
	panel.Query.Builder.QueryData[0].AggregateOperator = "latest" // only valid for metrics
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for 'latest' operator on logs data source")
	}
}

func TestValidatePanel_InvalidSpaceAggregation(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].SpaceAggregation = "invalid"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad spaceAggregation")
	}
}

func TestValidatePanel_InvalidTimeAggregation(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].TimeAggregation = "invalid"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad timeAggregation")
	}
}

func TestValidatePanel_PanelSerializesToJSON(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	data, err := json.Marshal(panel)
	if err != nil {
		t.Fatalf("failed to marshal panel to JSON: %v", err)
	}

	var decoded Panel
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("failed to unmarshal panel from JSON: %v", err)
	}

	result := ValidatePanel(decoded)
	if !result.Valid {
		t.Errorf("round-tripped panel should be valid, got errors: %v", result.Errors)
	}
}

func TestValidatePanel_ClickHouseQueryType(t *testing.T) {
	panel := Panel{
		ID:         "test-1",
		PanelTypes: PanelTypeTimeSeries,
		Title:      "CH Panel",
		Query: Query{
			QueryType: QueryTypeClickHouse,
			Builder:   QueryBuilderData{QueryData: []BuilderQuery{}, QueryFormulas: []BuilderFormula{}},
			PromQL:    []PromQLQuery{},
			ClickHouseQL: []CHQuery{
				{Name: "A", Query: "SELECT * FROM logs", Disabled: false},
			},
			ID: "q1",
		},
	}
	result := ValidatePanel(panel)
	if !result.Valid {
		t.Errorf("valid ClickHouse panel should pass, got errors: %v", result.Errors)
	}
}

func TestValidatePanel_EmptyClickHouseQuery(t *testing.T) {
	panel := Panel{
		ID:         "test-1",
		PanelTypes: PanelTypeTimeSeries,
		Title:      "CH Panel",
		Query: Query{
			QueryType:    QueryTypeClickHouse,
			Builder:      QueryBuilderData{QueryData: []BuilderQuery{}, QueryFormulas: []BuilderFormula{}},
			PromQL:       []PromQLQuery{},
			ClickHouseQL: []CHQuery{},
			ID:           "q1",
		},
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for empty clickhouse_sql array")
	}
}

func TestValidatePanel_InvalidFunctionName(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].Functions = []QueryFunction{
		{Name: "nonExistentFunction"},
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad function name")
	}
}

func TestValidatePanel_InvalidThresholdOperator(t *testing.T) {
	val := 50.0
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Thresholds = []Threshold{
		{
			Index:             "1",
			ThresholdOperator: "BETWEEN",
			ThresholdValue:    &val,
			ThresholdFormat:   "Text",
		},
	}
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad threshold operator")
	}
}

func TestValidatePanel_PiePanelWithoutGroupBy(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypePie, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].GroupBy = nil
	result := ValidatePanel(panel)
	found := false
	for _, w := range result.Warnings {
		if contains(w, "pie panel without groupBy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about pie panel without groupBy, got: %v", result.Warnings)
	}
}

func TestValidatePanel_InvalidSource(t *testing.T) {
	panel := CreateDefaultPanel(PanelTypeTimeSeries, DataSourceMetrics)
	panel.Query.Builder.QueryData[0].Source = "invalid"
	result := ValidatePanel(panel)
	if result.Valid {
		t.Error("expected invalid result for bad source value")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
