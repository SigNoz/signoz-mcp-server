package util

import "testing"

func TestResourceWebURL(t *testing.T) {
	cases := []struct {
		name         string
		base         string
		resourceType string
		id           string
		want         string
		wantOK       bool
	}{
		{"dashboard", "https://signoz.example.com", "dashboard", "019a2e3d-uuid", "https://signoz.example.com/dashboard/019a2e3d-uuid", true},
		{"alert", "https://signoz.example.com", "alert", "rule-123", "https://signoz.example.com/alerts/overview?ruleId=rule-123&tab=AlertRules", true},
		{"service with space", "https://signoz.example.com", "service", "cart service", "https://signoz.example.com/services/cart%20service", true},
		{"service with slash", "https://signoz.example.com", "service", "a/b", "https://signoz.example.com/services/a%2Fb", true},
		{"empty base omits", "", "dashboard", "x", "", false},
		{"empty id omits", "https://signoz.example.com", "dashboard", "", "", false},
		{"unknown type omits", "https://signoz.example.com", "trace", "x", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResourceWebURL(tc.base, tc.resourceType, tc.id)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("url = %q, want %q", got, tc.want)
			}
		})
	}
}
