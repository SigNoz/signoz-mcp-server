package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetExternalURL_ConfigurationAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, response, want string
		warn                 bool
	}{
		{"public URL", `{"data":{"external_url":"https://observe.example.com/"}}`, "https://observe.example.com", false},
		{"base path", `{"data":{"external_url":"https://observe.example.com/signoz/"}}`, "https://observe.example.com/signoz", false},
		{"localhost", `{"data":{"external_url":"http://localhost:3301"}}`, "http://localhost:3301", false},
		{"IPv6 zone", `{"data":{"external_url":"http://[fe80::1%25eth0]:3301"}}`, "http://[fe80::1%25eth0]:3301", false},
		{"empty", `{"data":{"external_url":""}}`, "", false},
		{"unset", `{"data":{"external_url":"<unset>"}}`, "", false},
		{"upstream default", `{"data":{"external_url":"//<unset>"}}`, "", false},
		{"serialized unset", `{"data":{"external_url":"//%3Cunset%3E"}}`, "", false},
		{"missing field", `{"data":{}}`, "", true},
		{"null field", `{"data":{"external_url":null}}`, "", true},
		{"wrong type", `{"data":{"external_url":5}}`, "", true},
		{"missing envelope", `{"external_url":"https://observe.example.com"}`, "", true},
		{"non JSON", `<html>login</html>`, "", true},
		{"relative", `{"data":{"external_url":"/signoz"}}`, "", true},
		{"unsupported scheme", `{"data":{"external_url":"javascript:alert(1)"}}`, "", true},
		{"query", `{"data":{"external_url":"https://observe.example.com/?token=synthetic-secret"}}`, "", true},
		{"fragment", `{"data":{"external_url":"https://observe.example.com/#synthetic-secret"}}`, "", true},
		{"userinfo", `{"data":{"external_url":"https://user:synthetic-secret@observe.example.com"}}`, "", true},
		{"malformed", `{"data":{"external_url":"https://user:synthetic-secret%zz@observe.example.com"}}`, "", true},
		{"bind address", `{"data":{"external_url":"http://[0:0:0:0:0:0:0:0]:8080"}}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, "/api/v1/global/config", r.URL.Path)
				assert.Equal(t, "test-key", r.Header.Get(SignozApiKey))
				assert.Equal(t, "proxy-value", r.Header.Get("X-Proxy-Auth"))
				_, _ = w.Write([]byte(tc.response))
			}))
			defer backend.Close()
			var logs bytes.Buffer
			client := NewClient(newBufferedLogger(&logs, slog.LevelWarn), backend.URL, "test-key", SignozApiKey, map[string]string{"X-Proxy-Auth": "proxy-value"})
			for range 2 {
				got, err := client.GetExternalURL(context.Background())
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
			require.EqualValues(t, 1, requests.Load(), "cache should cover valid and fallback results")
			require.Equal(t, tc.warn, logs.Len() > 0)
			require.NotContains(t, logs.String(), "synthetic-secret")
		})
	}
}

func TestGetExternalURL_StatusFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"unavailable"}`))
			}))
			defer backend.Close()
			var logs bytes.Buffer
			client := NewClient(newBufferedLogger(&logs, slog.LevelWarn), backend.URL, "test-key", SignozApiKey, nil)
			for range 2 {
				got, err := client.GetExternalURL(context.Background())
				require.Empty(t, got)
				if status == http.StatusUnauthorized || status == http.StatusForbidden {
					var statusErr *HTTPStatusError
					require.ErrorAs(t, err, &statusErr)
					require.Equal(t, status, statusErr.StatusCode)
				} else {
					require.NoError(t, err)
				}
			}
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				require.EqualValues(t, 2, requests.Load(), "auth failures must not turn into cached successful fallbacks")
			} else {
				require.EqualValues(t, 1, requests.Load())
				require.Contains(t, logs.String(), "global configuration unavailable")
			}
		})
	}
}

func TestGetExternalURL_ConcurrentDiscoverySharesLookup(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"external_url": "https://first.example.com"}})
	}))
	defer backend.Close()
	client := NewClient(logpkg.New("error"), backend.URL, "test-key", SignozApiKey, nil)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := client.GetExternalURL(context.Background())
			if err != nil || got != "https://first.example.com" {
				t.Errorf("discovery = %q, %v", got, err)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, requests.Load())
}

type externalURLTransport func(*http.Request) (*http.Response, error)

func (f externalURLTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGetExternalURL_RefreshesConfigurationAndRetriesFallback(t *testing.T) {
	for _, tc := range []struct {
		name, initial, cached string
		status                int
		freshFor              time.Duration
	}{
		{"configured", `{"data":{"external_url":"https://first.example.com"}}`, "https://first.example.com", http.StatusOK, 15 * time.Minute},
		{"unset", `{"data":{"external_url":"//<unset>"}}`, "", http.StatusOK, 15 * time.Minute},
		{"unavailable", `{"error":"unavailable"}`, "", http.StatusNotFound, time.Minute},
		{"invalid", `{"data":{"external_url":"/relative"}}`, "", http.StatusOK, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				response, status := tc.initial, tc.status
				client := NewClient(logpkg.New("error"), "https://api.example.com", "test-key", SignozApiKey, nil)
				client.httpClient = &http.Client{Transport: externalURLTransport(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
				})}
				got, err := client.GetExternalURL(context.Background())
				require.NoError(t, err)
				require.Equal(t, tc.cached, got)

				response, status = `{"data":{"external_url":"https://updated.example.com"}}`, http.StatusOK
				time.Sleep(tc.freshFor - time.Second)
				got, err = client.GetExternalURL(context.Background())
				require.NoError(t, err)
				require.Equal(t, tc.cached, got, "use cached configuration until its freshness window expires")

				time.Sleep(time.Second)
				got, err = client.GetExternalURL(context.Background())
				require.NoError(t, err)
				require.Equal(t, "https://updated.example.com", got, "refresh expired configuration so browser links converge on SigNoz's current setting")
			})
		})
	}
}
