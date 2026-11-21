package tools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// signoz_check_metric_usage returns a map keyed by metric name, so its notes
// share the top level with the metrics. Each result must still match the
// tool's advertised output schema, and a metric named "notes" must keep its
// entry; the notice then falls back to a text block with a WARN.
func TestMetricUsageResultWithNotesMatchesItsOutputSchema(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/metrics/dashboards":
			_, _ = w.Write([]byte(`{"status":"success","data":{"dashboards":[{"dashboardId":"d1","dashboardName":"Checkout"}]}}`))
		case "/api/v2/metrics/alerts":
			_, _ = w.Write([]byte(`{"status":"success","data":{"alerts":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	var logs bytes.Buffer
	h := &Handler{
		logger:         slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "k", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	output, err := compileToolSchema("signoz_check_metric_usage", "output", outputSchemaJSON(listTestTools(t, server)["signoz_check_metric_usage"].Tool))
	require.NoError(t, err)
	usageEntry := map[string]any{"dashboards": []any{"Checkout"}, "alerts": []any{}}

	t.Run("stray argument", func(t *testing.T) {
		result := callRegisteredTool(t, server, "signoz_check_metric_usage", `{"metricNames":["http.requests"],"stray":"x"}`)

		require.False(t, result.IsError, allTextBlocks(result))
		notes := structuredNotes(t, result)
		require.Len(t, notes, 1)
		require.Contains(t, notes[0], `"stray"`, "the ignored-argument notice must name the argument")
		structured, ok := result.StructuredContent.(map[string]any)
		require.True(t, ok, "StructuredContent must be an object: %#v", result.StructuredContent)
		require.Equal(t, usageEntry, structured["http.requests"])
		require.Equal(t, notes, trailingTextBlocks(result), "clients that read content keep the notice as its own block")
		require.NoError(t, validateSchemaValue(output.validator, result.StructuredContent, false), "a result with notes must match the advertised output schema")
	})

	t.Run("metric named notes", func(t *testing.T) {
		result := callRegisteredTool(t, server, "signoz_check_metric_usage", `{"metricNames":["notes"]}`)

		require.False(t, result.IsError, allTextBlocks(result))
		require.Equal(t, map[string]any{"notes": usageEntry}, result.StructuredContent)
		require.Len(t, result.Content, 1)
		require.NoError(t, validateSchemaValue(output.validator, result.StructuredContent, false), "a metric named notes must match the advertised output schema")
	})

	t.Run("metric named notes and a stray argument", func(t *testing.T) {
		logs.Reset()
		result := callRegisteredTool(t, server, "signoz_check_metric_usage", `{"metricNames":["notes"],"stray":"x"}`)

		require.False(t, result.IsError, allTextBlocks(result))
		require.Equal(t, map[string]any{"notes": usageEntry}, result.StructuredContent, "the metric's entry must not be overwritten")
		require.True(t, sameJSONValue(t, []byte(textContent(t, result)), result.StructuredContent), "block 0 must stay the untouched payload: %s", textContent(t, result))
		trailing := trailingTextBlocks(result)
		require.Len(t, trailing, 1, "with nowhere else to go, the notice stays a text block")
		require.True(t, strings.HasPrefix(trailing[0], inputValidationNoticePrefix), trailing[0])
		require.Contains(t, trailing[0], `"stray"`)
		require.NoError(t, validateSchemaValue(output.validator, result.StructuredContent, false))
		require.True(t, warnedAboutField(t, logs.String(), "notes"), "the fallback must log a WARN naming the colliding field; logs: %s", logs.String())
	})
}

// warnedAboutField reports whether a WARN record's error names field in quotes.
func warnedAboutField(t *testing.T, logs, field string) bool {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		var record struct {
			Level string `json:"level"`
			Error string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Level == "WARN" && strings.Contains(record.Error, `"`+field+`"`) {
			return true
		}
	}
	return false
}
