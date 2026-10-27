package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

func newBufferedLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})
	return slog.New(logpkg.NewContextHandler(base))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGetAlertByRuleID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v2/rules/ruleid-abc", r.URL.Path)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"ruleid-abc"}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.GetAlertByRuleID(context.Background(), "ruleid-abc")
	require.NoError(t, err)
}

func TestListAlertRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v3/rules", r.URL.Path)
		assert.Equal(t, "limit=200&offset=400", r.URL.RawQuery)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"rules":[{"id":"rule-1","alert":"High CPU","state":"inactive"}],"total":401}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	result, err := client.ListAlertRules(context.Background(), 200, 400)
	require.NoError(t, err)
	assert.Contains(t, string(result), `"id":"rule-1"`)
}

// expiredWorkspaceHTML mirrors the 404 page the SigNoz Cloud ingress serves
// for expired/deactivated workspaces (captured from production logs).
const expiredWorkspaceHTML = `<!DOCTYPE html>
<html>
<head><title>404 : This page does not exist :/</title></head>
<body><p>Either the workspace has expired or the workspace does not exist.</p></body>
</html>`

func TestValidateCredentials(t *testing.T) {
	tests := []struct {
		name          string
		saStatus      int    // status for /api/v1/service_accounts/me
		saBody        string // body for service_accounts/me; defaults to JSON
		expectedError bool
		checkErr      func(t *testing.T, err error)
	}{
		{
			name:          "service_accounts/me succeeds",
			saStatus:      http.StatusOK,
			expectedError: false,
		},
		{
			name:          "service_accounts/me unauthorized",
			saStatus:      http.StatusUnauthorized,
			expectedError: true,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrUnauthorized)
			},
		},
		{
			name:          "service_accounts/me forbidden",
			saStatus:      http.StatusForbidden,
			expectedError: true,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrUnauthorized)
			},
		},
		{
			name:          "service_accounts/me 500 is an unexpected status",
			saStatus:      http.StatusInternalServerError,
			expectedError: true,
			checkErr: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "unexpected status 500")
			},
		},
		{
			name:          "HTML 404 means no SigNoz API at the instance URL",
			saStatus:      http.StatusNotFound,
			saBody:        expiredWorkspaceHTML,
			expectedError: true,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrInstanceNotFound)
			},
		},
		{
			name:          "JSON 404 stays a generic unexpected status",
			saStatus:      http.StatusNotFound,
			expectedError: true,
			checkErr: func(t *testing.T, err error) {
				assert.NotErrorIs(t, err, ErrInstanceNotFound)
				assert.Contains(t, err.Error(), "unexpected status 404")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saRequests := 0

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
				assert.Equal(t, http.MethodGet, r.Method)

				if r.URL.Path != "/api/v1/service_accounts/me" {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				saRequests++
				body := `{"status":"ok"}`
				if tt.saBody != "" {
					body = tt.saBody
				}
				w.WriteHeader(tt.saStatus)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			err := client.ValidateCredentials(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
				if tt.checkErr != nil {
					tt.checkErr(t, err)
				}
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, 1, saRequests, "expected exactly one service_accounts/me call")
		})
	}
}

func TestEvaluateValidationResponse_DoesNotLogOrReturnRawBody(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		checkError func(*testing.T, error)
	}{
		{
			name:   "HTML 404 keeps instance-not-found classification",
			status: http.StatusNotFound,
			body:   "<html>SIGNOZ_API_KEY=validation-secret-canary</html>",
			checkError: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrInstanceNotFound)
			},
		},
		{
			name:   "generic status uses body-free fallback",
			status: http.StatusInternalServerError,
			body:   `{"status":"error","message":"SIGNOZ_API_KEY=validation-secret-canary"}`,
			checkError: func(t *testing.T, err error) {
				assert.Equal(t, "unexpected status 500", err.Error())
				var statusErr *HTTPStatusError
				assert.False(t, errors.As(err, &statusErr), "credential validation keeps its plain error contract")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			client := NewClient(newBufferedLogger(&logBuf, slog.LevelWarn), "https://example.test", "key", SignozApiKey, nil)

			err := client.evaluateValidationResponse(context.Background(), tc.status, []byte(tc.body))
			require.Error(t, err)
			tc.checkError(t, err)
			assert.NotContains(t, err.Error(), "validation-secret-canary")
			assert.NotContains(t, logBuf.String(), "validation-secret-canary")
		})
	}
}

