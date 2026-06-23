package tools

import (
	"context"
	"encoding/json"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

// TestHandleGetFieldValues_FieldContextPassedThrough guards against the silent-drop
// class of bug: the fieldContext arg must reach the client (and thus the
// /api/v1/fields/values?fieldContext=... query param), not be dropped by the handler.
func TestHandleGetFieldValues_FieldContextPassedThrough(t *testing.T) {
	var gotContext string
	mock := &signozclient.MockClient{
		GetFieldValuesFn: func(_ context.Context, _, _, _, _, fieldContext, _ string) (json.RawMessage, error) {
			gotContext = fieldContext
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)

	req := makeToolRequest("signoz_get_field_values", map[string]any{
		"signal":       "logs",
		"name":         "service.name",
		"fieldContext": "resource",
	})
	res, err := h.handleGetFieldValues(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, res))
	}
	if gotContext != "resource" {
		t.Fatalf("fieldContext not passed through: got %q, want %q", gotContext, "resource")
	}
}

// TestHandleGetFieldKeys_FieldContextAndDataTypePassedThrough guards the same
// contract for the field-keys tool.
func TestHandleGetFieldKeys_FieldContextAndDataTypePassedThrough(t *testing.T) {
	var gotContext, gotDataType string
	mock := &signozclient.MockClient{
		GetFieldKeysFn: func(_ context.Context, _, _, _, fieldContext, fieldDataType, _ string) (json.RawMessage, error) {
			gotContext = fieldContext
			gotDataType = fieldDataType
			return json.RawMessage(`{"status":"success","data":{}}`), nil
		},
	}
	h := newTestHandler(mock)

	req := makeToolRequest("signoz_get_field_keys", map[string]any{
		"signal":        "logs",
		"fieldContext":  "attribute",
		"fieldDataType": "string",
	})
	res, err := h.handleGetFieldKeys(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, res))
	}
	if gotContext != "attribute" || gotDataType != "string" {
		t.Fatalf("field filters not passed through: context=%q dataType=%q", gotContext, gotDataType)
	}
}
