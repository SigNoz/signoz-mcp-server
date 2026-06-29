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

// FIX A2: an invalid send_resolved value on create must carry the
// VALIDATION_FAILED structured code (a fixable parameter mistake), not a bare
// error result with no code.
func TestHandleCreateNotificationChannel_InvalidSendResolved_Code(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "test",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
		"send_resolved": "maybe",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for invalid send_resolved value")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Errorf("expected code=%s, got %s", CodeValidationFailed, code)
	}
}

// FIX A2: an invalid send_resolved value on update must also carry the
// VALIDATION_FAILED structured code.
func TestHandleUpdateNotificationChannel_InvalidSendResolved_Code(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "019b1af4-3ef5-734d-8ba8-cc12fb5b5978",
		"type":          "slack",
		"name":          "test",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
		"send_resolved": "maybe",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for invalid send_resolved value")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Errorf("expected code=%s, got %s", CodeValidationFailed, code)
	}
}

// FIX A2: an invalid channel type on create is input validation and must carry
// the VALIDATION_FAILED code (sibling validation paths code these).
func TestHandleCreateNotificationChannel_InvalidType_Code(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type": "invalid",
		"name": "test",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for invalid type")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Errorf("expected code=%s, got %s", CodeValidationFailed, code)
	}
}

// FIX A2: a missing per-type required field (built in buildReceiverJSON) is
// input validation and must carry the VALIDATION_FAILED code.
func TestHandleCreateNotificationChannel_MissingReceiverField_Code(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type": "slack",
		"name": "test",
		// missing slack_api_url
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing required receiver field")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Errorf("expected code=%s, got %s", CodeValidationFailed, code)
	}
}

// FIX N6-readback: when UpdateNotificationChannel succeeds but the follow-up
// GetNotificationChannel read-back fails, the result must NOT be IsError (the
// write succeeded) but must surface the read-back failure as a note so the
// client/LLM does not read a clean success as a verified state.
func TestHandleUpdateNotificationChannel_ReadBackFails_NotesFailure(t *testing.T) {
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(ctx context.Context, id string, receiverJSON []byte) error {
			return nil // update succeeds
		},
		GetNotificationChannelFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return nil, fmt.Errorf("read-back boom: 503 service unavailable")
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return nil // test send succeeds so only the read-back note is present
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "019b1af4-3ef5-734d-8ba8-cc12fb5b5978",
		"type":          "slack",
		"name":          "my-slack",
		"slack_api_url": "https://hooks.slack.com/services/T/B/x",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fail OPEN: the update succeeded, so the result must not be an error.
	if result.IsError {
		t.Fatalf("expected success result (update succeeded, read-back failure is advisory): %v", result.Content)
	}

	// The read-back failure must be surfaced somewhere in the content blocks.
	var all strings.Builder
	for _, c := range result.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			all.WriteString(tc.Text)
			all.WriteString("\n")
		}
	}
	combined := all.String()
	if !strings.Contains(combined, "read-back after update failed") {
		t.Errorf("expected a read-back failure note in result content, got: %s", combined)
	}
	if !strings.Contains(combined, "read-back boom: 503 service unavailable") {
		t.Errorf("expected the read-back error message in result content, got: %s", combined)
	}
	if !strings.Contains(combined, "the update itself succeeded") {
		t.Errorf("expected the note to reassure that the update succeeded, got: %s", combined)
	}
}