func TestGetAnalyticsIdentity_CachesResult(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"user-1","email":"u@example.com","orgId":"org-1"}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "Bearer jwt", "Authorization", nil)

	for i := 0; i < 5; i++ {
		identity, err := client.GetAnalyticsIdentity(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "user-1", identity.UserID)
	}

	assert.Equal(t, 1, requests, "expected identity cache to serve repeated lookups")
}

func TestGetAnalyticsIdentity_ConcurrentCallsDedupe(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"user-1","email":"u@example.com","orgId":"org-1"}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "Bearer jwt", "Authorization", nil)

	const callers = 10
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			_, err := client.GetAnalyticsIdentity(context.Background())
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	// The mutex serializes lookups; the first request populates the cache
	// and every other caller observes the cached result.
	assert.Equal(t, int32(1), requests.Load(), "expected concurrent callers to share a single upstream request")
}

func TestDoRequest_RetriedDriftWarnsOnceWithoutLeakingCredentials(t *testing.T) {
	var logBuf bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unavailable","message":"authorization: auth123temporary-secret-canary"}}`))
	}))
	defer server.Close()

	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 503")

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	var driftWarnings int
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		if rec["msg"] != errorEnvelopeWarning {
			continue
		}
		assert.NotZero(t, rec["response.body.size_bytes"])
		assert.Equal(t, true, rec["recognized"])
		assert.Equal(t, []any{"message"}, rec["fields"])
		driftWarnings++
	}

	assert.Equal(t, 1, driftWarnings, "shape drift must be warned once per request across retries")
	assert.NotContains(t, logBuf.String(), "auth123temporary-secret-canary")
}

func TestDoRequest_DriftStaysDetectableWhenARetrySucceeds(t *testing.T) {
	var logBuf bytes.Buffer
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"unavailable","message":"temporary-outage-canary"}} trailing`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	body, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, time.Second)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"success"}`, string(body))

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	var driftWarnings int
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		if rec["msg"] != errorEnvelopeWarning {
			continue
		}
		assert.Equal(t, false, rec["recognized"])
		assert.Equal(t, []any{"envelope"}, rec["fields"])
		driftWarnings++
	}

	assert.Equal(t, 1, driftWarnings, "transient shape drift must remain detectable")
	assert.NotContains(t, logBuf.String(), "temporary-outage-canary")
}

func TestDoRequest_HTTPStatusErrorPreservesFullBodyForParsing(t *testing.T) {
	var logBuf bytes.Buffer
	longMessage := strings.Repeat("x", 5000) + "tail"
	responseBody, err := json.Marshal(map[string]any{
		"status": "error",
		"error": map[string]any{
			"type":    "forbidden",
			"code":    "forbidden",
			"message": longMessage,
		},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()

	client := NewClient(newBufferedLogger(&logBuf, slog.LevelWarn), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err = client.doRequest(context.Background(), http.MethodPost, server.URL, []byte(`{}`), time.Second)
	require.Error(t, err)

	var statusErr *HTTPStatusError
	require.True(t, errors.As(err, &statusErr), "expected HTTPStatusError, got %T: %v", err, err)
	assert.Equal(t, http.StatusForbidden, statusErr.StatusCode)
	assert.True(t, json.Valid([]byte(statusErr.Body)), "stored body should remain parseable JSON")
	assert.Contains(t, statusErr.Body, longMessage)
	assert.Contains(t, err.Error(), "...(truncated)")
	assert.NotContains(t, err.Error(), "tail")

	assert.NotContains(t, logBuf.String(), longMessage)
	assert.NotContains(t, logBuf.String(), "tail")
}

func TestHTTPStatusError_UnrecognizedBodyUsesStatusFallback(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "unauthorized plain text", statusCode: http.StatusUnauthorized, body: "expired secret-canary", want: "unexpected status 401: authentication failed"},
		{name: "unauthorized malformed JSON", statusCode: http.StatusUnauthorized, body: `{"token":"secret-canary"`, want: "unexpected status 401: authentication failed"},
		{name: "unauthorized JSON string", statusCode: http.StatusUnauthorized, body: `"secret-canary"`, want: "unexpected status 401: authentication failed"},
		{name: "unauthorized JSON null", statusCode: http.StatusUnauthorized, body: `null`, want: "unexpected status 401: authentication failed"},
		{name: "forbidden HTML", statusCode: http.StatusForbidden, body: "<html>secret-canary</html>", want: "unexpected status 403: permission denied"},
		{name: "forbidden JSON array", statusCode: http.StatusForbidden, body: `["secret-canary"]`, want: "unexpected status 403: permission denied"},
		{name: "forbidden empty", statusCode: http.StatusForbidden, body: "", want: "unexpected status 403: permission denied"},
		{name: "bad request HTML", statusCode: http.StatusBadRequest, body: "<html>secret-canary</html>", want: "unexpected status 400"},
		{name: "internal proxy JSON", statusCode: http.StatusInternalServerError, body: `{"status":"error","message":"secret-canary"}`, want: "unexpected status 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPStatusError{StatusCode: tt.statusCode, Body: tt.body}
			assert.Equal(t, tt.want, err.Error())
			assert.NotContains(t, err.Error(), "secret-canary")
			assert.Equal(t, tt.body, err.Body, "raw body remains available to the recognized-envelope parser")
		})
	}
}

