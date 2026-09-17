package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

// twoPipelinesResponse is the wrapped envelope the query-service returns for
// GET /api/v1/logs/pipelines/latest: agent-config-version fields flattened
// alongside "pipelines" and "history".
const twoPipelinesResponse = `{
  "status": "success",
  "data": {
    "id": "cfg-1",
    "version": 7,
    "deployStatus": "DEPLOYED",
    "deploySequence": 3,
    "pipelines": [
      {
        "id": "p-nginx",
        "orderId": 1,
        "enabled": true,
        "name": "Nginx logs",
        "alias": "nginx-logs",
        "description": "parse nginx access logs",
        "filter": {"op": "AND", "items": [{"key": {"key": "service.name"}, "op": "=", "value": "nginx"}]},
        "config": [
          {"type": "regex_parser", "id": "op-1", "orderId": 1, "enabled": true, "name": "parse", "regex": "^(?P<code>\\d+)", "parse_from": "body", "parse_to": "attributes", "on_error": "send"},
          {"type": "add", "id": "op-2", "orderId": 2, "enabled": true, "name": "tag", "field": "attributes.tier", "value": "edge"}
        ]
      },
      {
        "id": "p-app",
        "orderId": 2,
        "enabled": false,
        "name": "App logs",
        "alias": "app-logs",
        "description": "",
        "filter": {"op": "AND", "items": []},
        "config": [
          {"type": "json_parser", "id": "op-3", "orderId": 1, "enabled": true, "name": "json"}
        ]
      }
    ],
    "history": [{"version": 6}]
  }
}`

func pipelinesMock(body string) *client.MockClient {
	return &client.MockClient{
		GetLogPipelinesFn: func(ctx context.Context, version string) (json.RawMessage, error) {
			return json.RawMessage(body), nil
		},
	}
}

func TestHandleListLogPipelines_ReturnsSummariesNotOperatorConfig(t *testing.T) {
	var capturedVersion string
	mock := &client.MockClient{
		GetLogPipelinesFn: func(ctx context.Context, version string) (json.RawMessage, error) {
			capturedVersion = version
			return json.RawMessage(twoPipelinesResponse), nil
		},
	}
	h := newTestHandler(mock)

	result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}
	if capturedVersion != "latest" {
		t.Fatalf("version = %q, want %q", capturedVersion, "latest")
	}

	body := textContent(t, result)
	for _, want := range []string{`"id":"p-nginx"`, `"name":"Nginx logs"`, `"alias":"nginx-logs"`, `"operatorCount":2`, `"operatorCount":1`, `"version":7`, `"deployStatus":"DEPLOYED"`, `"pipelinesFieldPresent":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("list body missing %s: %s", want, body)
		}
	}
	// The whole point of the two-tool split: the list must not carry the
	// operator chain or the filter set.
	for _, forbidden := range []string{"regex_parser", "json_parser", "parse_from", `"config"`, `"filter"`, `"history"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list body must not contain %s: %s", forbidden, body)
		}
	}
}

func TestHandleListLogPipelines_Paginates(t *testing.T) {
	h := newTestHandler(pipelinesMock(twoPipelinesResponse))

	result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{
		"limit":  "1",
		"offset": "1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"id":"p-app"`) {
		t.Fatalf("offset=1 page should hold the second pipeline: %s", body)
	}
	if strings.Contains(body, `"id":"p-nginx"`) {
		t.Fatalf("offset=1 page must not hold the first pipeline: %s", body)
	}
	if !strings.Contains(body, `"total":2`) || !strings.Contains(body, `"hasMore":false`) {
		t.Fatalf("unexpected pagination metadata: %s", body)
	}
}

