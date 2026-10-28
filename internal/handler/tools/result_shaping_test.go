package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// upstreamSpecs runs a query tool against a fake SigNoz and returns the result
// and the specs of the query_range body it sent, keyed by query name.
func upstreamSpecs(t *testing.T, tool string, args map[string]any) (string, map[string]map[string]any) {
	t.Helper()
	var body struct {
		CompositeQuery struct {
			Queries []struct {
				Spec map[string]any `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[],"data":[]}]}}}`))
	}))
	t.Cleanup(backend.Close)
	t.Setenv(config.SignozURL, strings.Replace(backend.URL, "127.0.0.1", "localhost", 1))
	t.Setenv(config.SignozApiKey, "test-key")
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	h := NewHandler(logpkg.New("error"), cfg)
	ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), cfg.URL), cfg.APIKey)

	handle := map[string]mcp.ToolHandlerFunc{
		"signoz_aggregate_logs": h.handleAggregateLogs,
		"signoz_query_metrics":  h.handleQueryMetrics,
	}[tool]
	result, err := handle(ctx, makeToolRequest(tool, args))
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := mcp.AsTextContent(content); ok {
			text.WriteString(block.Text + "\n")
		}
	}
	specs := map[string]map[string]any{}
	for _, query := range body.CompositeQuery.Queries {
		specs[query.Spec["name"].(string)] = query.Spec
	}
	return text.String(), specs
}

func requireSpecJSON(t *testing.T, want string, got any) {
	t.Helper()
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, want, string(raw))
}

func groupByNames(spec map[string]any) []string {
	var names []string
	for _, key := range spec["groupBy"].([]any) {
		names = append(names, key.(map[string]any)["name"].(string))
	}
	return names
}

func TestQueryTools_SendResultShapingUpstream(t *testing.T) {
	t.Run("aggregate_logs forwards several aggregations, having, and functions", func(t *testing.T) {
		_, specs := upstreamSpecs(t, "signoz_aggregate_logs", map[string]any{
			"aggregations": []any{"count()", "count_distinct(severity_text)"},
			"groupBy":      "service.name",
			"having":       " count() > 1 ",
			"functions":    `[{"name": "cutOffMin", "args": [{"value": 10}]}]`,
			"timeRange":    "1h",
		})
		spec := specs["A"]
		requireSpecJSON(t, `[{"expression":"count()"},{"expression":"count_distinct(severity_text)"}]`, spec["aggregations"])
		requireSpecJSON(t, `{"expression":"count() > 1"}`, spec["having"])
		requireSpecJSON(t, `[{"name":"cutOffMin","args":[{"value":10}]}]`, spec["functions"])
		requireSpecJSON(t, `[{"key":{"name":"count()"},"direction":"desc"}]`, spec["order"])
	})

	t.Run("aggregate_logs reads JSON-string aggregations and an array groupBy", func(t *testing.T) {
		_, specs := upstreamSpecs(t, "signoz_aggregate_logs", map[string]any{
			"aggregations": `[" avg(duration) ", ""]`,
			"groupBy":      []any{"service.name", " severity_text "},
			"timeRange":    "1h",
		})
		spec := specs["A"]
		requireSpecJSON(t, `[{"expression":"avg(duration)"}]`, spec["aggregations"])
		requireSpecJSON(t, `[{"key":{"name":"avg(duration)"},"direction":"desc"}]`, spec["order"])
		require.Equal(t, []string{"service.name", "severity_text"}, groupByNames(spec))
	})

	t.Run("query_metrics bounds the formula result and reads typed formulaQueries", func(t *testing.T) {
		text, specs := upstreamSpecs(t, "signoz_query_metrics", map[string]any{
			"metricName":  "http_requests_total",
			"metricType":  "gauge",
			"limit":       "50000",
			"orderBy":     "__result asc",
			"having":      "__result > 1",
			"formula":     "A / B",
			"timeRange":   "1h",
			"requestType": "time_series",
			"formulaQueries": []any{map[string]any{
				"name": "B", "metricName": "http_requests_total", "metricType": "gauge", "groupBy": "service.name, host.name",
			}},
		})
		require.Len(t, specs, 3)
		var formula map[string]any
		for name, spec := range specs {
			if name != "A" && name != "B" {
				formula = spec
			}
		}
		require.Equal(t, float64(MaxRawResultLimit), formula["limit"])
		requireSpecJSON(t, `[{"key":{"name":"__result"},"direction":"asc"}]`, formula["order"])
		requireSpecJSON(t, `{"expression":"__result > 1"}`, specs["A"]["having"])
		require.Equal(t, []string{"service.name", "host.name"}, groupByNames(specs["B"]))
		require.Contains(t, text, "limit clamped to")
	})
}
