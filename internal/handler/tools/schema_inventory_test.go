package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

var expectedInputSchemaTools = []string{
	"signoz_aggregate_logs",
	"signoz_aggregate_traces",
	"signoz_check_metric_cardinality",
	"signoz_check_metric_usage",
	"signoz_create_alert",
	"signoz_create_dashboard",
	"signoz_create_notification_channel",
	"signoz_create_view",
	"signoz_delete_alert",
	"signoz_delete_dashboard",
	"signoz_delete_notification_channel",
	"signoz_delete_view",
	"signoz_execute_builder_query",
	"signoz_fetch_doc",
	"signoz_get_alert",
	"signoz_get_alert_history",
	"signoz_get_dashboard",
	"signoz_get_field_keys",
	"signoz_get_field_values",
	"signoz_get_notification_channel",
	"signoz_get_service_top_operations",
	"signoz_get_top_metrics",
	"signoz_get_trace_details",
	"signoz_get_view",
	"signoz_import_dashboard",
	"signoz_list_alert_rules",
	"signoz_list_alerts",
	"signoz_list_dashboard_templates",
	"signoz_list_dashboards",
	"signoz_list_metrics",
	"signoz_list_notification_channels",
	"signoz_list_services",
	"signoz_list_views",
	"signoz_query_metrics",
	"signoz_search_docs",
	"signoz_search_logs",
	"signoz_search_traces",
	"signoz_update_alert",
	"signoz_update_dashboard",
	"signoz_update_notification_channel",
	"signoz_update_view",
}

var expectedOutputSchemaTools = []string{
	"signoz_check_metric_usage",
	"signoz_fetch_doc",
	"signoz_list_alert_rules",
	"signoz_list_alerts",
	"signoz_search_docs",
}

func registerAllToolHandlers(h *Handler, s *server.MCPServer) {
	h.RegisterMetricsHandlers(s)
	h.RegisterTopMetricsHandlers(s)
	h.RegisterMetricUsageHandlers(s)
	h.RegisterFieldsHandlers(s)
	h.RegisterAlertsHandlers(s)
	h.RegisterDashboardHandlers(s)
	h.RegisterServiceHandlers(s)
	h.RegisterQueryBuilderV5Handlers(s)
	h.RegisterLogsHandlers(s)
	h.RegisterViewHandlers(s)
	h.RegisterDocsHandlers(s)
	h.RegisterTracesHandlers(s)
	h.RegisterNotificationChannelHandlers(s)
	h.RegisterMetricCardinalityHandlers(s)
}

func registeredTestTools(t *testing.T) map[string]*server.ServerTool {
	t.Helper()
	h := newTestHandler(nil)
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))
	registerAllToolHandlers(h, s)
	return s.ListTools()
}

func TestRegisteredToolSchemasCompileAndMatchExactInventory(t *testing.T) {
	registered := registeredTestTools(t)
	var inputNames, outputNames []string

	for name, entry := range registered {
		inputRaw := inputSchemaJSON(entry.Tool)
		if len(inputRaw) > 0 {
			inputNames = append(inputNames, name)
			compiled, err := compileToolSchema(name, "input", inputRaw)
			if err != nil {
				t.Errorf("compile %s input schema: %v", name, err)
			} else if compiled == nil {
				t.Errorf("compile %s input schema returned nil", name)
			}
			var schema any
			if err := json.Unmarshal(inputRaw, &schema); err != nil {
				t.Errorf("decode %s input schema: %v", name, err)
			} else {
				assertNoClosedInputObjects(t, name, schema, "<root>")
			}
		}

		outputRaw := outputSchemaJSON(entry.Tool)
		if len(outputRaw) > 0 {
			outputNames = append(outputNames, name)
			compiled, err := compileToolSchema(name, "output", outputRaw)
			if err != nil {
				t.Errorf("compile %s output schema: %v", name, err)
			} else if compiled == nil {
				t.Errorf("compile %s output schema returned nil", name)
			}
		}
	}

	sort.Strings(inputNames)
	sort.Strings(outputNames)
	if !reflect.DeepEqual(inputNames, expectedInputSchemaTools) {
		t.Fatalf("input schema inventory changed\n got (%d): %v\nwant (%d): %v", len(inputNames), inputNames, len(expectedInputSchemaTools), expectedInputSchemaTools)
	}
	if !reflect.DeepEqual(outputNames, expectedOutputSchemaTools) {
		t.Fatalf("output schema inventory changed\n got (%d): %v\nwant (%d): %v", len(outputNames), outputNames, len(expectedOutputSchemaTools), expectedOutputSchemaTools)
	}
}

func assertNoClosedInputObjects(t *testing.T, toolName string, value any, path string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if closed, ok := typed["additionalProperties"].(bool); ok && !closed {
			t.Errorf("%s input schema advertises closed object at %s", toolName, path)
		}
		for key, child := range typed {
			assertNoClosedInputObjects(t, toolName, child, fmt.Sprintf("%s/%s", path, key))
		}
	case []any:
		for i, child := range typed {
			assertNoClosedInputObjects(t, toolName, child, fmt.Sprintf("%s/%d", path, i))
		}
	}
}

func TestAdvertisedUpdateSchemasAcceptRealWriteBackPayloads(t *testing.T) {
	registered := registeredTestTools(t)
	validate := func(toolName string, arguments map[string]any) {
		t.Helper()
		entry := registered[toolName]
		if entry == nil {
			t.Fatalf("tool %s is not registered", toolName)
		}
		compiled, err := compileToolSchema(toolName, "input", inputSchemaJSON(entry.Tool))
		if err != nil {
			t.Fatalf("compile %s advertised input schema: %v", toolName, err)
		}
		if err := validateSchemaValue(compiled.validator, arguments, true); err != nil {
			t.Fatalf("%s advertised schema rejected real write-back payload: %v", toolName, err)
		}
	}

	for _, auditFields := range []map[string]any{
		{"createdAt": "yesterday", "updatedAt": "today", "createdBy": "user", "updatedBy": "user"},
		{"createAt": "yesterday", "updateAt": "today", "createBy": "user", "updateBy": "user"},
	} {
		alert := validAlertWriteBackFixture()
		alert["id"] = validRuleUUIDv7
		for key, value := range auditFields {
			alert[key] = value
		}
		validate("signoz_update_alert", alert)
	}

	validate("signoz_update_dashboard", map[string]any{
		"id": "dashboard-1",
		"dashboard": map[string]any{
			"uuid":      "dashboard-1",
			"title":     "Latency",
			"layout":    []any{},
			"widgets":   []any{},
			"variables": map[string]any{},
			"panelMap":  map[string]any{"panel-1": map[string]any{"unknownServerField": true}},
		},
	})
}