func TestHTTPStatusError_FullyFilteredAuthorizationMessageUsesStatusFallback(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		upstreamTy string
		want       string
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, upstreamTy: "unauthorized", want: "unexpected status 401: authentication failed"},
		{name: "forbidden", statusCode: http.StatusForbidden, upstreamTy: "forbidden", want: "unexpected status 403: permission denied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPStatusError{
				StatusCode: tt.statusCode,
				Body: `{"status":"error","error":{"type":"` + tt.upstreamTy + `","code":"authz_failure",` +
					`"message":"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWxsYmFjay1jYW5hcnkifQ.signature"}}`,
			}
			assert.Equal(t, tt.want, err.Error())
		})
	}
}

func TestHTTPStatusError_RecognizedBodyUsesParsedSafeText(t *testing.T) {
	err := &HTTPStatusError{
		StatusCode: http.StatusBadRequest,
		Body: `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"bad query",` +
			`"url":"https://signoz.io/docs/search","suggestions":["narrow the query"],` +
			`"errors":[{"message":"bad field","suggestions":["use an existing field"]}],"retry":{"delay":1000000000}}}`,
	}

	assert.Contains(t, err.Error(), "unexpected status 400: bad query (bad field)")
}

func TestListServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/services", r.URL.Path)

		var requestBody map[string]string
		err := json.NewDecoder(r.Body).Decode(&requestBody)
		require.NoError(t, err)
		assert.Equal(t, "1640995200000000000", requestBody["start"])
		assert.Equal(t, "1641081600000000000", requestBody["end"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.ListServices(context.Background(), "1640995200000000000", "1641081600000000000")
	require.NoError(t, err)
}

func TestGetAlertHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// v2 timeline is a GET; params ride on the query string, not a body.
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v2/rules/ruleid-abc/history/timeline", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, "1640995200000", q.Get("start"))
		assert.Equal(t, "1641081600000", q.Get("end"))
		assert.Equal(t, "20", q.Get("limit"))
		assert.Equal(t, "desc", q.Get("order"))
		assert.Equal(t, "firing", q.Get("state"))
		assert.Equal(t, "severity = 'warning'", q.Get("filterExpression"))
		assert.Equal(t, "eyJvZmZzZXQiOjIwLCJsaW1pdCI6MjB9", q.Get("cursor"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"items":[],"total":0}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.GetAlertHistory(context.Background(), "ruleid-abc", &apiclient.GetRuleHistoryTimelineParams{
		Start:            1640995200000,
		End:              1641081600000,
		State:            ptr(apitypes.RuletypesAlertState("firing")),
		FilterExpression: ptr("severity = 'warning'"),
		Limit:            ptr(int64(20)),
		Order:            ptr(apitypes.Querybuildertypesv5OrderDirection("desc")),
		Cursor:           ptr("eyJvZmZzZXQiOjIwLCJsaW1pdCI6MjB9"),
	})
	require.NoError(t, err)
}

func TestGetTraceDetails_UsesCanonicalTraceIDFilter(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v5/query_range", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured = body

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.GetTraceDetails(context.Background(), "abc123", true, 1711123200000, 1711130400000)
	require.NoError(t, err)

	payload := string(captured)
	require.Contains(t, payload, `"expression":"trace_id = 'abc123'"`)

	// A quote in the ID stays inside the literal instead of adding conditions.
	_, err = client.GetTraceDetails(context.Background(), "x' OR trace_id != 'y", true, 1711123200000, 1711130400000)
	require.NoError(t, err)
	require.Contains(t, string(captured), `"expression":"trace_id = 'x\\' OR trace_id != \\'y'"`)
	require.Contains(t, payload, `"limit":1000`)
	require.NotContains(t, payload, `"expression":"traceID = 'abc123'"`)
	var request struct {
		CompositeQuery struct {
			Queries []struct {
				Spec struct {
					Order json.RawMessage `json:"order"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	require.NoError(t, json.Unmarshal(captured, &request))
	require.Len(t, request.CompositeQuery.Queries, 1)
	require.JSONEq(t, `[{"key":{"name":"timestamp"},"direction":"desc"}]`, string(request.CompositeQuery.Queries[0].Spec.Order))
}

func TestGetFieldKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/fields/keys", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, "metrics", q.Get("signal"))
		assert.Equal(t, "container.cpu.usage", q.Get("metricName"))
		assert.Equal(t, "cpu", q.Get("searchText"))
		assert.Equal(t, "resource", q.Get("fieldContext"))
		assert.Equal(t, "string", q.Get("fieldDataType"))
		assert.Equal(t, "otel", q.Get("source"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.GetFieldKeys(context.Background(), "metrics", "container.cpu.usage", "cpu", "resource", "string", "otel")
	require.NoError(t, err)
}

func TestGetFieldValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/fields/values", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, "metrics", q.Get("signal"))
		assert.Equal(t, "host.name", q.Get("name"))
		assert.Equal(t, "container.cpu.usage", q.Get("metricName"))
		assert.Equal(t, "prod", q.Get("searchText"))
		assert.Equal(t, "resource", q.Get("fieldContext"))
		assert.Equal(t, "otel", q.Get("source"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.GetFieldValues(context.Background(), "metrics", "host.name", "container.cpu.usage", "prod", "resource", "otel")
	require.NoError(t, err)
}

func TestDoRequest_RetryOn503ThenSuccess(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	logger := logpkg.New("debug")
	c := NewClient(logger, srv.URL, "test-key", "SIGNOZ-API-KEY", nil)

	result, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/test", nil, DefaultQueryTimeout)
	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
	assert.Contains(t, string(result), "success")
}

func TestDoRequest_RetriesExhausted(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"still down"}`))
	}))
	defer srv.Close()

	logger := logpkg.New("debug")
	c := NewClient(logger, srv.URL, "test-key", "SIGNOZ-API-KEY", nil)

	result, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/test", nil, DefaultQueryTimeout)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 3, attempts)
	assert.Contains(t, err.Error(), "503")
}

