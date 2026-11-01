package querybuilder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuoteFilterLiteral(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "checkout", want: "'checkout'"},
		{name: "apostrophe", value: "it's", want: "'it\\'s'"},
		{name: "backslash", value: "C:\\logs", want: "'C:\\\\logs'"},
		{name: "apostrophe after backslash", value: "it\\'s", want: "'it\\\\\\'s'"},
		{name: "double quotes are literal", value: "say \"hello\"", want: "'say \"hello\"'"},
		{name: "unicode", value: "café", want: "'café'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, QuoteFilterLiteral(tt.value))
		})
	}
}
