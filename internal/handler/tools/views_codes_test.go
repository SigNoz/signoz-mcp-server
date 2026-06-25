package tools

import (
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestViewValidationErrorsCarryCode pins the #365 downstream contract for the
// saved-views tools: user-input validation errors in views.go must carry the
// machine-readable VALIDATION_FAILED structured code (so an MCP client can
// branch fix-args vs retry), matching the sibling alerts/dashboards/
// notification_channels tools. Each of these calls short-circuits at the
// validation layer before any client call, so a bare MockClient suffices.
func TestViewValidationErrorsCarryCode(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	cases := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{"list_views invalid sourcePage", func() (*mcp.CallToolResult, error) {
			return h.handleListViews(testCtx(), makeToolRequest("signoz_list_views", map[string]any{
				"sourcePage": "bogus",
			}))
		}},
		{"create_view invalid sourcePage", func() (*mcp.CallToolResult, error) {
			return h.handleCreateView(testCtx(), makeToolRequest("signoz_create_view", map[string]any{
				"name":           "v",
				"sourcePage":     "bogus",
				"compositeQuery": map[string]any{},
			}))
		}},
		{"create_view missing compositeQuery", func() (*mcp.CallToolResult, error) {
			return h.handleCreateView(testCtx(), makeToolRequest("signoz_create_view", map[string]any{
				"name":       "v",
				"sourcePage": "traces",
			}))
		}},
		{"create_view signal/sourcePage mismatch", func() (*mcp.CallToolResult, error) {
			return h.handleCreateView(testCtx(), makeToolRequest("signoz_create_view", map[string]any{
				"name":       "v",
				"sourcePage": "traces",
				"compositeQuery": map[string]any{
					"queries": []any{
						map[string]any{
							"type": "builder_query",
							"spec": map[string]any{"signal": "logs"},
						},
					},
				},
			}))
		}},
		{"update_view missing view", func() (*mcp.CallToolResult, error) {
			return h.handleUpdateView(testCtx(), makeToolRequest("signoz_update_view", map[string]any{
				"id": "019b1af4-3ef5-734d-8ba8-cc12fb5b5978",
			}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call()
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected an error result, got success: %+v", res.Content)
			}
			if code := resultCode(t, res); code != CodeValidationFailed {
				t.Fatalf("code = %q, want %q (text: %s)", code, CodeValidationFailed, resultText(t, res))
			}
		})
	}
}
