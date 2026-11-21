package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// A body-scoped searchText misses text kept in attribute values, so an empty
// first page points at the wider scopes. Other scopes, later pages, and pages
// with rows get no such note.
func TestSearchLogs_EmptyBodyOnlySearchTextPointsAtWiderScopes(t *testing.T) {
	const (
		emptyPage = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}}}`
		onePage   = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"data":{"body":"timeout"}}]}]}}}`
	)
	tests := []struct {
		name     string
		args     map[string]any
		response string
		wantNote bool
	}{
		{name: "default scope, empty first page", args: map[string]any{"searchText": "order-42"}, response: emptyPage, wantNote: true},
		{name: "all scope, empty first page", args: map[string]any{"searchText": "order-42", "searchScope": "all"}, response: emptyPage},
		{name: "default scope, empty later page", args: map[string]any{"searchText": "order-42", "offset": "20"}, response: emptyPage},
		{name: "default scope, rows returned", args: map[string]any{"searchText": "timeout"}, response: onePage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(upstream.Close)
			h := &Handler{
				logger:         logpkg.New("error"),
				clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
			}

			result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", tc.args))
			require.NoError(t, err)
			require.False(t, result.IsError, allTextBlocks(result))
			require.Equal(t, tc.wantNote, strings.Contains(allTextBlocks(result), bodyOnlySearchTextNote), allTextBlocks(result))
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
		require.Equal(t, "resource.service.name = 'checkout'", buildLogFilterExpr("", "checkout", "", "", ""))
		require.Equal(t, "severity_text ILIKE 'ERROR%'", buildLogFilterExpr("", "", "ERROR", "", ""))
		require.Equal(t, "body CONTAINS 'timeout'", buildLogFilterExpr("", "", "", "timeout", ""))
	})

	t.Run("standalone filter is byte-for-byte unchanged", func(t *testing.T) {
		filter := "  severity_text = 'ERROR' OR body CONTAINS 'panic'  "
		require.Equal(t, filter, buildLogFilterExpr(filter, "", "", "", ""))
	})

	t.Run("OR filter is grouped before service", func(t *testing.T) {
		filter := "severity_text = 'ERROR' OR severity_text = 'FATAL'"
		want := "(severity_text = 'ERROR' OR severity_text = 'FATAL') AND resource.service.name = 'payments'"
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
		want := "(" + filter + `) AND resource.service.name = 'payment\'s%_' AND severity_text ILIKE 'WARN\\_\\%%' AND body CONTAINS 'timed out: it\'s C:\\\\logs\\%\\_'`
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
		require.Equal(t, "resource.service.name = 'api'", req.FilterExpression)
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
			require.Equal(t, "resource.service.name = 'api' AND "+want, req.FilterExpression)
		})
	}

	t.Run("unknown scope is rejected", func(t *testing.T) {
		_, err := parseSearchLogsArgs(map[string]any{"searchText": "x", "searchScope": "everywhere"})
		require.ErrorContains(t, err, `parameter validation failed: "searchScope" "everywhere" is not supported; use one of: body, attribute, resource, all`)
	})
}
