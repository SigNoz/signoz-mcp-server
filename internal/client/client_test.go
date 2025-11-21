package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

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
				expectedPath := fmt.Sprintf("/api/v1/rules/%s", tt.ruleID)
				assert.Equal(t, expectedPath, r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

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

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

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
	tests := []struct {
		name          string
		resp          map[string]interface{}
		statusCode    int
		expectedError bool
		expectedData  []map[string]interface{}
	}{
		{
			name: "successful dashboards retrieval",
			resp: map[string]interface{}{
				"status": "success",
				"data": []map[string]interface{}{
					{
						"id": "dashboard-uuid-1",
						"data": map[string]interface{}{
							"title":       "Apple Dashboard",
							"description": "Apple monitoring",
							"tags":        []string{"system", "monitoring"},
						},
						"createdAt": "2024-01-01T00:00:00Z",
						"updatedAt": "2024-01-01T00:00:00Z",
					},
					{
						"id": "dashboard-uuid-2",
						"data": map[string]interface{}{
							"title":       "Orange Dashboard",
							"description": "Orange monitoring",
							"tags":        []string{"app", "performance"},
						},
						"createdAt": "2024-01-02T00:00:00Z",
						"updatedAt": "2024-01-02T00:00:00Z",
					},
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: []map[string]interface{}{
				{
					"uuid":        "dashboard-uuid-1",
					"name":        "Apple Dashboard",
					"description": "Apple monitoring",
					"tags":        []string{"system", "monitoring"},
					"createdAt":   "2024-01-01T00:00:00Z",
					"updatedAt":   "2024-01-01T00:00:00Z",
				},
				{
					"uuid":        "dashboard-uuid-2",
					"name":        "Orange Dashboard",
					"description": "Orange monitoring",
					"tags":        []string{"app", "performance"},
					"createdAt":   "2024-01-02T00:00:00Z",
					"updatedAt":   "2024-01-02T00:00:00Z",
				},
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
			resp:          map[string]interface{}{"status": "success", "data": []map[string]interface{}{}},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData:  []map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/dashboards", r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

			ctx := context.Background()
			result, err := client.ListDashboards(ctx)

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
					for i, expectedDashboard := range tt.expectedData {
						if i < len(data) {
							if dashboard, ok := data[i].(map[string]interface{}); ok {
								assert.Equal(t, expectedDashboard["uuid"], dashboard["uuid"])
								assert.Equal(t, expectedDashboard["name"], dashboard["name"])
								assert.Equal(t, expectedDashboard["description"], dashboard["description"])
							}
						}
					}
				}
			}
		})
	}
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

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

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
		expectedData  []map[string]interface{}
	}{
		{
			name:   "successful alert history retrieval",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start:  1640995200000,
				End:    1641081600000,
				Offset: 0,
				Limit:  20,
				Order:  "desc",
				Filters: types.AlertHistoryFilters{
					Items: []interface{}{},
					Op:    "AND",
				},
			},
			resp: map[string]interface{}{
				"status": "success",
				"data": []map[string]interface{}{
					{
						"timestamp": "2022-01-01T10:00:00Z",
						"state":     "firing",
						"value":     85.5,
						"labels": map[string]interface{}{
							"service":  "frontend",
							"severity": "warning",
						},
					},
					{
						"timestamp": "2022-01-01T11:00:00Z",
						"state":     "resolved",
						"value":     45.2,
						"labels": map[string]interface{}{
							"service":  "frontend",
							"severity": "warning",
						},
					},
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedData: []map[string]interface{}{
				{
					"timestamp": "2022-01-01T10:00:00Z",
					"state":     "firing",
					"value":     85.5,
					"labels": map[string]interface{}{
						"service":  "frontend",
						"severity": "warning",
					},
				},
				{
					"timestamp": "2022-01-01T11:00:00Z",
					"state":     "resolved",
					"value":     45.2,
					"labels": map[string]interface{}{
						"service":  "frontend",
						"severity": "warning",
					},
				},
			},
		},
		{
			name:   "server error",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start:  1640995200000,
				End:    1641081600000,
				Offset: 0,
				Limit:  20,
				Order:  "desc",
				Filters: types.AlertHistoryFilters{
					Items: []interface{}{},
					Op:    "AND",
				},
			},
			resp:          map[string]interface{}{"status": "error", "message": "Internal server error"},
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:   "rule not found",
			ruleID: "non-existent-rule",
			request: types.AlertHistoryRequest{
				Start:  1640995200000,
				End:    1641081600000,
				Offset: 0,
				Limit:  20,
				Order:  "desc",
				Filters: types.AlertHistoryFilters{
					Items: []interface{}{},
					Op:    "AND",
				},
			},
			resp:          map[string]interface{}{"status": "error", "message": "Rule not found"},
			statusCode:    http.StatusNotFound,
			expectedError: true,
		},
		{
			name:   "empty response",
			ruleID: "ruleid-abc",
			request: types.AlertHistoryRequest{
				Start:  1640995200000,
				End:    1641081600000,
				Offset: 0,
				Limit:  20,
				Order:  "desc",
				Filters: types.AlertHistoryFilters{
					Items: []interface{}{},
					Op:    "AND",
				},
			},
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
				expectedPath := fmt.Sprintf("/api/v1/rules/%s/history/timeline", tt.ruleID)
				assert.Equal(t, expectedPath, r.URL.Path)

				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

				var requestBody types.AlertHistoryRequest
				err := json.NewDecoder(r.Body).Decode(&requestBody)
				require.NoError(t, err)
				assert.Equal(t, tt.request.Start, requestBody.Start)
				assert.Equal(t, tt.request.End, requestBody.End)
				assert.Equal(t, tt.request.Offset, requestBody.Offset)
				assert.Equal(t, tt.request.Limit, requestBody.Limit)
				assert.Equal(t, tt.request.Order, requestBody.Order)

				w.WriteHeader(tt.statusCode)
				responseBody, _ := json.Marshal(tt.resp)
				_, _ = w.Write(responseBody)
			}))
			defer server.Close()

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

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
				if data, ok := response["data"].([]interface{}); ok {
					assert.Equal(t, len(tt.expectedData), len(data))
					for i, expectedHistory := range tt.expectedData {
						if i < len(data) {
							if history, ok := data[i].(map[string]interface{}); ok {
								assert.Equal(t, expectedHistory["timestamp"], history["timestamp"])
								assert.Equal(t, expectedHistory["state"], history["state"])
								assert.Equal(t, expectedHistory["value"], history["value"])
								if labels, ok := history["labels"].(map[string]interface{}); ok {
									expectedLabels := expectedHistory["labels"].(map[string]interface{})
									assert.Equal(t, expectedLabels["service"], labels["service"])
									assert.Equal(t, expectedLabels["severity"], labels["severity"])
								}
							}
						}
					}
				}
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

			logger, _ := zap.NewDevelopment()
			client := NewClient(logger, server.URL, "test-api-key")

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

func TestCreateDashboard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/dashboards", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-api-key", r.Header.Get("SIGNOZ-API-KEY"))

		var body types.Dashboard
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)

		assert.NotEmpty(t, body.Title)
		assert.NotNil(t, body.Layout)
		assert.NotNil(t, body.Widgets)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","id":"dashboard-123"}`))
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	client := NewClient(logger, server.URL, "test-api-key")

	d := types.Dashboard{
		Title:   "whatever",
		Layout:  []types.LayoutItem{},
		Widgets: []types.Widget{},
	}

	ctx := context.Background()
	resp, err := client.CreateDashboard(ctx, d)
	require.NoError(t, err)

	var out map[string]interface{}
	err = json.Unmarshal(resp, &out)
	require.NoError(t, err)

	assert.Equal(t, "success", out["status"])
	assert.Equal(t, "dashboard-123", out["id"])
}
