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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestGetAlertByRuleID(t *testing.T) {
	tests := []struct {
		name          string
		ruleID        string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedData  map[string]interface{}
	}{
		{
			name:   "successful alert retrieval",
			ruleID: "ruleid-abc",
			resp: map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"id":          "ruleid-abc",
					"name":        "Test alert rule",
					"description": "This is a test alert rule",
					"condition":   "cpu_usage > 80",
					"enabled":     true,
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: map[string]interface{}{
				"id":          "ruleid-abc",
				"name":        "Test alert rule",
				"description": "This is a test alert rule",
				"condition":   "cpu_usage > 80",
				"enabled":     true,
			},
		},
		{
			name:          "alert not found",
			ruleID:        "non-existent-rule",
			resp:          map[string]interface{}{"status": "error", "message": "Alert rule not found"},
			statusCode:    http.StatusNotFound,
			expectedError: true,
		},
		{
			name:          "server error",
			ruleID:        "test-rule-123",
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "empty rule ID",
			ruleID:        "",
			resp:          map[string]interface{}{"status": "error", "message": "Invalid rule ID"},
			statusCode:    http.StatusBadRequest,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				expectedPath := fmt.Sprintf("/api/v2/rules/%s", tt.ruleID)
				assert.Equal(t, expectedPath, r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.GetAlertByRuleID(ctx, tt.ruleID)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)

				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)

				assert.Equal(t, "success", response["status"])
				if data, ok := response["data"].(map[string]interface{}); ok {
					assert.Equal(t, tt.expectedData["id"], data["id"])
					assert.Equal(t, tt.expectedData["name"], data["name"])
					assert.Equal(t, tt.expectedData["description"], data["description"])
					assert.Equal(t, tt.expectedData["condition"], data["condition"])
					assert.Equal(t, tt.expectedData["enabled"], data["enabled"])
				}
			}
		})
	}
}

func TestListAlertRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v2/rules", r.URL.Path)
		assert.Equal(t, "", r.URL.RawQuery)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
		assert.Equal(t, version.UserAgent(), r.Header.Get("User-Agent"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"rule-1","alert":"High CPU","state":"inactive"}]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	result, err := client.ListAlertRules(context.Background())
	require.NoError(t, err)
	assert.Contains(t, string(result), `"id":"rule-1"`)
}

func TestValidateCredentials(t *testing.T) {
	tests := []struct {
		name            string
		userMeStatus    int // status for /api/v1/user/me (always hit first)
		saStatus        int // status for /api/v1/service_accounts/me (only hit on user/me 502)
		expectedError   bool
		checkErr        func(t *testing.T, err error)
		expectUserMeHit bool
		expectSAHit     bool
	}{
		{
			name:            "user/me succeeds (legacy user-level key)",
			userMeStatus:    http.StatusOK,
			expectedError:   false,
			expectUserMeHit: true,
			expectSAHit:     false,
		},
		{
			name:            "user/me unauthorized returns error directly",
			userMeStatus:    http.StatusUnauthorized,
			expectedError:   true,
			expectUserMeHit: true,
			expectSAHit:     false,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrUnauthorized)
			},
		},
		{
			name:            "user/me 404 falls back to service_accounts/me success",
			userMeStatus:    http.StatusNotFound,
			saStatus:        http.StatusOK,
			expectedError:   false,
			expectUserMeHit: true,
			expectSAHit:     true,
		},
		{
			name:            "user/me 404 falls back to service_accounts/me unauthorized",
			userMeStatus:    http.StatusNotFound,
			saStatus:        http.StatusUnauthorized,
			expectedError:   true,
			expectUserMeHit: true,
			expectSAHit:     true,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrUnauthorized)
			},
		},
		{
			name:            "user/me 500 returns error directly without fallback",
			userMeStatus:    http.StatusInternalServerError,
			expectedError:   true,
			expectUserMeHit: true,
			expectSAHit:     false,
			checkErr: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "unexpected status 500")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userMeRequests := 0
			saRequests := 0

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
				assert.Equal(t, "custom-client/1.0 "+version.UserAgent(), r.Header.Get("User-Agent"))
				assert.Equal(t, http.MethodGet, r.Method)

				switch r.URL.Path {
				case "/api/v1/user/me":
					userMeRequests++
					w.WriteHeader(tt.userMeStatus)
				case "/api/v1/service_accounts/me":
					saRequests++
					w.WriteHeader(tt.saStatus)
				default:
					t.Fatalf("unexpected path %s", r.URL.Path)
				}

				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", map[string]string{
				"User-Agent": "custom-client/1.0",
			})

			err := client.ValidateCredentials(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
				if tt.checkErr != nil {
					tt.checkErr(t, err)
				}
			} else {
				assert.NoError(t, err)
			}

			if tt.expectUserMeHit {
				assert.Equal(t, 1, userMeRequests, "expected user/me to be called")
			} else {
				assert.Equal(t, 0, userMeRequests, "expected user/me NOT to be called")
			}
			if tt.expectSAHit {
				assert.Equal(t, 1, saRequests, "expected service_accounts/me to be called")
			} else {
				assert.Equal(t, 0, saRequests, "expected service_accounts/me NOT to be called")
			}
		})
	}
}

