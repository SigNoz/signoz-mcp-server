package main

import (
	"bytes"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

func TestPrintVersion(t *testing.T) {
	original := version.Version
	t.Cleanup(func() { version.Version = original })
	version.Version = "v1.2.3"

	tests := []struct {
		name    string
		args    []string
		handled bool
		want    string
	}{
		{name: "double dash", args: []string{"--version"}, handled: true, want: "signoz-mcp-server/v1.2.3\n"},
		{name: "single dash", args: []string{"-version"}, handled: true, want: "signoz-mcp-server/v1.2.3\n"},
		{name: "no args", args: nil, handled: false, want: ""},
		{name: "other args", args: []string{"--help"}, handled: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := printVersion(tt.args, &out); got != tt.handled {
				t.Fatalf("printVersion(%v) = %v, want %v", tt.args, got, tt.handled)
			}
			if out.String() != tt.want {
				t.Fatalf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}
