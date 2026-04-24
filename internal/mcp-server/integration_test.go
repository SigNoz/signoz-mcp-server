package mcp_server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// buildTestServer creates a fully-wired MCPServer suitable for in-process
// integration testing. It mirrors the real server setup in server.go.
func buildTestServer(t *testing.T) *server.MCPServer {
	t.Helper()

	log := logpkg.New("error")
	cfg := &config.Config{
		ClientCacheSize: 8,
		ClientCacheTTL:  5 * time.Minute,
	}
	handler := tools.NewHandler(log, cfg)

	s := server.NewMCPServer("SigNozMCP", version.Version,
		server.WithLogging(),
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(instructions.ServerInstructions),
	)

	handler.RegisterMetricsHandlers(s)
	handler.RegisterFieldsHandlers(s)
	handler.RegisterAlertsHandlers(s)
	handler.RegisterDashboardHandlers(s)
	handler.RegisterServiceHandlers(s)
	handler.RegisterQueryBuilderV5Handlers(s)
	handler.RegisterLogsHandlers(s)
	handler.RegisterViewHandlers(s)
	handler.RegisterTracesHandlers(s)
	handler.RegisterNotificationChannelHandlers(s)
	handler.RegisterResourceTemplates(s)
	prompts.RegisterPrompts(s.AddPrompt)

	return s
}

func TestIntegration_InitializeAndListTools(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	initResult, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "test-client",
				Version: version.Version,
			},
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if initResult.ServerInfo.Name != "SigNozMCP" {
		t.Errorf("expected server name SigNozMCP, got %q", initResult.ServerInfo.Name)
	}
	if initResult.Capabilities.Tools == nil {
		t.Error("expected tools capability to be present")
	}

	// List tools and verify count
	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	const expectedToolCount = 33
	if len(toolsResult.Tools) != expectedToolCount {
		t.Errorf("expected %d tools, got %d", expectedToolCount, len(toolsResult.Tools))
		for _, tool := range toolsResult.Tools {
			t.Logf("  tool: %s", tool.Name)
		}
	}
}

func TestIntegration_ListToolsInputSchemasAreOpenAPICompatible(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "test-client",
				Version: version.Version,
			},
		},
	}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	for _, tool := range toolsResult.Tools {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema for %s: %v", tool.Name, err)
		}
		var schema any
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatalf("unmarshal input schema for %s: %v", tool.Name, err)
		}
		if paths := booleanSubschemaPaths(schema, nil); len(paths) > 0 {
			t.Errorf("%s inputSchema has OpenAPI-incompatible boolean subschemas: %s", tool.Name, strings.Join(paths, ", "))
		}
	}
}

func TestIntegration_PromqlInstructionsResourceRegistered(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: version.Version},
		},
	}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	res, err := c.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "signoz://promql/instructions"},
	})
	if err != nil {
		t.Fatalf("ReadResource(signoz://promql/instructions) failed: %v", err)
	}
	if len(res.Contents) == 0 {
		t.Fatal("expected resource contents, got none")
	}
	tc, ok := res.Contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", res.Contents[0])
	}
	if tc.URI != "signoz://promql/instructions" {
		t.Errorf("URI = %q, want signoz://promql/instructions", tc.URI)
	}
	// Sanity check: the body must carry the OTel dotted-name guidance, the
	// anti-pattern framing, and the PR #11023 consumer-group-lag example.
	for _, want := range []string{
		"Prometheus 3.x UTF-8 quoted selector",
		"payment_latency_ms.bucket",
		"group_right",
	} {
		if !strings.Contains(tc.Text, want) {
			t.Errorf("resource body missing expected substring %q", want)
		}
	}
}

func booleanSubschemaPaths(schema any, path []string) []string {
	switch typed := schema.(type) {
	case bool:
		return []string{strings.Join(path, ".")}
	case map[string]any:
		var paths []string
		for _, field := range []string{"$defs", "definitions", "dependentSchemas", "patternProperties", "properties"} {
			if schemas, ok := typed[field].(map[string]any); ok {
				for name, child := range schemas {
					paths = append(paths, booleanSubschemaPaths(child, appendPath(path, field, name))...)
				}
			}
		}
		for _, field := range []string{"additionalItems", "contains", "else", "if", "items", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties"} {
			if child, ok := typed[field]; ok {
				paths = append(paths, booleanSubschemaPaths(child, appendPath(path, field))...)
			}
		}
		for _, field := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
			if schemas, ok := typed[field].([]any); ok {
				for i, child := range schemas {
					paths = append(paths, booleanSubschemaPaths(child, appendPath(path, field, strconv.Itoa(i)))...)
				}
			}
		}
		return paths
	default:
		return nil
	}
}

func appendPath(path []string, parts ...string) []string {
	next := make([]string, 0, len(path)+len(parts))
	next = append(next, path...)
	next = append(next, parts...)
	return next
}

func TestIntegration_ListPrompts(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: version.Version},
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	promptsResult, err := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}

	const expectedPromptCount = 4
	if len(promptsResult.Prompts) != expectedPromptCount {
		t.Errorf("expected %d prompts, got %d", expectedPromptCount, len(promptsResult.Prompts))
		for _, p := range promptsResult.Prompts {
			t.Logf("  prompt: %s", p.Name)
		}
	}
}

func TestIntegration_ListResourceTemplates(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: version.Version},
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	templatesResult, err := c.ListResourceTemplates(ctx, mcp.ListResourceTemplatesRequest{})
	if err != nil {
		t.Fatalf("ListResourceTemplates failed: %v", err)
	}

	const expectedTemplateCount = 2
	if len(templatesResult.ResourceTemplates) != expectedTemplateCount {
		t.Errorf("expected %d resource templates, got %d", expectedTemplateCount, len(templatesResult.ResourceTemplates))
		for _, rt := range templatesResult.ResourceTemplates {
			t.Logf("  resource template: %s", rt.Name)
		}
	}
}
