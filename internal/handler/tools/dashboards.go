package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/promql"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// Template fetch configuration for signoz_import_dashboard.
// templateRepoBaseURLVar is a var (not const) so tests can point it at a
// local httptest server. Templates are fetched from the SigNoz/dashboards
// `main` branch — we deliberately do not pin a SHA, so new upstream
// templates become reachable as soon as they land.
const (
	templateRepoRef      = "main"
	templateFetchTimeout = 30 * time.Second
)

var (
	templateRepoBaseURLVar = "https://raw.githubusercontent.com/SigNoz/dashboards"
	templateHTTPClient     = &http.Client{Timeout: templateFetchTimeout}
)

func (h *Handler) RegisterDashboardHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering dashboard handlers")

	tool := mcp.NewTool("signoz_list_dashboards",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants to discover tenant dashboards, browse their summaries, or find a dashboard UUID. It returns names, descriptions, tags, timestamps, and pagination metadata, not widget/query definitions; use signoz_get_dashboard for one full definition. When looking for a specific dashboard, follow pagination.nextOffset while pagination.hasMore is true before concluding it is absent."),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum dashboard summaries per page. Default 50; values above 1000 are clamped.")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of dashboard summaries to skip. Default 0; use pagination.nextOffset for the next page.")),
	)

	h.addTool(s, tool, h.handleListDashboards)

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants the complete definition of one known tenant dashboard, including its layout, variables, widgets, and queries. Use signoz_list_dashboards first when the UUID is unknown. Do not use this to browse summaries or curated templates; use signoz_list_dashboards or signoz_list_dashboard_templates respectively."),
		// Not mcp.Required(): the legacy alias "uuid" must remain a valid call for
		// schema-aware clients. The handler validates id/uuid presence.
		mcp.WithString("id", mcp.Description("Known dashboard UUID. Required; use signoz_list_dashboards to discover it.")),
	)

	h.addTool(s, getDashboardTool, h.handleGetDashboard)

	createDashboardTool := mcp.NewTool(
		"signoz_create_dashboard",
		withCreateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants a custom SigNoz dashboard built from a complete title, layout, variables, and widget configuration; use signoz_import_dashboard instead when a curated template fits. "+
				"Use signoz_create_view instead to save one Explorer query. Before composing the payload, read signoz://dashboard/instructions, signoz://dashboard/widgets-instructions, and signoz://dashboard/widgets-examples, then follow the query-specific resource linked by the widget guide.",
		),
		mcp.WithInputSchema[types.CreateDashboardInput](),
	)

	h.addTool(s, createDashboardTool, h.handleCreateDashboard)

	updateDashboardTool := mcp.NewTool(
		"signoz_update_dashboard",
		withUpdateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to change an existing SigNoz dashboard. This is a full replacement, not a partial patch: fetch it with signoz_get_dashboard, merge only the requested changes, and preserve every other field. "+
				"Use signoz_update_view instead for a saved Explorer query. Before composing changed widgets, read signoz://dashboard/instructions, signoz://dashboard/widgets-instructions, and signoz://dashboard/widgets-examples, then follow the query-specific resource linked by the widget guide.",
		),
		mcp.WithInputSchema[types.UpdateDashboardInput](),
	)

	h.addTool(s, updateDashboardTool, h.handleUpdateDashboard)

	deleteDashboardTool := mcp.NewTool("signoz_delete_dashboard",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user has confirmed they want to permanently delete one tenant dashboard. The deletion is irreversible. Use signoz_list_dashboards to discover the UUID when needed; do not use this for saved Explorer views, which use signoz_delete_view."),
		mcp.WithString("id", mcp.Description("UUID of the dashboard to delete. Required; use signoz_list_dashboards to discover it.")),
	)

	h.addTool(s, deleteDashboardTool, h.handleDeleteDashboard)

	importDashboardTool := mcp.NewTool(
		"signoz_import_dashboard",
		withCreateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants a new dashboard from a curated SigNoz/dashboards template, not a custom configuration. Pass a known relative template path; if it is unknown, call signoz_list_dashboard_templates first. The server fetches and validates the selected template, then creates the dashboard. Use signoz_create_dashboard for a custom layout or queries.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Relative JSON path from signoz_list_dashboard_templates, for example hostmetrics/hostmetrics.json. Do not pass a URL or absolute path.")),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
	)

	h.addTool(s, importDashboardTool, h.handleImportDashboard)

	listTemplatesTool := mcp.NewTool(
		"signoz_list_dashboard_templates",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to browse curated dashboard templates or discover a path for signoz_import_dashboard. It returns the complete bundled catalog with id, title, path, description, category, and keywords. It does not list dashboards already created in the tenant; use signoz_list_dashboards for those.",
		),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
	)

	h.addTool(s, listTemplatesTool, h.handleListDashboardTemplates)

	// resources for create and update dashboard
	h.registerDashboardResources(s)
}

