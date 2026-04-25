package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/promql"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterDashboardHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering dashboard handlers")

	tool := mcp.NewTool("signoz_list_dashboards",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific dashboard, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of dashboards to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	addTool(s, tool, h.handleListDashboards)

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get full details of a specific dashboard by UUID (returns complete dashboard configuration with all panels and queries)"),
		mcp.WithString("uuid", mcp.Required(), mcp.Description("Dashboard UUID")),
	)

	addTool(s, getDashboardTool, h.handleGetDashboard)

	createDashboardTool := mcp.NewTool(
		"signoz_create_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Creates a new monitoring dashboard based on the provided title, layout, and widget configuration. "+
				"CRITICAL: You MUST read these resources BEFORE generating any dashboard output:\n"+
				"1. signoz://dashboard/instructions - REQUIRED: Dashboard structure and basics\n"+
				"2. signoz://dashboard/widgets-instructions - REQUIRED: Widget configuration rules\n"+
				"3. signoz://dashboard/widgets-examples - REQUIRED: Complete widget examples with all required fields\n\n"+
				"QUERY-SPECIFIC RESOURCES (read based on query type used):\n"+
				"- For PromQL queries: signoz://promql/instructions\n"+
				"- For Query Builder queries: signoz://dashboard/query-builder-example\n"+
				"- For ClickHouse SQL on logs: signoz://dashboard/clickhouse-schema-for-logs + signoz://dashboard/clickhouse-logs-example\n"+
				"- For ClickHouse SQL on metrics: signoz://dashboard/clickhouse-schema-for-metrics + signoz://dashboard/clickhouse-metrics-example\n"+
				"- For ClickHouse SQL on traces: signoz://dashboard/clickhouse-schema-for-traces + signoz://dashboard/clickhouse-traces-example\n\n"+
				"IMPORTANT: The widgets-examples resource contains complete, working widget configurations. "+
				"You must consult it to ensure all required fields (id, panelTypes, title, query, selectedLogFields, selectedTracesFields, thresholds, contextLinks) are properly populated.",
		),
		mcp.WithInputSchema[types.CreateDashboardInput](),
	)

	addTool(s, createDashboardTool, h.handleCreateDashboard)

	updateDashboardTool := mcp.NewTool(
		"signoz_update_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Update an existing dashboard by supplying its UUID along with a fully assembled dashboard JSON object.\n\n"+
				"MANDATORY FIRST STEP: Read signoz://dashboard/widgets-examples before doing ANYTHING else. This is NON-NEGOTIABLE.\n\n"+
				"The provided object must represent the complete post-update state, combining the current dashboard data and the intended modifications.\n\n"+
				"REQUIRED RESOURCES (read ALL before generating output):\n"+
				"1. signoz://dashboard/instructions\n"+
				"2. signoz://dashboard/widgets-instructions\n"+
				"3. signoz://dashboard/widgets-examples ← CRITICAL: Shows complete widget field structure\n\n"+
				"CONDITIONAL RESOURCES (based on query type):\n"+
				"• PromQL → signoz://promql/instructions\n"+
				"• Query Builder → signoz://dashboard/query-builder-example\n"+
				"• ClickHouse Logs → signoz://dashboard/clickhouse-schema-for-logs + signoz://dashboard/clickhouse-logs-example\n"+
				"• ClickHouse Metrics → signoz://dashboard/clickhouse-schema-for-metrics + signoz://dashboard/clickhouse-metrics-example\n"+
				"• ClickHouse Traces → signoz://dashboard/clickhouse-schema-for-traces + signoz://dashboard/clickhouse-traces-example\n\n"+
				"WARNING: Failing to consult widgets-examples will result in incomplete widget configurations missing required fields "+
				"(id, panelTypes, title, query, selectedLogFields, selectedTracesFields, thresholds, contextLinks).",
		),
		mcp.WithInputSchema[types.UpdateDashboardInput](),
	)

	addTool(s, updateDashboardTool, h.handleUpdateDashboard)

	deleteDashboardTool := mcp.NewTool("signoz_delete_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Delete a dashboard by its UUID. This action is irreversible. Use signoz_list_dashboards to find dashboard UUIDs."),
		mcp.WithString("uuid", mcp.Required(), mcp.Description("Dashboard UUID to delete")),
	)

	addTool(s, deleteDashboardTool, h.handleDeleteDashboard)

	// resources for create and update dashboard
	h.registerDashboardResources(s)
}

