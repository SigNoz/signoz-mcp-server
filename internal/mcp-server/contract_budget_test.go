package mcp_server

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	maxServerInstructionsBytes   = 2048
	maxServerPreambleBytes       = 512
	maxToolDescriptionBytes      = 1024
	maxParameterDescriptionBytes = 1024
	requestTypeTargetBytes       = 512
	maxTopLevelProperties        = 15
)

// These are the only current schemas above the ordinary property limit.
// Exact inventories prevent growth and expose accidental contract changes.
var grandfatheredWideSchemaProperties = map[string][]string{
	"signoz_aggregate_traces": {
		"aggregateOn",
		"aggregation",
		"end",
		"error",
		"filter",
		"groupBy",
		"limit",
		"maxDuration",
		"minDuration",
		"operation",
		"orderBy",
		"requestType",
		"searchContext",
		"service",
		"start",
		"stepInterval",
		"timeRange",
	},
	"signoz_create_alert": {
		"alert",
		"alertType",
		"annotations",
		"condition",
		"description",
		"disabled",
		"evalWindow",
		"evaluation",
		"frequency",
		"labels",
		"notificationSettings",
		"preferredChannels",
		"ruleType",
		"schemaVersion",
		"searchContext",
		"source",
		"version",
	},
	"signoz_create_notification_channel": {
		"email_html",
		"email_to",
		"msteams_text",
		"msteams_title",
		"msteams_webhook_url",
		"name",
		"opsgenie_api_key",
		"opsgenie_description",
		"opsgenie_message",
		"opsgenie_priority",
		"pagerduty_description",
		"pagerduty_routing_key",
		"pagerduty_severity",
		"searchContext",
		"send_resolved",
		"slack_api_url",
		"slack_channel",
		"slack_text",
		"slack_title",
		"type",
		"webhook_password",
		"webhook_url",
		"webhook_username",
	},
	"signoz_query_metrics": {
		"end",
		"filter",
		"formula",
		"formulaQueries",
		"groupBy",
		"isMonotonic",
		"metricName",
		"metricType",
		"reduceTo",
		"requestType",
		"searchContext",
		"source",
		"spaceAggregation",
		"start",
		"stepInterval",
		"temporality",
		"timeAggregation",
		"timeRange",
	},
	"signoz_update_alert": {
		"alert",
		"alertType",
		"annotations",
		"condition",
		"description",
		"disabled",
		"evalWindow",
		"evaluation",
		"frequency",
		"id",
		"labels",
		"notificationSettings",
		"preferredChannels",
		"ruleId",
		"ruleType",
		"schemaVersion",
		"searchContext",
		"source",
		"version",
	},
	"signoz_update_notification_channel": {
		"email_html",
		"email_to",
		"id",
		"msteams_text",
		"msteams_title",
		"msteams_webhook_url",
		"name",
		"opsgenie_api_key",
		"opsgenie_description",
		"opsgenie_message",
		"opsgenie_priority",
		"pagerduty_description",
		"pagerduty_routing_key",
		"pagerduty_severity",
		"searchContext",
		"send_resolved",
		"slack_api_url",
		"slack_channel",
		"slack_text",
		"slack_title",
		"type",
		"webhook_password",
		"webhook_url",
		"webhook_username",
	},
}

func TestWireContractBudgets(t *testing.T) {
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
				required: []string{"signoz_update_alert", "v2alpha1 threshold alerts over metrics, logs, traces, or exceptions", "metric-only v1 anomaly", "signoz://alert/instructions", "Before creating", "signoz_list_notification_channels", "verify user-provided names", "create-time validation", "missing or invalid", "never guess"},
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
				required: []string{"higher-level tools cannot express", "signoz_search_logs", "signoz_aggregate_traces", "signoz_query_metrics", "signoz://metrics-aggregation-guide", "input builder_query limit to 10000", "builder_formula result limit to 100", "non-empty spec.order"},
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
