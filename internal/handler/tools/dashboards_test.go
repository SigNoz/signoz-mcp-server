package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
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