func (h *Handler) handleListDashboards(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_dashboards")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListDashboards(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list dashboards", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var dashboards map[string]any
	if err := json.Unmarshal(result, &dashboards); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse dashboards response", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}

	data, ok := dashboards["data"].([]any)
	if !ok {
		h.logger.ErrorContext(ctx, "Invalid dashboards response format", slog.String("data", logpkg.TruncAny(dashboards["data"])))
		return mcp.NewToolResultError("invalid response format: expected data array"), nil
	}

	total := len(data)
	pagedData := paginate.Array(data, offset, limit)

	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap dashboards with pagination", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, ok := req.Params.Arguments.(map[string]any)["uuid"].(string)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid uuid parameter type", slog.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError(`Parameter validation failed: "uuid" must be a string. Example: {"uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
	}
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty uuid parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.GetDashboard(ctx, uuid)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get dashboard", slog.String("uuid", uuid), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleCreateDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map.")
		return mcp.NewToolResultError(`Parameter validation failed: The dashboard configuration object is empty or improperly formatted.`), nil
	}
	delete(rawConfig, "searchContext")

	// Validate and normalize via the dashboardbuilder + panelbuilder pipeline.
	cleanJSON, err := dashboard.ValidateFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Dashboard validation failed", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Dashboard validation error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_dashboard")
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.CreateDashboardRaw(ctx, cleanJSON)

	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create dashboard in SigNoz", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleUpdateDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map from Claude.")
		return mcp.NewToolResultError(`Parameter validation failed: The dashboard configuration object is empty or improperly formatted.`), nil
	}

	// Extract UUID before validation (it's at the top level, not inside dashboard data).
	uuid, _ := rawConfig["uuid"].(string)
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty uuid parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use list_dashboards tool to see available dashboards.`), nil
	}

	// Extract the dashboard sub-object for validation.
	dashboardRaw, ok := rawConfig["dashboard"].(map[string]any)
	if !ok || len(dashboardRaw) == 0 {
		return mcp.NewToolResultError(`Parameter validation failed: "dashboard" field is required and must be a valid object.`), nil
	}

	// Validate and normalize via the dashboardbuilder + panelbuilder pipeline.
	cleanJSON, err := dashboard.ValidateFromMap(dashboardRaw)
	if err != nil {
		h.logger.WarnContext(ctx, "Dashboard validation failed", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Dashboard validation error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	err = client.UpdateDashboardRaw(ctx, uuid, cleanJSON)

	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to update dashboard in SigNoz", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}

	return mcp.NewToolResultText("dashboard updated"), nil
}

func (h *Handler) handleDeleteDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, ok := req.Params.Arguments.(map[string]any)["uuid"].(string)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid uuid parameter type", slog.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError(`Parameter validation failed: "uuid" must be a string. Example: {"uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
	}
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty uuid parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	err = client.DeleteDashboard(ctx, uuid)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to delete dashboard", slog.String("uuid", uuid), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("SigNoz API Error: %s", err.Error())), nil
	}
	return mcp.NewToolResultText("dashboard deleted"), nil
}