func TestGetAnalyticsIdentity(t *testing.T) {
	tests := []struct {
		name              string
		authHeaderName    string
		expectedHeader    string
		expectedHeaderVal string
		expectedPath      string
		statusCode        int
		responseBody      string
		expectedIdentity  *AnalyticsIdentity
		checkErr          func(t *testing.T, err error)
	}{
		{
			name:              "authorization auth resolves via v2 users me",
			authHeaderName:    "Authorization",
			expectedHeader:    "Authorization",
			expectedHeaderVal: "Bearer jwt-token",
			expectedPath:      "/api/v2/users/me",
			statusCode:        http.StatusOK,
			responseBody:      `{"status":"success","data":{"id":"user-123","displayName":"Ada Lovelace","email":"user@example.com","orgId":"org-123"}}`,
			expectedIdentity: &AnalyticsIdentity{
				OrgID:     "org-123",
				UserID:    "user-123",
				Name:      "Ada Lovelace",
				Email:     "user@example.com",
				Principal: "user",
			},
		},
		{
			name:              "api key auth resolves via service accounts me",
			authHeaderName:    "SIGNOZ-API-KEY",
			expectedHeader:    "SIGNOZ-API-KEY",
			expectedHeaderVal: "test-api-key",
			expectedPath:      "/api/v1/service_accounts/me",
			statusCode:        http.StatusOK,
			responseBody:      `{"status":"success","data":{"id":"sa-123","name":"ingest-bot","email":"service@example.com","orgId":"org-456"}}`,
			expectedIdentity: &AnalyticsIdentity{
				OrgID:     "org-456",
				UserID:    "sa-123",
				Name:      "ingest-bot",
				Email:     "service@example.com",
				Principal: "service_account",
			},
		},
		{
			name:              "authorization auth does not fall back from v2 users me",
			authHeaderName:    "Authorization",
			expectedHeader:    "Authorization",
			expectedHeaderVal: "Bearer jwt-token",
			expectedPath:      "/api/v2/users/me",
			statusCode:        http.StatusNotFound,
			responseBody:      `{"status":"error"}`,
			checkErr: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "unexpected status 404")
			},
		},
		{
			name:              "unauthorized identity lookup returns auth error",
			authHeaderName:    "SIGNOZ-API-KEY",
			expectedHeader:    "SIGNOZ-API-KEY",
			expectedHeaderVal: "test-api-key",
			expectedPath:      "/api/v1/service_accounts/me",
			statusCode:        http.StatusUnauthorized,
			responseBody:      `{"status":"error"}`,
			checkErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrUnauthorized)
			},
		},
		{
			name:              "malformed success response returns parse error",
			authHeaderName:    "SIGNOZ-API-KEY",
			expectedHeader:    "SIGNOZ-API-KEY",
			expectedHeaderVal: "test-api-key",
			expectedPath:      "/api/v1/service_accounts/me",
			statusCode:        http.StatusOK,
			responseBody:      `{"status":"success","data":{"orgId":"org-123"}}`,
			checkErr: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "missing data.id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, tt.expectedPath, r.URL.Path)
				assert.Equal(t, tt.expectedHeaderVal, r.Header.Get(tt.expectedHeader))

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			apiKey := "test-api-key"
			if tt.authHeaderName == "Authorization" {
				apiKey = "Bearer jwt-token"
			}
			client := NewClient(logger, server.URL, apiKey, tt.authHeaderName, nil)

			identity, err := client.GetAnalyticsIdentity(context.Background())

			if tt.checkErr != nil {
				assert.Error(t, err)
				tt.checkErr(t, err)
				assert.Nil(t, identity)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedIdentity, identity)
			}
			assert.Equal(t, 1, requests, "expected exactly one identity request")
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

