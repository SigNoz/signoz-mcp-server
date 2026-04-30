package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleDeleteDashboard_Success(t *testing.T) {
	// Simulate a create-then-delete flow: the mock "creates" a dashboard and
	// then the delete handler removes it by UUID.
	const createdUUID = "abc-123-def"

	created := false
	deleted := false

	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			created = true
			return json.RawMessage(fmt.Sprintf(`{"status":"success","data":{"uuid":"%s"}}`, createdUUID)), nil
		},
		DeleteDashboardFn: func(ctx context.Context, id string) error {
			if id != createdUUID {
				t.Errorf("expected uuid=%s, got %q", createdUUID, id)
			}
			deleted = true
			return nil
		},
	}

	h := newTestHandler(mock)

	// Step 1: create a dashboard
	createResult, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", map[string]any{
		"title":   "Temp Dashboard",
		"widgets": []any{},
		"layout":  []any{},
	}))
	if err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}
	if createResult.IsError {
		t.Fatalf("create returned error result: %v", createResult.Content)
	}
	if !created {
		t.Fatal("CreateDashboardRawFn was not called")
	}

	// Step 2: delete the dashboard we just created
	deleteResult, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": createdUUID,
	}))
	if err != nil {
		t.Fatalf("unexpected error on delete: %v", err)
	}
	if deleteResult.IsError {
		t.Fatalf("delete returned error result: %v", deleteResult.Content)
	}
	if !deleted {
		t.Fatal("DeleteDashboardFn was not called")
	}
}

func TestHandleCreateDashboard_StripsSearchContext(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			gotBody = append([]byte(nil), dashboardJSON...)
			return json.RawMessage(`{"status":"success","data":{"uuid":"dashboard-123"}}`), nil
		},
	}

	h := newTestHandler(mock)
	result, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", map[string]any{
		"searchContext": "create a dashboard for service latency",
		"title":         "Latency Dashboard",
		"widgets":       []any{},
		"layout":        []any{},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(gotBody) == 0 {
		t.Fatal("CreateDashboardRawFn was not called")
	}

	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("dashboard payload should be JSON: %v\n%s", err, gotBody)
	}
	if _, hasSearchContext := parsed["searchContext"]; hasSearchContext {
		t.Errorf("searchContext should be stripped from the API payload: %s", gotBody)
	}
	if parsed["title"] != "Latency Dashboard" {
		t.Errorf("title = %v, want Latency Dashboard", parsed["title"])
	}
}

func TestHandleDeleteDashboard_EmptyUUID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty uuid")
	}
}

func TestHandleDeleteDashboard_MissingUUID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing uuid")
	}
}

func TestHandleDeleteDashboard_ClientError(t *testing.T) {
	mock := &client.MockClient{
		DeleteDashboardFn: func(ctx context.Context, id string) error {
			return fmt.Errorf("not found")
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": "nonexistent-uuid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when client returns error")
	}
}

func TestHandleCreateDashboard_RejectsV4(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	// v4-shaped widget: aggregateAttribute + aggregateOperator, no aggregations[].
	payload := map[string]any{
		"title":   "T",
		"version": "v5",
		"widgets": []any{
			map[string]any{
				"id":         "w1",
				"panelTypes": "graph",
				"title":      "Bad widget",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []any{
							map[string]any{
								"queryName":  "A",
								"dataSource": "metrics",
								"expression": "A",
								"aggregateOperator": "rate",
								"aggregateAttribute": map[string]any{
									"key": "system.cpu.time",
								},
							},
						},
					},
				},
			},
		},
	}

	res, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", payload))
	if err != nil {
		t.Fatalf("handler returned go-error %v; expected mcp tool error result", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true, got tool success: %+v", res)
	}
	text := readToolErrorText(t, res)
	if !strings.Contains(text, "v4 widget shapes are not supported") {
		t.Fatalf("error text missing v4-rejection prefix:\n%s", text)
	}
	if !strings.Contains(text, "aggregateOperator") {
		t.Fatalf("error text did not name aggregateOperator:\n%s", text)
	}
}

func TestHandleCreateDashboard_AcceptsV5(t *testing.T) {
	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"uuid":"dashboard-v5-ok"}}`), nil
		},
	}
	h := newTestHandler(mock)

	payload := map[string]any{
		"title":   "T",
		"version": "v5",
		"widgets": []any{
			map[string]any{
				"id":         "w1",
				"panelTypes": "graph",
				"title":      "Good widget",
				"query": map[string]any{
					"queryType": "builder",
					"builder": map[string]any{
						"queryData": []any{
							map[string]any{
								"queryName":  "A",
								"dataSource": "metrics",
								"expression": "A",
								"aggregations": []any{
									map[string]any{
										"metricName":       "system.cpu.time",
										"timeAggregation":  "rate",
										"spaceAggregation": "sum",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	res, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", payload))
	if err != nil {
		t.Fatalf("handler returned go-error %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got tool error: %s", readToolErrorText(t, res))
	}
}

func TestHandleUpdateDashboard_RejectsV4(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	// v4-shaped widget: aggregateAttribute + aggregateOperator, no aggregations[].
	// Wrapped per the update tool's argument schema: {uuid, dashboard}.
	req := makeToolRequest("signoz_update_dashboard", map[string]any{
		"uuid": "test-uuid",
		"dashboard": map[string]any{
			"title":   "T",
			"version": "v5",
			"widgets": []any{
				map[string]any{
					"id":         "w1",
					"panelTypes": "graph",
					"title":      "Bad widget",
					"query": map[string]any{
						"queryType": "builder",
						"builder": map[string]any{
							"queryData": []any{
								map[string]any{
									"queryName":  "A",
									"dataSource": "metrics",
									"expression": "A",
									"aggregateOperator": "rate",
									"aggregateAttribute": map[string]any{
										"key": "system.cpu.time",
									},
								},
							},
						},
					},
				},
			},
		},
	})

	res, err := h.handleUpdateDashboard(testCtx(), req)
	if err != nil {
		t.Fatalf("handler returned go-error %v; expected mcp tool error result", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true, got tool success: %+v", res)
	}
	text := readToolErrorText(t, res)
	if !strings.Contains(text, "v4 widget shapes are not supported") {
		t.Fatalf("error text missing v4-rejection prefix:\n%s", text)
	}
	if !strings.Contains(text, "aggregateOperator") {
		t.Fatalf("error text did not name aggregateOperator:\n%s", text)
	}
}

// readToolErrorText extracts the error string from an MCP tool result.
// The mcp-go SDK wraps text content in []mcp.Content; this helper picks the
// first text block.
func readToolErrorText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
