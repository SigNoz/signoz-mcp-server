package dashboardbuilder

import (
	"encoding/json"
	"fmt"
)

// rawDashboardVariable is an intermediate type used during JSON parsing to detect
// whether multiSelect and showALLOption were explicitly set in the JSON input.
// Go's bool type defaults to false, making it impossible to distinguish "explicitly
// set to false" from "not provided" with the final DashboardVariable type.
type rawDashboardVariable struct {
	ID                        string `json:"id"`
	Name                      string `json:"name,omitempty"`
	Description               string `json:"description"`
	Type                      string `json:"type"`
	QueryValue                string `json:"queryValue,omitempty"`
	CustomValue               string `json:"customValue,omitempty"`
	TextboxValue              string `json:"textboxValue,omitempty"`
	Sort                      string `json:"sort"`
	MultiSelect               *bool  `json:"multiSelect,omitempty"`
	ShowALLOption             *bool  `json:"showALLOption,omitempty"`
	SelectedValue             any    `json:"selectedValue,omitempty"`
	DefaultValue              string `json:"defaultValue,omitempty"`
	DynamicVariablesAttribute string `json:"dynamicVariablesAttribute,omitempty"`
	DynamicVariablesSource    string `json:"dynamicVariablesSource,omitempty"`
	Order                     *int   `json:"order,omitempty"`
}

// rawDashboardData uses rawDashboardVariable for intermediate parsing.
type rawDashboardData struct {
	Title       string                           `json:"title"`
	Description string                           `json:"description,omitempty"`
	Tags        []string                         `json:"tags,omitempty"`
	Name        string                           `json:"name,omitempty"`
	Version     string                           `json:"version,omitempty"`
	Variables   map[string]*rawDashboardVariable `json:"variables"`
	Widgets     []WidgetOrRow                    `json:"widgets,omitempty"`
	Layout      []LayoutItem                     `json:"layout,omitempty"`
	PanelMap    map[string]*PanelMapEntry        `json:"panelMap,omitempty"`
}

// ParseFromJSON parses raw JSON bytes into a validated DashboardData.
// It applies defaults and runs validation.
func ParseFromJSON(data []byte) (*DashboardData, error) {
	raw := &rawDashboardData{}
	if err := json.Unmarshal(data, raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Track which variables had multiSelect/showALLOption explicitly set.
	multiSelectSet := make(map[string]bool)
	showALLSet := make(map[string]bool)

	// Convert raw variables to final variables.
	vars := make(map[string]*DashboardVariable, len(raw.Variables))
	for key, rv := range raw.Variables {
		v := &DashboardVariable{
			ID:                        rv.ID,
			Name:                      rv.Name,
			Description:               rv.Description,
			Type:                      rv.Type,
			QueryValue:                rv.QueryValue,
			CustomValue:               rv.CustomValue,
			TextboxValue:              rv.TextboxValue,
			Sort:                      rv.Sort,
			SelectedValue:             rv.SelectedValue,
			DefaultValue:              rv.DefaultValue,
			DynamicVariablesAttribute: rv.DynamicVariablesAttribute,
			DynamicVariablesSource:    rv.DynamicVariablesSource,
			Order:                     rv.Order,
		}
		if rv.MultiSelect != nil {
			v.MultiSelect = *rv.MultiSelect
			multiSelectSet[key] = true
		}
		if rv.ShowALLOption != nil {
			v.ShowALLOption = *rv.ShowALLOption
			showALLSet[key] = true
		}
		vars[key] = v
	}

	d := &DashboardData{
		Title:       raw.Title,
		Description: raw.Description,
		Tags:        raw.Tags,
		Name:        raw.Name,
		Version:     raw.Version,
		Variables:   vars,
		Widgets:     raw.Widgets,
		Layout:      raw.Layout,
		PanelMap:    raw.PanelMap,
	}

	ApplyDefaults(d, multiSelectSet, showALLSet)

	if verr := Validate(d); verr != nil {
		return nil, verr
	}
	return d, nil
}

// ParseFromMap parses a map[string]any (the StorableDashboardData format)
// into a validated DashboardData.
func ParseFromMap(m map[string]any) (*DashboardData, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal map to JSON: %w", err)
	}
	return ParseFromJSON(data)
}

// ToMap converts the DashboardData to map[string]any,
// compatible with StorableDashboardData / PostableDashboard.
func (d *DashboardData) ToMap() (map[string]any, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal dashboard data: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("cannot unmarshal to map: %w", err)
	}
	return m, nil
}

// ToJSON serializes the DashboardData to JSON bytes.
func (d *DashboardData) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}
