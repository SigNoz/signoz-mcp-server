package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestCheckMetricUsage_Route404DoesNotSilentlySucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found\n"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := newBufferedLogger(&buf, -4)
	c := NewClient(logger, srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := c.CheckMetricUsage(context.Background(), []string{"system.cpu.time"})
	assert.Error(t, err, "route-level 404 must propagate as error, not be swallowed")
}

func TestCheckMetricUsage_DeduplicatesInputNames(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
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
	assert.Equal(t, 2, callCount)
}
