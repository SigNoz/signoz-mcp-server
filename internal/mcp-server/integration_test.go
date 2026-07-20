package mcp_server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var initClickhouseSchemaOnce sync.Once

// buildTestServer creates a fully-wired MCPServer suitable for in-process
// integration testing. It mirrors the real server setup in server.go.
func buildTestServer(t *testing.T) *server.MCPServer {
	t.Helper()
	initClickhouseSchemaOnce.Do(dashboard.InitClickhouseSchema)

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

	handler.RegisterAllToolHandlers(s)
	handler.RegisterResourceTemplates(s)
	prompts.RegisterPrompts(func(prompt mcp.Prompt, promptHandler server.PromptHandlerFunc) {
		handler.RegisterPrompt(s, prompt, promptHandler)
	})

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

	// List tools and verify parity with manifest metadata.
	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	expectedToolNames := manifestToolNames(t)
	actualToolNames := listedToolNames(t, toolsResult.Tools)
	if !reflect.DeepEqual(actualToolNames, expectedToolNames) {
		t.Errorf("tools/list names differ from manifest.json\nexpected (%d): %v\nactual (%d): %v",
			len(expectedToolNames), expectedToolNames, len(actualToolNames), actualToolNames)
	}
}

type manifestDocument struct {
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
	Resources []struct {
		URI string `json:"uri"`
	} `json:"resources"`
}

func readManifest(t *testing.T) manifestDocument {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest manifestDocument
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	return manifest
}

