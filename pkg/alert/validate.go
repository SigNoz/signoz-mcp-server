// Package alert provides validation and normalization for SigNoz alert rule
// payloads before they are sent to the SigNoz API.
package alert

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Valid enum values for alert fields.
var (
	validAlertTypes = map[string]bool{
		"METRIC_BASED_ALERT":     true,
		"LOGS_BASED_ALERT":       true,
		"TRACES_BASED_ALERT":     true,
		"EXCEPTIONS_BASED_ALERT": true,
	}

	validRuleTypes = map[string]bool{
		"threshold_rule": true,
		"promql_rule":    true,
		"anomaly_rule":   true,
	}

	validQueryTypes = map[string]bool{
		"builder":        true,
		"promql":         true,
		"clickhouse_sql": true,
	}

	validCompareOps = map[string]bool{
		"1": true, "2": true, "3": true, "4": true,
		"above": true, "below": true, "equal": true, "not_equal": true,
	}

	validMatchTypes = map[string]bool{
		"1": true, "2": true, "3": true, "4": true, "5": true,
		"at_least_once": true, "all_the_times": true,
		"on_average": true, "in_total": true, "last": true,
	}

	validBuilderQueryTypes = map[string]bool{
		"builder_query":   true,
		"builder_formula": true,
	}

	validSignals = map[string]bool{
		"metrics": true,
		"logs":    true,
		"traces":  true,
	}

	// alertTypeToSignal maps alert types to their expected builder query signals.
	alertTypeToSignal = map[string]string{
		"METRIC_BASED_ALERT": "metrics",
		"LOGS_BASED_ALERT":   "logs",
		"TRACES_BASED_ALERT": "traces",
	}
)

// ValidationError accumulates multiple validation errors.
type ValidationError struct {
	errors []string
}

func (v *ValidationError) Add(field, msg string) {
	v.errors = append(v.errors, fmt.Sprintf("%s: %s", field, msg))
}

func (v *ValidationError) Addf(field, format string, args ...any) {
	v.errors = append(v.errors, fmt.Sprintf("%s: %s", field, fmt.Sprintf(format, args...)))
}

func (v *ValidationError) HasErrors() bool {
	return len(v.errors) > 0
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("alert validation failed with %d error(s):\n  %s",
		len(v.errors), strings.Join(v.errors, "\n  "))
}

// ValidateFromMap validates an alert rule from a map[string]any (MCP tool arguments),
// applies defaults, and returns clean JSON bytes ready for the SigNoz API.
func ValidateFromMap(m map[string]any) ([]byte, error) {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal arguments: %w", err)
	}
	return Validate(jsonBytes)
}

// Validate parses raw alert JSON, validates required fields, enums, condition
// structure, cross-validates alert type vs query signal, applies defaults, and
// returns normalized JSON bytes.
func Validate(jsonBytes []byte) ([]byte, error) {
	var rule map[string]any
	if err := json.Unmarshal(jsonBytes, &rule); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	errs := &ValidationError{}

	validateRequired(rule, errs)
	validateEnums(rule, errs)
	validateCondition(rule, errs)
	validateCrossConstraints(rule, errs)

	if errs.HasErrors() {
		return nil, errs
	}

	applyDefaults(rule)
	applyV2Defaults(rule)

	out, err := json.Marshal(rule)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize validated alert: %w", err)
	}
	return out, nil
}

// validateRequired checks that all required top-level fields are present.
func validateRequired(rule map[string]any, errs *ValidationError) {
	if strVal(rule, "alert") == "" {
		errs.Add("alert", "is required and must not be empty")
	}
	if strVal(rule, "alertType") == "" {
		errs.Add("alertType", "is required (METRIC_BASED_ALERT, LOGS_BASED_ALERT, TRACES_BASED_ALERT, or EXCEPTIONS_BASED_ALERT)")
	}
	if strVal(rule, "ruleType") == "" {
		errs.Add("ruleType", "is required (threshold_rule, promql_rule, or anomaly_rule)")
	}
	if v, ok := rule["condition"]; !ok {
		errs.Add("condition", "is required")
	} else if _, isMap := v.(map[string]any); !isMap {
		errs.Add("condition", "must be an object containing compositeQuery and thresholds")
	}
}

