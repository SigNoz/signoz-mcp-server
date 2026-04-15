package dashboardbuilder

import (
	"encoding/json"
	"testing"
)

func TestNew_MinimalBuild(t *testing.T) {
	d, err := New("Test Dashboard").Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if d.Title != "Test Dashboard" {
		t.Errorf("expected title %q, got %q", "Test Dashboard", d.Title)
	}
	if d.Version != DashboardVersion {
		t.Errorf("expected version %q, got %q", DashboardVersion, d.Version)
	}
	if d.Variables == nil {
		t.Error("expected variables to be initialized")
	}
	if d.PanelMap == nil {
		t.Error("expected panelMap to be initialized")
	}
}

func TestBuilder_WithVariableAndWidget(t *testing.T) {
	d, err := New("Service Dashboard").
		Description("Monitoring dashboard").
		Tags("sre", "latency").
		AddVariable("service", NewDynamicVariable("service.name", DynamicSourceTraces)).
		AddTimeSeriesWidget("P99 Latency", NewBuilderQuery(
			map[string]any{
				"queryName":  "A",
				"dataSource": DataSourceTraces,
				"expression": "A",
			},
		)).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if d.Description != "Monitoring dashboard" {
		t.Errorf("expected description %q, got %q", "Monitoring dashboard", d.Description)
	}
	if len(d.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(d.Tags))
	}
	if len(d.Variables) != 1 {
		t.Fatalf("expected 1 variable, got %d", len(d.Variables))
	}
	svc := d.Variables["service"]
	if svc.Type != VariableTypeDynamic {
		t.Errorf("expected variable type %q, got %q", VariableTypeDynamic, svc.Type)
	}
	if !svc.MultiSelect {
		t.Error("expected multiSelect=true for dynamic variable")
	}
	if !svc.ShowALLOption {
		t.Error("expected showALLOption=true for dynamic variable")
	}
	if len(d.Widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(d.Widgets))
	}
	if d.Widgets[0].PanelTypes != PanelTypeGraph {
		t.Errorf("expected panelType %q, got %q", PanelTypeGraph, d.Widgets[0].PanelTypes)
	}
	if len(d.Layout) != 1 {
		t.Fatalf("expected 1 layout item, got %d", len(d.Layout))
	}
}

func TestBuilder_MultipleWidgetTypes(t *testing.T) {
	d, err := New("Multi Widget").
		AddValueWidget("Error Rate", NewPromQLQuery(PromQLQuery{
			Name:  "A",
			Query: "sum(rate(errors[5m]))",
		})).
		AddTableWidget("Top Services", NewClickHouseQuery(ClickHouseQuery{
			Name:  "A",
			Query: "SELECT * FROM services LIMIT 10",
		})).
		AddBarWidget("Distribution", NewBuilderQuery(
			map[string]any{
				"queryName":  "A",
				"dataSource": DataSourceMetrics,
				"expression": "A",
			},
		)).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(d.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(d.Widgets))
	}
	expectedTypes := []string{PanelTypeValue, PanelTypeTable, PanelTypeBar}
	for i, expected := range expectedTypes {
		if d.Widgets[i].PanelTypes != expected {
			t.Errorf("widget %d: expected panelType %q, got %q", i, expected, d.Widgets[i].PanelTypes)
		}
	}
	if len(d.Layout) != 3 {
		t.Fatalf("expected 3 layout items, got %d", len(d.Layout))
	}
}

func TestBuilder_WithRows(t *testing.T) {
	d, err := New("Row Dashboard").
		AddRow("Section 1").
		AddTimeSeriesWidget("Widget 1", NewBuilderQuery(
			map[string]any{
				"queryName": "A", "dataSource": DataSourceMetrics, "expression": "A",
			},
		)).
		AddRow("Section 2").
		AddTimeSeriesWidget("Widget 2", NewBuilderQuery(
			map[string]any{
				"queryName": "A", "dataSource": DataSourceLogs, "expression": "A",
			},
		)).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(d.Widgets) != 4 {
		t.Fatalf("expected 4 widgets (2 rows + 2 panels), got %d", len(d.Widgets))
	}
	if d.Widgets[0].PanelTypes != PanelTypeRow {
		t.Error("expected first widget to be a row")
	}
	if d.Widgets[2].PanelTypes != PanelTypeRow {
		t.Error("expected third widget to be a row")
	}
}

