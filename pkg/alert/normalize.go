// Package alert normalizes SigNoz alert rule payloads before they are sent
// to the SigNoz API: the shared Query Builder limit/order bounds, the
// MCP-owned defaults, and the v1 versus v2alpha1 schema split. SigNoz is the
// only validator of rule bodies; its 400 reaches clients as VALIDATION_FAILED
// carrying the upstream message.
package alert

import (
	"encoding/json"
	"fmt"
)

// NormalizeFromMap marshals the arguments and normalizes them as an alert body.
func NormalizeFromMap(m map[string]any) ([]byte, error) {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal arguments: %w", err)
	}
	return Normalize(jsonBytes)
}

// Normalize parses raw alert JSON, applies the shared Query Builder bounds and
// the MCP defaults, and returns the normalized bytes.
func Normalize(jsonBytes []byte) ([]byte, error) {
	var rule map[string]any
	if err := json.Unmarshal(jsonBytes, &rule); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
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
		return nil, fmt.Errorf("failed to serialize normalized alert: %w", err)
	}
	return out, nil
}

// validateRequired checks that all required top-level fields are present.

// applyDefaults fills in missing fields with sensible defaults.
func applyDefaults(rule map[string]any) {
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
	// A true default, not an overwrite: an explicit schemaVersion (including
	// a legitimate v1-shaped rule) passes through for SigNoz to handle.
	if strVal(rule, "schemaVersion") == "" {
		rule["schemaVersion"] = "v2alpha1"
	}

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

// floatVal returns m[key] as a float64 when it is a JSON number.
func floatVal(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}
