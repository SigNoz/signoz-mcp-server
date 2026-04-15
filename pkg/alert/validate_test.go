package alert

import (
	"encoding/json"
	"strings"
	"testing"
)

// minimalValidAlert returns a minimal valid alert rule as map[string]any.
// Uses v1-style op/target/matchType which will be auto-converted to v2.
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
			"op":        "1",
			"target":    float64(100),
			"matchType": "1",
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

	// Check v2 defaults were applied
	if parsed["version"] != "v5" {
		t.Errorf("expected version=v5, got %v", parsed["version"])
	}
	if parsed["schemaVersion"] != "v2" {
		t.Errorf("expected schemaVersion=v2, got %v", parsed["schemaVersion"])
	}
	if parsed["source"] != "mcp" {
		t.Errorf("expected source=mcp, got %v", parsed["source"])
	}

	// v1 evalWindow/frequency should be removed
	if _, has := parsed["evalWindow"]; has {
		t.Error("evalWindow should be removed (converted to evaluation block)")
	}
	if _, has := parsed["frequency"]; has {
		t.Error("frequency should be removed (converted to evaluation block)")
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

	// Check v1→v2 threshold conversion
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
	if spec["op"] != "1" {
		t.Errorf("expected threshold op=1, got %v", spec["op"])
	}
	if spec["matchType"] != "1" {
		t.Errorf("expected threshold matchType=1, got %v", spec["matchType"])
	}
	if spec["name"] != "warning" {
		t.Errorf("expected threshold name=warning (from default severity), got %v", spec["name"])
	}

	// v1 condition-level fields should be cleared
	if cond["op"] != "" {
		t.Errorf("expected condition.op to be cleared, got %v", cond["op"])
	}
	if cond["matchType"] != "" {
		t.Errorf("expected condition.matchType to be cleared, got %v", cond["matchType"])
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

func TestValidate_V1ToV2Conversion(t *testing.T) {
	alert := map[string]any{
		"alert":     "V1 Alert",
		"alertType": "LOGS_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"evalWindow": "10m0s",
		"frequency":  "2m0s",
		"labels": map[string]any{
			"severity": "critical",
		},
		"preferredChannels": []any{"slack-alerts"},
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "logs",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"op":        "1",
			"target":    float64(500),
			"matchType": "4",
			"targetUnit": "count",
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

	// schemaVersion should be v2
	if parsed["schemaVersion"] != "v2" {
		t.Errorf("expected schemaVersion=v2, got %v", parsed["schemaVersion"])
	}

	// evalWindow/frequency should be removed, evaluation block created
	if _, has := parsed["evalWindow"]; has {
		t.Error("evalWindow should be removed")
	}
	if _, has := parsed["frequency"]; has {
		t.Error("frequency should be removed")
	}
	eval := parsed["evaluation"].(map[string]any)
	evalSpec := eval["spec"].(map[string]any)
	if evalSpec["evalWindow"] != "10m0s" {
		t.Errorf("expected evaluation.spec.evalWindow=10m0s (from v1), got %v", evalSpec["evalWindow"])
	}
	if evalSpec["frequency"] != "2m0s" {
		t.Errorf("expected evaluation.spec.frequency=2m0s (from v1), got %v", evalSpec["frequency"])
	}

	// v1 threshold should be converted to v2
	cond := parsed["condition"].(map[string]any)
	thresholds := cond["thresholds"].(map[string]any)
	specs := thresholds["spec"].([]any)
	spec := specs[0].(map[string]any)
	if spec["name"] != "critical" {
		t.Errorf("expected threshold name=critical (from severity label), got %v", spec["name"])
	}
	if spec["target"] != float64(500) {
		t.Errorf("expected threshold target=500, got %v", spec["target"])
	}
	if spec["targetUnit"] != "count" {
		t.Errorf("expected threshold targetUnit=count, got %v", spec["targetUnit"])
	}
	if spec["op"] != "1" {
		t.Errorf("expected threshold op=1, got %v", spec["op"])
	}
	if spec["matchType"] != "4" {
		t.Errorf("expected threshold matchType=4, got %v", spec["matchType"])
	}

	// preferredChannels should be copied to threshold channels
	channels, ok := spec["channels"].([]any)
	if !ok || len(channels) != 1 || channels[0] != "slack-alerts" {
		t.Errorf("expected threshold channels=[slack-alerts], got %v", spec["channels"])
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

func TestValidate_NoThresholdOrOp(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "op")
	delete(cond, "target")
	delete(cond, "matchType")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when neither op/target nor thresholds is set")
	}
	if !strings.Contains(err.Error(), "op+target") || !strings.Contains(err.Error(), "thresholds") {
		t.Errorf("error should mention both options, got: %v", err)
	}
}

func TestValidate_V1ThresholdPartialOpOnly(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "target")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when only op is set without target")
	}
	if !strings.Contains(err.Error(), "both op and target") {
		t.Errorf("error should mention both op and target, got: %v", err)
	}
}

func TestValidate_V1ThresholdPartialTargetOnly(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "op")

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error when only target is set without op")
	}
	if !strings.Contains(err.Error(), "both op and target") {
		t.Errorf("error should mention both op and target, got: %v", err)
	}
}

func TestValidate_V2ThresholdMissingOpAndMatchType(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	delete(cond, "op")
	delete(cond, "target")
	delete(cond, "matchType")
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

func TestValidate_InvalidCompareOp(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cond["op"] = "invalid_op"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for invalid op")
	}
	if !strings.Contains(err.Error(), "invalid_op") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestValidate_InvalidMatchType(t *testing.T) {
	alert := minimalValidAlert()
	cond := alert["condition"].(map[string]any)
	cond["matchType"] = "invalid_match"

	_, err := ValidateFromMap(alert)
	if err == nil {
		t.Fatal("expected error for invalid matchType")
	}
	if !strings.Contains(err.Error(), "invalid_match") {
		t.Errorf("error should mention the invalid value, got: %v", err)
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
	delete(cond, "op")
	delete(cond, "target")
	delete(cond, "matchType")
	cond["alertOnAbsent"] = true

	result, err := ValidateFromMap(alert)
	if err != nil {
		t.Fatalf("expected no error for alertOnAbsent=true without threshold, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should still have v2 schema but no auto-generated thresholds
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if parsed["schemaVersion"] != "v2" {
		t.Errorf("expected schemaVersion=v2, got %v", parsed["schemaVersion"])
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
	delete(cond, "op")
	delete(cond, "target")
	delete(cond, "matchType")
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

	// evalWindow/frequency should be removed
	if _, has := parsed["evalWindow"]; has {
		t.Error("evalWindow should be removed")
	}
	if _, has := parsed["frequency"]; has {
		t.Error("frequency should be removed")
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