func (h *Handler) handleListDashboards(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_dashboards")
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.ListDashboards(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list dashboards", err)
		return upstreamError(err), nil
	}

	var dashboards map[string]any
	if err := json.Unmarshal(result, &dashboards); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse dashboards response", logpkg.ErrAttr(err))
		return upstreamResponseError("failed to parse response: " + err.Error()), nil
	}

	// Upstream returns `data: null`, omits `data`, or — on some deployments —
	// returns an empty object/scalar when there are no dashboards. Treat any
	// non-array shape as zero rows rather than surfacing a format error (mirrors
	// the list_views coerce-to-empty-page pattern).
	var data []any
	if raw, present := dashboards["data"]; present && raw != nil {
		if arr, ok := raw.([]any); ok {
			data = arr
		} else {
			h.logger.DebugContext(ctx, "dashboards response data was not an array; treating as empty",
				slog.String("data", logpkg.TruncAny(raw)))
		}
	}

	if base, hasURL := util.GetSigNozURL(ctx); hasURL {
		for _, item := range data {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			uuid, _ := m["uuid"].(string)
			if webURL, ok := util.ResourceWebURL(base, "dashboard", uuid); ok {
				m["webUrl"] = webURL
			}
		}
	}

	total := len(data)
	pagedData := paginate.Array(data, offset, limit)

	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap dashboards with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleGetDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	uuid := readResourceID(args, "uuid")
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards. Example: {"id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_dashboard", slog.String("id", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.GetDashboard(ctx, uuid)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get dashboard", err, slog.String("uuid", uuid))
		return upstreamError(err), nil
	}
	data = enrichDashboardWebURL(ctx, data, uuid)
	return structuredResult(data), nil
}

// enrichDashboardWebURL injects a webUrl deep link into a single-dashboard
// passthrough body. Delegates to util.InjectWebURL, which preserves large
// int64 fields and fails open on unparseable input.
func enrichDashboardWebURL(ctx context.Context, data []byte, uuid string) []byte {
	base, _ := util.GetSigNozURL(ctx)
	return util.InjectWebURL(data, base, "dashboard", uuid)
}

