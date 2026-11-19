package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const (
	testLogPipelineID     = "019947a7-f200-7000-8000-000000000001"
	testLogPipelineSecond = "019947a7-f200-7000-8000-000000000002"
)

const logPipelinesResponseBody = `{"status":"success","data":{"pipelines":[
{"id":"` + testLogPipelineID + `","orderId":1,"enabled":true,"name":"Parse JSON bodies","alias":"parse-json","description":"Parses structured log bodies","createdAt":"2026-01-01T00:00:00Z","createdBy":"user","updatedAt":"2026-01-02T00:00:00Z","updatedBy":"user","filter":{"op":"AND","items":[{"key":{"key":"body","dataType":"string","type":"","isColumn":true,"isJSON":false},"value":".*","op":"regex"}]},"config":[{"type":"json_parser","id":"json-parser-1","orderId":1,"parse_from":"body","parse_to":"attributes"},{"type":"severity_parser","id":"severity-parser-1","orderId":2,"parse_from":"body.severity","overwrite_text":true}]},
{"id":"` + testLogPipelineSecond + `","orderId":2,"enabled":false,"name":"Drop debug","alias":"drop-debug","description":"","createdAt":"2026-01-01T00:00:00Z","createdBy":"user","updatedAt":"2026-01-02T00:00:00Z","updatedBy":"user","filter":null,"config":null}
]}}`

type logPipelineRequest struct {
	method string
	path   string
	apiKey string
}

func newLogPipelineToolServer(t *testing.T, status int, body string) (*mcp.Server, *[]logPipelineRequest) {
	t.Helper()
	var requests []logPipelineRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, logPipelineRequest{
			method: r.Method,
			path:   r.URL.Path,
			apiKey: r.Header.Get("SIGNOZ-API-KEY"),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil))
	server := newMCPTestServer()
	h.RegisterLogPipelineHandlers(server)
	return server, &requests
}

func TestListLogPipelines_ReturnsSummariesFromLatestConfiguration(t *testing.T) {
	server, requests := newLogPipelineToolServer(t, http.StatusOK, logPipelinesResponseBody)
	result := callRegisteredTool(t, server, "signoz_list_log_pipelines", `{}`)

	require.False(t, result.IsError, allTextBlocks(result))
	require.Len(t, *requests, 1)
	require.Equal(t, http.MethodGet, (*requests)[0].method)
	require.Equal(t, "/api/v1/logs/pipelines/latest", (*requests)[0].path)
	require.Equal(t, "test-api-key", (*requests)[0].apiKey)

	var payload struct {
		Pipelines []map[string]any `json:"pipelines"`
		Total     int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal([]byte(textContent(t, result)), &payload))
	require.Equal(t, 2, payload.Total)
	require.Len(t, payload.Pipelines, 2)

	first := payload.Pipelines[0]
	require.Equal(t, testLogPipelineID, first["id"])
	require.Equal(t, "Parse JSON bodies", first["name"])
	require.Equal(t, "parse-json", first["alias"])
	require.Equal(t, "Parses structured log bodies", first["description"])
	require.Equal(t, true, first["enabled"])
	require.Equal(t, float64(1), first["orderId"])
	require.Equal(t, float64(2), first["processorCount"])
	require.NotContains(t, first, "config")
	require.NotContains(t, first, "filter")

	second := payload.Pipelines[1]
	require.Equal(t, false, second["enabled"])
	require.Equal(t, float64(0), second["processorCount"])
}

func TestGetLogPipeline_ReturnsTheCompletePipelineByID(t *testing.T) {
	server, requests := newLogPipelineToolServer(t, http.StatusOK, logPipelinesResponseBody)
	result := callRegisteredTool(t, server, "signoz_get_log_pipeline", `{"id":"`+testLogPipelineID+`"}`)

	require.False(t, result.IsError, allTextBlocks(result))
	require.Len(t, *requests, 1)
	require.Equal(t, http.MethodGet, (*requests)[0].method)
	require.Equal(t, "/api/v1/logs/pipelines/latest", (*requests)[0].path)

	var pipeline map[string]any
	require.NoError(t, json.Unmarshal([]byte(textContent(t, result)), &pipeline))
	require.Equal(t, testLogPipelineID, pipeline["id"])
	require.Equal(t, "Parse JSON bodies", pipeline["name"])
	require.Equal(t, "Parses structured log bodies", pipeline["description"])
	require.Equal(t, "AND", pipeline["filter"].(map[string]any)["op"])

	processors, ok := pipeline["config"].([]any)
	require.True(t, ok)
	require.Len(t, processors, 2)
	require.Equal(t, "json_parser", processors[0].(map[string]any)["type"])
	require.Equal(t, "severity_parser", processors[1].(map[string]any)["type"])

	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, textContent(t, result), string(structured))
}

func TestGetLogPipeline_UnknownIDIsCodedNotFound(t *testing.T) {
	server, requests := newLogPipelineToolServer(t, http.StatusOK, logPipelinesResponseBody)
	result := callRegisteredTool(t, server, "signoz_get_log_pipeline", `{"id":"019947a7-f200-7000-8000-000000000999"}`)

	require.True(t, result.IsError)
	require.Equal(t, CodeNotFound, resultCode(t, result))
	require.Contains(t, resultText(t, result), "signoz_list_log_pipelines")
	require.Len(t, *requests, 1)
}

func TestListLogPipelines_EmptyConfigurationIsSuccessful(t *testing.T) {
	server, _ := newLogPipelineToolServer(t, http.StatusOK, `{"status":"success","data":{"pipelines":null}}`)
	result := callRegisteredTool(t, server, "signoz_list_log_pipelines", `{}`)

	require.False(t, result.IsError, allTextBlocks(result))
	require.JSONEq(t, `{"pipelines":[],"total":0}`, textContent(t, result))
}

func TestListLogPipelines_UpstreamPermissionFailureIsCoded(t *testing.T) {
	server, _ := newLogPipelineToolServer(t, http.StatusForbidden,
		`{"status":"error","error":{"code":"forbidden","type":"forbidden","message":"viewer role required","errors":[]}}`)
	result := callRegisteredTool(t, server, "signoz_list_log_pipelines", `{}`)

	require.True(t, result.IsError)
	require.Equal(t, CodePermissionDenied, resultCode(t, result))
	require.Equal(t, float64(http.StatusForbidden), resultStructuredMap(t, result)["status"])
}
