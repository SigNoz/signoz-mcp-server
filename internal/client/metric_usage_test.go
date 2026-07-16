package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsMetricNotFound404(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "non-status error", err: fmt.Errorf("unexpected status 404: generic string"), want: false},
		{name: "non-404 status", err: &HTTPStatusError{StatusCode: 500, Body: `{"status":"error"}`}, want: false},
		{name: "route-level 404 (plain text)", err: &HTTPStatusError{StatusCode: 404, Body: "404 page not found\n"}, want: false},
		{
			name: "metric-level 404 (live SigNoz envelope)",
			err: &HTTPStatusError{
				StatusCode: 404,
				Body:       `{"status":"error","error":{"code":"not_found","errors":[],"message":"metric not found: \"mcp.pr205.nonexistent\"","suggestions":[],"type":"not-found"}}`,
			},
			want: true,
		},
		{name: "old loose SigNoz-shaped 404", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"error","error":"metric not found"}`}, want: false},
		{name: "proxy JSON 404 without status field", err: &HTTPStatusError{StatusCode: 404, Body: `{"error":"not found"}`}, want: false},
		{name: "proxy JSON 404 with wrong status", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"fail","error":"not found"}`}, want: false},
		{name: "proxy JSON 404 with generic error object", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"error","error":{"code":"not_found","type":"not-found","message":"route not found"}}`}, want: false},
		{name: "metric 404 with wrong code", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"error","error":{"code":"unknown","type":"not-found","message":"metric not found: \"x\""}}`}, want: false},
		{name: "metric 404 with wrong type", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"error","error":{"code":"not_found","type":"not_found","message":"metric not found: \"x\""}}`}, want: false},
		{name: "metric 404 without message prefix", err: &HTTPStatusError{StatusCode: 404, Body: `{"status":"error","error":{"code":"not_found","type":"not-found","message":"metric missing: \"x\""}}`}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isMetricNotFound404(tc.err))
		})
	}
}

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

func TestParseDashboardNames_AllowsEmptyDashboardName(t *testing.T) {
	body := `{"status":"success","data":{"dashboards":[
		{"dashboardName":"","dashboardId":"1","widgetId":"w1","widgetName":"CPU"}
	]}}`
	names, err := parseDashboardNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{""}, names)
}

func TestParseDashboardNames_MissingDashboardName(t *testing.T) {
	body := `{"status":"success","data":{"dashboards":[
		{"dashboardId":"1","widgetId":"w1","widgetName":"CPU"}
	]}}`
	_, err := parseDashboardNames([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dashboardName")
}

func TestParseDashboardNames_MalformedJSON(t *testing.T) {
	_, err := parseDashboardNames([]byte(`not json`))
	assert.Error(t, err)
}

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

func TestParseAlertNames_AllowsEmptyAlertName(t *testing.T) {
	body := `{"status":"success","data":{"alerts":[
		{"alertName":"","alertId":"a1"}
	]}}`
	names, err := parseAlertNames([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{""}, names)
}

func TestParseAlertNames_MissingAlertName(t *testing.T) {
	body := `{"status":"success","data":{"alerts":[
		{"alertId":"a1"}
	]}}`
	_, err := parseAlertNames([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alertName")
}

func TestCheckMetricUsage_ContractCheckDashboards(t *testing.T) {
	cases := []struct {
		name      string
		dashBody  string
		alertBody string
		wantWarn  string
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

func TestCheckMetricUsage_MissingDashboardNameStoredAsPerMetricError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/api/v2/metrics/dashboards":
			_, _ = w.Write([]byte(`{"status":"success","data":{"dashboards":[{"dashboardId":"d1","widgetId":"w1","widgetName":"CPU"}]}}`))
		case "/api/v2/metrics/alerts":
			_, _ = w.Write([]byte(`{"status":"success","data":{"alerts":[]}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
	require.NoError(t, err)
	assert.Contains(t, result["system.cpu.time"].Error, "dashboardName")
}

func TestCheckMetricUsage_Route404StoredAsPerMetricError(t *testing.T) {
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

func TestCheckMetricUsage_AuthzFailurePropagates(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unauthenticated","message":"invalid credentials","type":"unauthorized"}}`))
			}))
			defer srv.Close()

			c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
			result, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
			require.Error(t, err, "authz failures must propagate as batch-level errors")
			assert.Nil(t, result)

			var statusErr *HTTPStatusError
			require.True(t, errors.As(err, &statusErr), "expected typed HTTP status error")
			assert.Equal(t, tc.statusCode, statusErr.StatusCode)
		})
	}
}

func TestCheckMetricUsage_PartialFailureRetainsOtherResults(t *testing.T) {
	okBody, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}, "alerts": []any{}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("metricName") == "metric.fail" {
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

func TestCheckMetricUsage_OneSideFailurePreservesKnownUsage(t *testing.T) {
	dashBody := `{"status":"success","data":{"dashboards":[{"dashboardName":"Host Metrics","dashboardId":"d1"}]}}`
	alertBody := `{"status":"success","data":{"alerts":[{"alertName":"High CPU","alertId":"a1"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/metrics/dashboards":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dashBody))
		case "/api/v2/metrics/alerts":
			if r.URL.Query().Get("metricName") == "metric.partial" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"oops"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(alertBody))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := c.CheckMetricUsage(context.Background(), []string{"metric.partial"})
	require.NoError(t, err)
	usage := result["metric.partial"]
	assert.Equal(t, []string{"Host Metrics"}, usage.Dashboards, "known dashboard usage must be preserved")
	assert.Equal(t, []string{}, usage.Alerts)
	assert.Contains(t, usage.Error, "alerts lookup")
}

func TestCheckMetricUsage_SoftCapRejectsOversizedBatch(t *testing.T) {
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
	okBody, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}, "alerts": []any{}}})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("metricName") == "metric.slow" {
			<-r.Context().Done()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

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
		assert.Equal(t, "system.cpu/time", r.URL.Query().Get("metricName"))
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/api/v2/metrics/dashboards":
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"dashboards": []any{}}})
			_, _ = w.Write(body)
		case "/api/v2/metrics/alerts":
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"alerts": []any{}}})
			_, _ = w.Write(body)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	_, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu/time", "system.cpu/time", ""})
	require.NoError(t, err)
	assert.Equal(t, int32(2), callCount.Load())
}