func TestHandleListLogPipelines_EnabledOnly(t *testing.T) {
	tests := []struct {
		name        string
		enabledOnly any
		wantIDs     []string
		wantMissing []string
	}{
		{"absent returns all", nil, []string{"p-nginx", "p-app"}, nil},
		{"string true filters", "true", []string{"p-nginx"}, []string{"p-app"}},
		{"bool true filters", true, []string{"p-nginx"}, []string{"p-app"}},
		{"false returns all", "false", []string{"p-nginx", "p-app"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(pipelinesMock(twoPipelinesResponse))
			args := map[string]any{}
			if tt.enabledOnly != nil {
				args["enabledOnly"] = tt.enabledOnly
			}
			result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handler returned error result: %s", textContent(t, result))
			}
			body := textContent(t, result)
			for _, id := range tt.wantIDs {
				if !strings.Contains(body, `"id":"`+id+`"`) {
					t.Fatalf("body missing pipeline %s: %s", id, body)
				}
			}
			for _, id := range tt.wantMissing {
				if strings.Contains(body, `"id":"`+id+`"`) {
					t.Fatalf("body must not contain pipeline %s: %s", id, body)
				}
			}
		})
	}
}

func TestHandleListLogPipelines_InvalidEnabledOnly(t *testing.T) {
	h := newTestHandler(pipelinesMock(twoPipelinesResponse))
	result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{
		"enabledOnly": "yes-please",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a validation error result")
	}
	if code := resultStructuredMap(t, result)["code"]; code != CodeValidationFailed {
		t.Fatalf("code = %v, want %q", code, CodeValidationFailed)
	}
}

func TestHandleGetLogPipeline_ByIDReturnsFullConfig(t *testing.T) {
	h := newTestHandler(pipelinesMock(twoPipelinesResponse))
	result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"id": "p-nginx"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(textContent(t, result)), &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if got["id"] != "p-nginx" {
		t.Fatalf("id = %v, want p-nginx", got["id"])
	}
	cfg, ok := got["config"].([]any)
	if !ok || len(cfg) != 2 {
		t.Fatalf("expected the complete 2-operator config chain, got %#v", got["config"])
	}
	first, ok := cfg[0].(map[string]any)
	if !ok || first["type"] != "regex_parser" || first["parse_from"] != "body" {
		t.Fatalf("operator fields did not survive passthrough: %#v", cfg[0])
	}
	filter, ok := got["filter"].(map[string]any)
	if !ok || filter["op"] != "AND" {
		t.Fatalf("expected the full filter set, got %#v", got["filter"])
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent on the single-pipeline result")
	}
}

func TestHandleGetLogPipeline_ByName(t *testing.T) {
	tests := []struct {
		name   string
		lookup string
		wantID string
	}{
		{"exact name", "App logs", "p-app"},
		{"case-insensitive name", "app LOGS", "p-app"},
		{"alias", "nginx-logs", "p-nginx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(pipelinesMock(twoPipelinesResponse))
			result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"name": tt.lookup}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handler returned error result: %s", textContent(t, result))
			}
			if !strings.Contains(textContent(t, result), `"id":"`+tt.wantID+`"`) {
				t.Fatalf("expected pipeline %s, got: %s", tt.wantID, textContent(t, result))
			}
		})
	}
}

