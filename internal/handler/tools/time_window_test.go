package tools

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestTimeWindowedTools_SendTheResolvedWindowUpstream pins that every
// time-windowed tool accepts RFC 3339 bounds and sends SigNoz the same window in
// the unit its route takes: milliseconds, or nanoseconds for the service routes.
func TestTimeWindowedTools_SendTheResolvedWindowUpstream(t *testing.T) {
	const (
		window     = `"start":"2026-11-19T14:30:00+05:30","end":"2026-11-19T10:00:00Z"`
		startMs    = "1795078800000"
		endMs      = "1795082400000"
		startNanos = "1795078800000000000"
		endNanos   = "1795082400000000000"
	)

	var mu sync.Mutex
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, r.URL.RawQuery+" "+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	t.Cleanup(upstream.Close)
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)

	tests := []struct {
		tool       string
		arguments  string
		start, end string
	}{
		{"signoz_search_logs", `{` + window + `}`, startMs, endMs},
		{"signoz_aggregate_logs", `{"aggregations":"count()",` + window + `}`, startMs, endMs},
		{"signoz_search_traces", `{` + window + `}`, startMs, endMs},
		{"signoz_aggregate_traces", `{"aggregations":"count()",` + window + `}`, startMs, endMs},
		{"signoz_query_metrics", `{"metricName":"m","metricType":"gauge",` + window + `}`, startMs, endMs},
		{"signoz_list_metrics", `{` + window + `}`, startMs, endMs},
		{"signoz_get_top_metrics", `{` + window + `}`, startMs, endMs},
		{"signoz_check_metric_cardinality", `{"metricName":"m",` + window + `}`, startMs, endMs},
		{"signoz_get_alert_history", `{"id":"0196634d-5d66-75c4-b778-e317f49dab7a",` + window + `}`, startMs, endMs},
		{"signoz_list_services", `{` + window + `}`, startNanos, endNanos},
		{"signoz_get_service_top_operations", `{"service":"frontend",` + window + `}`, startNanos, endNanos},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			mu.Lock()
			requests = nil
			mu.Unlock()

			callRegisteredTool(t, server, tt.tool, tt.arguments)

			mu.Lock()
			defer mu.Unlock()
			for _, request := range requests {
				// The millisecond values are prefixes of the nanosecond ones, so a
				// millisecond route must also not carry the nanosecond window.
				wrongUnit := tt.start == startMs && strings.Contains(request, startNanos)
				if strings.Contains(request, tt.start) && strings.Contains(request, tt.end) && !wrongUnit {
					return
				}
			}
			t.Fatalf("no upstream request carried start %s and end %s; requests: %q", tt.start, tt.end, requests)
		})
	}
}