func TestDoRequest_RetryLogsDebugThenWarn(t *testing.T) {
	var logBuf bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error","message":"temporary outage"}`))
	}))
	defer server.Close()

	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 503")

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	var sawRetryDebug bool
	var sawTerminalWarn bool
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		switch rec["msg"] {
		case "Retryable status, will retry":
			assert.Equal(t, "DEBUG", rec["level"])
			sawRetryDebug = true
		case "SigNoz request returned unexpected status":
			assert.Equal(t, "WARN", rec["level"])
			assert.Equal(t, true, rec["retryable"])
			assert.Equal(t, true, rec["retries_exhausted"])
			sawTerminalWarn = true
		}
	}

	assert.True(t, sawRetryDebug, "expected intermediate retry log at DEBUG")
	assert.True(t, sawTerminalWarn, "expected terminal retry exhaustion log at WARN")
}

func TestDoRequest_SucceedsAfterRetryWithoutRetriesExhaustedLog(t *testing.T) {
	var logBuf bytes.Buffer
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"error","message":"temporary outage"}`))
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
	var sawRetryDebug bool
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		switch rec["msg"] {
		case "Retryable status, will retry":
			sawRetryDebug = true
		case "SigNoz request returned unexpected status":
			t.Fatalf("unexpected terminal warn log on eventual success: %v", rec)
		}
		if _, ok := rec["retries_exhausted"]; ok {
			t.Fatalf("unexpected retries_exhausted field on eventual success path: %v", rec)
		}
	}

	assert.True(t, sawRetryDebug, "expected intermediate retry log before success")
}

func TestDoRequest_NonRetryableStatusOmitsRetriesExhausted(t *testing.T) {
	var logBuf bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","message":"bad request"}`))
	}))
	defer server.Close()

	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 400")

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	var sawTerminalWarn bool
	var sawRetryDebug bool
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		if rec["msg"] == "Retryable status, will retry" {
			sawRetryDebug = true
		}
		if rec["msg"] != "SigNoz request returned unexpected status" {
			continue
		}
		assert.Equal(t, "WARN", rec["level"])
		assert.Equal(t, false, rec["retryable"])
		if _, ok := rec["retries_exhausted"]; ok {
			t.Fatalf("unexpected retries_exhausted field in non-retryable log: %v", rec)
		}
		sawTerminalWarn = true
	}

	assert.False(t, sawRetryDebug, "did not expect retry log for non-retryable status")
	assert.True(t, sawTerminalWarn, "expected terminal non-retryable warning log")
}

func TestDoRequest_NonRetryableStatusReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource"}}`))
	}))
	defer server.Close()

	client := NewClient(logpkg.New("error"), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.doRequest(context.Background(), http.MethodPost, server.URL, strings.NewReader(`{}`), time.Second)
	require.Error(t, err)

	var statusErr *HTTPStatusError
	require.True(t, errors.As(err, &statusErr), "expected HTTPStatusError, got %T: %v", err, err)
	assert.Equal(t, http.StatusForbidden, statusErr.StatusCode)
	assert.Contains(t, statusErr.Body, "authz_forbidden")
	assert.Contains(t, err.Error(), "unexpected status 403")
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

	_, err = client.doRequest(context.Background(), http.MethodPost, server.URL, strings.NewReader(`{}`), time.Second)
	require.Error(t, err)

	var statusErr *HTTPStatusError
	require.True(t, errors.As(err, &statusErr), "expected HTTPStatusError, got %T: %v", err, err)
	assert.Equal(t, http.StatusForbidden, statusErr.StatusCode)
	assert.True(t, json.Valid([]byte(statusErr.Body)), "stored body should remain parseable JSON")
	assert.Contains(t, statusErr.Body, longMessage)
	assert.Contains(t, err.Error(), "...(truncated)")
	assert.NotContains(t, err.Error(), "tail")

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	require.NotEmpty(t, lines)
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &rec))
	assert.Equal(t, "SigNoz request returned unexpected status", rec["msg"])
	response, _ := rec["response"].(string)
	assert.Contains(t, response, "...(truncated)")
	assert.NotContains(t, response, "tail")
}