// id wins over name so the selector is unambiguous and testable.
func TestHandleGetLogPipeline_IDWinsOverName(t *testing.T) {
	h := newTestHandler(pipelinesMock(twoPipelinesResponse))
	result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{
		"id":   "p-nginx",
		"name": "App logs",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %s", textContent(t, result))
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"id":"p-nginx"`) || strings.Contains(body, `"id":"p-app"`) {
		t.Fatalf("id should win over name, got: %s", body)
	}
}

func TestHandleGetLogPipeline_RequiresIDOrName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{"both absent", map[string]any{}},
		{"both blank", map[string]any{"id": "", "name": "   "}},
		{"only searchContext", map[string]any{"searchContext": "show me the nginx pipeline"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(pipelinesMock(twoPipelinesResponse))
			result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a validation error result")
			}
			if code := resultStructuredMap(t, result)["code"]; code != CodeValidationFailed {
				t.Fatalf("code = %v, want %q", code, CodeValidationFailed)
			}
		})
	}
}

func TestHandleGetLogPipeline_NotFoundListsAvailable(t *testing.T) {
	h := newTestHandler(pipelinesMock(twoPipelinesResponse))
	result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"name": "Ghost logs"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("a missing pipeline must be an error, not an empty success")
	}
	if code := resultStructuredMap(t, result)["code"]; code != CodeNotFound {
		t.Fatalf("code = %v, want %q", code, CodeNotFound)
	}
	msg := textContent(t, result)
	for _, want := range []string{"Ghost logs", "Nginx logs", "p-nginx", "App logs", "p-app"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("not-found message should name %q so the agent can self-correct: %s", want, msg)
		}
	}
}

func TestHandleGetLogPipeline_NotFoundWhenNoneConfigured(t *testing.T) {
	h := newTestHandler(pipelinesMock(`{"status":"success","data":{"version":1,"pipelines":[]}}`))
	result, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"id": "p-nginx"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := resultStructuredMap(t, result)["code"]; code != CodeNotFound {
		t.Fatalf("code = %v, want %q", code, CodeNotFound)
	}
	if !strings.Contains(textContent(t, result), "no pipelines configured") {
		t.Fatalf("unexpected message: %s", textContent(t, result))
	}
}

// A legacy/unwrapped {...} body (older builds, some proxies) must still parse.
func TestHandleLogPipelines_UnwrappedLegacyEnvelope(t *testing.T) {
	legacy := `{"version":4,"deployStatus":"IN_PROGRESS","pipelines":[{"id":"p-legacy","name":"Legacy","enabled":true,"orderId":1,"config":[{"type":"add","id":"o-1"}]}]}`

	h := newTestHandler(pipelinesMock(legacy))
	listResult, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("legacy envelope must still parse: %s", textContent(t, listResult))
	}
	body := textContent(t, listResult)
	if !strings.Contains(body, `"id":"p-legacy"`) || !strings.Contains(body, `"version":4`) || !strings.Contains(body, `"pipelinesFieldPresent":true`) {
		t.Fatalf("unexpected legacy list body: %s", body)
	}

	h = newTestHandler(pipelinesMock(legacy))
	getResult, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"id": "p-legacy"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if getResult.IsError {
		t.Fatalf("legacy envelope must still parse for get: %s", textContent(t, getResult))
	}
	if !strings.Contains(textContent(t, getResult), `"type":"add"`) {
		t.Fatalf("expected the full config chain from the legacy envelope: %s", textContent(t, getResult))
	}
}

// A genuinely empty pipeline list must be distinguishable from an upstream shape
// change: pipelinesFieldPresent is true for the former, false for the latter.
func TestHandleListLogPipelines_EmptyVsShapeChange(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantPresent string
	}{
		{"empty array", `{"status":"success","data":{"version":2,"pipelines":[]}}`, `"pipelinesFieldPresent":true`},
		{"explicit null", `{"status":"success","data":{"version":2,"pipelines":null}}`, `"pipelinesFieldPresent":true`},
		{"key renamed upstream", `{"status":"success","data":{"version":2,"logPipelines":[]}}`, `"pipelinesFieldPresent":false`},
		{"non-array value", `{"status":"success","data":{"version":2,"pipelines":{"p-1":{}}}}`, `"pipelinesFieldPresent":false`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(pipelinesMock(tt.body))
			result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("parse must fail open, got error result: %s", textContent(t, result))
			}
			body := textContent(t, result)
			if !strings.Contains(body, tt.wantPresent) {
				t.Fatalf("body missing %s: %s", tt.wantPresent, body)
			}
			if !strings.Contains(body, `"total":0`) {
				t.Fatalf("expected zero items, got: %s", body)
			}
		})
	}
}

func TestHandleLogPipelines_UnparseableBody(t *testing.T) {
	h := newTestHandler(pipelinesMock(`not json at all`))

	result, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result for an unparseable body")
	}

	h = newTestHandler(pipelinesMock(`[1,2,3]`))
	result, err = h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"id": "p-1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result for a non-object body")
	}
}

func TestHandleLogPipelines_UpstreamErrorPropagates(t *testing.T) {
	mock := &client.MockClient{
		GetLogPipelinesFn: func(ctx context.Context, version string) (json.RawMessage, error) {
			return nil, errors.New("boom")
		},
	}
	h := newTestHandler(mock)

	listRes, err := h.handleListLogPipelines(testCtx(), makeToolRequest("signoz_list_log_pipelines", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !listRes.IsError {
		t.Fatalf("expected an upstream error result")
	}

	getRes, err := h.handleGetLogPipeline(testCtx(), makeToolRequest("signoz_get_log_pipeline", map[string]any{"id": "p-1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !getRes.IsError {
		t.Fatalf("expected an upstream error result")
	}
}
