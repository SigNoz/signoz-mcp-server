package alert

import (
	"encoding/json"
	"strings"
	"testing"
)

// minimalValidAlert returns a minimal valid alert rule using v2alpha1 schema.
// minimalValidAlert returns a minimal valid alert rule using v2alpha1 schema.
func minimalValidAlert() map[string]any {
	return map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":         "A",
							"signal":       "metrics",
							"stepInterval": 60,
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
					},
				},
			},
		},
	}
}

func TestNormalize_MinimalValidAlert(t *testing.T) {
	alert := minimalValidAlert()
	result, err := NormalizeFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// Check defaults were applied. version is deliberately absent: upstream
	// defaults an empty version to v5 itself, so the MCP adds no copy.
	if _, present := parsed["version"]; present {
		t.Errorf("version should be left to upstream's default, got %v", parsed["version"])
	}
	if parsed["schemaVersion"] != "v2alpha1" {
		t.Errorf("expected schemaVersion=v2alpha1, got %v", parsed["schemaVersion"])
	}
	if parsed["source"] != "mcp" {
		t.Errorf("expected source=mcp, got %v", parsed["source"])
	}

	// evaluation block should exist
	eval, ok := parsed["evaluation"].(map[string]any)
	if !ok {
		t.Fatal("expected evaluation block")
	}
	if eval["kind"] != "rolling" {
		t.Errorf("expected evaluation.kind=rolling, got %v", eval["kind"])
	}
	evalSpec := eval["spec"].(map[string]any)
	if evalSpec["evalWindow"] != "5m0s" {
		t.Errorf("expected evaluation.spec.evalWindow=5m0s, got %v", evalSpec["evalWindow"])
	}

	// notificationSettings should exist
	if _, ok := parsed["notificationSettings"].(map[string]any); !ok {
		t.Error("expected notificationSettings to be set")
	}

	// labels
	labels, ok := parsed["labels"].(map[string]any)
	if !ok {
		t.Fatal("expected labels to be a map")
	}
	if labels["severity"] != "warning" {
		t.Errorf("expected severity=warning, got %v", labels["severity"])
	}

	// thresholds should be preserved
	cond := parsed["condition"].(map[string]any)
	if cond["selectedQueryName"] != "A" {
		t.Errorf("expected selectedQueryName=A, got %v", cond["selectedQueryName"])
	}
	thresholds := cond["thresholds"].(map[string]any)
	if thresholds["kind"] != "basic" {
		t.Errorf("expected thresholds.kind=basic, got %v", thresholds["kind"])
	}
	specs := thresholds["spec"].([]any)
	if len(specs) != 1 {
		t.Fatalf("expected 1 threshold spec, got %d", len(specs))
	}
	spec := specs[0].(map[string]any)
	if spec["target"] != float64(100) {
		t.Errorf("expected threshold target=100, got %v", spec["target"])
	}
	// annotations
	annotations, ok := parsed["annotations"].(map[string]any)
	if !ok {
		t.Fatal("expected annotations to be a map")
	}
	if _, hasDesc := annotations["description"]; !hasDesc {
		t.Error("expected default description annotation")
	}
}

func TestNormalize_V2ThresholdsPreserved(t *testing.T) {
	alert := map[string]any{
		"alert":     "V2 Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"selectedQueryName": "A",
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "critical",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"pagerduty"},
					},
					map[string]any{
						"name":      "warning",
						"target":    float64(50),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"slack"},
					},
				},
			},
		},
		"evaluation": map[string]any{
			"kind": "rolling",
			"spec": map[string]any{
				"evalWindow": "15m0s",
				"frequency":  "5m0s",
			},
		},
	}

	result, err := NormalizeFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	// v2 thresholds should be preserved as-is
	cond := parsed["condition"].(map[string]any)
	thresholds := cond["thresholds"].(map[string]any)
	specs := thresholds["spec"].([]any)
	if len(specs) != 2 {
		t.Fatalf("expected 2 threshold specs preserved, got %d", len(specs))
	}

	// evaluation should be preserved
	eval := parsed["evaluation"].(map[string]any)
	evalSpec := eval["spec"].(map[string]any)
	if evalSpec["evalWindow"] != "15m0s" {
		t.Errorf("expected preserved evalWindow=15m0s, got %v", evalSpec["evalWindow"])
	}
}

