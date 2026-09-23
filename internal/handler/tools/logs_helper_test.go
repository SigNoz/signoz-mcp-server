package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuoteLogFilterLiteral(t *testing.T) {
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
			require.Equal(t, tt.want, quoteLogFilterLiteral(tt.value))
		})
	}
}

func TestQuoteLogContainsLiteralEscapesGrammarAndLIKE(t *testing.T) {
	value := `timed out: it's C:\logs%_`
	want := `'timed out: it\'s C:\\\\logs\\%\\_'`
	require.Equal(t, want, quoteLogContainsLiteral(value))
}

func TestBuildLogFilterExpr(t *testing.T) {
	t.Run("individual convenience filters", func(t *testing.T) {
		require.Equal(t, "service.name = 'checkout'", buildLogFilterExpr("", "checkout", "", "", ""))
		require.Equal(t, "severity_text = 'ERROR'", buildLogFilterExpr("", "", "ERROR", "", ""))
		require.Equal(t, "body CONTAINS 'timeout'", buildLogFilterExpr("", "", "", "timeout", ""))
	})

	t.Run("standalone filter is byte-for-byte unchanged", func(t *testing.T) {
		filter := "  severity_text = 'ERROR' OR body CONTAINS 'panic'  "
		require.Equal(t, filter, buildLogFilterExpr(filter, "", "", "", ""))
	})

	t.Run("OR filter is grouped before service", func(t *testing.T) {
		filter := "severity_text = 'ERROR' OR severity_text = 'FATAL'"
		want := "(severity_text = 'ERROR' OR severity_text = 'FATAL') AND service.name = 'payments'"
		require.Equal(t, want, buildLogFilterExpr(filter, "payments", "", "", ""))
	})

	t.Run("OR filter is grouped before search text", func(t *testing.T) {
		filter := "service.name = 'api' OR service.name = 'worker'"
		want := "(service.name = 'api' OR service.name = 'worker') AND body CONTAINS 'timeout'"
		require.Equal(t, want, buildLogFilterExpr(filter, "", "", "timeout", ""))
	})

	t.Run("combined filter preserves inner bytes and escapes convenience literals", func(t *testing.T) {
		filter := `search('timeout', body) AND body CONTAINS 'raw%_ \\'`
		got := buildLogFilterExpr(filter, "payment's%_", "WARN_%", `timed out: it's C:\logs%_`, "")
		want := "(" + filter + `) AND service.name = 'payment\'s%_' AND severity_text = 'WARN_%' AND body CONTAINS 'timed out: it\'s C:\\\\logs\\%\\_'`
		require.Equal(t, want, got)
	})
}

func TestReadLogFilterExpr(t *testing.T) {
	t.Run("filter accepted with surrounding bytes preserved", func(t *testing.T) {
		got, err := readLogFilterExpr(map[string]any{"filter": " body CONTAINS 'x' "})
		require.NoError(t, err)
		require.Equal(t, " body CONTAINS 'x' ", got)
	})

	t.Run("whitespace-only filter is absent when convenience filters are set", func(t *testing.T) {
		req, err := parseSearchLogsArgs(map[string]any{"filter": "   ", "service": "api"})
		require.NoError(t, err)
		require.Equal(t, "service.name = 'api'", req.FilterExpression)
	})

	t.Run("query rejected even when filter is present", func(t *testing.T) {
		_, err := readLogFilterExpr(map[string]any{"filter": "body CONTAINS 'x'", "query": "body CONTAINS 'legacy'"})
		require.EqualError(t, err, logsLegacyQueryAliasError)
	})

	t.Run("query-only rejected", func(t *testing.T) {
		_, err := readLogFilterExpr(map[string]any{"query": "body CONTAINS 'legacy'"})
		require.EqualError(t, err, logsLegacyQueryAliasError)
	})

	t.Run("every supplied legacy query form rejected", func(t *testing.T) {
		for name, value := range map[string]any{
			"null":     nil,
			"empty":    "",
			"spaces":   "   ",
			"object":   map[string]any{"expression": "legacy"},
			"argument": []any{"legacy"},
		} {
			t.Run(name, func(t *testing.T) {
				_, err := readLogFilterExpr(map[string]any{"query": value})
				require.EqualError(t, err, logsLegacyQueryAliasError)
			})
		}
	})
}

func TestParseSearchLogsArgs_SearchScope(t *testing.T) {
	for scope, want := range map[string]string{
		"":          `body CONTAINS 'it\'s 50\\%'`,
		"body":      `body CONTAINS 'it\'s 50\\%'`,
		"attribute": `search('it\'s 50%', attribute)`,
		"resource":  `search('it\'s 50%', resource)`,
		"all":       `search('it\'s 50%')`,
	} {
		t.Run("scope "+scope, func(t *testing.T) {
			req, err := parseSearchLogsArgs(map[string]any{"searchText": "it's 50%", "searchScope": scope, "service": "api"})
			require.NoError(t, err)
			require.Equal(t, "service.name = 'api' AND "+want, req.FilterExpression)
		})
	}

	t.Run("unknown scope is rejected", func(t *testing.T) {
		_, err := parseSearchLogsArgs(map[string]any{"searchText": "x", "searchScope": "everywhere"})
		require.ErrorContains(t, err, "use one of: body, attribute, resource, all")
	})

	t.Run("scope without text is rejected", func(t *testing.T) {
		_, err := parseSearchLogsArgs(map[string]any{"searchScope": "all"})
		require.ErrorContains(t, err, `"searchScope" needs "searchText"`)
	})
}
