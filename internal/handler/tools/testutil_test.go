package tools

import (
	"context"
	"encoding/json"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	official "github.com/modelcontextprotocol/go-sdk/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

type registeredTool struct{ Tool mcp.Tool }

func newMCPTestServer() *mcp.Server {
	return official.NewServer(&official.Implementation{Name: "test", Version: "0.0.0"}, nil)
}

func listTestTools(t *testing.T, server *mcp.Server) map[string]*registeredTool {
	t.Helper()
	clientTransport, serverTransport := official.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := official.NewClient(&official.Implementation{Name: "test", Version: "0.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	result, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*registeredTool, len(result.Tools))
	for _, tool := range result.Tools {
		out[tool.Name] = &registeredTool{Tool: *tool}
	}
	return out
}

func callTestToolFromJSONRPC(ctx context.Context, t *testing.T, server *mcp.Server, message json.RawMessage) *mcp.CallToolResult {
	t.Helper()
	var request struct {
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(message, &request); err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := official.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := official.NewClient(&official.Implementation{Name: "test", Version: "0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientSession.Close() }()
	result, err := clientSession.CallTool(ctx, &official.CallToolParams{Name: request.Params.Name, Arguments: request.Params.Arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

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
