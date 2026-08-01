package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDataRetention_RecordedLiveResponses(t *testing.T) {
	metricsBody := readRetentionFixture(t, "metrics-success.json")
	tracesBody := readRetentionFixture(t, "traces-success.json")
	logsBody := readRetentionFixture(t, "logs-v2-success.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/settings/ttl" && r.URL.Query().Get("type") == retentionSignalMetrics:
			_, _ = w.Write(metricsBody)
		case r.URL.Path == "/api/v1/settings/ttl" && r.URL.Query().Get("type") == retentionSignalTraces:
			_, _ = w.Write(tracesBody)
		case r.URL.Path == "/api/v2/settings/ttl":
			_, _ = w.Write(logsBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "k", SignozApiKey, nil)
	result, err := c.GetDataRetention(context.Background())
	require.NoError(t, err)

	for signal, policy := range map[string]RetentionPolicy{
		retentionSignalMetrics: result.Metrics,
		retentionSignalTraces:  result.Traces,
		retentionSignalLogs:    result.Logs,
	} {
		assert.True(t, policy.CurrentStateKnown, signal)
		assert.Equal(t, retentionStatusSuccess, policy.ChangeStatus, signal)
	}
	require.NotNil(t, result.Metrics.CurrentRetentionHours)
	assert.EqualValues(t, 2160, *result.Metrics.CurrentRetentionHours)
	require.NotNil(t, result.Traces.CurrentRetentionHours)
	assert.EqualValues(t, 720, *result.Traces.CurrentRetentionHours)
	require.NotNil(t, result.Logs.CurrentRetentionHours)
	assert.EqualValues(t, 720, *result.Logs.CurrentRetentionHours)
	assert.Empty(t, result.Logs.CustomRules)
}

func TestGetDataRetention_NormalizesAllSignals(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "test-api-key", r.Header.Get(SignozApiKey))

		key := r.URL.Path + "?" + r.URL.RawQuery
		mu.Lock()
		hits[key]++
		mu.Unlock()

		switch {
		case r.URL.Path == "/api/v1/settings/ttl" && r.URL.Query().Get("type") == retentionSignalMetrics:
			_, _ = w.Write([]byte(`{
				"metrics_ttl_duration_hrs":720,
				"metrics_move_ttl_duration_hrs":-1,
				"expected_metrics_ttl_duration_hrs":720,
				"expected_metrics_move_ttl_duration_hrs":-1,
				"status":"success"
			}`))
		case r.URL.Path == "/api/v1/settings/ttl" && r.URL.Query().Get("type") == retentionSignalTraces:
			_, _ = w.Write([]byte(`{
				"traces_ttl_duration_hrs":360,
				"traces_move_ttl_duration_hrs":168,
				"expected_traces_ttl_duration_hrs":720,
				"expected_traces_move_ttl_duration_hrs":-1,
				"status":"pending"
			}`))
		case r.URL.Path == "/api/v2/settings/ttl" && r.URL.RawQuery == "":
			_, _ = w.Write([]byte(`{
				"version":"v2",
				"status":"success",
				"default_ttl_days":15,
				"ttl_conditions":[{
					"conditions":[{"key":"service_name","values":["checkout","payment"]}],
					"ttlDays":30
				}],
				"cold_storage_ttl_days":-1
			}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "test-api-key", SignozApiKey, nil)
	result, err := c.GetDataRetention(context.Background())
	require.NoError(t, err)

	require.NotNil(t, result.Metrics.CurrentRetentionHours)
	assert.EqualValues(t, 720, *result.Metrics.CurrentRetentionHours)
	assert.True(t, result.Metrics.CurrentStateKnown)
	assert.Equal(t, retentionStatusSuccess, result.Metrics.ChangeStatus)

	require.NotNil(t, result.Traces.CurrentRetentionHours)
	assert.EqualValues(t, 360, *result.Traces.CurrentRetentionHours)
	assert.True(t, result.Traces.CurrentStateKnown)
	assert.Equal(t, retentionStatusPending, result.Traces.ChangeStatus)

	require.NotNil(t, result.Logs.CurrentRetentionHours)
	assert.EqualValues(t, 360, *result.Logs.CurrentRetentionHours)
	assert.True(t, result.Logs.CurrentStateKnown)
	assert.Equal(t, retentionStatusSuccess, result.Logs.ChangeStatus)
	require.Len(t, result.Logs.CustomRules, 1)
	assert.EqualValues(t, 720, result.Logs.CustomRules[0].RetentionHours)
	assert.Equal(t, "service_name", result.Logs.CustomRules[0].Conditions[0].Key)
	assert.Equal(t, []string{"checkout", "payment"}, result.Logs.CustomRules[0].Conditions[0].Values)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits["/api/v1/settings/ttl?type=metrics"])
	assert.Equal(t, 1, hits["/api/v1/settings/ttl?type=traces"])
	assert.Equal(t, 1, hits["/api/v2/settings/ttl?"])
	assert.Len(t, hits, 3)
}

func TestFetchLogsRetention_InFlightCustomPolicyReportsCurrentUnknown(t *testing.T) {
	for _, status := range []string{retentionStatusPending, retentionStatusFailed} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{
					"version":"v2",
					"status":"` + status + `",
					"default_ttl_days":45,
					"ttl_conditions":[{
						"conditions":[{"key":"deployment_environment","values":["prod"]}],
						"ttlDays":90
					}],
					"cold_storage_ttl_days":7
				}`))
			}))
			defer srv.Close()

			c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "k", SignozApiKey, nil)
			policy, err := c.fetchLogsRetention(context.Background())
			require.NoError(t, err)

			assert.Equal(t, status, policy.ChangeStatus)
			assert.False(t, policy.CurrentStateKnown)
			assert.Nil(t, policy.CurrentRetentionHours)
			assert.Empty(t, policy.CustomRules)
		})
	}
}

