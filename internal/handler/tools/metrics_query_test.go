package tools

import (
	"encoding/json"
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

// TestParseMetricMetadataFromResponse_PartialFieldDrift pins that when a
// metric row matches by name and carries a type but is MISSING its companion
// fields (temporality / isMonotonic), the decoded result is DETECTABLY
// incomplete (drift flags set, empty temporality) instead of silently decoding
// a wrong non-monotonic default as authoritative. The WARN itself fires in
// fetchMetricMetadata; here we assert the parse result the WARN keys off.
func TestParseMetricMetadataFromResponse_PartialFieldDrift(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		wantType        string
		wantTemporality string
		wantTempMissing bool
		wantMonotonic   bool
		wantMonoMissing bool
	}{
		{
			name:            "sum missing temporality and isMonotonic (drift)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum"}]}}`,
			wantType:        "sum",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: true,
		},
		{
			name:            "sum with full metadata (no drift)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum","temporality":"Cumulative","isMonotonic":true}]}}`,
			wantType:        "sum",
			wantTemporality: "Cumulative",
			wantTempMissing: false,
			wantMonotonic:   true,
			wantMonoMissing: false,
		},
		{
			name:            "sum explicit isMonotonic false is NOT drift",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum","temporality":"Delta","isMonotonic":false}]}}`,
			wantType:        "sum",
			wantTemporality: "Delta",
			wantTempMissing: false,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "explicit empty temporality is present, NOT drift",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"system.cpu.utilization","type":"gauge","temporality":""}]}}`,
			wantType:        "gauge",
			wantTemporality: "",
			wantTempMissing: false,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "gauge omitting isMonotonic is NOT drift (only temporality flagged)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"system.cpu.utilization","type":"gauge"}]}}`,
			wantType:        "gauge",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "array format, sum drift",
			body:            `[{"metricName":"http.server.duration","type":"sum"}]`,
			wantType:        "sum",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, err := parseMetricMetadataFromResponse(json.RawMessage(tc.body), "http.server.duration")
			if tc.wantType == "gauge" {
				meta, err = parseMetricMetadataFromResponse(json.RawMessage(tc.body), "system.cpu.utilization")
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta == nil {
				t.Fatalf("expected a matched metric, got nil")
			}
			if meta.MetricType != tc.wantType {
				t.Fatalf("MetricType = %q, want %q", meta.MetricType, tc.wantType)
			}
			if meta.Temporality != tc.wantTemporality {
				t.Fatalf("Temporality = %q, want %q", meta.Temporality, tc.wantTemporality)
			}
			if meta.TemporalityMissing != tc.wantTempMissing {
				t.Fatalf("TemporalityMissing = %v, want %v", meta.TemporalityMissing, tc.wantTempMissing)
			}
			if meta.IsMonotonic != tc.wantMonotonic {
				t.Fatalf("IsMonotonic = %v, want %v", meta.IsMonotonic, tc.wantMonotonic)
			}
			if meta.IsMonotonicMissing != tc.wantMonoMissing {
				t.Fatalf("IsMonotonicMissing = %v, want %v", meta.IsMonotonicMissing, tc.wantMonoMissing)
			}
		})
	}
}

// The metrics search matches substrings, so a longer neighbor's row must never
// stand in for the requested name and lend it the wrong type.
func TestParseMetricMetadataFromResponse_SubstringMatchIsNotTheMetric(t *testing.T) {
	body := `{"status":"success","data":{"metrics":[{"metricName":"http_requests_total","type":"sum","temporality":"cumulative","isMonotonic":true}]}}`
	meta, err := parseMetricMetadataFromResponse(json.RawMessage(body), "http_requests")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("a substring match was used as the metric: %+v", meta)
	}
}

// SigNoz always sends isMonotonic today; if a release stops sending it, the
// result must say the value was assumed rather than report it as fetched.
func TestQueryMetrics_ReportsIsMonotonicSigNozDidNotReturn(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		wantNote string
	}{
		{
			name:     "sum without isMonotonic",
			metadata: `{"type":"sum","temporality":"cumulative","unit":"","description":""}`,
			wantNote: "isMonotonic: unknown (not returned by metadata; assumed false)",
		},
		{
			name:     "sum with isMonotonic false",
			metadata: `{"type":"sum","temporality":"cumulative","isMonotonic":false,"unit":"","description":""}`,
			wantNote: "isMonotonic: false (auto-fetched)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v2/metrics/metadata":
					_, _ = w.Write([]byte(`{"status":"success","data":` + tc.metadata + `}`))
				case "/api/v5/query_range":
					_, _ = w.Write([]byte(`{"status":"success","data":{"type":"scalar","data":{"results":[]}}}`))
				default:
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv(config.SignozURL, strings.Replace(backend.URL, "127.0.0.1", "localhost", 1))
			t.Setenv(config.SignozApiKey, "test-key")
			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			h := NewHandler(logpkg.New("error"), cfg)
			ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), cfg.URL), cfg.APIKey)

			result, err := h.handleQueryMetrics(ctx, makeToolRequest("signoz_query_metrics", map[string]any{
				"metricName": "queue.depth.changes", "timeRange": "1h", "requestType": "scalar",
			}))
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			var text strings.Builder
			for _, content := range result.Content {
				if block, ok := mcp.AsTextContent(content); ok {
					text.WriteString(block.Text + "\n")
				}
			}
			require.Contains(t, text.String(), tc.wantNote)
		})
	}
}
