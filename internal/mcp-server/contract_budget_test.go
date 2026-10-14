package mcp_server

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/guardrails"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxServerInstructionsBytes   = guardrails.MaxServerInstructionsBytes
	maxServerPreambleBytes       = guardrails.MaxServerPreambleBytes
	maxToolDescriptionBytes      = guardrails.MaxToolDescriptionBytes
	maxParameterDescriptionBytes = guardrails.MaxParameterDescriptionBytes
	requestTypeTargetBytes       = guardrails.RequestTypeTargetBytes
	maxTopLevelProperties        = guardrails.MaxTopLevelProperties
	maxToolNameBytes             = guardrails.MaxToolNameBytes
	maxCombinedToolNameBytes     = guardrails.MaxCombinedToolNameBytes
	maxInputSchemaNestingDepth   = guardrails.MaxInputSchemaNestingDepth
)

var toolNamePattern = regexp.MustCompile(`^[a-z0-9_]+$`)
var advertisedResourcePattern = regexp.MustCompile("signoz://[^\\s\"'<>`]+")

// These are the server aliases published in first-party setup examples or
// sent in initialize. Cursor combines alias + separator + tool name and caps
// that identifier at 60 characters.
var officialServerAliases = guardrails.OfficialServerAliases

var grandfatheredWideSchemaProperties = guardrails.GrandfatheredWideSchemaProperties

func TestGuardrail_WireContractBudgets(t *testing.T) {
	initializeResult, listedTools := initializedWireCatalog(t)

	t.Run("server instructions and preamble", func(t *testing.T) {
		instructions := initializeResult.Instructions
		if got := len(instructions); got > maxServerInstructionsBytes {
			t.Errorf("initialize instructions = %d bytes, limit %d", got, maxServerInstructionsBytes)
		}

		headingOffset := firstMarkdownHeadingOffset(instructions)
		if headingOffset < 0 {
			t.Fatal("initialize instructions contain no Markdown heading after the preamble")
		}
		preamble := strings.TrimSpace(instructions[:headingOffset])
		if preamble == "" {
			t.Fatal("initialize instructions must start with a self-contained preamble before the first heading")
		}
		if got := len(preamble); got > maxServerPreambleBytes {
			t.Errorf("initialize preamble = %d bytes, limit %d", got, maxServerPreambleBytes)
		}
		t.Logf("initialize instructions=%d bytes preamble=%d bytes", len(instructions), len(preamble))
		for _, required := range []string{"same still-current prepared operation"} {
			if !strings.Contains(instructions, required) {
				t.Errorf("initialize instructions missing safe preflight-reuse guidance %q", required)
			}
		}
	})

	t.Run("tool and parameter descriptions", func(t *testing.T) {
		toolsByName := make(map[string]*mcp.Tool, len(listedTools))
		maxToolName, maxToolBytes := "", 0
		maxParameterPath, maxParameterBytes := "", 0
		for _, tool := range listedTools {
			toolsByName[tool.Name] = tool
			if len(tool.Description) > maxToolBytes {
				maxToolName, maxToolBytes = tool.Name, len(tool.Description)
			}
			if got := len(tool.Description); got > maxToolDescriptionBytes {
				t.Errorf("%s description = %d bytes, limit %d", tool.Name, got, maxToolDescriptionBytes)
			}

			walkSchemaDescriptions(inputSchema(t, tool), "inputSchema", func(path, description string) {
				if len(description) > maxParameterBytes {
					maxParameterPath, maxParameterBytes = tool.Name+" "+path, len(description)
				}
				if got := len(description); got > maxParameterDescriptionBytes {
					t.Errorf("%s %s description = %d bytes, limit %d", tool.Name, path, got, maxParameterDescriptionBytes)
				}
			})
		}
		t.Logf("maximum tool description=%s (%d bytes); maximum parameter description=%s (%d bytes)", maxToolName, maxToolBytes, maxParameterPath, maxParameterBytes)

		for _, toolName := range []string{"signoz_aggregate_logs", "signoz_aggregate_traces"} {
			tool, ok := toolsByName[toolName]
			if !ok {
				t.Fatalf("targeted requestType budget tool %s is not registered", toolName)
			}
			requestType, ok := schemaProperties(t, toolName, inputSchema(t, tool))["requestType"].(map[string]any)
			if !ok {
				t.Fatalf("%s requestType schema is missing or not an object", toolName)
			}
			description, _ := requestType["description"].(string)
			t.Logf("%s requestType description=%d bytes", toolName, len(description))
			if got := len(description); got > requestTypeTargetBytes {
				t.Errorf("%s requestType description = %d bytes, target %d", toolName, got, requestTypeTargetBytes)
			}
		}

		// Concept handles anchored by docs/client-visible-writing-style.md; the
		// rest of each description is pinned by the wire catalog.
		conceptHandles := map[string][]string{
			"signoz_create_alert":     {"same still-current prepared operation"},
			"signoz_update_alert":     {"full replacement", "same still-current prepared operation"},
			"signoz_update_dashboard": {"full replacement"},
		}
		for toolName, handles := range conceptHandles {
			tool, ok := toolsByName[toolName]
			if !ok {
				t.Fatalf("concept-handle tool %s is not registered", toolName)
			}
			for _, handle := range handles {
				if !strings.Contains(tool.Description, handle) {
					t.Errorf("%s description missing concept handle %q", toolName, handle)
				}
			}
		}
	})

	t.Run("top-level property inventories", func(t *testing.T) {
		seenGrandfathered := make(map[string]bool, len(grandfatheredWideSchemaProperties))
		for _, tool := range listedTools {
			properties := schemaProperties(t, tool.Name, inputSchema(t, tool))
			actual := make([]string, 0, len(properties))
			for name := range properties {
				actual = append(actual, name)
			}
			sort.Strings(actual)

			expected, grandfathered := grandfatheredWideSchemaProperties[tool.Name]
			if !grandfathered {
				if len(actual) > maxTopLevelProperties {
					t.Errorf("%s has %d top-level properties, limit %d; redesign or add an explicitly reviewed exact inventory", tool.Name, len(actual), maxTopLevelProperties)
				}
				continue
			}

			seenGrandfathered[tool.Name] = true
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s top-level property inventory changed\n got (%d): %v\nwant (%d): %v", tool.Name, len(actual), actual, len(expected), expected)
			}
		}

		for toolName := range grandfatheredWideSchemaProperties {
			if !seenGrandfathered[toolName] {
				t.Errorf("grandfathered wide-schema tool %s is not registered", toolName)
			}
		}
	})

	t.Run("tool names and schema nesting", func(t *testing.T) {
		maxDepthTool, maxDepth := "", 0
		for _, tool := range listedTools {
			if got := len(tool.Name); got > maxToolNameBytes {
				t.Errorf("%s name = %d bytes, limit %d", tool.Name, got, maxToolNameBytes)
			}
			if !toolNamePattern.MatchString(tool.Name) {
				t.Errorf("tool name %q must contain only lowercase ASCII letters, digits, and underscores", tool.Name)
			}
			for _, alias := range officialServerAliases {
				combined := alias + "_" + tool.Name
				if got := len(combined); got > maxCombinedToolNameBytes {
					t.Errorf("combined tool name %q = %d bytes, Cursor limit %d", combined, got, maxCombinedToolNameBytes)
				}
			}

			schema := inputSchema(t, tool)
			depth := schemaNestingDepth(schema)
			if depth > maxDepth {
				maxDepthTool, maxDepth = tool.Name, depth
			}
			if depth > maxInputSchemaNestingDepth {
				t.Errorf("%s input schema nesting depth = %d, limit %d", tool.Name, depth, maxInputSchemaNestingDepth)
			}
		}
		t.Logf("maximum input schema nesting=%s (depth %d)", maxDepthTool, maxDepth)
	})
}