func TestListMetricKeys(t *testing.T) {
	tests := []struct {
		name          string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedData  []string
	}{
		{
			name: "successful metric keys retrieval",
			resp: map[string]interface{}{
				"status": "success",
				"data": []string{
					"cpu_data",
					"memory_data",
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: []string{
				"cpu_data",
				"memory_data",
			},
		},
		{
			name:          "server error",
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "unauthorized",
			resp:          map[string]interface{}{"status": "error", "message": "Unauthorized"},
			statusCode:    http.StatusUnauthorized,
			expectedError: true,
		},
		{
			name:          "empty response",
			resp:          map[string]interface{}{"status": "success", "data": []string{}},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/metrics/filters/keys", r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.ListMetricKeys(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)

				assert.Equal(t, "success", response["status"])
				if data, ok := response["data"].([]interface{}); ok {
					assert.Equal(t, len(tt.expectedData), len(data))
					for i, expectedKey := range tt.expectedData {
						if i < len(data) {
							assert.Equal(t, expectedKey, data[i])
						}
					}
				}
			}
		})
	}
}

func TestListDashboards(t *testing.T) {
	t.Run("forwards v2 list with pagination params", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/api/v2/dashboards", r.URL.Path)
			assert.Equal(t, "25", r.URL.Query().Get("limit"))
			assert.Equal(t, "50", r.URL.Query().Get("offset"))
			assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
			w.WriteHeader(http.StatusOK)
			v2Resp := `{"dashboards":[{"id":"dashboard-uuid-1","name":"apple","spec":{"display":{"name":"Apple Dashboard"}}}],"tags":[],"total":1}`
			_, _ = w.Write([]byte(v2Resp))
		}))
		defer mockServer.Close()

		client := NewClient(logpkg.New("debug"), mockServer.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
		result, err := client.ListDashboards(context.Background(), 25, 50)
		require.NoError(t, err)

		var response map[string]any
		require.NoError(t, json.Unmarshal(result, &response))
		assert.EqualValues(t, 1, response["total"])
		dashboards, ok := response["dashboards"].([]any)
		require.True(t, ok)
		assert.Len(t, dashboards, 1)
	})

	t.Run("server error", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		defer mockServer.Close()

		client := NewClient(logpkg.New("debug"), mockServer.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
		result, err := client.ListDashboards(context.Background(), 0, 0)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestListServices(t *testing.T) {
	tests := []struct {
		name          string
		start         string
		end           string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedData  []map[string]interface{}
	}{
		{
			name:  "successful services retrieval",
			start: "1640995200000000000",
			end:   "1641081600000000000",
			resp: map[string]interface{}{
				"status": "success",
				"data": []map[string]interface{}{
					{
						"serviceName": "frontend",
						"p99":         100.5,
						"avgDuration": 50.2,
						"numCalls":    1000.0,
					},
					{
						"serviceName": "backend",
						"p99":         200.3,
						"avgDuration": 75.8,
						"numCalls":    500.0,
					},
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: []map[string]interface{}{
				{
					"serviceName": "frontend",
					"p99":         100.5,
					"avgDuration": 50.2,
					"numCalls":    1000.0,
				},
				{
					"serviceName": "backend",
					"p99":         200.3,
					"avgDuration": 75.8,
					"numCalls":    500.0,
				},
			},
		},
		{
			name:          "server error",
			start:         "1640995200000000000",
			end:           "1641081600000000000",
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "unauthorized",
			start:         "1640995200000000000",
			end:           "1641081600000000000",
			resp:          map[string]interface{}{"status": "error", "message": "Unauthorized"},
			statusCode:    http.StatusUnauthorized,
			expectedError: true,
		},
		{
			name:          "empty response",
			start:         "1640995200000000000",
			end:           "1641081600000000000",
			resp:          map[string]interface{}{"status": "success", "data": []map[string]interface{}{}},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData:  []map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v1/services", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				var requestBody map[string]string
				err := json.NewDecoder(r.Body).Decode(&requestBody)
				require.NoError(t, err)
				assert.Equal(t, tt.start, requestBody["start"])
				assert.Equal(t, tt.end, requestBody["end"])

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.ListServices(ctx, tt.start, tt.end)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {

				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)

				assert.Equal(t, "success", response["status"])
				if data, ok := response["data"].([]interface{}); ok {
					assert.Equal(t, len(tt.expectedData), len(data))
					for i, expectedService := range tt.expectedData {
						if i < len(data) {
							if service, ok := data[i].(map[string]interface{}); ok {
								assert.Equal(t, expectedService["serviceName"], service["serviceName"])
								assert.Equal(t, expectedService["p99"], service["p99"])
								assert.Equal(t, expectedService["avgDuration"], service["avgDuration"])
								assert.Equal(t, expectedService["numCalls"], service["numCalls"])
							}
						}
					}
				}
			}
		})
	}
}

func TestGetAlertHistory(t *testing.T) {
	tests := []struct {
		name          string
		ruleID        string
		request       types.AlertHistoryRequest
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedItems int
	}{
		{
			name:   "successful alert history retrieval with state and filter",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start:            1640995200000,
				End:              1641081600000,
				State:            "firing",
				FilterExpression: "severity = 'warning'",
				Limit:            20,
				Order:            "desc",
			},
			resp: map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"items": []map[string]interface{}{
						{"ruleId": "ruleid-abc", "state": "firing", "value": 85.5, "unixMilli": 1640995200000},
						{"ruleId": "ruleid-abc", "state": "inactive", "value": 45.2, "unixMilli": 1640998800000},
					},
					"total":      2,
					"nextCursor": "",
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedItems: 2,
		},
		{
			name:   "cursor paginated request",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start:  1640995200000,
				End:    1641081600000,
				Limit:  20,
				Order:  "desc",
				Cursor: "eyJvZmZzZXQiOjIwLCJsaW1pdCI6MjB9",
			},
			resp: map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"items":      []map[string]interface{}{},
					"total":      20,
					"nextCursor": "",
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedItems: 0,
		},
		{
			name:   "server error",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start: 1640995200000,
				End:   1641081600000,
				Limit: 20,
				Order: "desc",
			},
			resp:          map[string]interface{}{"status": "error", "error": map[string]interface{}{"message": "Internal server error"}},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:   "rule not found",
			ruleID: "non-existent-rule",
			request: types.AlertHistoryRequest{
				Start: 1640995200000,
				End:   1641081600000,
				Limit: 20,
				Order: "desc",
			},
			resp:          map[string]interface{}{"status": "error", "error": map[string]interface{}{"message": "Rule not found"}},
			statusCode:    http.StatusNotFound,
			expectedError: true,
		},
		{
			name:   "empty response",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start: 1640995200000,
				End:   1641081600000,
				Limit: 20,
				Order: "desc",
			},
			resp:          map[string]interface{}{"status": "success", "data": map[string]interface{}{"items": []map[string]interface{}{}, "total": 0}},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedItems: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// v2 timeline is a GET; params ride on the query string, not a body.
				assert.Equal(t, http.MethodGet, r.Method)
				expectedPath := fmt.Sprintf("/api/v2/rules/%s/history/timeline", tt.ruleID)
				assert.Equal(t, expectedPath, r.URL.Path)
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				q := r.URL.Query()
				assert.Equal(t, fmt.Sprintf("%d", tt.request.Start), q.Get("start"))
				assert.Equal(t, fmt.Sprintf("%d", tt.request.End), q.Get("end"))
				assert.Equal(t, fmt.Sprintf("%d", tt.request.Limit), q.Get("limit"))
				assert.Equal(t, tt.request.Order, q.Get("order"))
				// Optional params are omitted when empty (server applies defaults).
				assert.Equal(t, tt.request.State, q.Get("state"))
				assert.Equal(t, tt.request.FilterExpression, q.Get("filterExpression"))
				assert.Equal(t, tt.request.Cursor, q.Get("cursor"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.GetAlertHistory(ctx, tt.ruleID, tt.request)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)

				assert.Equal(t, "success", response["status"])
				data, ok := response["data"].(map[string]interface{})
				require.True(t, ok, "expected v2 data object with items[]")
				items, _ := data["items"].([]interface{})
				assert.Equal(t, tt.expectedItems, len(items))
			}
		})
	}
}