func TestDoRequest_NoRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	logger := logpkg.New("debug")
	c := NewClient(logger, srv.URL, "test-key", "SIGNOZ-API-KEY", nil)

	_, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/test", nil, DefaultQueryTimeout)
	assert.Error(t, err)
	assert.Equal(t, 1, attempts)
	assert.Contains(t, err.Error(), "400")
}

func TestDoRequest_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	logger := logpkg.New("debug")
	c := NewClient(logger, srv.URL, "test-key", "SIGNOZ-API-KEY", nil)

	result, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/test", nil, DefaultQueryTimeout)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Contains(t, string(result), "success")
}

func TestGuardrail_MutatingPOSTNotRetriedAfterRetryableStatus(t *testing.T) {
	var attempts atomic.Int32
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var err error
		requestBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"may already have committed"}`))
	}))
	defer server.Close()

	client := NewClient(logpkg.New("error"), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	_, err := client.doRequest(context.Background(), http.MethodPost, server.URL, []byte(`{"name":"created-once"}`), time.Second)
	require.Error(t, err)
	assert.Equal(t, int32(1), attempts.Load())
	assert.JSONEq(t, `{"name":"created-once"}`, string(requestBody))
}

func TestGuardrail_MutatingPOSTNotRetriedAfterAmbiguousTransportFailure(t *testing.T) {
	var attempts atomic.Int32
	var requestBody []byte
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		var err error
		requestBody, err = io.ReadAll(req.Body)
		require.NoError(t, err)
		return nil, io.ErrUnexpectedEOF
	})
	client := NewClient(logpkg.New("error"), "http://example.invalid", "test-api-key", "SIGNOZ-API-KEY", nil)
	client.httpClient.Transport = transport

	_, err := client.doRequest(context.Background(), http.MethodPost, "http://example.invalid/api/v1/dashboards", []byte(`{"title":"created-once"}`), time.Second)
	require.Error(t, err)
	assert.Equal(t, int32(1), attempts.Load())
	assert.JSONEq(t, `{"title":"created-once"}`, string(requestBody))
}

func TestGuardrail_ReadOnlyPOSTRetries(t *testing.T) {
	var attempts atomic.Int32
	var requestBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requestBodies = append(requestBodies, requestBody)
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"result":[]}}`))
	}))
	defer server.Close()

	client := NewClient(logpkg.New("error"), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	result, err := client.QueryBuilderV5(context.Background(), []byte(`{"schemaVersion":"v1"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":{"result":[]}}`, string(result))
	assert.Equal(t, int32(2), attempts.Load())
	require.Len(t, requestBodies, 2)
	for _, requestBody := range requestBodies {
		assert.JSONEq(t, `{"schemaVersion":"v1"}`, string(requestBody))
	}
}

func TestNewClient_SetsCustomHeaders(t *testing.T) {
	customHeaders := map[string]string{
		"CF-Access-Client-Id":     "test-id.access",
		"CF-Access-Client-Secret": "test-secret",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify standard headers are present
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		// Verify custom headers are injected
		assert.Equal(t, "test-id.access", r.Header.Get("CF-Access-Client-Id"))
		assert.Equal(t, "test-secret", r.Header.Get("CF-Access-Client-Secret"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", customHeaders)

	_, err := client.ListAlerts(context.Background(), types.ListAlertsParams{})
	assert.NoError(t, err)
}

func TestNewClient_ReservedHeadersSkippedWithoutLoggingTheirValues(t *testing.T) {
	customHeaders := map[string]string{
		"Content-Type":        "text/plain",
		"SIGNOZ-API-KEY":      "overridden-key",
		"User-Agent":          "custom-client/1.0",
		"CF-Access-Client-Id": "test-id",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reserved headers should NOT be overridden by custom headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
		assert.Equal(t, "custom-client/1.0 "+version.UserAgent(), r.Header.Get("User-Agent"))

		// Non-reserved custom headers should still be injected
		assert.Equal(t, "test-id", r.Header.Get("CF-Access-Client-Id"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	var logs bytes.Buffer
	client := NewClient(newBufferedLogger(&logs, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", customHeaders)

	_, err := client.ListAlerts(context.Background(), types.ListAlertsParams{})
	assert.NoError(t, err)
	require.Contains(t, logs.String(), `"header":"SIGNOZ-API-KEY"`, "the ignored header is still reported by name")
	require.NotContains(t, logs.String(), "overridden-key",
		"a custom header that collides with the auth header carries a credential; its value must never be logged")
}

func TestCreateAlertRule_v2Returns201(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"rule-123"}}`))
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	data, err := client.CreateAlertRule(context.Background(), []byte(`{"alert":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/rules", gotPath)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Contains(t, string(data), `"rule-123"`)
}

func TestUpdateAlertRule_v2Returns204(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.UpdateAlertRule(context.Background(), "abc-123", []byte(`{"alert":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/rules/abc-123", gotPath)
	assert.Equal(t, http.MethodPut, gotMethod)
}

func TestDeleteAlertRule_v2Returns204(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.DeleteAlertRule(context.Background(), "abc-123")
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/rules/abc-123", gotPath)
	assert.Equal(t, http.MethodDelete, gotMethod)
}

func TestTestNotificationChannel_UsesNewPath(t *testing.T) {
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.TestNotificationChannel(context.Background(), []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test/hook"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/notification_channels/test", gotPath)
	// The body is forwarded verbatim; SigNoz is the only validator of its shape.
	assert.JSONEq(t, `{"config":{"kind":"webhook","spec":{"url":"https://example.test/hook"}}}`, string(gotBody))
}

func TestUpdateNotificationChannel_UsesV2FullReplacement(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"url":"https://example.test/hook","username":"","password":"","bearerToken":""}},"id":"550e8400-e29b-41d4-a716-446655440000","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}}`))
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.UpdateNotificationChannel(context.Background(), "550e8400-e29b-41d4-a716-446655440000", []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test/hook"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/notification_channels/550e8400-e29b-41d4-a716-446655440000", gotPath)
	assert.Equal(t, http.MethodPut, gotMethod)
}

func TestDeleteNotificationChannel(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.DeleteNotificationChannel(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/notification_channels/550e8400-e29b-41d4-a716-446655440000", gotPath)
	assert.Equal(t, http.MethodDelete, gotMethod)
}

func TestListViews(t *testing.T) {
	var gotPath, gotRawQuery, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.ListViews(context.Background(), "traces", "ak")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v2/saved_views", gotPath)
	assert.Contains(t, gotRawQuery, "source=traces")
	assert.Contains(t, gotRawQuery, "name=ak")
	assert.NotContains(t, gotRawQuery, "category=")
}

func TestListDashboards_ForwardsFilterSortOrder(t *testing.T) {
	var gotPath, gotMethod string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"dashboards":[],"tags":[],"total":0}}`))
	}))
	defer server.Close()

	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.ListDashboards(context.Background(), 50, 0, "name CONTAINS 'overview'", "name", "asc")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v2/dashboards", gotPath)
	// The MCP `filter` maps to the v2 API's `query` param; sort/order pass through as-is.
	assert.Equal(t, "name CONTAINS 'overview'", gotQuery.Get("query"))
	assert.Equal(t, "name", gotQuery.Get("sort"))
	assert.Equal(t, "asc", gotQuery.Get("order"))
	assert.Equal(t, "50", gotQuery.Get("limit"))
}

func TestListDashboards_OmitsEmptyListParams(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"dashboards":[],"tags":[],"total":0}}`))
	}))
	defer server.Close()

	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.ListDashboards(context.Background(), 50, 0, "", "", "")
	require.NoError(t, err)
	for _, p := range []string{"query", "sort", "order"} {
		_, has := gotQuery[p]
		assert.Falsef(t, has, "empty %s must not be sent", p)
	}
}

func TestGetView(t *testing.T) {
	var gotPath, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	defer server.Close()
	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.GetView(context.Background(), "view-uuid-1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v2/saved_views/view-uuid-1", gotPath)
}

func TestCreateView(t *testing.T) {
	var gotBody []byte
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"new-id"}}`))
	}))
	defer server.Close()
	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	body := []byte(`{"name":"x","source":"traces","spec":{}}`)
	_, err := c.CreateView(context.Background(), body)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v2/saved_views", gotPath)
	assert.JSONEq(t, string(body), string(gotBody))
}

func TestUpdateView(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	defer server.Close()
	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.UpdateView(context.Background(), "view-1", []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/api/v2/saved_views/view-1", gotPath)
}

func TestDeleteView(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()
	c := NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)
	_, err := c.DeleteView(context.Background(), "view-1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/api/v2/saved_views/view-1", gotPath)
}

// Broken bodies stay terminal; known auth rejections must still reach the
// coded error path instead of becoming successful discovery fallbacks.
func TestDoRequest_ResponseBodyFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/truncated=%t", status, truncated), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if truncated {
						w.Header().Set("Content-Length", "100")
					}
					w.WriteHeader(status)
					if truncated {
						_, _ = w.Write([]byte("partial"))
						return
					}
					chunk := bytes.Repeat([]byte("a"), 1<<20)
					var written int64
					for written <= maxResponseBytes {
						n, err := w.Write(chunk)
						if err != nil {
							return
						}
						written += int64(n)
					}
				}))
				t.Cleanup(server.Close)

				client := NewClient(logpkg.New("error"), server.URL, "test-api-key", SignozApiKey, nil)
				_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, 30*time.Second)
				require.Error(t, err)
				if status == http.StatusOK {
					if truncated {
						require.ErrorIs(t, err, io.ErrUnexpectedEOF)
					} else {
						require.Contains(t, err.Error(), "exceeds maximum allowed size")
					}
				} else {
					var statusErr *HTTPStatusError
					require.ErrorAs(t, err, &statusErr, "body failures must preserve authentication status")
					assert.Equal(t, status, statusErr.StatusCode)
					assert.Empty(t, statusErr.Body, "discard unreadable error bodies")
				}
			})
		}
	}
}

// TestDoRequest_AllowsLargeUnderCapResponse verifies a large-but-under-cap
// response is returned intact, so the guard does not regress legitimate large
// (e.g. ~10k-row) results.
func TestDoRequest_AllowsLargeUnderCapResponse(t *testing.T) {
	body := bytes.Repeat([]byte("b"), 4<<20) // 4 MiB, well under cap
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	got, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, len(body), len(got))
}
