package mcp_server

import (
	"context"
	"strings"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

// TestE2E_GetAlert_NoArguments_ReturnsValidationError reproduces the exact
// production crash end-to-end, through the real MCP pipeline: the in-process
// client JSON-marshals the request, the server deserializes it via
// HandleMessage, runs the middleware chain (recovery enabled, as in prod), and
// dispatches to the handler.
//
// A tools/call for signoz_get_alert with no "arguments" object is encoded as
// JSON with the key omitted (json:"arguments,omitempty"), so the server
// deserializes Params.Arguments to an untyped nil — the precise condition that
// panicked in production (interface conversion: interface {} is nil, not
// map[string]interface {}).
//
// Pre-fix, WithRecovery turned that panic into the JSON-RPC error
// "panic recovered in signoz_get_alert tool handler: ..." (CallTool returns a
// non-nil error). Post-fix, the handler must return a normal tool result
// carrying the parameter-validation message — no panic, no transport error.
func TestE2E_GetAlert_NoArguments_ReturnsValidationError(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test-client", Version: version.Version},
		},
	}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Deliberately leave Arguments unset — this is what a client sending no
	// "arguments" object produces after a JSON round-trip.
	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "signoz_get_alert"},
	})

	// Pre-fix the recovered panic surfaces here as a non-nil transport error.
	if err != nil {
		t.Fatalf("CallTool returned a transport error (handler panicked): %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a validation error tool result, got: %+v", res)
	}

	text := firstTextContent(t, res.Content)
	if !strings.Contains(text, "ruleId") {
		t.Fatalf("expected a ruleId validation message, got: %s", text)
	}
	if strings.Contains(strings.ToLower(text), "panic") {
		t.Fatalf("response still surfaces a panic instead of a clean validation error: %s", text)
	}
}
