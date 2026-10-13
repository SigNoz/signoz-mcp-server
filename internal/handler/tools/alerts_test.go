package tools

import (
	"testing"
)

func TestUsesPolicyRouting(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want bool
	}{
		{name: "threshold policy", args: map[string]any{"ruleType": "threshold_rule", "notificationSettings": map[string]any{"usePolicy": true}}, want: true},
		{name: "promql policy", args: map[string]any{"ruleType": "promql_rule", "notificationSettings": map[string]any{"usePolicy": true}}, want: true},
		{name: "direct routing", args: map[string]any{"ruleType": "threshold_rule", "notificationSettings": map[string]any{"usePolicy": false}}},
		{name: "missing settings", args: map[string]any{"ruleType": "threshold_rule"}},
		{name: "non-boolean policy", args: map[string]any{"ruleType": "threshold_rule", "notificationSettings": map[string]any{"usePolicy": "true"}}},
		{name: "anomaly v1", args: map[string]any{"ruleType": "anomaly_rule", "notificationSettings": map[string]any{"usePolicy": true}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesPolicyRouting(tc.args); got != tc.want {
				t.Fatalf("usesPolicyRouting() = %v, want %v", got, tc.want)
			}
		})
	}
}
