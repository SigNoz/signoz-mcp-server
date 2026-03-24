package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

// newTestHandler returns a Handler whose GetClient always returns the given mock.
// No real HTTP client or cache is created.
func newTestHandler(mock signozclient.Client) *Handler {
	return &Handler{
		logger:         zap.NewNop(),
		clientOverride: mock,
	}
}

// makeToolRequest builds a mcp.CallToolRequest with the given tool name and
// string arguments. This mirrors what the MCP framework delivers to handlers.
func makeToolRequest(toolName string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
}

// testCtx returns a background context suitable for handler tests.
// Because we use clientOverride, no API key or URL is needed in the context.
func testCtx() context.Context {
	return context.Background()
}
