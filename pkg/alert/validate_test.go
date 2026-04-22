package alert

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestValidate_MinimalValidAlert(t *testing.T) {
	alert := minimalValidAlert()
	result, err := ValidateFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// Check defaults were applied
	if parsed["version"] != "v5" {
		t.Errorf("expected version=v5, got %v", parsed["version"])
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

func TestValidate_V2ThresholdsPreserved(t *testing.T) {
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

	result, err := ValidateFromMap(alert)
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

func TestValidate_MissingAlertName(t *testing.T) {
	alert := minimalValidAlert()
	delete(alert, "alert")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing alert name")
	}
	if !strings.Contains(err.Error(), "alert") {
		t.Errorf("error should mention 'alert', got: %v", err)
	}
}

func TestValidate_MissingAlertType(t *testing.T) {
	alert := minimalValidAlert()
	delete(alert, "alertType")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing alertType")
	}
	if !strings.Contains(err.Error(), "alertType") {
		t.Errorf("error should mention 'alertType', got: %v", err)
	}
}

func TestValidate_InvalidAlertType(t *testing.T) {
	alert := minimalValidAlert()
	alert["alertType"] = "INVALID_TYPE"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for invalid alertType")
	}
	if !strings.Contains(err.Error(), "INVALID_TYPE") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestValidate_InvalidRuleType(t *testing.T) {
	alert := minimalValidAlert()
	alert["ruleType"] = "invalid_rule"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for invalid ruleType")
	}
	if !strings.Contains(err.Error(), "invalid_rule") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestValidate_MissingCondition(t *testing.T) {
	alert := minimalValidAlert()
	delete(alert, "condition")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing condition")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Errorf("error should mention 'condition', got: %v", err)
	}
}

func TestValidate_MissingCompositeQuery(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "compositeQuery")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing compositeQuery")
	}
	if !strings.Contains(err.Error(), "compositeQuery") {
		t.Errorf("error should mention 'compositeQuery', got: %v", err)
	}
}

func TestValidate_EmptyQueries(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	cq["queries"] = []any{}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for empty queries")
	}
	if !strings.Contains(err.Error(), "at least one query") {
		t.Errorf("error should mention queries requirement, got: %v", err)
	}
}

func TestValidate_NoThresholds(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "thresholds")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when thresholds are missing")
	}
	if !strings.Contains(err.Error(), "thresholds") {
		t.Errorf("error should mention thresholds, got: %v", err)
	}
}

func TestValidate_V2ThresholdMissingOpAndMatchType(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cond["thresholds"] = map[string]any{
		"kind": "basic",
		"spec": []any{
			map[string]any{
				"name":   "critical",
				"target": float64(5),
			},
		},
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when v2 threshold spec is missing op and matchType")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, ".op") {
		t.Errorf("error should mention missing op, got: %v", errStr)
	}
	if !strings.Contains(errStr, ".matchType") {
		t.Errorf("error should mention missing matchType, got: %v", errStr)
	}
}

func TestValidate_AnomalyRuleWithNonMetricAlertType(t *testing.T) {
	alert := minimalValidAlert()
	alert["ruleType"] = "anomaly_rule"
	alert["alertType"] = "LOGS_BASED_ALERT"
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	queries := cq["queries"].([]any)
	spec := queries[0].(map[string]any)["spec"].(map[string]any)
	spec["signal"] = "logs"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for anomaly_rule with non-metric alertType")
	}
	if !strings.Contains(err.Error(), "anomaly_rule") && !strings.Contains(err.Error(), "METRIC_BASED_ALERT") {
		t.Errorf("error should mention the constraint, got: %v", err)
	}
}

func TestValidate_PromQLRuleWithBuilderQueryType(t *testing.T) {
	alert := minimalValidAlert()
	alert["ruleType"] = "promql_rule"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for promql_rule with builder queryType")
	}
	if !strings.Contains(err.Error(), "promql") {
		t.Errorf("error should mention promql constraint, got: %v", err)
	}
}

func TestValidate_SignalMismatch(t *testing.T) {
	alert := minimalValidAlert()
	alert["alertType"] = "LOGS_BASED_ALERT"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for signal mismatch")
	}
	if !strings.Contains(err.Error(), "signal") {
		t.Errorf("error should mention signal mismatch, got: %v", err)
	}
}

func TestValidate_AlertOnAbsentNoThreshold(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "thresholds")
	cond["alertOnAbsent"] = true

	result, err := ValidateFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error for alertOnAbsent=true without threshold, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if parsed["schemaVersion"] != "v2alpha1" {
		t.Errorf("expected schemaVersion=v2alpha1, got %v", parsed["schemaVersion"])
	}
}

