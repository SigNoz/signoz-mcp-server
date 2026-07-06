package tools

import (
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestValidationErrorsCarryCode pins the #365 downstream-contract: the loud
// input-validation errors that Family E added across the read/search/aggregate/
// metrics tools (malformed or wrong-typed timestamps / timeRange) carry the
// machine-readable VALIDATION_FAILED structured code, not just a text message —
// so an MCP client can branch on it (fix args vs retry) the same way it does for
// the per-field guards. These calls short-circuit at the validation layer before
// any client call, so a bare mock client is sufficient.
func TestValidationErrorsCarryCode(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	cases := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{"list_services malformed start", func() (*mcp.CallToolResult, error) {
			return h.handleListServices(testCtx(), makeToolRequest("signoz_list_services", map[string]any{"start": "yesterday"}))
		}},
		{"list_services non-string timeRange", func() (*mcp.CallToolResult, error) {
			return h.handleListServices(testCtx(), makeToolRequest("signoz_list_services", map[string]any{"timeRange": 24}))
		}},
		{"top_operations malformed start", func() (*mcp.CallToolResult, error) {
			return h.handleGetServiceTopOperations(testCtx(), makeToolRequest("signoz_get_service_top_operations", map[string]any{"service": "frontend", "start": "yesterday"}))
		}},
		{"aggregate_logs malformed timeRange", func() (*mcp.CallToolResult, error) {
			// Valid aggregation ("count", not "count()") so the malformed timeRange is
			// what trips validation — exercising the resolveTimestamps->coded path.
			return h.handleAggregateLogs(testCtx(), makeToolRequest("signoz_aggregate_logs", map[string]any{"timeRange": "24hours", "aggregation": "count"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call()
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if code := resultCode(t, res); code != CodeValidationFailed {
				t.Fatalf("code = %q, want %q (text: %s)", code, CodeValidationFailed, resultText(t, res))
			}
		})
	}
}