func TestQueryBuilderV5(t *testing.T) {
	tests := []struct {
		name          string
		queryBody     []byte
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedData  map[string]interface{}
	}{
		{
			name: "successful query execution",
			queryBody: []byte(`{
				"schemaVersion": "v1",
				"start": 1640995200000,
				"end": 1641081600000,
				"requestType": "raw",
				"compositeQuery": {
					"queries": [{
						"type": "builder_query",
						"spec": {
							"name": "A",
							"signal": "traces",
							"disabled": false,
							"limit": 10,
							"offset": 0,
							"order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
							"having": {"expression": ""},
							"selectFields": [
								{"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
								{"name": "duration_nano", "fieldDataType": "", "signal": "traces", "fieldContext": "span"}
							]
						}
					}]
				},
				"formatOptions": {
					"formatTableResultForUI": false,
					"fillGaps": false
				},
				"variables": {}
			}`),
			resp: map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"result": []map[string]interface{}{
						{
							"service.name":  "frontend",
							"duration_nano": 150000000,
							"timestamp":     "2022-01-01T10:00:00Z",
						},
						{
							"service.name":  "backend",
							"duration_nano": 250000000,
							"timestamp":     "2022-01-01T10:01:00Z",
						},
					},
					"total": 2,
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: map[string]interface{}{
				"result": []map[string]interface{}{
					{
						"service.name":  "frontend",
						"duration_nano": 150000000.0,
						"timestamp":     "2022-01-01T10:00:00Z",
					},
					{
						"service.name":  "backend",
						"duration_nano": 250000000.0,
						"timestamp":     "2022-01-01T10:01:00Z",
					},
				},
				"total": 2.0,
			},
		},
		{
			name:          "server error",
			queryBody:     []byte(`{"invalid": "query"}`),
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "invalid query",
			queryBody:     []byte(`{"invalid": "query"}`),
			resp:          map[string]interface{}{"status": "error", "message": "Invalid query format"},
			statusCode:    http.StatusBadRequest,
			expectedError: true,
		},
		{
			name:      "empty response",
			queryBody: []byte(`{"schemaVersion": "v1", "start": 1640995200000, "end": 1641081600000, "requestType": "raw", "compositeQuery": {"queries": []}, "formatOptions": {"formatTableResultForUI": false, "fillGaps": false}, "variables": {}}`),
			resp: map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"result": []map[string]interface{}{},
					"total":  0,
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: map[string]interface{}{
				"result": []map[string]interface{}{},
				"total":  0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v5/query_range", r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, tt.queryBody, body)

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.QueryBuilderV5(ctx, tt.queryBody)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)

				assert.Equal(t, "success", response["status"])
				if data, ok := response["data"].(map[string]interface{}); ok {
					assert.Equal(t, tt.expectedData["total"], data["total"])
					if result, ok := data["result"].([]interface{}); ok {
						expectedResult := tt.expectedData["result"].([]map[string]interface{})
						assert.Equal(t, len(expectedResult), len(result))
						for i, expectedItem := range expectedResult {
							if i < len(result) {
								if item, ok := result[i].(map[string]interface{}); ok {
									assert.Equal(t, expectedItem["service.name"], item["service.name"])
									assert.Equal(t, expectedItem["duration_nano"], item["duration_nano"])
									assert.Equal(t, expectedItem["timestamp"], item["timestamp"])
								}
							}
						}
					}
				}
			}
		})
	}
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
	require.Contains(t, payload, `"limit":1000`)
	require.Contains(t, payload, `"order":[{"key":{"name":"timestamp"},"direction":"desc"}]`)
	require.NotContains(t, payload, `"expression":"traceID = 'abc123'"`)
}

