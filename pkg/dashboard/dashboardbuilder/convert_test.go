package dashboardbuilder

import (
	"encoding/json"
	"os"
	"testing"
)

func TestParseFromJSON_MinimalFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/minimal.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := ParseFromJSON(data)
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	if d.Title != "Minimal Dashboard" {
		t.Errorf("expected title %q, got %q", "Minimal Dashboard", d.Title)
	}
	if d.Version != DashboardVersion {
		t.Errorf("expected version %q, got %q", DashboardVersion, d.Version)
	}
	if len(d.Widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(d.Widgets))
	}
	if d.Widgets[0].ID == "" {
		t.Error("expected auto-generated widget ID")
	}
	if d.Widgets[0].Opacity != DefaultOpacity {
		t.Errorf("expected opacity %q, got %q", DefaultOpacity, d.Widgets[0].Opacity)
	}
	if len(d.Layout) != 1 {
		t.Fatalf("expected auto-generated layout with 1 item, got %d", len(d.Layout))
	}
}

func TestParseFromJSON_FullFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/full.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := ParseFromJSON(data)
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	if d.Title != "Full Dashboard" {
		t.Errorf("expected title %q, got %q", "Full Dashboard", d.Title)
	}
	if len(d.Variables) != 4 {
		t.Errorf("expected 4 variables, got %d", len(d.Variables))
	}
	if len(d.Widgets) != 4 {
		t.Errorf("expected 4 widgets (1 row + 3 panels), got %d", len(d.Widgets))
	}
	if len(d.Layout) != 4 {
		t.Errorf("expected 4 layout items, got %d", len(d.Layout))
	}

	// Verify variable types preserved.
	if svc, ok := d.Variables["service"]; ok {
		if svc.Type != VariableTypeDynamic {
			t.Errorf("expected service variable type %q, got %q", VariableTypeDynamic, svc.Type)
		}
	} else {
		t.Error("expected 'service' variable")
	}
}

func TestParseFromJSON_DynamicVariableDefaultsApplied(t *testing.T) {
	input := `{
		"title": "Test",
		"variables": {
			"svc": {
				"description": "service filter",
				"dynamicVariablesAttribute": "service.name",
				"dynamicVariablesSource": "Traces"
			}
		}
	}`

	d, err := ParseFromJSON([]byte(input))
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	v := d.Variables["svc"]
	if v.Type != VariableTypeDynamic {
		t.Errorf("expected type %q, got %q", VariableTypeDynamic, v.Type)
	}
	if !v.MultiSelect {
		t.Error("expected multiSelect=true for DYNAMIC (not explicitly set)")
	}
	if !v.ShowALLOption {
		t.Error("expected showALLOption=true for DYNAMIC (not explicitly set)")
	}
	if v.Sort != SortASC {
		t.Errorf("expected sort %q, got %q", SortASC, v.Sort)
	}
}

func TestParseFromJSON_DynamicVariableExplicitFalse(t *testing.T) {
	input := `{
		"title": "Test",
		"variables": {
			"svc": {
				"description": "svc",
				"type": "DYNAMIC",
				"multiSelect": false,
				"showALLOption": false,
				"dynamicVariablesAttribute": "service.name",
				"dynamicVariablesSource": "Traces"
			}
		}
	}`

	d, err := ParseFromJSON([]byte(input))
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	v := d.Variables["svc"]
	if v.MultiSelect {
		t.Error("expected multiSelect=false (explicitly set)")
	}
	if v.ShowALLOption {
		t.Error("expected showALLOption=false (explicitly set)")
	}
}

func TestParseFromJSON_InvalidJSON(t *testing.T) {
	_, err := ParseFromJSON([]byte(`not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseFromJSON_ValidationError(t *testing.T) {
	input := `{"variables": {}}`
	_, err := ParseFromJSON([]byte(input))
	if err == nil {
		t.Fatal("expected validation error for missing title")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

func TestParseFromMap(t *testing.T) {
	m := map[string]interface{}{
		"title": "From Map",
		"variables": map[string]interface{}{},
	}

	d, err := ParseFromMap(m)
	if err != nil {
		t.Fatalf("ParseFromMap failed: %v", err)
	}
	if d.Title != "From Map" {
		t.Errorf("expected title %q, got %q", "From Map", d.Title)
	}
}

func TestToMap_RoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/full.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := ParseFromJSON(data)
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	m, err := d.ToMap()
	if err != nil {
		t.Fatalf("ToMap failed: %v", err)
	}

	// Verify title survives round-trip.
	title, ok := m["title"].(string)
	if !ok || title != "Full Dashboard" {
		t.Errorf("expected title %q in map, got %v", "Full Dashboard", m["title"])
	}

	// Verify variables survive.
	vars, ok := m["variables"].(map[string]interface{})
	if !ok {
		t.Fatal("expected variables in map")
	}
	if len(vars) != 4 {
		t.Errorf("expected 4 variables in map, got %d", len(vars))
	}
}

func TestToJSON_RoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/full.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d1, err := ParseFromJSON(data)
	if err != nil {
		t.Fatalf("ParseFromJSON failed: %v", err)
	}

	jsonBytes, err := d1.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	d2, err := ParseFromJSON(jsonBytes)
	if err != nil {
		t.Fatalf("second ParseFromJSON failed: %v", err)
	}

	// Compare key fields.
	if d1.Title != d2.Title {
		t.Errorf("title mismatch: %q vs %q", d1.Title, d2.Title)
	}
	if len(d1.Widgets) != len(d2.Widgets) {
		t.Errorf("widget count mismatch: %d vs %d", len(d1.Widgets), len(d2.Widgets))
	}
	if len(d1.Variables) != len(d2.Variables) {
		t.Errorf("variable count mismatch: %d vs %d", len(d1.Variables), len(d2.Variables))
	}

	// Deep equality via JSON serialization.
	j1, _ := json.Marshal(d1)
	j2, _ := json.Marshal(d2)
	if string(j1) != string(j2) {
		t.Error("round-trip produced different JSON")
	}
}