func TestFetchLogsRetention_V2LegacyRepresentationUsesExactV1Hours(t *testing.T) {
	var v2Hits, v1Hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/settings/ttl":
			v2Hits++
			// The compatibility response rounds 361 legacy hours down to 15 days.
			_, _ = w.Write([]byte(`{
				"version":"v1",
				"status":"failed",
				"default_ttl_days":15,
				"cold_storage_ttl_days":-1,
				"expected_logs_ttl_duration_hrs":721,
				"expected_logs_move_ttl_duration_hrs":169
			}`))
		case "/api/v1/settings/ttl":
			v1Hits++
			assert.Equal(t, retentionSignalLogs, r.URL.Query().Get("type"))
			_, _ = w.Write([]byte(`{
				"logs_ttl_duration_hrs":361,
				"logs_move_ttl_duration_hrs":-1,
				"expected_logs_ttl_duration_hrs":721,
				"expected_logs_move_ttl_duration_hrs":169,
				"status":"failed"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "k", SignozApiKey, nil)
	policy, err := c.fetchLogsRetention(context.Background())
	require.NoError(t, err)

	assert.True(t, policy.CurrentStateKnown)
	require.NotNil(t, policy.CurrentRetentionHours)
	assert.EqualValues(t, 361, *policy.CurrentRetentionHours)
	assert.Equal(t, retentionStatusFailed, policy.ChangeStatus)
	assert.Equal(t, 1, v2Hits)
	assert.Equal(t, 1, v1Hits)
}

func TestFetchLogsRetention_OnlyRouteNotFoundFallsBackToLegacy(t *testing.T) {
	tests := []struct {
		name            string
		v2Status        int
		v2Body          string
		wantFallback    bool
		wantStatusError int
	}{
		{name: "route not found", v2Status: http.StatusNotFound, v2Body: "404 page not found\n", wantFallback: true},
		{name: "JSON not found", v2Status: http.StatusNotFound, v2Body: `{"status":"error"}`, wantStatusError: http.StatusNotFound},
		{name: "HTML not found", v2Status: http.StatusNotFound, v2Body: `<html>not found</html>`, wantStatusError: http.StatusNotFound},
		{name: "unauthorized", v2Status: http.StatusUnauthorized, v2Body: `{"status":"error"}`, wantStatusError: http.StatusUnauthorized},
		{name: "forbidden", v2Status: http.StatusForbidden, v2Body: `{"status":"error"}`, wantStatusError: http.StatusForbidden},
		{name: "server error", v2Status: http.StatusInternalServerError, v2Body: `{"status":"error"}`, wantStatusError: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			legacyHits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/settings/ttl":
					w.WriteHeader(tc.v2Status)
					_, _ = w.Write([]byte(tc.v2Body))
				case "/api/v1/settings/ttl":
					legacyHits++
					assert.Equal(t, retentionSignalLogs, r.URL.Query().Get("type"))
					_, _ = w.Write([]byte(`{"logs_ttl_duration_hrs":360,"logs_move_ttl_duration_hrs":-1,"status":""}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			c := NewClient(newBufferedLogger(&bytes.Buffer{}, -4), srv.URL, "k", SignozApiKey, nil)
			policy, err := c.fetchLogsRetention(context.Background())
			if tc.wantFallback {
				require.NoError(t, err)
				require.NotNil(t, policy.CurrentRetentionHours)
				assert.EqualValues(t, 360, *policy.CurrentRetentionHours)
				assert.Equal(t, 1, legacyHits)
				return
			}

			require.Error(t, err)
			assert.Equal(t, 0, legacyHits, "non-route-level failures must not fall back")
			var statusErr *HTTPStatusError
			require.True(t, errors.As(err, &statusErr))
			assert.Equal(t, tc.wantStatusError, statusErr.StatusCode)
		})
	}
}

func TestRetentionContractDriftWarnsAndFails(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown logs version", body: `{"version":"v3","status":"success","default_ttl_days":15}`},
		{name: "missing logs default", body: `{"version":"v2","status":"success"}`},
		{name: "malformed JSON", body: `not json`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			var logs bytes.Buffer
			c := NewClient(newBufferedLogger(&logs, -4), srv.URL, "k", SignozApiKey, nil)
			_, err := c.fetchLogsRetention(context.Background())
			require.Error(t, err)
			assert.Contains(t, logs.String(), "Unexpected response shape from data retention endpoint")
			assert.Contains(t, logs.String(), `"signal":"logs"`)
		})
	}
}

func readRetentionFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/retention/" + name)
	require.NoError(t, err)
	return body
}