func TestCreateDashboardRaw(t *testing.T) {
	// Pass-through: the client forwards the raw v6 body to POST /api/v2/dashboards
	// and returns the response body verbatim.
	payload := []byte(`{"schemaVersion":"v6","generateName":true,"tags":[],"spec":{"display":{"name":"x"},"variables":[],"panels":{},"layouts":[]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v2/dashboards", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		got, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, string(payload), string(got))

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"dashboard-123"}`))
	}))
	defer server.Close()

	client := NewClient(logpkg.New("debug"), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	resp, err := client.CreateDashboardRaw(context.Background(), payload)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(resp, &out))
	assert.Equal(t, "dashboard-123", out["id"])
}

func TestUpdateDashboardRaw(t *testing.T) {
	payload := []byte(`{"schemaVersion":"v6","name":"x","tags":[],"spec":{"display":{"name":"x"},"variables":[],"panels":{},"layouts":[]}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v2/dashboards/id-123", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
		got, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, string(payload), string(got))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"id-123"}`))
	}))
	defer srv.Close()

	client := NewClient(logpkg.New("debug"), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	resp, err := client.UpdateDashboardRaw(context.Background(), "id-123", payload)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"id-123"}`, string(resp))
}

func TestPatchDashboardRaw(t *testing.T) {
	patch := []byte(`[{"op":"replace","path":"/spec/display/name","value":"new"}]`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/v2/dashboards/id-123", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))
		got, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, string(patch), string(got))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"id-123"}`))
	}))
	defer srv.Close()

	client := NewClient(logpkg.New("debug"), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	resp, err := client.PatchDashboardRaw(context.Background(), "id-123", patch)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"id-123"}`, string(resp))
}

func TestDeleteDashboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v2/dashboards/dash-456", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(logpkg.New("debug"), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)
	err := client.DeleteDashboard(context.Background(), "dash-456")
	require.NoError(t, err)
}

func TestGetFieldKeys(t *testing.T) {
	tests := []struct {
		name          string
		signal        string
		metricName    string
		searchText    string
		fieldContext  string
		fieldDataType string
		source        string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
	}{
		{
			name:          "successful retrieval with all params",
			signal:        "metrics",
			metricName:    "container.cpu.usage",
			searchText:    "cpu",
			fieldContext:  "resource",
			fieldDataType: "string",
			source:        "otel",
			resp: map[string]interface{}{
				"status": "success",
				"data":   []string{"host.name", "k8s.pod.name"},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
		},
		{
			name:          "successful retrieval with only required param",
			signal:        "traces",
			metricName:    "",
			searchText:    "",
			fieldContext:  "",
			fieldDataType: "",
			source:        "",
			resp: map[string]interface{}{
				"status": "success",
				"data":   []string{"service.name", "http.method"},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
		},
		{
			name:          "server error",
			signal:        "logs",
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "unauthorized",
			signal:        "metrics",
			resp:          map[string]interface{}{"status": "error", "message": "Unauthorized"},
			statusCode:    http.StatusUnauthorized,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/fields/keys", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				q := r.URL.Query()
				assert.Equal(t, tt.signal, q.Get("signal"))
				assert.Equal(t, tt.metricName, q.Get("metricName"))
				assert.Equal(t, tt.searchText, q.Get("searchText"))
				assert.Equal(t, tt.fieldContext, q.Get("fieldContext"))
				assert.Equal(t, tt.fieldDataType, q.Get("fieldDataType"))
				assert.Equal(t, tt.source, q.Get("source"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.GetFieldKeys(ctx, tt.signal, tt.metricName, tt.searchText, tt.fieldContext, tt.fieldDataType, tt.source)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)

				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)
				assert.Equal(t, "success", response["status"])
			}
		})
	}
}

func TestGetFieldValues(t *testing.T) {
	tests := []struct {
		name          string
		signal        string
		fieldName     string
		metricName    string
		searchText    string
		fieldContext  string
		source        string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
	}{
		{
			name:         "successful retrieval with all params",
			signal:       "metrics",
			fieldName:    "host.name",
			metricName:   "container.cpu.usage",
			searchText:   "prod",
			fieldContext: "resource",
			source:       "otel",
			resp: map[string]interface{}{
				"status": "success",
				"data":   []string{"prod-host-1", "prod-host-2"},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
		},
		{
			name:       "successful retrieval with only required params",
			signal:     "traces",
			fieldName:  "service.name",
			metricName: "",
			searchText: "",
			source:     "",
			resp: map[string]interface{}{
				"status": "success",
				"data":   []string{"frontend", "backend"},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
		},
		{
			name:          "server error",
			signal:        "logs",
			fieldName:     "severity",
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:          "unauthorized",
			signal:        "metrics",
			fieldName:     "host.name",
			resp:          map[string]interface{}{"status": "error", "message": "Unauthorized"},
			statusCode:    http.StatusUnauthorized,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/fields/values", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				q := r.URL.Query()
				assert.Equal(t, tt.signal, q.Get("signal"))
				assert.Equal(t, tt.fieldName, q.Get("name"))
				assert.Equal(t, tt.metricName, q.Get("metricName"))
				assert.Equal(t, tt.searchText, q.Get("searchText"))
				assert.Equal(t, tt.fieldContext, q.Get("fieldContext"))
				assert.Equal(t, tt.source, q.Get("source"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger := logpkg.New("debug")
			client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			ctx := context.Background()
			result, err := client.GetFieldValues(ctx, tt.signal, tt.fieldName, tt.metricName, tt.searchText, tt.fieldContext, tt.source)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)

				var response map[string]interface{}
				err = json.Unmarshal(result, &response)
				require.NoError(t, err)
				assert.Equal(t, "success", response["status"])
			}
		})
	}
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

func TestDoRequest_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	logger := logpkg.New("debug")
	c := NewClient(logger, srv.URL, "test-key", "SIGNOZ-API-KEY", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := c.doRequest(ctx, http.MethodGet, srv.URL+"/test", nil, DefaultQueryTimeout)
	assert.Error(t, err)
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

func TestNewClient_NilHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Standard headers should still be set when custom headers map is nil
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.ListAlerts(context.Background(), types.ListAlertsParams{})
	assert.NoError(t, err)
}

func TestNewClient_EmptyHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer server.Close()

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", map[string]string{})

	_, err := client.ListAlerts(context.Background(), types.ListAlertsParams{})
	assert.NoError(t, err)
}

func TestNewClient_ReservedHeadersSkipped(t *testing.T) {
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

	logger := logpkg.New("debug")
	client := NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", customHeaders)

	_, err := client.ListAlerts(context.Background(), types.ListAlertsParams{})
	assert.NoError(t, err)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.TestNotificationChannel(context.Background(), []byte(`{"name":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/channels/test", gotPath)
}

