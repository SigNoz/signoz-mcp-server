package tools

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
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

// A caller who sends metricType gets the temporality and monotonicity they
// left out from the metadata, and keeps every value they sent; an isMonotonic
// that is not a boolean is rejected before any lookup. A lookup that fails
// leaves the query running on the caller's values, logged and noted.
func TestQueryMetrics_FillsOnlyTheMetadataACallerLeftOut(t *testing.T) {
	const cumulativeCounter = `{"type":"sum","temporality":"cumulative","isMonotonic":true}`
	type aggregation struct {
		Temporality     string `json:"temporality"`
		TimeAggregation string `json:"timeAggregation"`
	}
	tests := []struct {
		name           string
		metadataStatus int
		args           map[string]any
		wantLookups    int
		want           []string
		wantSent       map[string]aggregation
		wantWarn       string
		wantCode       string // the call fails with this code and sends no query
	}{
		{
			name: "metricType alone on a cumulative counter", metadataStatus: http.StatusOK,
			args:        map[string]any{"metricType": "sum"},
			wantLookups: 1,
			want:        []string{"metricType: sum (caller-provided)", "temporality: cumulative (auto-fetched)", "isMonotonic: true (auto-fetched)", "timeAggregation: rate"},
			wantSent:    map[string]aggregation{"A": {"", "rate"}},
		},
		{
			name: "the caller's monotonicity wins", metadataStatus: http.StatusOK,
			args:        map[string]any{"metricType": "sum", "isMonotonic": "false"},
			wantLookups: 1,
			want:        []string{"isMonotonic: false (caller-provided)", "temporality: cumulative (auto-fetched)", "timeAggregation: avg"},
			wantSent:    map[string]aggregation{"A": {"", "avg"}},
		},
		{
			name: "the caller's monotonicity wins when the type is fetched too", metadataStatus: http.StatusOK,
			args:        map[string]any{"isMonotonic": false},
			wantLookups: 1,
			want:        []string{"metricType: sum (auto-fetched)", "isMonotonic: false (caller-provided)", "timeAggregation: avg"},
			wantSent:    map[string]aggregation{"A": {"cumulative", "avg"}},
		},
		{
			name: "an isMonotonic that is not a boolean is rejected", metadataStatus: http.StatusOK,
			args:     map[string]any{"metricType": "sum", "isMonotonic": "yes"},
			wantCode: CodeValidationFailed,
			want:     []string{`"isMonotonic"`, `"yes"`},
		},
		{
			name: "a metadata lookup the caller may not make", metadataStatus: http.StatusForbidden,
			args:        map[string]any{"metricType": "sum"},
			wantLookups: 1,
			wantCode:    CodePermissionDenied,
		},
		{
			name: "every value sent needs no lookup", metadataStatus: http.StatusInternalServerError,
			args:     map[string]any{"metricType": "sum", "isMonotonic": true, "temporality": "delta"},
			want:     []string{"temporality: delta (caller-provided)", "isMonotonic: true (caller-provided)", "timeAggregation: rate"},
			wantSent: map[string]aggregation{"A": {"delta", "rate"}},
		},
		{
			name: "a sent false is the caller's, so it needs no lookup either", metadataStatus: http.StatusInternalServerError,
			args:     map[string]any{"metricType": "sum", "isMonotonic": false, "temporality": "cumulative"},
			want:     []string{"temporality: cumulative (caller-provided)", "isMonotonic: false (caller-provided)", "timeAggregation: avg"},
			wantSent: map[string]aggregation{"A": {"cumulative", "avg"}},
		},
		{
			name: "a failed lookup keeps the query running", metadataStatus: http.StatusInternalServerError,
			args:        map[string]any{"metricType": "sum"},
			wantLookups: 1,
			want:        []string{"temporality: unknown (metadata lookup failed; SigNoz resolves it)", "isMonotonic: unknown (metadata lookup failed; assumed false)", "timeAggregation: avg"},
			wantSent:    map[string]aggregation{"A": {"", "avg"}},
			wantWarn:    "Metric metadata lookup failed",
		},
		{
			name: "a metric missing from the metadata", metadataStatus: http.StatusNotFound,
			args:        map[string]any{"metricType": "sum", "isMonotonic": true},
			wantLookups: 1,
			want:        []string{"temporality: unknown (metric not found in metadata; SigNoz resolves it)", "timeAggregation: rate"},
			wantSent:    map[string]aggregation{"A": {"", "rate"}},
			wantWarn:    "Metric not found in metadata",
		},
		{
			name: "a formula sub-query with metricType alone", metadataStatus: http.StatusOK,
			args: map[string]any{
				"metricType": "sum", "isMonotonic": true, "temporality": "cumulative", "formula": "A / B",
				"formulaQueries": []any{map[string]any{"name": "B", "metricName": "http.requests", "metricType": "sum"}},
			},
			wantLookups: 1,
			want:        []string{"query B: temporality: cumulative (auto-fetched)", "query B: isMonotonic: true (auto-fetched)", "query B: timeAggregation: rate"},
			wantSent:    map[string]aggregation{"A": {"cumulative", "rate"}, "B": {"", "rate"}},
		},
		{
			name: "a formula sub-query's monotonicity wins when its type is fetched", metadataStatus: http.StatusOK,
			args: map[string]any{
				"metricType": "sum", "isMonotonic": true, "temporality": "cumulative", "formula": "A / B",
				"formulaQueries": []any{map[string]any{"name": "B", "metricName": "http.requests", "isMonotonic": false}},
			},
			wantLookups: 1,
			want:        []string{"query B: timeAggregation: avg"},
			wantSent:    map[string]aggregation{"A": {"cumulative", "rate"}, "B": {"", "avg"}},
		},
		{
			name: "a formula sub-query that sends false", metadataStatus: http.StatusInternalServerError,
			args: map[string]any{
				"metricType": "sum", "isMonotonic": true, "temporality": "cumulative", "formula": "A / B",
				"formulaQueries": []any{map[string]any{"name": "B", "metricName": "http.requests", "metricType": "sum", "isMonotonic": false, "temporality": "cumulative"}},
			},
			want:     []string{"query B: isMonotonic: false (caller-provided)", "query B: timeAggregation: avg"},
			wantSent: map[string]aggregation{"A": {"cumulative", "rate"}, "B": {"cumulative", "avg"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			sent := map[string]aggregation{}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v2/metrics/metadata":
					lookups++
					w.WriteHeader(tc.metadataStatus)
					_, _ = w.Write([]byte(`{"status":"success","data":` + cumulativeCounter + `}`))
				case "/api/v5/query_range":
					var request struct {
						CompositeQuery struct {
							Queries []struct {
								Spec struct {
									Name         string        `json:"name"`
									Aggregations []aggregation `json:"aggregations"`
								} `json:"spec"`
							} `json:"queries"`
						} `json:"compositeQuery"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					for _, query := range request.CompositeQuery.Queries {
						if len(query.Spec.Aggregations) > 0 {
							sent[query.Spec.Name] = query.Spec.Aggregations[0]
						}
					}
					_, _ = w.Write([]byte(`{"status":"success","data":{"type":"time_series","data":{"results":[]}}}`))
				default:
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(backend.Close)
			var logs bytes.Buffer
			h := &Handler{
				logger:         slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
				clientOverride: signozclient.NewClient(logpkg.New("error"), backend.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
			}
			args := map[string]any{"metricName": "http.requests", "timeRange": "1h"}
			for key, value := range tc.args {
				args[key] = value
			}

			result, err := h.handleQueryMetrics(testCtx(), makeToolRequest("signoz_query_metrics", args))
			require.NoError(t, err)
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, resultCode(t, result))
				for _, text := range tc.want {
					require.Contains(t, textContent(t, result), text)
				}
				require.Equal(t, tc.wantLookups, lookups, "metadata lookups")
				require.Empty(t, sent, "no query is sent")
				return
			}
			notes := resultNotes(t, result)
			for _, text := range tc.want {
				require.Contains(t, notes, text)
			}
			require.Equal(t, tc.wantLookups, lookups, "metadata lookups")
			require.Equal(t, tc.wantSent, sent, "aggregations sent upstream")
			if tc.wantWarn == "" {
				require.Empty(t, logs.String())
			} else {
				require.Contains(t, logs.String(), tc.wantWarn)
				require.NotContains(t, logs.String(), "test-api-key")
			}
		})
	}
}
