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

func TestGetOrgOverview_RequestContract(t *testing.T) {
	var gotMethod, gotPath, gotRawQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"telemetry.logs.count":1}}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	client := NewClient(newBufferedLogger(&logs, -4), srv.URL, "Bearer test-token", "Authorization", nil)
	result, err := client.GetOrgOverview(context.Background())

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"success","data":{"telemetry.logs.count":1}}`, string(result))
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/stats", gotPath)
	assert.Empty(t, gotRawQuery)
	assert.Equal(t, "Bearer test-token", gotAuth)
}

func TestGetOrgOverview_KeepsBaseURLPathPrefix(t *testing.T) {
	for _, suffix := range []string{"/signoz", "/signoz/"} {
		t.Run(suffix, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
			}))
			defer srv.Close()

			client := NewClient(newBufferedLogger(&bytes.Buffer{}, 0), srv.URL+suffix, "test-key", SignozApiKey, nil)
			_, err := client.GetOrgOverview(context.Background())

			require.NoError(t, err)
			assert.Equal(t, "/signoz/api/v1/stats", gotPath)
		})
	}
}

// Self-hosted SigNoz serves its web app with 200 for API routes it lacks, and an
// auth proxy serves a 200 login page; neither may reach the agent as data.
func TestGetOrgOverview_RejectsWebPageServedAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "web app page", contentType: "text/html; charset=utf-8", body: "<!doctype html><title>SigNoz</title>page-canary"},
		{name: "login page without html content type", contentType: "text/plain", body: "\n  <html><form action=\"/oauth2/sign_in\">page-canary</form></html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			var logs bytes.Buffer
			client := NewClient(newBufferedLogger(&logs, -4), srv.URL, "test-key", SignozApiKey, nil)
			result, err := client.GetOrgOverview(context.Background())

			require.ErrorIs(t, err, ErrNonJSONResponse)
			assert.Nil(t, result)
			assert.Contains(t, logs.String(), "SigNoz returned a web page instead of JSON")
			assert.NotContains(t, logs.String(), "page-canary")
		})
	}
}
