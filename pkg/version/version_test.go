package version

import "testing"

func TestUserAgent(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "release", version: "v0.8.1", want: "signoz-mcp-server/v0.8.1"},
		{name: "branch", version: "main-abcdef0", want: "signoz-mcp-server/main-abcdef0"},
		{name: "trimmed", version: "  v0.8.1  ", want: "signoz-mcp-server/v0.8.1"},
		{name: "blank fallback", version: " ", want: "signoz-mcp-server/dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			if got := UserAgent(); got != tt.want {
				t.Fatalf("UserAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}