func TestGuardrail_AdvertisedResourcePointersResolve(t *testing.T) {
	dashboard.InitClickhouseSchema()
	ctx := context.Background()
	client, err := newIntegrationClient(t, buildTestServer(t))
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}
	initializeResult, err := client.Initialize(ctx, &mcp.InitializeParams{
		ProtocolVersion: "2025-11-25",
		ClientInfo:      &mcp.Implementation{Name: "resource-integrity-test", Version: version.Version},
	})
	if err != nil {
		t.Fatalf("initialize in-process client: %v", err)
	}
	toolsResult, err := client.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	resourcesResult, err := client.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	templatesResult, err := client.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	promptsResult, err := client.ListPrompts(ctx, &mcp.ListPromptsParams{})
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}

	advertisedDescriptions := []string{initializeResult.Instructions}
	for _, tool := range toolsResult.Tools {
		advertisedDescriptions = append(advertisedDescriptions, tool.Description)
		walkSchemaDescriptions(inputSchema(t, tool), "inputSchema", func(_ string, description string) {
			advertisedDescriptions = append(advertisedDescriptions, description)
		})
		encodedTool, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal advertised tool %s: %v", tool.Name, err)
		}
		var wireTool map[string]any
		if err := json.Unmarshal(encodedTool, &wireTool); err != nil {
			t.Fatalf("decode advertised tool %s: %v", tool.Name, err)
		}
		walkSchemaDescriptions(wireTool["outputSchema"], "outputSchema", func(_ string, description string) {
			advertisedDescriptions = append(advertisedDescriptions, description)
		})
	}
	for _, resource := range resourcesResult.Resources {
		advertisedDescriptions = append(advertisedDescriptions, resource.Description)
	}
	for _, resourceTemplate := range templatesResult.ResourceTemplates {
		advertisedDescriptions = append(advertisedDescriptions, resourceTemplate.Description)
	}
	for _, prompt := range promptsResult.Prompts {
		advertisedDescriptions = append(advertisedDescriptions, prompt.Description)
		for _, argument := range prompt.Arguments {
			advertisedDescriptions = append(advertisedDescriptions, argument.Description)
		}
	}
	resourceByURI := make(map[string]*mcp.Resource, len(resourcesResult.Resources))
	for _, resource := range resourcesResult.Resources {
		resourceByURI[resource.URI] = resource
	}

	referenced := make(map[string]struct{})
	for _, uri := range advertisedResourceURIs(strings.Join(advertisedDescriptions, "\n")) {
		referenced[uri] = struct{}{}
	}
	if len(referenced) == 0 {
		t.Fatal("advertised catalog contains no signoz:// resource pointers")
	}

	for uri := range referenced {
		resource, ok := resourceByURI[uri]
		if !ok {
			t.Errorf("advertised resource pointer %s is not present in resources/list", uri)
			continue
		}
		if resource.MIMEType == "" {
			t.Errorf("advertised resource %s has no MIME type", uri)
			continue
		}
		readResult, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Errorf("read advertised resource %s: %v", uri, err)
			continue
		}
		if len(readResult.Contents) == 0 {
			t.Errorf("advertised resource %s returned no content", uri)
			continue
		}
		for i, content := range readResult.Contents {
			assertResourceContentIntegrity(t, uri, resource.MIMEType, i, content)
		}
	}
	t.Logf("verified %d advertised resource pointers through resources/read", len(referenced))
}