func TestBuildMap_ProducesValidMap(t *testing.T) {
	m, err := New("Map Test").
		AddTimeSeriesWidget("W1", NewBuilderQuery(
			map[string]any{
				"queryName": "A", "dataSource": DataSourceMetrics, "expression": "A",
			},
		)).
		BuildMap()

	if err != nil {
		t.Fatalf("BuildMap failed: %v", err)
	}

	title, ok := m["title"].(string)
	if !ok || title != "Map Test" {
		t.Errorf("expected title %q in map, got %v", "Map Test", m["title"])
	}

	// Ensure it's valid JSON.
	_, err = json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal map: %v", err)
	}
}

func TestBuilder_AllVariableTypes(t *testing.T) {
	d, err := New("Variable Test").
		AddVariable("dynamic_var", NewDynamicVariable("host.name", DynamicSourceLogs)).
		AddVariable("query_var", NewQueryVariable("SELECT DISTINCT service FROM traces")).
		AddVariable("custom_var", NewCustomVariable("prod,staging,dev")).
		AddVariable("text_var", NewTextboxVariable("default_val")).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(d.Variables) != 4 {
		t.Fatalf("expected 4 variables, got %d", len(d.Variables))
	}

	tests := map[string]string{
		"dynamic_var": VariableTypeDynamic,
		"query_var":   VariableTypeQuery,
		"custom_var":  VariableTypeCustom,
		"text_var":    VariableTypeTextbox,
	}
	for key, expectedType := range tests {
		v, ok := d.Variables[key]
		if !ok {
			t.Errorf("missing variable %q", key)
			continue
		}
		if v.Type != expectedType {
			t.Errorf("variable %q: expected type %q, got %q", key, expectedType, v.Type)
		}
	}
}

func TestBuilder_ValidationFailure(t *testing.T) {
	// Empty title should fail.
	_, err := New("").Build()
	if err == nil {
		t.Fatal("expected validation error for empty title")
	}
}

func TestNewDynamicVariable(t *testing.T) {
	v := NewDynamicVariable("service.name", DynamicSourceTraces)
	if v.Type != VariableTypeDynamic {
		t.Errorf("expected type %q, got %q", VariableTypeDynamic, v.Type)
	}
	if v.DynamicVariablesAttribute != "service.name" {
		t.Errorf("expected attribute %q, got %q", "service.name", v.DynamicVariablesAttribute)
	}
	if v.DynamicVariablesSource != DynamicSourceTraces {
		t.Errorf("expected source %q, got %q", DynamicSourceTraces, v.DynamicVariablesSource)
	}
	if !v.MultiSelect {
		t.Error("expected multiSelect=true")
	}
	if !v.ShowALLOption {
		t.Error("expected showALLOption=true")
	}
}

func TestNewQueryVariable(t *testing.T) {
	v := NewQueryVariable("SELECT 1")
	if v.Type != VariableTypeQuery {
		t.Errorf("expected type %q, got %q", VariableTypeQuery, v.Type)
	}
	if v.QueryValue != "SELECT 1" {
		t.Errorf("expected queryValue %q, got %q", "SELECT 1", v.QueryValue)
	}
}

func TestNewCustomVariable(t *testing.T) {
	v := NewCustomVariable("a,b,c")
	if v.Type != VariableTypeCustom {
		t.Errorf("expected type %q, got %q", VariableTypeCustom, v.Type)
	}
	if v.CustomValue != "a,b,c" {
		t.Errorf("expected customValue %q, got %q", "a,b,c", v.CustomValue)
	}
}

func TestNewTextboxVariable(t *testing.T) {
	v := NewTextboxVariable("hello")
	if v.Type != VariableTypeTextbox {
		t.Errorf("expected type %q, got %q", VariableTypeTextbox, v.Type)
	}
	if v.TextboxValue != "hello" {
		t.Errorf("expected textboxValue %q, got %q", "hello", v.TextboxValue)
	}
	if v.DefaultValue != "hello" {
		t.Errorf("expected defaultValue %q, got %q", "hello", v.DefaultValue)
	}
}
