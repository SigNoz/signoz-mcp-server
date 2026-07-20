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
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
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
		for _, required := range []string{
			"SigNoz tools",
			"metrics",
			"logs",
			"traces",
			"dashboards",
			"alerts",
			"resource-attribute filters",
			"redundant",
		} {
			if !strings.Contains(preamble, required) {
				t.Errorf("initialize preamble missing self-contained guidance %q", required)
			}
		}
	})

	t.Run("tool and parameter descriptions", func(t *testing.T) {
		toolsByName := make(map[string]mcp.Tool, len(listedTools))
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
			for _, required := range []string{`"scalar" (default)`, "grouped/ranked", `"time_series"`, "time-bucketed values", "one series per group", "when something happened"} {
				if !strings.Contains(description, required) {
					t.Errorf("%s requestType description missing semantic guidance %q", toolName, required)
				}
			}
		}

		alertsTool, ok := toolsByName["signoz_list_alerts"]
		if !ok {
			t.Fatal("signoz_list_alerts is not registered")
		}
		alertsFilter, ok := schemaProperties(t, "signoz_list_alerts", inputSchema(t, alertsTool))["filter"].(map[string]any)
		if !ok {
			t.Fatal("signoz_list_alerts filter schema is missing or not an object")
		}
		alertsFilterDescription, _ := alertsFilter["description"].(string)
		for _, required := range []string{"alert-label comparisons", "=~ (regex)", "!~ (negative regex)", `alertname="HighCPU"`, "All comparisons must match"} {
			if !strings.Contains(alertsFilterDescription, required) {
				t.Errorf("signoz_list_alerts filter description missing %q", required)
			}
		}
		if strings.Contains(alertsFilterDescription, "Prometheus matcher") {
			t.Error("signoz_list_alerts filter description should explain the syntax instead of naming Prometheus matchers")
		}

		type selectionContract struct {
			prefix   string
			required []string
		}
		selectionContracts := map[string]selectionContract{
			"signoz_list_alerts": {
				prefix:   "Use this when",
				required: []string{"Alertmanager alert instances", "Do not use it for configured rules or history", "signoz_list_alert_rules", "signoz_get_alert_history", "Filter by alert labels"},
			},
			"signoz_create_alert": {
				prefix:   "Use this when",
				required: []string{"signoz_update_alert", "v2alpha1 threshold alerts over metrics, logs, traces, or exceptions", "metric-only v1 anomaly", "signoz://alert/instructions", "Before creating", "signoz_list_notification_channels", "verify user-provided names", "If validation still rejects a channel name", "never guess"},
			},
			"signoz_create_dashboard": {
				prefix:   "Use this when",
				required: []string{"custom SigNoz dashboard", "signoz_import_dashboard", "signoz://dashboard/widgets-examples"},
			},
			"signoz_update_dashboard": {
				prefix:   "Use this when",
				required: []string{"full replacement, not a partial patch", "signoz_get_dashboard", "preserve every other field"},
			},
			"signoz_execute_builder_query": {
				prefix:   "Use this only when",
				required: []string{"dedicated log, trace, and metric tools cannot express", "signoz_search_logs", "signoz_aggregate_traces", "signoz_query_metrics", "signoz://metrics-aggregation-guide", "input builder_query limit to 10000", "builder_formula result limit to 100", "non-empty spec.order"},
			},
		}
		for toolName, contract := range selectionContracts {
			tool, ok := toolsByName[toolName]
			if !ok {
				t.Fatalf("selection-contract tool %s is not registered", toolName)
			}
			t.Logf("%s description=%d bytes", toolName, len(tool.Description))
			if !strings.HasPrefix(tool.Description, contract.prefix) {
				t.Errorf("%s description must start with %q", toolName, contract.prefix)
			}
			for _, required := range contract.required {
				if !strings.Contains(tool.Description, required) {
					t.Errorf("%s description missing selection guidance %q", toolName, required)
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
	client, err := mcpclient.NewInProcessClient(buildTestServer(t))
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}
	initializeResult, err := client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "resource-integrity-test", Version: version.Version},
	}})
	if err != nil {
		t.Fatalf("initialize in-process client: %v", err)
	}
	toolsResult, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	resourcesResult, err := client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	templatesResult, err := client.ListResourceTemplates(ctx, mcp.ListResourceTemplatesRequest{})
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	promptsResult, err := client.ListPrompts(ctx, mcp.ListPromptsRequest{})
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
	resourceByURI := make(map[string]mcp.Resource, len(resourcesResult.Resources))
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
		readResult, err := client.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}})
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

func TestGuardrail_AdvertisedResourceURIsPreserveFullPointer(t *testing.T) {
	got := advertisedResourceURIs("Read signoz://docs/foo.json, then signoz://docs/query?signal=logs%2Fraw+v1&limit=10.")
	want := []string{
		"signoz://docs/foo.json",
		"signoz://docs/query?signal=logs%2Fraw+v1&limit=10",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertisedResourceURIs() = %v, want %v", got, want)
	}
}

func initializedWireCatalog(t *testing.T) (*mcp.InitializeResult, []mcp.Tool) {
	t.Helper()

	client, err := mcpclient.NewInProcessClient(buildTestServer(t))
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}
	initializeResult, err := client.Initialize(context.Background(), mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "contract-budget-test",
				Version: version.Version,
			},
		},
	})
	if err != nil {
		t.Fatalf("initialize in-process client: %v", err)
	}
	toolsResult, err := client.ListTools(context.Background(), mcp.ListToolsRequest{})
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

func assertResourceContentIntegrity(t *testing.T, uri, advertisedMIME string, index int, content mcp.ResourceContents) {
	t.Helper()
	var contentURI, contentMIME, payload string
	switch value := content.(type) {
	case mcp.TextResourceContents:
		contentURI, contentMIME, payload = value.URI, value.MIMEType, value.Text
	case *mcp.TextResourceContents:
		contentURI, contentMIME, payload = value.URI, value.MIMEType, value.Text
	case mcp.BlobResourceContents:
		contentURI, contentMIME, payload = value.URI, value.MIMEType, value.Blob
	case *mcp.BlobResourceContents:
		contentURI, contentMIME, payload = value.URI, value.MIMEType, value.Blob
	default:
		t.Errorf("resource %s content[%d] has unsupported type %T", uri, index, content)
		return
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
