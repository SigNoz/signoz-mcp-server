package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- isMetricNotFound404 ---

func TestIsMetricNotFound404(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "non-404 error", err: fmt.Errorf("unexpected status 500: internal error"), want: false},
		{name: "route-level 404 (plain text)", err: fmt.Errorf("unexpected status 404: 404 page not found\n"), want: false},
		{name: "metric-level 404 (JSON body)", err: fmt.Errorf(`unexpected status 404: {"status":"error","error":"metric not found"}`), want: true},
		{name: "metric-level 404 with leading brace", err: fmt.Errorf(`unexpected status 404: {"id":"abc"}`), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isMetricNotFound404(tc.err))
		})
	}
}

// --- parseDashboardNames ---

func TestParseDashboardNames_Deduplicates(t *testing.T) {
	body := `{"status":"success","data":{"dashboards":[
		{"dashboardName":"Host Metrics","dashboardId":"1","widgetId":"w1","widgetName":"CPU"},
		{"dashboardName":"Host Metrics","dashboardId":"1","widgetId":"w2","widgetName":"Memory"},
		{"dashboardName":"K8s Overview","dashboardId":"2","widgetId":"w3","widgetName":"Pods"}
	]}}`
	names, err := parseDashboardNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{"Host Metrics", "K8s Overview"}, names)
}

func TestParseDashboardNames_EmptyArray(t *testing.T) {
	body := `{"status":"success","data":{"dashboards":[]}}`
	names, err := parseDashboardNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{}, names)
}

func TestParseDashboardNames_MalformedJSON(t *testing.T) {
	_, err := parseDashboardNames([]byte(`not json`))
	assert.Error(t, err)
}

// --- parseAlertNames ---

func TestParseAlertNames_ReturnsNames(t *testing.T) {
	body := `{"status":"success","data":{"alerts":[
		{"alertName":"High CPU","alertId":"a1"},
		{"alertName":"Low Memory","alertId":"a2"}
	]}}`
	names, err := parseAlertNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{"High CPU", "Low Memory"}, names)
}

func TestParseAlertNames_EmptyArray(t *testing.T) {
	body := `{"status":"success","data":{"alerts":[]}}`
	names, err := parseAlertNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{}, names)
}

// --- Contract check WARN ---

func TestCheckMetricUsage_ContractCheckDashboards(t *testing.T) {
	cases := []struct {
		name         string
		dashBody     string
		alertBody    string
		wantWarn     string
	}{
		{
			name:      "valid dashboard shape — no warn",
			dashBody:  `{"status":"success","data":{"dashboards":[]}}`,
			alertBody: `{"status":"success","data":{"alerts":[]}}`,
		},
		{
			name:      "dashboard key renamed — warn",
			dashBody:  `{"status":"success","data":{"items":[]}}`,
			alertBody: `{"status":"success","data":{"alerts":[]}}`,
			wantWarn:  "dashboards endpoint",
		},
		{
			name:      "dashboard data absent — warn",
			dashBody:  `{"status":"success"}`,
			alertBody: `{"status":"success","data":{"alerts":[]}}`,
			wantWarn:  "dashboards endpoint",
		},
		{
			name:      "alert key renamed — warn",
			dashBody:  `{"status":"success","data":{"dashboards":[]}}`,
			alertBody: `{"status":"success","data":{"items":[]}}`,
			wantWarn:  "alerts endpoint",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call++
				w.WriteHeader(http.StatusOK)
				if call == 1 {
					_, _ = w.Write([]byte(tc.dashBody))
				} else {
					_, _ = w.Write([]byte(tc.alertBody))
				}
			}))
			defer srv.Close()

			var buf bytes.Buffer
			logger := newBufferedLogger(&buf, -4)
			c := NewClient(logger, srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			result, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
			require.NoError(t, err)
			assert.NotNil(t, result)

			logged := buf.String()
			if tc.wantWarn != "" {
				assert.Contains(t, logged, tc.wantWarn, "expected WARN for contract violation")
			} else {
				assert.NotContains(t, logged, "Unexpected response shape", "expected no WARN for valid shape")
			}
		})
	}
}

// --- CheckMetricUsage integration ---

func TestCheckMetricUsage_Route404StoredAsPerMetricError(t *testing.T) {
	// Route-level 404 (plain text) must not be silently swallowed as empty usage.
	// With per-metric error storage it surfaces in MetricUsage.Error, not as a
	// batch-level error return.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found\n"))
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
	require.NoError(t, err)
	assert.NotEmpty(t, result["system.cpu.time"].Error, "route-level 404 must surface as per-metric error")
}

func TestCheckMetricUsage_5xxStoredAsPerMetricError(t *testing.T) {
	// A transient 5xx on one metric must not discard results for the rest.
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
	require.NoError(t, err, "5xx must not propagate as batch-level error")
	assert.NotEmpty(t, result["system.cpu.time"].Error, "5xx must surface as per-metric error")
}

func TestCheckMetricUsage_PartialFailureRetainsOtherResults(t *testing.T) {
	// When one metric's request fails, other metrics' results must still be returned.
	// Failure is keyed on URL path (not call order) so the test is goroutine-order independent.
	okBody, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}, "alerts": []any{}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "metric.fail") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"oops"}`))
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(okBody)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(context.Background(), []string{"metric.fail", "metric.ok"})
	require.NoError(t, err)
	assert.NotEmpty(t, result["metric.fail"].Error, "failed metric must have error set")
	assert.Empty(t, result["metric.ok"].Error, "successful metric must have no error")
}

func TestCheckMetricUsage_SoftCapRejectsOversizedBatch(t *testing.T) {
	// Batches exceeding MaxMetricUsageNames must be rejected without hitting the backend.
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	names := make([]string, MaxMetricUsageNames+1)
	for i := range names {
		names[i] = fmt.Sprintf("metric.%d", i)
	}

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	_, err := c.CheckMetricUsage(context.Background(), names)
	require.Error(t, err, "oversized batch must be rejected")
	assert.Contains(t, err.Error(), "too many metric names")
	assert.Equal(t, int32(0), callCount.Load(), "no backend calls must be made for oversized batch")
}

func TestCheckMetricUsage_OverallDeadlineReturnsPartialResults(t *testing.T) {
	// When the overall deadline fires, already-completed metrics must be returned
	// and in-flight metrics must surface with an error — not silently dropped.
	okBody, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}, "alerts": []any{}}})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "metric.slow") {
			// Block until the request context is cancelled (simulates a slow backend).
			<-r.Context().Done()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	// Very short timeout so the test does not block.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(ctx, []string{"metric.fast", "metric.slow"})
	require.NoError(t, err, "deadline must not propagate as a batch-level error")
	assert.Empty(t, result["metric.fast"].Error, "fast metric must succeed")
	assert.NotEmpty(t, result["metric.slow"].Error, "slow metric must surface deadline as per-metric error")
}

func TestCheckMetricUsage_DeduplicatesInputNames(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		if r.URL.Path[len(r.URL.Path)-len("dashboards"):] == "dashboards" {
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}}})
			_, _ = w.Write(body)
		} else {
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"alerts": []any{}}})
			_, _ = w.Write(body)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	_, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time", "system.cpu.time", ""})
	require.NoError(t, err)
	// Deduplicated to 1 unique name, blank filtered — so 2 API calls (dashboards + alerts)
	assert.Equal(t, int32(2), callCount.Load())
}
