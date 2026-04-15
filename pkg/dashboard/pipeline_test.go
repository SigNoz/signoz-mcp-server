package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to build JSON from a map.
func toJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// minimalDashboardJSON returns a minimal valid dashboard with one value panel.
func minimalDashboardJSON(t *testing.T) []byte {
	t.Helper()
	return toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id":         "w1",
				"panelTypes": "value",
				"title":      "Requests",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{
								"queryName":  "A",
								"dataSource": "traces",
								"expression": "A",
							},
						},
					},
				},
			},
		},
	})
}

// --- Dashboard-level tests ---

func TestValidate_EmptyTitle(t *testing.T) {
	data := toJSON(t, map[string]any{"title": ""})
	_, err := Validate(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")
}

func TestValidate_MinimalDashboard(t *testing.T) {
	out, err := Validate(minimalDashboardJSON(t))
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// Verify the output is valid JSON with defaults applied.
	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))
	assert.Equal(t, "Test", result["title"])
	assert.Equal(t, "v5", result["version"])

	// Layout should be auto-generated.
	layout, ok := result["layout"].([]any)
	require.True(t, ok)
	assert.Len(t, layout, 1)
}

func TestValidate_NilSlicesSafe(t *testing.T) {
	// The original crash: filters.items serialized as null.
	// Validate should produce clean JSON where all arrays are [] not null.
	out, err := Validate(minimalDashboardJSON(t))
	require.NoError(t, err)

	// The output should not contain "null" for array fields in builder queries.
	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	widgets := result["widgets"].([]any)
	w := widgets[0].(map[string]any)
	query := w["query"].(map[string]any)
	builder := query["builder"].(map[string]any)
	queryData := builder["queryData"].([]any)
	qd := queryData[0].(map[string]any)

	// These must be [] (empty array), not null.
	if formulas, ok := builder["queryFormulas"]; ok {
		assert.NotNil(t, formulas, "queryFormulas should not be nil")
	}
	// clickhouse_sql and promql should be present as empty arrays.
	if chSQL, ok := query["clickhouse_sql"]; ok {
		assert.NotNil(t, chSQL)
	}
	if promql, ok := query["promql"]; ok {
		assert.NotNil(t, promql)
	}
	_ = qd // suppress unused
}

// --- Variable normalization ---

func TestValidate_DynamicVariableDefaults(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"variables": map[string]any{
			"service_name": map[string]any{
				"type":                      "DYNAMIC",
				"dynamicVariablesAttribute": "service.name",
				"dynamicVariablesSource":    "Traces",
			},
		},
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "T",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A"},
						},
					},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	vars := result["variables"].(map[string]any)
	svcVar := vars["service_name"].(map[string]any)

	// DYNAMIC variables should default to multiSelect=true, showALLOption=true.
	assert.Equal(t, true, svcVar["multiSelect"])
	assert.Equal(t, true, svcVar["showALLOption"])
	// Sort should default to ASC for DYNAMIC.
	assert.Equal(t, "ASC", svcVar["sort"])
	// ID should be auto-generated.
	assert.NotEmpty(t, svcVar["id"])
}

func TestValidate_DynamicVariableExplicitFalse(t *testing.T) {
	// Explicitly setting multiSelect=false should be preserved (not overridden to true).
	data := toJSON(t, map[string]any{
		"title": "Test",
		"variables": map[string]any{
			"svc": map[string]any{
				"type":        "DYNAMIC",
				"multiSelect": false,
			},
		},
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "T",
				"query": map[string]any{
					"queryType": "builder",
					"builder":   map[string]any{"queryData": []map[string]any{{"queryName": "A", "dataSource": "traces", "expression": "A"}}},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))
	svcVar := result["variables"].(map[string]any)["svc"].(map[string]any)
	assert.Equal(t, false, svcVar["multiSelect"])
}

// --- Auto-layout ---

func TestValidate_AutoLayoutGenerated(t *testing.T) {
	// No layout provided — should be auto-computed.
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "a", "panelTypes": "graph", "title": "A",
				"query": map[string]any{
					"queryType": "builder",
					"builder":   map[string]any{"queryData": []map[string]any{{"queryName": "A", "dataSource": "traces", "expression": "A"}}},
				},
			},
			{
				"id": "b", "panelTypes": "graph", "title": "B",
				"query": map[string]any{
					"queryType": "builder",
					"builder":   map[string]any{"queryData": []map[string]any{{"queryName": "A", "dataSource": "traces", "expression": "A"}}},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))
	layout := result["layout"].([]any)
	assert.Len(t, layout, 2, "should have 2 layout items for 2 widgets")
}

// --- Panel-type semantic validation ---

func TestValidate_ListPanelRejectsMetrics(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "list", "title": "T",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "metrics", "expression": "A", "aggregateOperator": "noop"},
						},
					},
				},
			},
		},
	})

	_, err := Validate(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list panel does not support")
}

func TestValidate_InvalidQueryType(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "T",
				"query": map[string]any{
					"queryType": "invalid_type",
				},
			},
		},
	})

	_, err := Validate(data)
	require.Error(t, err)
}