// validateEnums checks that enum fields have valid values.
func validateEnums(rule map[string]any, errs *ValidationError) {
	if at := strVal(rule, "alertType"); at != "" && !validAlertTypes[at] {
		errs.Addf("alertType", "must be one of METRIC_BASED_ALERT, LOGS_BASED_ALERT, TRACES_BASED_ALERT, EXCEPTIONS_BASED_ALERT; got %q", at)
	}
	if rt := strVal(rule, "ruleType"); rt != "" && !validRuleTypes[rt] {
		errs.Addf("ruleType", "must be one of threshold_rule, promql_rule, anomaly_rule; got %q", rt)
	}

	cond := mapVal(rule, "condition")
	if cond == nil {
		return
	}

	cq := mapVal(cond, "compositeQuery")
	if cq != nil {
		if qt := strVal(cq, "queryType"); qt != "" && !validQueryTypes[qt] {
			errs.Addf("condition.compositeQuery.queryType", "must be one of builder, promql, clickhouse_sql; got %q", qt)
		}
	}
}

// validateCondition checks the condition and composite query structure.
func validateCondition(rule map[string]any, errs *ValidationError) {
	cond := mapVal(rule, "condition")
	if cond == nil {
		return // already caught by validateRequired
	}

	cq := mapVal(cond, "compositeQuery")
	if cq == nil {
		errs.Add("condition.compositeQuery", "is required")
		return
	}

	if strVal(cq, "queryType") == "" {
		errs.Add("condition.compositeQuery.queryType", "is required (builder, promql, or clickhouse_sql)")
	}

	queries := sliceVal(cq, "queries")
	if len(queries) == 0 {
		errs.Add("condition.compositeQuery.queries", "must contain at least one query")
		return
	}

	// Require v2alpha1 thresholds (unless alertOnAbsent is set)
	hasThresholds := mapVal(cond, "thresholds") != nil
	hasAlertOnAbsent := boolVal(cond, "alertOnAbsent")

	if !hasThresholds && !hasAlertOnAbsent {
		errs.Add("condition.thresholds", "is required (v2alpha1 schema); use condition.thresholds with kind and spec array")
	}

	// Validate individual queries
	queryType := strVal(cq, "queryType")
	for i, q := range queries {
		prefix := fmt.Sprintf("condition.compositeQuery.queries[%d]", i)
		qm, ok := q.(map[string]any)
		if !ok {
			errs.Add(prefix, "must be an object with type and spec fields")
			continue
		}

		qType := strVal(qm, "type")
		if qType == "" {
			errs.Add(prefix+".type", "is required (builder_query or builder_formula)")
			continue
		}
		if !validBuilderQueryTypes[qType] {
			errs.Addf(prefix+".type", "must be builder_query or builder_formula; got %q", qType)
			continue
		}

		spec := mapVal(qm, "spec")
		if spec == nil {
			errs.Add(prefix+".spec", "is required")
			continue
		}

		if strVal(spec, "name") == "" {
			errs.Add(prefix+".spec.name", "is required (e.g. A, B, C)")
		}

		// For builder queries, validate signal
		if qType == "builder_query" && queryType == "builder" {
			sig := strVal(spec, "signal")
			if sig == "" {
				errs.Add(prefix+".spec.signal", "is required for builder queries (metrics, logs, or traces)")
			} else if !validSignals[sig] {
				errs.Addf(prefix+".spec.signal", "must be metrics, logs, or traces; got %q", sig)
			}
		}

		// For promql/clickhouse_sql queries, require the query text
		if qType == "builder_query" && (queryType == "promql" || queryType == "clickhouse_sql") {
			if strVal(spec, "query") == "" {
				errs.Addf(prefix+".spec.query", "is required for %s queries", queryType)
			}
		}
	}

	// Validate v2alpha1 thresholds structure
	if hasThresholds {
		thresholds := mapVal(cond, "thresholds")
		if strVal(thresholds, "kind") == "" {
			errs.Add("condition.thresholds.kind", "is required (use 'basic')")
		}
		specs := sliceVal(thresholds, "spec")
		if len(specs) == 0 {
			errs.Add("condition.thresholds.spec", "must contain at least one threshold")
		}
		for i, s := range specs {
			prefix := fmt.Sprintf("condition.thresholds.spec[%d]", i)
			sm, ok := s.(map[string]any)
			if !ok {
				errs.Add(prefix, "must be an object with name, target, op, and matchType fields")
				continue
			}
			if strVal(sm, "name") == "" {
				errs.Add(prefix+".name", "is required (critical, warning, or info)")
			}
			if sm["target"] == nil {
				errs.Add(prefix+".target", "is required")
			}
			op := strVal(sm, "op")
			if op == "" {
				errs.Add(prefix+".op", "is required (e.g. above, below, equal, not_equal)")
			} else if !validCompareOps[op] {
				errs.Addf(prefix+".op", "must be a valid operator; got %q", op)
			}
			mt := strVal(sm, "matchType")
			if mt == "" {
				errs.Add(prefix+".matchType", "is required (e.g. at_least_once, all_the_times, on_average, in_total, last)")
			} else if !validMatchTypes[mt] {
				errs.Addf(prefix+".matchType", "must be a valid match type; got %q", mt)
			}
		}
	}
}