func TestNormalize_PreservesExistingLabels(t *testing.T) {
	alert := minimalValidAlert()
	alert["labels"] = map[string]any{
		"severity": "critical",
		"team":     "backend",
	}

	result, err := NormalizeFromMap(alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	labels := parsed["labels"].(map[string]any)
	if labels["severity"] != "critical" {
		t.Errorf("should preserve existing severity=critical, got %v", labels["severity"])
	}
	if labels["team"] != "backend" {
		t.Errorf("should preserve team label, got %v", labels["team"])
	}
}

func TestNormalize_ExistingEvaluationPreserved(t *testing.T) {
	alert := minimalValidAlert()
	alert["evaluation"] = map[string]any{
		"kind": "rolling",
		"spec": map[string]any{
			"evalWindow": "10m0s",
			"frequency":  "2m0s",
		},
	}

	result, err := NormalizeFromMap(alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if parsed["schemaVersion"] != "v2alpha1" {
		t.Errorf("expected schemaVersion=v2alpha1, got %v", parsed["schemaVersion"])
	}

	// existing evaluation should be preserved
	eval := parsed["evaluation"].(map[string]any)
	evalSpec := eval["spec"].(map[string]any)
	if evalSpec["evalWindow"] != "10m0s" {
		t.Errorf("expected preserved evalWindow=10m0s, got %v", evalSpec["evalWindow"])
	}
	if evalSpec["frequency"] != "2m0s" {
		t.Errorf("expected preserved frequency=2m0s, got %v", evalSpec["frequency"])
	}
}

func TestNormalize_FormulaWithExpression(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	cq["unit"] = "percent"
	queries := cq["queries"].([]any)
	queries = append(queries, map[string]any{
		"type": "builder_formula",
		"spec": map[string]any{
			"name":       "F1",
			"expression": "A * 100",
		},
	})
	cq["queries"] = queries
	cond["selectedQueryName"] = "F1"

	result, err := NormalizeFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error for valid formula, got: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	// unit should be preserved
	parsedCond := parsed["condition"].(map[string]any)
	parsedCQ := parsedCond["compositeQuery"].(map[string]any)
	if parsedCQ["unit"] != "percent" {
		t.Errorf("expected compositeQuery.unit=percent, got %v", parsedCQ["unit"])
	}
}

func TestNormalize_AnomalyRule_Accepted(t *testing.T) {
	out, err := NormalizeFromMap(minimalValidAnomalyRule())
	if err != nil {
		t.Fatalf("expected anomaly rule to validate, got: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	// v1 anomaly rule must not carry v2 schemaVersion or auto-evaluation.
	if v, present := parsed["schemaVersion"]; present {
		t.Errorf("schemaVersion must be absent for anomaly_rule (v1 shape); got %v", v)
	}
	if _, present := parsed["evaluation"]; present {
		t.Errorf("evaluation block must be absent for anomaly_rule (v1 shape)")
	}
	// But top-level evalWindow/frequency are preserved verbatim.
	if parsed["evalWindow"] != "24h" {
		t.Errorf("expected evalWindow=24h, got %v", parsed["evalWindow"])
	}
	if parsed["frequency"] != "3h" {
		t.Errorf("expected frequency=3h, got %v", parsed["frequency"])
	}
	querySpec := parsed["condition"].(map[string]any)["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	if _, present := querySpec["limit"]; present {
		t.Errorf("limit must not be injected into alert queries (it caps grouped evaluation to top-N), got %v", querySpec["limit"])
	}
	if functions, ok := querySpec["functions"].([]any); !ok || len(functions) != 1 {
		t.Fatalf("anomaly functions were not preserved: %v", querySpec["functions"])
	}
}

func TestNormalize_AnomalyPreservesPreferredChannels(t *testing.T) {
	t.Run("anomaly preserves preferredChannels", func(t *testing.T) {
		rule := minimalValidAnomalyRule()
		rule["preferredChannels"] = []any{"slack-alerts"}
		out, err := NormalizeFromMap(rule)
		if err != nil {
			t.Fatalf("expected anomaly preferredChannels to validate, got: %v", err)
		}
		if !strings.Contains(string(out), `"preferredChannels":["slack-alerts"]`) {
			t.Fatalf("preferredChannels not preserved: %s", out)
		}
	})
}

func TestNormalize_PromqlEnvelope_Accepted(t *testing.T) {
	rule := map[string]any{
		"alert":     "Consumer lag high",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "promql_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "promql",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "promql",
						"spec": map[string]any{
							"name":  "A",
							"query": "up",
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{map[string]any{"name": "critical", "target": 1, "op": "above", "matchType": "all_the_times"}},
			},
		},
	}
	if _, err := NormalizeFromMap(rule); err != nil {
		t.Fatalf("expected promql envelope to validate, got: %v", err)
	}
}

func TestNormalize_MetricAggregationShape_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	cond := rule["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	spec := cq["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	spec["aggregations"] = []any{
		map[string]any{"metricName": "k8s.pod.cpu_request_utilization", "timeAggregation": "avg", "spaceAggregation": "max"},
	}
	out, err := NormalizeFromMap(rule)
	if err != nil {
		t.Fatalf("expected metric aggregation shape to validate, got: %v", err)
	}
	if !strings.Contains(string(out), "metricName") {
		t.Error("expected output to preserve metricName field")
	}
}

func TestNormalize_AbsentFor_PassesThrough(t *testing.T) {
	rule := minimalValidAlert()
	cond := rule["condition"].(map[string]any)
	cond["alertOnAbsent"] = true
	cond["absentFor"] = 15
	out, err := NormalizeFromMap(rule)
	if err != nil {
		t.Fatalf("expected alertOnAbsent+absentFor to validate, got: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(out, &parsed)
	parsedCond := parsed["condition"].(map[string]any)
	if parsedCond["absentFor"] != float64(15) {
		t.Errorf("expected absentFor=15 to pass through, got %v", parsedCond["absentFor"])
	}
}

func TestNormalize_RenotifyAlertStates_Accepted(t *testing.T) {
	for _, state := range []string{"firing", "nodata"} {
		rule := minimalValidAlert()
		rule["notificationSettings"] = map[string]any{
			"renotify": map[string]any{
				"enabled":     true,
				"interval":    "30m",
				"alertStates": []any{state},
			},
		}
		if _, err := NormalizeFromMap(rule); err != nil {
			t.Errorf("expected alertState=%q to validate, got: %v", state, err)
		}
	}
}

func minimalValidAnomalyRule() map[string]any {
	return map[string]any{
		"alert":      "Anomalous ingest drop",
		"alertType":  "METRIC_BASED_ALERT",
		"ruleType":   "anomaly_rule",
		"evalWindow": "24h",
		"frequency":  "3h",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"metricName": "otelcol_receiver_accepted_spans", "timeAggregation": "rate", "spaceAggregation": "sum"},
							},
							"functions": []any{
								map[string]any{"name": "anomaly", "args": []any{
									map[string]any{"name": "z_score_threshold", "value": 2},
								}},
							},
						},
					},
				},
			},
			"op":          "below",
			"matchType":   "all_the_times",
			"target":      float64(2),
			"algorithm":   "standard",
			"seasonality": "daily",
		},
	}
}

func TestNormalize_ExplicitSchemaVersionPassesThrough(t *testing.T) {
	rule := minimalValidAlert()
	rule["schemaVersion"] = "v1"
	out, err := NormalizeFromMap(rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), `"schemaVersion":"v1"`) {
		t.Fatalf("explicit schemaVersion was overwritten: %s", out)
	}
}