func manifestToolNames(t *testing.T) []string {
	t.Helper()

	manifest := readManifest(t)

	names := make([]string, 0, len(manifest.Tools))
	seen := make(map[string]struct{}, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		if tool.Name == "" {
			t.Fatal("manifest.json contains a tool without a name")
		}
		if _, ok := seen[tool.Name]; ok {
			t.Fatalf("manifest.json contains duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func manifestResourceURIs(t *testing.T) []string {
	t.Helper()

	manifest := readManifest(t)
	uris := make([]string, 0, len(manifest.Resources))
	seen := make(map[string]struct{}, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		if resource.URI == "" {
			t.Fatal("manifest.json contains a resource without a URI")
		}
		if _, ok := seen[resource.URI]; ok {
			t.Fatalf("manifest.json contains duplicate resource URI %q", resource.URI)
		}
		seen[resource.URI] = struct{}{}
		uris = append(uris, resource.URI)
	}
	sort.Strings(uris)
	return uris
}

func listedToolNames(t *testing.T, listedTools []mcp.Tool) []string {
	t.Helper()

	names := make([]string, 0, len(listedTools))
	seen := make(map[string]struct{}, len(listedTools))
	for _, tool := range listedTools {
		if tool.Name == "" {
			t.Fatal("tools/list returned a tool without a name")
		}
		if _, ok := seen[tool.Name]; ok {
			t.Fatalf("tools/list returned duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
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

func TestIntegration_AllToolsExposeSearchContext(t *testing.T) {
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
		schema := inputSchema(t, tool)
		properties := schemaProperties(t, tool.Name, schema)
		searchContext, ok := properties["searchContext"].(map[string]any)
		if !ok {
			t.Errorf("%s inputSchema is missing top-level searchContext", tool.Name)
			continue
		}
		if searchContext["type"] != "string" {
			t.Errorf("%s searchContext type = %v, want string", tool.Name, searchContext["type"])
		}
		for _, field := range schemaRequiredFields(schema) {
			if field == "searchContext" {
				t.Errorf("%s searchContext should not be marked required", tool.Name)
			}
		}
	}
}

// TestIntegration_ToolSchemasNeverExposeLiteralRequiredDescription guards
// SigNoz/signoz-ai-assistant#359 at the real registration boundary: it lists
// the registered tools (as a client would) and asserts that no field anywhere
// in any tool's input schema has the literal description "required" — the
// artifact google/jsonschema-go produces from a stray `jsonschema:"required"`
// tag (it uses the `jsonschema` tag value as the field description).
//
// This complements the unit tests in internal/handler/tools: if a typed-struct
// field ever regains a `jsonschema:"required"` tag, or a new field is added
// without an authored `jsonschema` description, the leak surfaces here.
func TestIntegration_ToolSchemasNeverExposeLiteralRequiredDescription(t *testing.T) {
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

	var sawCreateAlert bool
	for _, tool := range toolsResult.Tools {
		schema := inputSchema(t, tool)
		for _, d := range collectSchemaDescriptions(schema) {
			if d == "required" {
				t.Errorf("%s inputSchema exposes a field description of %q (a stray jsonschema:\"required\" tag)", tool.Name, "required")
			}
		}
		// End-to-end proof that authored descriptions reach the real
		// registration output for at least one typed tool, not just in the
		// unit harness.
		if tool.Name == "signoz_create_alert" {
			sawCreateAlert = true
			props := schemaProperties(t, tool.Name, schema)
			alert, _ := props["alert"].(map[string]any)
			if alert == nil || alert["description"] != "Name of the alert rule. Must be unique and descriptive." {
				t.Errorf("signoz_create_alert .alert description = %v, want authored prose", alert["description"])
			}
		}
	}
	if !sawCreateAlert {
		t.Fatalf("signoz_create_alert was not registered; cannot verify descriptions")
	}
}

// collectSchemaDescriptions returns every "description" string found anywhere in
// a JSON-schema-shaped value.
func collectSchemaDescriptions(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		if d, ok := v["description"].(string); ok {
			out = append(out, d)
		}
		for _, val := range v {
			out = append(out, collectSchemaDescriptions(val)...)
		}
	case []any:
		for _, val := range v {
			out = append(out, collectSchemaDescriptions(val)...)
		}
	}
	return out
}

func TestIntegration_FilterExpressionToolsAdvertiseCanonicalFilter(t *testing.T) {
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
	toolsByName := make(map[string]mcp.Tool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolsByName[tool.Name] = tool
	}

	for _, name := range []string{
		"signoz_search_logs",
		"signoz_search_traces",
		"signoz_aggregate_logs",
		"signoz_aggregate_traces",
		"signoz_query_metrics",
	} {
		tool, ok := toolsByName[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		props := schemaProperties(t, name, inputSchema(t, tool))
		if _, ok := props["filter"]; !ok {
			t.Errorf("%s should advertise canonical filter param", name)
		}
		if _, ok := props["query"]; ok {
			t.Errorf("%s should not advertise legacy query alias", name)
		}
	}

	// "False friends": tools whose filter-ish param is a distinct, legitimate
	// param that must stay advertised — NOT the canonical filter-expression
	// alias the #213 sweep converged. Guards against a future query→filter
	// rename wrongly touching them.
	//   - signoz_list_alerts.filter      : Prometheus matcher expression
	//   - signoz_execute_builder_query.query : the full QB v5 JSON object
	falseFriends := map[string]string{
		"signoz_list_alerts":           "filter",
		"signoz_execute_builder_query": "query",
	}
	for name, param := range falseFriends {
		tool, ok := toolsByName[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		props := schemaProperties(t, name, inputSchema(t, tool))
		if _, ok := props[param]; !ok {
			t.Errorf("%s should keep %q param", name, param)
		}
	}

	// signoz_search_docs was renamed (#367): its free-text param is now the
	// canonical "searchText". The legacy "query" key is still accepted by the
	// handler as a PERMANENT silent alias but is no longer advertised in the
	// schema. Pin both halves of that contract.
	if tool, ok := toolsByName["signoz_search_docs"]; ok {
		props := schemaProperties(t, "signoz_search_docs", inputSchema(t, tool))
		if _, ok := props["searchText"]; !ok {
			t.Errorf("signoz_search_docs should advertise canonical %q param", "searchText")
		}
		if _, ok := props["query"]; ok {
			t.Errorf("signoz_search_docs should NOT advertise legacy %q param (handler-only alias)", "query")
		}
	} else {
		t.Fatalf("tool signoz_search_docs not registered")
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

func inputSchema(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()

	b, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema for %s: %v", tool.Name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("unmarshal input schema for %s: %v", tool.Name, err)
	}
	return schema
}

func schemaProperties(t *testing.T, toolName string, schema map[string]any) map[string]any {
	t.Helper()

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s inputSchema.properties = %#v, want object", toolName, schema["properties"])
	}
	return properties
}

func schemaRequiredFields(schema map[string]any) []string {
	rawRequired, _ := schema["required"].([]any)
	required := make([]string, 0, len(rawRequired))
	for _, field := range rawRequired {
		if s, ok := field.(string); ok {
			required = append(required, s)
		}
	}
	return required
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

func TestIntegration_InitializeListAndReadResources(t *testing.T) {
	s := buildTestServer(t)
	ctx := context.Background()

	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("failed to create in-process client: %v", err)
	}

	initResult, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: version.Version},
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if initResult.Capabilities.Resources == nil {
		t.Error("expected resources capability to be present")
	}

	resourcesResult, err := c.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}

	expectedURIs := manifestResourceURIs(t)
	actualURIs := make([]string, 0, len(resourcesResult.Resources))
	seenURIs := make(map[string]struct{}, len(resourcesResult.Resources))
	seenNames := make(map[string]struct{}, len(resourcesResult.Resources))
	for _, resource := range resourcesResult.Resources {
		if resource.URI == "" || resource.Name == "" || resource.Description == "" || resource.MIMEType == "" {
			t.Fatalf("resources/list contains incomplete metadata: %+v", resource)
		}
		if len(resource.Description) > 1024 {
			t.Errorf("%s description = %d bytes, limit 1024", resource.URI, len(resource.Description))
		}
		if _, duplicate := seenURIs[resource.URI]; duplicate {
			t.Fatalf("resources/list contains duplicate URI %q", resource.URI)
		}
		if _, duplicate := seenNames[resource.Name]; duplicate {
			t.Fatalf("resources/list contains duplicate name %q", resource.Name)
		}
		seenURIs[resource.URI] = struct{}{}
		seenNames[resource.Name] = struct{}{}
		actualURIs = append(actualURIs, resource.URI)
	}
	sort.Strings(actualURIs)
	if !reflect.DeepEqual(actualURIs, expectedURIs) {
		t.Errorf("resources/list URIs differ from manifest.json\nexpected: %v\nactual: %v", expectedURIs, actualURIs)
	}

	for _, resource := range resourcesResult.Resources {
		resource := resource
		t.Run(resource.URI, func(t *testing.T) {
			if resource.MIMEType != "text/markdown" {
				t.Errorf("resource MIME type = %q, want text/markdown", resource.MIMEType)
			}
			// The sitemap is backed by the asynchronously built docs index, which
			// buildTestServer deliberately does not initialize. Its readable-content
			// contract is covered by TestE2EDocsSearchFetchAndSitemap.
			if resource.URI == "signoz://docs/sitemap" {
				t.Skip("requires initialized docs index")
			}
			if resource.Size == nil {
				t.Fatal("static resource does not advertise its byte size")
			}

			result, err := c.ReadResource(ctx, mcp.ReadResourceRequest{
				Params: mcp.ReadResourceParams{URI: resource.URI},
			})
			if err != nil {
				t.Fatalf("ReadResource(%s) failed: %v", resource.URI, err)
			}
			if len(result.Contents) != 1 {
				t.Fatalf("ReadResource(%s) returned %d content items, want 1", resource.URI, len(result.Contents))
			}
			content, ok := result.Contents[0].(mcp.TextResourceContents)
			if !ok {
				t.Fatalf("ReadResource(%s) returned %T, want TextResourceContents", resource.URI, result.Contents[0])
			}
			if content.URI != resource.URI {
				t.Errorf("ReadResource(%s) content URI = %q", resource.URI, content.URI)
			}
			if content.MIMEType != resource.MIMEType {
				t.Errorf("ReadResource(%s) MIME type = %q, want %q", resource.URI, content.MIMEType, resource.MIMEType)
			}
			if strings.TrimSpace(content.Text) == "" {
				t.Errorf("ReadResource(%s) returned empty text", resource.URI)
			}
			if got, want := *resource.Size, int64(len(content.Text)); got != want {
				t.Errorf("resource size = %d, read content size = %d", got, want)
			}
		})
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

	if got, want := len(templatesResult.ResourceTemplates), 2; got != want {
		t.Fatalf("resource template count = %d, want %d", got, want)
	}
	for _, template := range templatesResult.ResourceTemplates {
		if template.URITemplate == nil || template.URITemplate.Raw() == "" ||
			template.Name == "" || template.Description == "" || template.MIMEType == "" {
			t.Errorf("resource template has incomplete metadata: %+v", template)
		}
		if len(template.Description) > 1024 {
			t.Errorf("resource template description = %d bytes, limit 1024", len(template.Description))
		}
	}
}