func TestUpdateNotificationChannel_Returns204(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client := NewClient(logpkg.New("debug"), srv.URL, "k", "SIGNOZ-API-KEY", nil)

	err := client.UpdateNotificationChannel(context.Background(), "42", []byte(`{"name":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/channels/42", gotPath)
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

	err := client.DeleteNotificationChannel(context.Background(), "42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/channels/42", gotPath)
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
	_, err := c.ListViews(context.Background(), "traces", "ak", "ops")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/explorer/views", gotPath)
	assert.Contains(t, gotRawQuery, "sourcePage=traces")
	assert.Contains(t, gotRawQuery, "name=ak")
	assert.Contains(t, gotRawQuery, "category=ops")
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
	assert.Equal(t, "/api/v1/explorer/views/view-uuid-1", gotPath)
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
	body := []byte(`{"name":"x","sourcePage":"traces","compositeQuery":{}}`)
	_, err := c.CreateView(context.Background(), body)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/explorer/views", gotPath)
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
	assert.Equal(t, "/api/v1/explorer/views/view-1", gotPath)
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
	assert.Equal(t, "/api/v1/explorer/views/view-1", gotPath)
}

func TestSharedTransportPoolTuning(t *testing.T) {
	// Idle-connection limits are raised above the stdlib defaults (per-host 2,
	// total 100) so a multi-tenant server reuses keep-alive connections under
	// concurrency instead of re-handshaking. Every other test in this file already
	// drives requests through sharedTransport (NewClient wires it in), so this just
	// guards the tuned values and that the transport is a proper DefaultTransport clone.
	require.Equal(t, 20, sharedTransport.MaxIdleConnsPerHost, "MaxIdleConnsPerHost")
	require.Equal(t, 200, sharedTransport.MaxIdleConns, "MaxIdleConns")
	require.NotZero(t, sharedTransport.TLSHandshakeTimeout, "cloned DefaultTransport: TLSHandshakeTimeout preserved")
	require.NotNil(t, sharedTransport.DialContext, "cloned DefaultTransport: DialContext preserved")
}

// TestDoRequest_RejectsOversizeResponse verifies the response-size guard: a
// backend response larger than maxResponseBytes is rejected with a clear error
// (never silently truncated into invalid JSON), bounding single-request memory
// on the shared multi-tenant pod.
func TestDoRequest_RejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Stream just past the cap without buffering it all server-side.
		chunk := bytes.Repeat([]byte("a"), 1<<20) // 1 MiB
		var written int64
		for written <= maxResponseBytes {
			n, err := w.Write(chunk)
			if err != nil {
				return
			}
			written += int64(n)
		}
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	client := NewClient(newBufferedLogger(&logBuf, slog.LevelDebug), server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

	_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
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
