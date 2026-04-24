package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleListServices_ExplicitStartEndOverrideTimeRange(t *testing.T) {
	var capturedStart string
	var capturedEnd string
	mock := &client.MockClient{
		ListServicesFn: func(ctx context.Context, start, end string) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`[]`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_services", map[string]any{
		"timeRange": "1h",
		"start":     "1711123200000000000",
		"end":       "1711130400000000000",
	})

	result, err := h.handleListServices(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedStart != "1711123200000000000" {
		t.Fatalf("start = %q, want explicit start", capturedStart)
	}
	if capturedEnd != "1711130400000000000" {
		t.Fatalf("end = %q, want explicit end", capturedEnd)
	}
}
