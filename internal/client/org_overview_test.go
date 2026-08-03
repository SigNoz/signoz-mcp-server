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