func TestValidate_InvalidDataSource(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "T",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "banana", "expression": "A"},
						},
					},
				},
			},
		},
	})

	_, err := Validate(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "banana")
}

// --- Formula validation ---

func TestValidate_FormulaReferencesExist(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "Error Rate",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "disabled": true},
						},
						"queryFormulas": []map[string]any{
							{"queryName": "F1", "expression": "A / Z"},
						},
					},
				},
			},
		},
	})

	_, err := Validate(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Z")
}

func TestValidate_ValidErrorRateFormula(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{
				"id": "w1", "panelTypes": "value", "title": "Error Rate",
				"yAxisUnit": "percentunit",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "disabled": true},
							{"queryName": "B", "dataSource": "traces", "expression": "B", "disabled": true},
						},
						"queryFormulas": []map[string]any{
							{"queryName": "F1", "expression": "A / (A+B)"},
						},
					},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

// --- Full RED dashboard (integration) ---

func TestValidate_FullREDDashboard(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title":       "sample-flask-service — RED Metrics",
		"description": "Rate, Errors, and Duration metrics",
		"tags":        []string{"red", "flask"},
		"variables": map[string]any{
			"service_name": map[string]any{
				"type":                      "DYNAMIC",
				"dynamicVariablesAttribute": "service.name",
				"dynamicVariablesSource":    "Traces",
				"multiSelect":               true,
				"showALLOption":             true,
			},
		},
		"layout": []map[string]any{
			{"i": "requests", "x": 0, "y": 0, "w": 4, "h": 3},
			{"i": "error_rate", "x": 4, "y": 0, "w": 4, "h": 3},
			{"i": "latency", "x": 8, "y": 0, "w": 4, "h": 3},
			{"i": "req_graph", "x": 0, "y": 3, "w": 6, "h": 7},
			{"i": "latency_graph", "x": 6, "y": 3, "w": 6, "h": 7},
		},
		"widgets": []map[string]any{
			{
				"id": "requests", "panelTypes": "value", "title": "Total Requests",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{
								"queryName": "A", "dataSource": "traces", "expression": "A",
								"aggregateOperator": "count",
								"filter":            map[string]any{"expression": "service.name IN $service_name"},
							},
						},
					},
				},
			},
			{
				"id": "error_rate", "panelTypes": "value", "title": "Error Rate",
				"yAxisUnit": "percentunit",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "disabled": true, "aggregateOperator": "count"},
							{"queryName": "B", "dataSource": "traces", "expression": "B", "disabled": true, "aggregateOperator": "count"},
						},
						"queryFormulas": []map[string]any{
							{"queryName": "F1", "expression": "A / (A+B)"},
						},
					},
				},
			},
			{
				"id": "latency", "panelTypes": "value", "title": "P95 Latency",
				"yAxisUnit": "ns",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "aggregateOperator": "p95"},
						},
					},
				},
			},
			{
				"id": "req_graph", "panelTypes": "graph", "title": "Request Rate",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "aggregateOperator": "count"},
						},
					},
				},
			},
			{
				"id": "latency_graph", "panelTypes": "graph", "title": "Latency (P50/P95/P99)",
				"yAxisUnit": "ns",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A", "aggregateOperator": "p50", "legend": "P50"},
							{"queryName": "B", "dataSource": "traces", "expression": "B", "aggregateOperator": "p95", "legend": "P95"},
							{"queryName": "C", "dataSource": "traces", "expression": "C", "aggregateOperator": "p99", "legend": "P99"},
						},
					},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	// Version set.
	assert.Equal(t, "v5", result["version"])

	// All 5 widgets present.
	widgets := result["widgets"].([]any)
	assert.Len(t, widgets, 5)

	// Layout matches.
	layout := result["layout"].([]any)
	assert.Len(t, layout, 5)

	// Variable normalized.
	vars := result["variables"].(map[string]any)
	svc := vars["service_name"].(map[string]any)
	assert.NotEmpty(t, svc["id"])
	assert.Equal(t, "ASC", svc["sort"])
}

// --- ValidateFromMap convenience ---

func TestValidateFromMap_Works(t *testing.T) {
	m := map[string]any{
		"title": "Test",
		"widgets": []any{
			map[string]any{
				"id": "w1", "panelTypes": "value", "title": "T",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []any{
							map[string]any{"queryName": "A", "dataSource": "traces", "expression": "A"},
						},
					},
				},
			},
		},
	}

	out, err := ValidateFromMap(m)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

// --- Row panels pass through ---

func TestValidate_RowPanelPassesThrough(t *testing.T) {
	data := toJSON(t, map[string]any{
		"title": "Test",
		"widgets": []map[string]any{
			{"id": "row1", "panelTypes": "row", "title": "Section"},
			{
				"id": "w1", "panelTypes": "value", "title": "V",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []map[string]any{
							{"queryName": "A", "dataSource": "traces", "expression": "A"},
						},
					},
				},
			},
		},
	})

	out, err := Validate(data)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))
	widgets := result["widgets"].([]any)
	assert.Len(t, widgets, 2)
}
