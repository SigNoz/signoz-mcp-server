package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListMetrics_FallsBackToLegacyMetadataWhenV2CatalogReturnsHTML(t *testing.T) {
	const metricName = "k8s.node.cpu.usage"

	var gotCatalogPath, gotMetadataPath, gotMetadataName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/metrics":
			gotCatalogPath = r.URL.Path
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<!doctype html><html><body>SigNoz UI</body></html>`))
		case "/api/v2/metrics/metadata":
			gotMetadataPath = r.URL.Path
			gotMetadataName = r.URL.Query().Get("metricName")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"success","data":{"description":"Total CPU usage","type":"gauge","unit":"{cpu}","temporality":"unspecified","isMonotonic":false}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(logpkg.New("debug"), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	result, err := c.ListMetrics(context.Background(), 0, 0, 10, metricName, "")
	require.NoError(t, err)

	assert.Equal(t, "/api/v2/metrics", gotCatalogPath)
	assert.Equal(t, "/api/v2/metrics/metadata", gotMetadataPath)
	assert.Equal(t, metricName, gotMetadataName)

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			Metrics []struct {
				MetricName  string `json:"metricName"`
				Type        string `json:"type"`
				Temporality string `json:"temporality"`
				IsMonotonic bool   `json:"isMonotonic"`
			} `json:"metrics"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(result, &resp))
	require.Len(t, resp.Data.Metrics, 1)
	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, metricName, resp.Data.Metrics[0].MetricName)
	assert.Equal(t, "gauge", resp.Data.Metrics[0].Type)
	assert.Equal(t, "unspecified", resp.Data.Metrics[0].Temporality)
	assert.False(t, resp.Data.Metrics[0].IsMonotonic)
}
