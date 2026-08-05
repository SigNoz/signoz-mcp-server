package util

import "testing"

func TestNormalizeClientSource(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "default", input: "", want: ClientSourceUserClient},
		{name: "assistant", input: " ai-assistant ", want: ClientSourceAIAssistant},
		{name: "unknown", input: "attacker-controlled-source", want: ClientSourceOther},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeClientSource(tt.input); got != tt.want {
				t.Fatalf("NormalizeClientSource(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
