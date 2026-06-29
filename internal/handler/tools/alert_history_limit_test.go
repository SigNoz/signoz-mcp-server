package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestHandleGetAlertHistory_LimitClamped pins that an oversized limit is
// clamped to MaxRawResultLimit BEFORE it is forwarded to the backend (so the
// per-request memory guard can't be bypassed), and that the clamp is surfaced
// as an advisory note on the result. The mock records the outgoing request, so
// we assert against the limit the backend actually receives.
func TestHandleGetAlertHistory_LimitClamped(t *testing.T) {
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-hist",
		"timeRange": "24h",
		"limit":     "100000",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	// The limit forwarded upstream must be clamped, not the raw 100000.
	if capturedReq.Limit != MaxRawResultLimit {
		t.Errorf("forwarded limit = %d, want clamped to MaxRawResultLimit (%d)", capturedReq.Limit, MaxRawResultLimit)
	}

	// The clamp must be surfaced in the result notes so the caller knows the
	// page was bounded server-side.
	if !resultNotesContain(result, "result limited to") {
		t.Errorf("expected a clamp advisory note in result content, got: %v", allTextBlocks(result))
	}
}

// TestHandleGetAlertHistory_LimitNotClamped pins that an in-bounds limit is
// forwarded verbatim and carries NO clamp note (only the completeness note).
func TestHandleGetAlertHistory_LimitNotClamped(t *testing.T) {
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-hist",
		"timeRange": "24h",
		"limit":     "50",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedReq.Limit != 50 {
		t.Errorf("forwarded limit = %d, want 50 (unchanged)", capturedReq.Limit)
	}
	if resultNotesContain(result, "result limited to") {
		t.Errorf("did not expect a clamp note for an in-bounds limit, got: %v", allTextBlocks(result))
	}
}

// resultNotesContain reports whether any text content block of the result
// contains the given substring. resultWithNotes appends notes as content blocks
// trailing the JSON payload block.
func resultNotesContain(r *mcp.CallToolResult, substr string) bool {
	for _, block := range allTextBlocks(r) {
		if strings.Contains(block, substr) {
			return true
		}
	}
	return false
}

// allTextBlocks returns every text content block of a tool result.
func allTextBlocks(r *mcp.CallToolResult) []string {
	var out []string
	for _, c := range r.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			out = append(out, tc.Text)
		}
	}
	return out
}
