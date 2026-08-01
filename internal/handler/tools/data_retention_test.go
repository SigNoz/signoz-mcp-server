package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataRetentionTool_AdvertisesScopeAndExample(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))
	h.RegisterDataRetentionHandlers(s)

	registered, ok := s.ListTools()["signoz_get_data_retention"]
	require.True(t, ok)
	assert.Contains(t, registered.Tool.Description, "newly ingested data")
	assert.Contains(t, registered.Tool.Description, "currentStateKnown")
	assert.Contains(t, registered.Tool.Description, "retention only")
	assert.Contains(t, registered.Tool.Description, "signoz_list_metrics")
	assert.Contains(t, registered.Tool.Description, `source="meter"`)
	assert.Contains(t, registered.Tool.Description, "Example arguments")

	outputSchema := string(outputSchemaJSON(registered.Tool))
	assert.Contains(t, outputSchema, "newly ingested data")
	assert.Contains(t, outputSchema, "Older data")
	assert.Contains(t, outputSchema, "active overrides are unknown")
}

func TestHandleGetDataRetention_ReturnsStructuredSnapshotAndSettingsURL(t *testing.T) {
	mock := &client.MockClient{
		GetDataRetentionFn: func(context.Context) (*client.DataRetention, error) {
			return &client.DataRetention{
				Metrics: client.RetentionPolicy{
					CurrentStateKnown:     true,
					CurrentRetentionHours: retentionHoursPtr(720),
					ChangeStatus:          "success",
				},
				Traces: client.RetentionPolicy{
					CurrentStateKnown:     true,
					CurrentRetentionHours: retentionHoursPtr(360),
					ChangeStatus:          "idle",
				},
				Logs: client.RetentionPolicy{
					CurrentStateKnown:     true,
					CurrentRetentionHours: retentionHoursPtr(360),
					ChangeStatus:          "success",
					CustomRules: []client.CustomRetentionRule{{
						Conditions:     []client.RetentionCondition{{Key: "service_name", Values: []string{"checkout"}}},
						RetentionHours: 720,
					}},
				},
			}, nil
		},
	}

	h := newTestHandler(mock)
	result, err := h.handleGetDataRetention(ctxWithURL(), requestWithNilArguments("signoz_get_data_retention"))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent)

	var output client.DataRetention
	require.NoError(t, json.Unmarshal([]byte(textContent(t, result)), &output))
	assert.Equal(t, "https://signoz.example.com/settings", output.WebURL)
	require.NotNil(t, output.Metrics.CurrentRetentionHours)
	assert.EqualValues(t, 720, *output.Metrics.CurrentRetentionHours)
	require.Len(t, output.Logs.CustomRules, 1)
	assert.Equal(t, "service_name", output.Logs.CustomRules[0].Conditions[0].Key)

	structuredJSON, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	assert.JSONEq(t, textContent(t, result), string(structuredJSON))
}

func TestHandleGetDataRetention_AuthzFailureReturnsTopLevelCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantCode   string
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantCode: CodeUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, wantCode: CodePermissionDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&client.MockClient{
				GetDataRetentionFn: func(context.Context) (*client.DataRetention, error) {
					return nil, fmt.Errorf("logs retention: %w", &client.HTTPStatusError{
						StatusCode: tc.statusCode,
						Body:       `{"status":"error","error":{"message":"denied"}}`,
					})
				},
			})

			result, err := h.handleGetDataRetention(testCtx(), makeToolRequest("signoz_get_data_retention", map[string]any{}))
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Equal(t, tc.wantCode, resultCode(t, result))
			assert.Equal(t, tc.statusCode, resultStructuredMap(t, result)["status"])
		})
	}
}

func retentionHoursPtr(value int64) *int64 {
	return &value
}