// validateCrossConstraints checks constraints that span multiple fields.
func validateCrossConstraints(rule map[string]any, errs *ValidationError) {
	alertType := strVal(rule, "alertType")
	ruleType := strVal(rule, "ruleType")

	// anomaly_rule only works with METRIC_BASED_ALERT
	if ruleType == "anomaly_rule" && alertType != "" && alertType != "METRIC_BASED_ALERT" {
		errs.Addf("ruleType", "anomaly_rule can only be used with METRIC_BASED_ALERT, got alertType=%q", alertType)
	}

	// promql_rule requires queryType=promql
	cond := mapVal(rule, "condition")
	if cond == nil {
		return
	}
	cq := mapVal(cond, "compositeQuery")
	if cq == nil {
		return
	}

	queryType := strVal(cq, "queryType")

	if ruleType == "promql_rule" && queryType != "" && queryType != "promql" {
		errs.Addf("condition.compositeQuery.queryType", "must be 'promql' when ruleType is promql_rule; got %q", queryType)
	}

	// For builder queries, validate signal matches alertType
	if queryType == "builder" && alertType != "" {
		expectedSignal, hasExpectedSignal := alertTypeToSignal[alertType]
		if !hasExpectedSignal {
			// EXCEPTIONS_BASED_ALERT doesn't enforce a signal — it can use clickhouse_sql or builder
			return
		}
		queries := sliceVal(cq, "queries")
		for i, q := range queries {
			qm, ok := q.(map[string]any)
			if !ok {
				continue
			}
			if strVal(qm, "type") != "builder_query" {
				continue // formulas don't have signals
			}
			spec := mapVal(qm, "spec")
			if spec == nil {
				continue
			}
			sig := strVal(spec, "signal")
			if sig != "" && sig != expectedSignal {
				prefix := fmt.Sprintf("condition.compositeQuery.queries[%d].spec.signal", i)
				errs.Addf(prefix, "expected %q for alertType=%s, got %q", expectedSignal, alertType, sig)
			}
		}
	}
}

// applyDefaults fills in missing fields with sensible defaults.
func applyDefaults(rule map[string]any) {
	// version defaults to v5
	if strVal(rule, "version") == "" {
		rule["version"] = "v5"
	}

	// source defaults to mcp
	if strVal(rule, "source") == "" {
		rule["source"] = "mcp"
	}

	// Default severity label
	labels, ok := rule["labels"].(map[string]any)
	if !ok || labels == nil {
		labels = map[string]any{}
		rule["labels"] = labels
	}
	if _, hasSeverity := labels["severity"]; !hasSeverity {
		labels["severity"] = "warning"
	}

	// Default annotations if missing
	if _, hasAnnotations := rule["annotations"]; !hasAnnotations {
		rule["annotations"] = map[string]any{
			"description": "This alert is fired when the defined metric (current value: {{$value}}) crosses the threshold ({{$threshold}})",
			"summary":     "The rule threshold is set to {{$threshold}}, and the observed metric value is {{$value}}",
		}
	}

	// Default composite query panelType
	cond := mapVal(rule, "condition")
	if cond != nil {
		cq := mapVal(cond, "compositeQuery")
		if cq != nil {
			if strVal(cq, "panelType") == "" {
				cq["panelType"] = "graph"
			}

			// Default selectedQueryName to first query's name
			if strVal(cond, "selectedQueryName") == "" {
				queries := sliceVal(cq, "queries")
				if len(queries) > 0 {
					if qm, ok := queries[0].(map[string]any); ok {
						if spec := mapVal(qm, "spec"); spec != nil {
							if name := strVal(spec, "name"); name != "" {
								cond["selectedQueryName"] = name
							}
						}
					}
				}
			}
		}
	}
}

// applyV2Defaults sets v2alpha1 schema fields and defaults.
// This runs after applyDefaults.
func applyV2Defaults(rule map[string]any) {
	rule["schemaVersion"] = "v2alpha1"

	// Default evaluation block if missing
	if rule["evaluation"] == nil {
		rule["evaluation"] = map[string]any{
			"kind": "rolling",
			"spec": map[string]any{
				"evalWindow": "5m0s",
				"frequency":  "1m0s",
			},
		}
	}

	// Default notificationSettings if not present
	if rule["notificationSettings"] == nil {
		rule["notificationSettings"] = map[string]any{
			"renotify": map[string]any{
				"enabled":  false,
				"interval": "30m",
			},
		}
	}
}

// --- map access helpers ---

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func mapVal(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func sliceVal(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func boolVal(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