// registerDashboardResources registers all MCP resources needed for dashboard creation/update.
func (h *Handler) registerDashboardResources(s *server.MCPServer) {
	clickhouseLogsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-logs",
		"ClickHouse Logs Schema",
		mcp.WithResourceDescription("ClickHouse schema for logs_v2, logs_v2_resource, tag_attributes_v2 and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseLogsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.LogsSchema,
			},
		}, nil
	})

	clickhouseLogsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-logs-example",
		"Clickhouse Examples for logs",
		mcp.WithResourceDescription("ClickHouse SQL query examples for SigNoz logs. Includes resource filter patterns (CTE), timeseries queries, value queries, common use cases (Kubernetes clusters, error logs by service), and key patterns for timestamp filtering, attribute access (resource vs standard, indexed vs non-indexed), severity filters, variables, and performance optimization tips."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseLogsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForLogs,
			},
		}, nil
	})

	clickhouseMetricsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-metrics",
		"ClickHouse Metrics Schema",
		mcp.WithResourceDescription("ClickHouse schema for samples_v4, exp_hist, time_series_v4 (and 6hrs/1day variants) and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseMetricsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.MetricsSchema,
			},
		}, nil
	})

	clickhouseMetricsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-metrics-example",
		"Clickhouse Examples for Metrics",
		mcp.WithResourceDescription("ClickHouse SQL query examples for SigNoz metrics. Includes basic queries , rate calculation patterns for counter metrics (using lagInFrame and runningDifference), error rate calculations (ratio of two metrics), histogram quantile queries for latency percentiles (P95, P99), and key patterns for time series table selection by granularity, timestamp filtering, label filtering, time interval aggregation, variables, and performance optimization"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseMetricsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForMetrics,
			},
		}, nil
	})

	clickhouseTracesSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-traces",
		"ClickHouse Traces Schema",
		mcp.WithResourceDescription("ClickHouse schema for signoz_index_v3, signoz_spans, signoz_error_index_v2, traces_v3_resource, dependency_graph_minutes_v2, trace_summary, top_level_operations and their distributed counterparts. requires dashboard instructions at signoz://dashboard/instructions"),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseTracesSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.TracesSchema,
			},
		}, nil
	})

	clickhouseTracesExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-traces-example",
		"Clickhouse Examples for Traces",
		mcp.WithResourceDescription("ClickHouse SQL examples for SigNoz traces: resource filters, timeseries/value/table queries, span event extraction, latency analysis, and performance tips."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(clickhouseTracesExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.ClickhouseSqlQueryForTraces,
			},
		}, nil
	})

	promqlExample := mcp.NewResource(
		"signoz://promql/instructions",
		"PromQL Instructions",
		mcp.WithResourceDescription("SigNoz PromQL guide — used by promql_rule alerts and PromQL dashboard widgets. Covers OTel dotted metric names (Prometheus 3.x UTF-8 quoted selector form), the anti-pattern table of forms that return no data, dotted resource attributes in by() and label matchers, examples by metric type, and the pre-flight checklist for PromQL alerts."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(promqlExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     promql.Instructions,
			},
		}, nil
	})

	queryBuilderExample := mcp.NewResource(
		"signoz://dashboard/query-builder-example",
		"Query Builder Examples",
		mcp.WithResourceDescription("SigNoz Query Builder reference: CRITICAL OpenTelemetry metric naming conventions (dot vs underscore suffixes), filtering, aggregation, legend formatting for grouped charts, search syntax, operators, field existence behavior, full-text search, functions, advanced examples, and best practices."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(queryBuilderExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.Querybuilder,
			},
		}, nil
	})

	dashboardInstructions := mcp.NewResource(
		"signoz://dashboard/instructions",
		"Dashboard Basic Instructions",
		mcp.WithResourceDescription("SigNoz dashboard basics: title, tags, description, and comprehensive variable configuration rules (types, properties, referencing, chaining)."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(dashboardInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.Basics,
			},
		}, nil
	})

	widgetsInstructions := mcp.NewResource(
		"signoz://dashboard/widgets-instructions",
		"Dashboard Basic Instructions",
		mcp.WithResourceDescription("SigNoz dashboard widgets: 7 panel types (Bar, Histogram, List, Pie, Table, Timeseries, Value) with use cases, configuration options, and critical layout rules (grid coordinates, dimensions, legends)."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(widgetsInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.WidgetsInstructions,
			},
		}, nil
	})

	widgetsExamplesResource := mcp.NewResource(
		"signoz://dashboard/widgets-examples",
		"Dashboard Widgets Examples",
		mcp.WithResourceDescription("Complete widget configurations with required fields, panel-specific examples, validation checks, troubleshooting, and legend formatting for grouped chart queries."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(widgetsExamplesResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     dashboard.WidgetExamples,
			},
		}, nil
	})
}