func initializedWireCatalog(t *testing.T) (*mcp.InitializeResult, []*mcp.Tool) {
	t.Helper()

	client, err := newIntegrationClient(t, buildTestServer(t))
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}
	initializeResult, err := client.Initialize(context.Background(), &mcp.InitializeParams{
		ProtocolVersion: "2025-11-25",
		ClientInfo: &mcp.Implementation{
			Name:    "contract-budget-test",
			Version: version.Version,
		},
	})
	if err != nil {
		t.Fatalf("initialize in-process client: %v", err)
	}
	toolsResult, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list initialized tools: %v", err)
	}
	return initializeResult, toolsResult.Tools
}

func firstMarkdownHeadingOffset(text string) int {
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		trimmed = strings.TrimSuffix(trimmed, "\r")
		hashes := 0
		for hashes < len(trimmed) && trimmed[hashes] == '#' {
			hashes++
		}
		if hashes > 0 && hashes <= 6 && hashes < len(trimmed) && trimmed[hashes] == ' ' {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func advertisedResourceURIs(text string) []string {
	matches := advertisedResourcePattern.FindAllString(text, -1)
	for i := range matches {
		matches[i] = strings.TrimRight(matches[i], ".,;:!?)]}")
	}
	return matches
}

func walkSchemaDescriptions(node any, path string, visit func(path, description string)) {
	switch value := node.(type) {
	case map[string]any:
		if description, ok := value["description"].(string); ok {
			visit(path, description)
		}
		for key, child := range value {
			if key != "description" {
				walkSchemaDescriptions(child, path+"."+key, visit)
			}
		}
	case []any:
		for _, child := range value {
			walkSchemaDescriptions(child, path, visit)
		}
	}
}

func schemaNestingDepth(node any) int {
	object, ok := node.(map[string]any)
	if !ok {
		return 0
	}
	maxDepth := 1
	visitChild := func(child any) {
		if depth := 1 + schemaNestingDepth(child); depth > maxDepth {
			maxDepth = depth
		}
	}
	for key, child := range object {
		switch key {
		case "properties", "$defs", "definitions", "dependentSchemas", "patternProperties":
			children, _ := child.(map[string]any)
			for _, nested := range children {
				visitChild(nested)
			}
		case "items", "additionalItems", "additionalProperties", "contains", "else", "if", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties":
			visitChild(child)
		case "allOf", "anyOf", "oneOf", "prefixItems":
			children, _ := child.([]any)
			for _, nested := range children {
				visitChild(nested)
			}
		}
	}
	return maxDepth
}

func assertResourceContentIntegrity(t *testing.T, uri, advertisedMIME string, index int, content *mcp.ResourceContents) {
	t.Helper()
	if content == nil {
		t.Errorf("resource %s content[%d] is nil", uri, index)
		return
	}
	contentURI, contentMIME := content.URI, content.MIMEType
	payload := content.Text
	if payload == "" {
		payload = string(content.Blob)
	}
	if contentURI != uri {
		t.Errorf("resource %s content[%d] URI = %q", uri, index, contentURI)
	}
	if contentMIME != advertisedMIME {
		t.Errorf("resource %s content[%d] MIME = %q, advertised %q", uri, index, contentMIME, advertisedMIME)
	}
	if strings.TrimSpace(payload) == "" {
		t.Errorf("resource %s content[%d] is empty", uri, index)
	}
}
