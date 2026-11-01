package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Both trace tools build the same filter from filter plus shortcuts; SigNoz
// binds AND tighter than OR, so an unparenthesized OR filter escapes them.
func TestTraceShortcutsComposeWithFilter(t *testing.T) {
	parsers := map[string]func(map[string]any) (string, error){
		"search": func(args map[string]any) (string, error) {
			req, err := parseSearchTracesArgs(args)
			if err != nil {
				return "", err
			}
			return req.FilterExpression, nil
		},
		"aggregate": func(args map[string]any) (string, error) {
			req, err := parseAggregateTracesArgs(mergeArgs(map[string]any{"aggregations": "count()"}, args))
			if err != nil {
				return "", err
			}
			return req.FilterExpression, nil
		},
	}
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "OR filter is grouped before shortcuts",
			args: map[string]any{"filter": "service.name = 'a' OR has_error = true", "service": "checkout", "error": false},
			want: "(service.name = 'a' OR has_error = true) AND service.name = 'checkout' AND has_error = false",
		},
		{
			name: "standalone filter is byte-for-byte unchanged",
			args: map[string]any{"filter": " service.name = 'a' OR has_error = true "},
			want: " service.name = 'a' OR has_error = true ",
		},
		{
			name: "shortcut values are quoted literals",
			args: map[string]any{"service": `pay\ments`, "operation": "SELECT * FROM users WHERE name = 'x'"},
			want: `service.name = 'pay\\ments' AND name = 'SELECT * FROM users WHERE name = \'x\''`,
		},
	}
	for parserName, parse := range parsers {
		for _, tt := range tests {
			t.Run(parserName+"/"+tt.name, func(t *testing.T) {
				got, err := parse(tt.args)
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			})
		}
	}
}

func TestTraceDurationRejectsNonScalar(t *testing.T) {
	_, err := parseSearchTracesArgs(map[string]any{"minDuration": map[string]any{"ms": 500}})
	require.EqualError(t, err, `parameter validation failed: "minDuration" must be a duration such as "500ms" or a number of nanoseconds`)
}
