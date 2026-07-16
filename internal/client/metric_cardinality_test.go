package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMetricCardinality_ContractCheck(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantWarn bool
		wantErr  bool
	}{
		{
			name:     "valid shape — no warn",
			body:     `{"status":"success","data":{"attributes":[{"key":"k8s.pod.uid","valueCount":42,"values":["uid-1"]}],"totalKeys":1}}`,
			wantWarn: false,
		},
		{
			name:     "data field absent — warn",
			body:     `{"status":"success"}`,
			wantWarn: true,
		},
		{
			name:     "data present but attributes absent — warn",
			body:     `{"status":"success","data":{"items":[{"key":"k8s.pod.uid","valueCount":42}]}}`,
			wantWarn: true,
		},
		{
			name:     "data present, attributes null — warn",
			body:     `{"status":"success","data":{"attributes":null}}`,
			wantWarn: true,
		},
		{
			name:     "empty attributes array — no warn (valid zero-label metric)",
			body:     `{"status":"success","data":{"attributes":[],"totalKeys":0}}`,
			wantWarn: false,
		},
		{
			name:     "malformed JSON — warn",
			body:     `not json`,
			wantWarn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotMetricName, gotStart, gotEnd string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotMetricName = r.URL.Query().Get("metricName")
				gotStart = r.URL.Query().Get("start")
				gotEnd = r.URL.Query().Get("end")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			var buf bytes.Buffer
			logger := newBufferedLogger(&buf, -4) // DEBUG level captures WARN
			c := NewClient(logger, srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			result, err := c.GetMetricCardinality(context.Background(), "k8s.pod.uid", 0, 1000)

			// Pin the upstream contract: metricName is a query parameter on
			// /api/v2/metrics/attributes, NOT a path segment. SigNoz binds it from
			// the query string (see #208 discussion r3530397545); asserting the URL
			// here catches the path/query regression the body-only cases could not.
			assert.Equal(t, "/api/v2/metrics/attributes", gotPath)
			assert.Equal(t, "k8s.pod.uid", gotMetricName)
			assert.Equal(t, "0", gotStart)
			assert.Equal(t, "1000", gotEnd)

			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, result)

			logged := buf.String()
			if tc.wantWarn {
				assert.Contains(t, logged, "Unexpected response shape", "expected WARN log for contract violation")
			} else {
				assert.NotContains(t, logged, "Unexpected response shape", "expected no WARN log for valid shape")
			}
		})
	}
}

// TestGetMetricCardinality_MetricNameWithSlash pins that a metric name containing
// slashes (e.g. cloud-provider metrics like run.googleapis.com/request_latencies)
// round-trips through the metricName query parameter intact — the exact case the
// SigNoz request type flags, and one a path-segment URL would have mangled.
func TestGetMetricCardinality_MetricNameWithSlash(t *testing.T) {
	const metric = "run.googleapis.com/request_latencies"
	var gotPath, gotMetricName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMetricName = r.URL.Query().Get("metricName")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"attributes":[],"totalKeys":0}}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := newBufferedLogger(&buf, -4)
	c := NewClient(logger, srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := c.GetMetricCardinality(context.Background(), metric, 0, 1000)
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/metrics/attributes", gotPath)
	assert.Equal(t, metric, gotMetricName, "metric name with slashes must round-trip via the query param, not the path")
}
