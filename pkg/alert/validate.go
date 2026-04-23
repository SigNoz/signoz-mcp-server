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

	// validCompareOps mirrors SigNoz pkg/types/ruletypes/compare.go.
	// All numeric, literal, short, and symbolic aliases are accepted.
	validCompareOps = map[string]bool{
		// above
		"1": true, "above": true, ">": true,
		// below
		"2": true, "below": true, "<": true,
		// equal
		"3": true, "equal": true, "eq": true, "=": true,
		// not_equal
		"4": true, "not_equal": true, "not_eq": true, "!=": true,
		// above_or_equal
		"5": true, "above_or_equal": true, "above_or_eq": true, ">=": true,
		// below_or_equal
		"6": true, "below_or_equal": true, "below_or_eq": true, "<=": true,
		// outside_bounds
		"7": true, "outside_bounds": true,
	}

	// validMatchTypes mirrors SigNoz pkg/types/ruletypes/match.go.
	validMatchTypes = map[string]bool{
		"1": true, "at_least_once": true,
		"2": true, "all_the_times": true,
		"3": true, "on_average": true, "avg": true,
		"4": true, "in_total": true, "sum": true,
		"5": true, "last": true,
	}

	// validQueryEnvelopeTypes covers every composite-query envelope accepted
	// by SigNoz qbtypes.QueryEnvelope.
	validQueryEnvelopeTypes = map[string]bool{
		"builder_query":          true,
		"builder_formula":        true,
		"builder_trace_operator": true,
		"promql":                 true,
		"clickhouse_sql":         true,
	}

	// validAlertStates is the accepted set for renotify.alertStates.
	validAlertStates = map[string]bool{
		"firing": true,
		"nodata": true,
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
	validateNotificationSettings(rule, errs)
	validateCrossConstraints(rule, errs)

	if errs.HasErrors() {
		return nil, errs
	}

	applyDefaults(rule)
	// Anomaly rules use the v1 schema (top-level evalWindow/frequency,
	// condition.op/matchType/target). They must not carry a v2alpha1
	// schemaVersion or an evaluation block.
	if strVal(rule, "ruleType") != "anomaly_rule" {
		applyV2Defaults(rule)
	}

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
// Threshold/PromQL rules use the v2alpha1 thresholds block; anomaly rules use
// the v1 shape (top-level evalWindow/frequency + condition.op/matchType/target/
// algorithm/seasonality) which is enforced in a separate branch.
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

	ruleType := strVal(rule, "ruleType")
	isAnomaly := ruleType == "anomaly_rule"

	// Anomaly rules use the v1 shape at the top level — no thresholds block.
	// Threshold/PromQL rules must carry condition.thresholds unless they are
	// using alertOnAbsent as the sole trigger.
	hasThresholds := mapVal(cond, "thresholds") != nil
	hasAlertOnAbsent := boolVal(cond, "alertOnAbsent")

	if isAnomaly {
		if hasThresholds {
			errs.Add("condition.thresholds", "must be omitted for anomaly_rule (v1 schema); use condition.op/matchType/target/algorithm/seasonality at the condition level instead")
		}
		validateAnomalyFields(rule, cond, errs)
	} else if !hasThresholds && !hasAlertOnAbsent {
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
			errs.Add(prefix+".type", "is required (e.g. builder_query, builder_formula, promql, clickhouse_sql)")
			continue
		}
		if !validQueryEnvelopeTypes[qType] {
			errs.Addf(prefix+".type", "must be one of builder_query, builder_formula, builder_trace_operator, promql, clickhouse_sql; got %q", qType)
			continue
		}

		// Envelope type must align with compositeQuery.queryType.
		switch queryType {
		case "promql":
			if qType != "promql" && qType != "builder_formula" {
				errs.Addf(prefix+".type", "must be 'promql' (or 'builder_formula' for a formula) when compositeQuery.queryType=promql; got %q", qType)
			}
		case "clickhouse_sql":
			if qType != "clickhouse_sql" && qType != "builder_formula" {
				errs.Addf(prefix+".type", "must be 'clickhouse_sql' (or 'builder_formula' for a formula) when compositeQuery.queryType=clickhouse_sql; got %q", qType)
			}
		case "builder":
			// 'builder' queries use builder_query / builder_formula / builder_trace_operator.
			if qType != "builder_query" && qType != "builder_formula" && qType != "builder_trace_operator" {
				errs.Addf(prefix+".type", "must be builder_query, builder_formula, or builder_trace_operator when compositeQuery.queryType=builder; got %q", qType)
			}
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

		// For promql / clickhouse_sql envelopes, require the query text on the
		// spec itself.
		if qType == "promql" || qType == "clickhouse_sql" {
			if strVal(spec, "query") == "" {
				errs.Addf(prefix+".spec.query", "is required for %s queries", qType)
			}
		}

		// For builder_formula, require expression
		if qType == "builder_formula" {
			if strVal(spec, "expression") == "" {
				errs.Add(prefix+".spec.expression", "is required for builder_formula (e.g. A / B * 100)")
			}
		}
	}

	// Validate v2alpha1 thresholds structure (skipped for anomaly).
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
				errs.Add(prefix+".name", "is required (critical, error, warning, or info)")
			}
			if sm["target"] == nil {
				errs.Add(prefix+".target", "is required")
			}
			op := strVal(sm, "op")
			if op == "" {
				errs.Add(prefix+".op", "is required (e.g. above, below, equal, not_equal, above_or_equal, below_or_equal, outside_bounds)")
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

// validateAnomalyFields enforces the v1 anomaly-rule shape: top-level
// evalWindow + frequency, and condition.op + matchType + target + algorithm +
// seasonality. The compositeQuery.queries must carry an anomaly function spec.
func validateAnomalyFields(rule, cond map[string]any, errs *ValidationError) {
	if strVal(rule, "evalWindow") == "" {
		errs.Add("evalWindow", "is required for anomaly_rule (v1 schema). Use a Go duration string, e.g. 24h")
	}
	if strVal(rule, "frequency") == "" {
		errs.Add("frequency", "is required for anomaly_rule (v1 schema). Use a Go duration string, e.g. 3h")
	}

	op := strVal(cond, "op")
	if op == "" {
		errs.Add("condition.op", "is required for anomaly_rule")
	} else if !validCompareOps[op] {
		errs.Addf("condition.op", "must be a valid operator; got %q", op)
	}

	mt := strVal(cond, "matchType")
	if mt == "" {
		errs.Add("condition.matchType", "is required for anomaly_rule")
	} else if !validMatchTypes[mt] {
		errs.Addf("condition.matchType", "must be a valid match type; got %q", mt)
	}

	if cond["target"] == nil {
		errs.Add("condition.target", "is required for anomaly_rule (z-score threshold)")
	}

	if strVal(cond, "algorithm") == "" {
		errs.Add("condition.algorithm", "is required for anomaly_rule (e.g. standard)")
	}
	if strVal(cond, "seasonality") == "" {
		errs.Add("condition.seasonality", "is required for anomaly_rule (hourly, daily, or weekly)")
	}
}

// validateNotificationSettings enforces the accepted set for renotify.alertStates.
func validateNotificationSettings(rule map[string]any, errs *ValidationError) {
	ns := mapVal(rule, "notificationSettings")
	if ns == nil {
		return
	}
	renotify := mapVal(ns, "renotify")
	if renotify == nil {
		return
	}
	states := sliceVal(renotify, "alertStates")
	for i, s := range states {
		str, ok := s.(string)
		if !ok {
			errs.Addf(fmt.Sprintf("notificationSettings.renotify.alertStates[%d]", i), "must be a string; got %T", s)
			continue
		}
		if !validAlertStates[str] {
			errs.Addf(fmt.Sprintf("notificationSettings.renotify.alertStates[%d]", i), "must be 'firing' or 'nodata'; got %q", str)
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