func TestValidate_PreservesExistingLabels(t *testing.T) {
	alert := minimalValidAlert()
	alert["labels"] = map[string]any{
		"severity": "critical",
		"team":     "backend",
	}

	result, err := ValidateFromMap(alert)
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

func TestValidate_V2ThresholdsMissingSpec(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cond["thresholds"] = map[string]any{
		"kind": "basic",
		"spec": []any{},
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for empty thresholds spec")
	}
	if !strings.Contains(err.Error(), "at least one threshold") {
		t.Errorf("error should mention threshold requirement, got: %v", err)
	}
}

func TestValidate_QueryMissingName(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	queries := cq["queries"].([]any)
	spec := queries[0].(map[string]any)["spec"].(map[string]any)
	delete(spec, "name")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing query name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention 'name', got: %v", err)
	}
}

func TestValidate_QueryMissingSignal(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	queries := cq["queries"].([]any)
	spec := queries[0].(map[string]any)["spec"].(map[string]any)
	delete(spec, "signal")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for missing signal")
	}
	if !strings.Contains(err.Error(), "signal") {
		t.Errorf("error should mention 'signal', got: %v", err)
	}
}

func TestValidate_InvalidQueryType(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	cq["queryType"] = "invalid_query_type"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for invalid queryType")
	}
	if !strings.Contains(err.Error(), "invalid_query_type") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	alert := map[string]any{
		"alert":     "",
		"alertType": "INVALID",
		"ruleType":  "INVALID",
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for multiple invalid fields")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "alert") || !strings.Contains(errStr, "alertType") || !strings.Contains(errStr, "ruleType") || !strings.Contains(errStr, "condition") {
		t.Errorf("error should mention all invalid fields, got: %v", errStr)
	}
}

func TestValidate_ExistingEvaluationPreserved(t *testing.T) {
	alert := minimalValidAlert()
	alert["evaluation"] = map[string]any{
		"kind": "rolling",
		"spec": map[string]any{
			"evalWindow": "10m0s",
			"frequency":  "2m0s",
		},
	}

	result, err := ValidateFromMap(alert)
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

func TestValidate_ConditionNotObject(t *testing.T) {
	alert := map[string]any{
		"alert":     "Test",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": "not-an-object",
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when condition is not an object")
	}
	if !strings.Contains(err.Error(), "condition") || !strings.Contains(err.Error(), "compositeQuery") {
		t.Errorf("error should mention condition must be an object with compositeQuery, got: %v", err)
	}
}

func TestValidate_ThresholdSpecNotObject(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cond["thresholds"] = map[string]any{
		"kind": "basic",
		"spec": []any{"not-an-object"},
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when threshold spec entry is not an object")
	}
	if !strings.Contains(err.Error(), "name, target, op, and matchType") {
		t.Errorf("error should describe expected threshold fields, got: %v", err)
	}
}

func TestValidate_PromQLQueryMissingQueryText(t *testing.T) {
	alert := map[string]any{
		"alert":     "PromQL Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "promql_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "promql",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name": "A",
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name": "warning", "target": float64(5),
						"op": "1", "matchType": "1",
					},
				},
			},
		},
	}

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when promql query is missing query text")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("error should mention missing query, got: %v", err)
	}
}

func TestValidate_FormulaMissingExpression(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	queries := cq["queries"].([]any)
	// Add a formula with empty expression
	queries = append(queries, map[string]any{
		"type": "builder_formula",
		"spec": map[string]any{
			"name": "F1",
		},
	})
	cq["queries"] = queries

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when builder_formula is missing expression")
	}
	if !strings.Contains(err.Error(), "expression") {
		t.Errorf("error should mention missing expression, got: %v", err)
	}
}

func TestValidate_FormulaWithExpression(t *testing.T) {
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

	result, err := ValidateFromMap(alert)
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

// --- Anomaly rule (v1 schema) tests ---

// minimalValidAnomalyRule returns a minimal anomaly rule using the v1 shape.
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

func TestValidate_AnomalyRule_Accepted(t *testing.T) {
	out, err := ValidateFromMap(minimalValidAnomalyRule())
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
}

func TestValidate_AnomalyRule_RejectsThresholds(t *testing.T) {
	rule := minimalValidAnomalyRule()
	rule["condition"].(map[string]any)["thresholds"] = map[string]any{
		"kind": "basic",
		"spec": []any{map[string]any{"name": "warning", "target": 2, "op": "above", "matchType": "at_least_once"}},
	}
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection when anomaly_rule carries thresholds block")
	}
	if !strings.Contains(err.Error(), "thresholds") {
		t.Errorf("expected error about thresholds, got: %v", err)
	}
}

func TestValidate_AnomalyRule_RequiresEvalWindow(t *testing.T) {
	rule := minimalValidAnomalyRule()
	delete(rule, "evalWindow")
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection when anomaly_rule omits evalWindow")
	}
	if !strings.Contains(err.Error(), "evalWindow") {
		t.Errorf("expected error about evalWindow, got: %v", err)
	}
}

func TestValidate_AnomalyRule_RequiresAlgorithmAndSeasonality(t *testing.T) {
	rule := minimalValidAnomalyRule()
	cond := rule["condition"].(map[string]any)
	delete(cond, "algorithm")
	delete(cond, "seasonality")
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection when anomaly_rule omits algorithm/seasonality")
	}
	msg := err.Error()
	if !strings.Contains(msg, "algorithm") || !strings.Contains(msg, "seasonality") {
		t.Errorf("expected errors about algorithm AND seasonality, got: %v", err)
	}
}

