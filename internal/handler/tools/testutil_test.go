package tools

import (
	"context"
	"testing"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

// newTestHandler returns a Handler whose GetClient always returns the given mock.
// No real HTTP client or cache is created.
func newTestHandler(mock signozclient.Client) *Handler {
	return &Handler{
		logger:         logpkg.New("error"),
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

// ctxWithURL returns a test context carrying a fixed SigNoz instance origin.
func ctxWithURL() context.Context {
	return util.SetSigNozURL(context.Background(), "https://signoz.example.com")
}

// textContent extracts the first text content block from a tool result.
func textContent(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		t.Fatalf("first content block is not text")
	}
	return tc.Text
}