func (h *Handler) handleCreateDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map.")
		return notAConfigObjectError(), nil
	}
	delete(rawConfig, "searchContext")

	// Validate and normalize via the dashboardbuilder + panelbuilder pipeline.
	cleanJSON, err := dashboard.ValidateFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Dashboard validation failed", logpkg.ErrAttr(err))
		return validationResult(fmt.Sprintf("Dashboard validation error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_dashboard")
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.CreateDashboardRaw(ctx, cleanJSON)

	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create dashboard in SigNoz", err)
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleImportDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}
	path, ok := args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return validationError("path", `must be a non-empty string, e.g. "hostmetrics/hostmetrics.json". Use signoz_list_dashboard_templates to discover available paths.`), nil
	}
	path = strings.TrimSpace(path)
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return validationError("path", `must be a relative template path within the SigNoz/dashboards repo (e.g. "hostmetrics/hostmetrics.json"), not an absolute path or URL.`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_import_dashboard", slog.String("path", path))

	body, err := fetchTemplate(ctx, path)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to fetch dashboard template", err, slog.String("path", path))
		return errorWithCause(err, CodeUpstreamError, fmt.Sprintf("Template fetch error: %s", err.Error())), nil
	}

	var rawConfig map[string]any
	if err := json.Unmarshal(body, &rawConfig); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse template JSON", slog.String("path", path), logpkg.ErrAttr(err))
		return upstreamResponseError(fmt.Sprintf("Template parse error: %s", err.Error())), nil
	}
	if len(rawConfig) == 0 {
		return upstreamResponseError("Template is empty after parsing."), nil
	}

	cleanJSON, err := dashboard.ValidateFromMap(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Template validation failed", slog.String("path", path), logpkg.ErrAttr(err))
		return upstreamResponseError(fmt.Sprintf("Template validation error: %s", err.Error())), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.CreateDashboardRaw(ctx, cleanJSON)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create dashboard from template", err, slog.String("path", path))
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// fetchTemplate downloads a dashboard template JSON from the SigNoz/dashboards
// `main` branch. Returns the raw response body.
func fetchTemplate(ctx context.Context, path string) ([]byte, error) {
	rel := strings.TrimPrefix(path, "/")
	// Preserve "/" separators between path segments while encoding each.
	segments := strings.Split(rel, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	fullURL := fmt.Sprintf("%s/%s/%s", templateRepoBaseURLVar, templateRepoRef, strings.Join(segments, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := templateHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("template not found at %s (HTTP 404). Verify the path exists on the main branch of the SigNoz/dashboards repo", path)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, path)
	}

	return io.ReadAll(resp.Body)
}

func (h *Handler) handleListDashboardTemplates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_dashboard_templates")

	entries := listDashboardTemplates()
	body, err := json.Marshal(entries)
	if err != nil {
		return InternalErrorResult(fmt.Sprintf("failed to encode templates: %s", err.Error())), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func (h *Handler) handleUpdateDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map from Claude.")
		return notAConfigObjectError(), nil
	}

	// Extract id before validation (it's at the top level, not inside dashboard data).
	uuid := readResourceID(rawConfig, "uuid")
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
	}

	// Extract the dashboard sub-object for validation.
	dashboardRaw, ok := rawConfig["dashboard"].(map[string]any)
	if !ok || len(dashboardRaw) == 0 {
		return validationError("dashboard", "is required and must be a valid object."), nil
	}

	// Validate and normalize via the dashboardbuilder + panelbuilder pipeline.
	cleanJSON, err := dashboard.ValidateFromMap(dashboardRaw)
	if err != nil {
		h.logger.WarnContext(ctx, "Dashboard validation failed", logpkg.ErrAttr(err))
		return validationResult(fmt.Sprintf("Dashboard validation error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	err = client.UpdateDashboardRaw(ctx, uuid, cleanJSON)

	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to update dashboard in SigNoz", err)
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText("dashboard updated"), nil
}

func (h *Handler) handleDeleteDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	uuid := readResourceID(args, "uuid")
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards. Example: {"id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_dashboard", slog.String("id", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	err = client.DeleteDashboard(ctx, uuid)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete dashboard", err, slog.String("uuid", uuid))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText("dashboard deleted"), nil
}

// registerDashboardResources registers all MCP resources needed for dashboard creation/update.
func (h *Handler) registerDashboardResources(s *server.MCPServer) {
	clickhouseLogsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-logs",
		"ClickHouse Logs Schema",
		mcp.WithResourceDescription("Read this before writing ClickHouse SQL for a dashboard widget over SigNoz logs. It lists tables and columns from the schema bundled with this server. Also read signoz://dashboard/clickhouse-logs-example. If the live SigNoz instance rejects a table or column, follow that error because the bundled schema may lag."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.LogsSchema))),
	)

	h.addResource(s, clickhouseLogsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.LogsSchema,
			},
		}, nil
	})

	clickhouseLogsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-logs-example",
		"ClickHouse Logs Examples",
		mcp.WithResourceDescription("Read this after signoz://dashboard/clickhouse-schema-for-logs when composing a raw ClickHouse logs widget. It covers timestamp and attribute filters, resource CTEs, variables, timeseries/value shapes, and performance patterns."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.ClickhouseSqlQueryForLogs))),
	)

	h.addResource(s, clickhouseLogsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.ClickhouseSqlQueryForLogs,
			},
		}, nil
	})

	clickhouseMetricsSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-metrics",
		"ClickHouse Metrics Schema",
		mcp.WithResourceDescription("Read this before writing ClickHouse SQL for a dashboard widget over SigNoz metrics. It lists tables and columns from the schema bundled with this server. Also read signoz://dashboard/clickhouse-metrics-example. If the live SigNoz instance rejects a table or column, follow that error because the bundled schema may lag."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.MetricsSchema))),
	)

	h.addResource(s, clickhouseMetricsSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.MetricsSchema,
			},
		}, nil
	})

	clickhouseMetricsExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-metrics-example",
		"ClickHouse Metrics Examples",
		mcp.WithResourceDescription("Read this after signoz://dashboard/clickhouse-schema-for-metrics when composing a raw ClickHouse metrics widget. It covers counter rates, error ratios, histogram quantiles, time-series tables, variables, and performance patterns."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.ClickhouseSqlQueryForMetrics))),
	)

	h.addResource(s, clickhouseMetricsExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.ClickhouseSqlQueryForMetrics,
			},
		}, nil
	})

	clickhouseTracesSchemaResource := mcp.NewResource(
		"signoz://dashboard/clickhouse-schema-for-traces",
		"ClickHouse Traces Schema",
		mcp.WithResourceDescription("Read this before writing ClickHouse SQL for a dashboard widget over SigNoz traces. It lists tables and columns from the schema bundled with this server. Also read signoz://dashboard/clickhouse-traces-example. If the live SigNoz instance rejects a table or column, follow that error because the bundled schema may lag."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.TracesSchema))),
	)

	h.addResource(s, clickhouseTracesSchemaResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.TracesSchema,
			},
		}, nil
	})

	clickhouseTracesExample := mcp.NewResource(
		"signoz://dashboard/clickhouse-traces-example",
		"ClickHouse Traces Examples",
		mcp.WithResourceDescription("Read this after signoz://dashboard/clickhouse-schema-for-traces when composing a raw ClickHouse traces widget. It covers resource filters, timeseries/value/table result shapes, span events, latency analysis, and performance patterns."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.ClickhouseSqlQueryForTraces))),
	)

	h.addResource(s, clickhouseTracesExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.ClickhouseSqlQueryForTraces,
			},
		}, nil
	})

	promqlExample := mcp.NewResource(
		"signoz://promql/instructions",
		"PromQL Instructions",
		mcp.WithResourceDescription("Read this when composing a PromQL dashboard widget or promql_rule alert. It explains SigNoz's Prometheus 3.x quoted-selector form for dotted OTel metric names, resource labels, examples, and query checks; do not use it for Query Builder or ClickHouse SQL."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(promql.Instructions))),
	)

	h.addResource(s, promqlExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     promql.Instructions,
			},
		}, nil
	})

	queryBuilderExample := mcp.NewResource(
		"signoz://dashboard/query-builder-example",
		"Query Builder Examples",
		mcp.WithResourceDescription("Read this when composing a Query Builder dashboard widget. It covers signal-specific aggregations, filters, metric naming, grouped legends, functions, and examples; use the ClickHouse or PromQL resources instead for those query types."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.Querybuilder))),
	)

	h.addResource(s, queryBuilderExample, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.Querybuilder,
			},
		}, nil
	})

	dashboardInstructions := mcp.NewResource(
		"signoz://dashboard/instructions",
		"Dashboard Basic Instructions",
		mcp.WithResourceDescription("Read this before creating or fully replacing a dashboard. It explains dashboard fields, metadata, variables, variable chaining, and layout. Also read signoz://dashboard/widgets-instructions for widget and query choices."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.Basics))),
	)

	h.addResource(s, dashboardInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.Basics,
			},
		}, nil
	})

	widgetsInstructions := mcp.NewResource(
		"signoz://dashboard/widgets-instructions",
		"Dashboard Widget Instructions",
		mcp.WithResourceDescription("Read this when choosing or building dashboard widgets. It explains when to use each panel type, required layout and legend fields, and which detailed guide to read for each query type. Also read signoz://dashboard/instructions."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.WidgetsInstructions))),
	)

	h.addResource(s, widgetsInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.WidgetsInstructions,
			},
		}, nil
	})

	widgetsExamplesResource := mcp.NewResource(
		"signoz://dashboard/widgets-examples",
		"Dashboard Widgets Examples",
		mcp.WithResourceDescription("Read this after the dashboard and widget instructions when building panels. It includes examples by panel type, validation checks, troubleshooting, and grouped-series legends. Verify field names in the target SigNoz workspace."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.WidgetExamples))),
	)

	h.addResource(s, widgetsExamplesResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.WidgetExamples,
			},
		}, nil
	})
}