// --- PromQL envelope tests ---

func TestValidate_PromqlEnvelope_Accepted(t *testing.T) {
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
	if _, err := ValidateFromMap(rule); err != nil {
		t.Fatalf("expected promql envelope to validate, got: %v", err)
	}
}

func TestValidate_PromqlEnvelope_RejectsBuilderQueryType(t *testing.T) {
	rule := map[string]any{
		"alert":     "Bad type",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "promql_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "promql",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{"name": "A", "query": "up"},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{map[string]any{"name": "critical", "target": 1, "op": "above", "matchType": "at_least_once"}},
			},
		},
	}
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection when queryType=promql but envelope type=builder_query")
	}
}

func TestValidate_PromqlEnvelope_RequiresQueryText(t *testing.T) {
	rule := map[string]any{
		"alert":     "Missing query",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "promql_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "promql",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "promql",
						"spec": map[string]any{"name": "A"},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{map[string]any{"name": "critical", "target": 1, "op": "above", "matchType": "at_least_once"}},
			},
		},
	}
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection when promql envelope is missing spec.query")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("expected error about missing query, got: %v", err)
	}
}

// --- Metric aggregation shape test ---

func TestValidate_MetricAggregationShape_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	cond := rule["condition"].(map[string]any)
	cq := cond["compositeQuery"].(map[string]any)
	spec := cq["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	spec["aggregations"] = []any{
		map[string]any{"metricName": "k8s.pod.cpu_request_utilization", "timeAggregation": "avg", "spaceAggregation": "max"},
	}
	out, err := ValidateFromMap(rule)
	if err != nil {
		t.Fatalf("expected metric aggregation shape to validate, got: %v", err)
	}
	if !strings.Contains(string(out), "metricName") {
		t.Error("expected output to preserve metricName field")
	}
}

// --- absentFor passes through unchanged ---

func TestValidate_AbsentFor_PassesThrough(t *testing.T) {
	rule := minimalValidAlert()
	cond := rule["condition"].(map[string]any)
	cond["alertOnAbsent"] = true
	cond["absentFor"] = 15
	delete(cond, "thresholds") // alertOnAbsent is enough to pass the v2 gate
	out, err := ValidateFromMap(rule)
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

// --- renotify.alertStates validation ---

func TestValidate_RenotifyAlertStates_Accepted(t *testing.T) {
	for _, state := range []string{"firing", "nodata"} {
		rule := minimalValidAlert()
		rule["notificationSettings"] = map[string]any{
			"renotify": map[string]any{
				"enabled":     true,
				"interval":    "30m",
				"alertStates": []any{state},
			},
		}
		if _, err := ValidateFromMap(rule); err != nil {
			t.Errorf("expected alertState=%q to validate, got: %v", state, err)
		}
	}
}

func TestValidate_RenotifyAlertStates_RejectsInvalid(t *testing.T) {
	rule := minimalValidAlert()
	rule["notificationSettings"] = map[string]any{
		"renotify": map[string]any{
			"enabled":     true,
			"interval":    "30m",
			"alertStates": []any{"flapping"},
		},
	}
	_, err := ValidateFromMap(rule)
	if err == nil {
		t.Fatal("expected rejection for alertStates=flapping")
	}
	if !strings.Contains(err.Error(), "flapping") {
		t.Errorf("expected error to mention the invalid value, got: %v", err)
	}
}

// --- Broadened CompareOp/MatchType enums ---

func TestValidate_CompareOp_AboveOrEqual_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	spec := rule["condition"].(map[string]any)["thresholds"].(map[string]any)["spec"].([]any)[0].(map[string]any)
	spec["op"] = "above_or_equal"
	if _, err := ValidateFromMap(rule); err != nil {
		t.Errorf("expected op=above_or_equal to validate, got: %v", err)
	}
}

func TestValidate_CompareOp_OutsideBounds_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	spec := rule["condition"].(map[string]any)["thresholds"].(map[string]any)["spec"].([]any)[0].(map[string]any)
	spec["op"] = "outside_bounds"
	if _, err := ValidateFromMap(rule); err != nil {
		t.Errorf("expected op=outside_bounds to validate, got: %v", err)
	}
}

func TestValidate_MatchType_AvgAlias_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	spec := rule["condition"].(map[string]any)["thresholds"].(map[string]any)["spec"].([]any)[0].(map[string]any)
	spec["matchType"] = "avg"
	if _, err := ValidateFromMap(rule); err != nil {
		t.Errorf("expected matchType=avg to validate, got: %v", err)
	}
}

func TestValidate_MatchType_SumAlias_Accepted(t *testing.T) {
	rule := minimalValidAlert()
	spec := rule["condition"].(map[string]any)["thresholds"].(map[string]any)["spec"].([]any)[0].(map[string]any)
	spec["matchType"] = "sum"
	if _, err := ValidateFromMap(rule); err != nil {
		t.Errorf("expected matchType=sum to validate, got: %v", err)
	}
}
