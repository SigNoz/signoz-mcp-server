package otel

import "testing"

func TestNormalizeMCPMethod(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{method: "tools/call", want: "tools/call"},
		{method: "notifications/tools/list_changed", want: "notifications/tools/list_changed"},
		{method: "server/discover", want: "server/discover"},
		{method: "attacker/generated-method", want: UnknownMCPMethod},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			if got := NormalizeMCPMethod(test.method); got != test.want {
				t.Fatalf("NormalizeMCPMethod(%q) = %q, want %q", test.method, got, test.want)
			}
		})
	}
}
