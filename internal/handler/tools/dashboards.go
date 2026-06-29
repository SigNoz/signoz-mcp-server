package tools

import (
	"context"
	_ "embed"
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
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// v2 dashboard tool input schemas, extracted from the SigNoz OpenAPI spec
// (docs/api/openapi.yml) as self-contained JSON Schemas with the Perses plugin
// oneOf unions intact. They are served to MCP clients verbatim via
// WithRawInputSchema — the handlers are pure pass-throughs to the v2 API, which
// is the authoritative validator.
//
//go:embed schemas/dashboard_create.json
var createDashboardSchema []byte

//go:embed schemas/dashboard_update.json
var updateDashboardSchema []byte

//go:embed schemas/dashboard_patch.json
var patchDashboardSchema []byte

// rawInputSchema wires a pre-built JSON Schema as a tool's input schema. It
// clears the default object InputSchema that mcp.NewTool seeds, because
// mcp-go's Tool.MarshalJSON rejects a tool that has BOTH InputSchema and
// RawInputSchema set (mcp.WithRawInputSchema alone leaves the default in place).
func rawInputSchema(schema []byte) mcp.ToolOption {
	return func(t *mcp.Tool) {
		t.InputSchema = mcp.ToolInputSchema{}
		t.RawInputSchema = json.RawMessage(schema)
	}
}

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
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes a 'total' count of all dashboards. When searching for a specific dashboard, page through all results using 'offset' (increment by 'limit') until you've covered 'total'. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.DefaultString("50"), mcp.Description("Maximum number of dashboards to return per page. Use this to paginate through large result sets. Default: 50, max: 1000 (higher values are clamped). Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.DefaultString("0"), mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Default: 0. Must be >= 0.")),
	)

	addTool(s, tool, h.handleListDashboards)

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get full details of a specific dashboard by ID (returns complete dashboard configuration with all panels and queries)"),
		// Not mcp.Required(): the legacy alias "uuid" must remain a valid call for
		// schema-aware clients. The handler validates id/uuid presence.
		mcp.WithString("id", mcp.Description("Dashboard UUID. Required.")),
	)

	addTool(s, getDashboardTool, h.handleGetDashboard)

	createDashboardTool := mcp.NewTool(
		"signoz_create_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Creates a new monitoring dashboard based on the provided Perses (v6) spec — 'display', 'variables' (array), 'panels' (map keyed by panel id), and 'layouts' (array). "+
				"CRITICAL: You MUST read these resources BEFORE generating any dashboard output:\n"+
				"1. signoz://dashboard/instructions - REQUIRED: Dashboard structure and basics\n"+
				"2. signoz://dashboard/widgets-instructions - REQUIRED: Widget configuration rules\n"+
				"3. signoz://dashboard/widgets-examples - REQUIRED: worked v6 panel examples to copy the structure from\n"+
				"QUERY-SPECIFIC RESOURCES (read based on query type used):\n"+
				"- For PromQL queries: signoz://promql/instructions\n"+
				"- For Query Builder queries: signoz://dashboard/query-builder-example\n"+
				"- For ClickHouse SQL on logs: signoz://dashboard/clickhouse-schema-for-logs + signoz://dashboard/clickhouse-logs-example\n"+
				"- For ClickHouse SQL on metrics: signoz://dashboard/clickhouse-schema-for-metrics + signoz://dashboard/clickhouse-metrics-example\n"+
				"- For ClickHouse SQL on traces: signoz://dashboard/clickhouse-schema-for-traces + signoz://dashboard/clickhouse-traces-example\n\n"+
				"IMPORTANT: Follow this tool's JSON Schema exactly — 'schemaVersion' must be \"v6\" and each panel/query selects a typed plugin by its 'kind' discriminator. "+
				"Do NOT set 'name' — omit it and the server derives one from 'spec.display.name'. "+
				"The SigNoz API validates the payload server-side (unknown fields are rejected); if it returns an error, read the message and resubmit a corrected dashboard.",
		),
		rawInputSchema(createDashboardSchema),
	)

	addTool(s, createDashboardTool, h.handleCreateDashboard)

	updateDashboardTool := mcp.NewTool(
		"signoz_update_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Update an existing dashboard by supplying its UUID along with a fully assembled Perses (v6) dashboard JSON object (schemaVersion \"v6\", name, tags, and the full spec).\n\n"+
				"MANDATORY FIRST STEP: Read signoz://dashboard/instructions before doing ANYTHING else. This is NON-NEGOTIABLE.\n\n"+
				"The provided object must represent the complete post-update state, combining the current dashboard data and the intended modifications. The 'name' is IMMUTABLE — change the human title via 'spec.display.name'. For small, targeted edits prefer signoz_patch_dashboard, which avoids re-sending the whole dashboard.\n\n"+
				"REQUIRED RESOURCES (read ALL before generating output):\n"+
				"1. signoz://dashboard/instructions\n"+
				"2. signoz://dashboard/widgets-instructions\n"+
				"3. signoz://dashboard/widgets-examples\n"+
				"CONDITIONAL RESOURCES (based on query type):\n"+
				"• PromQL → signoz://promql/instructions\n"+
				"• Query Builder → signoz://dashboard/query-builder-example\n"+
				"• ClickHouse Logs → signoz://dashboard/clickhouse-schema-for-logs + signoz://dashboard/clickhouse-logs-example\n"+
				"• ClickHouse Metrics → signoz://dashboard/clickhouse-schema-for-metrics + signoz://dashboard/clickhouse-metrics-example\n"+
				"• ClickHouse Traces → signoz://dashboard/clickhouse-schema-for-traces + signoz://dashboard/clickhouse-traces-example\n\n"+
				"WARNING: 'schemaVersion' must be \"v6\" and unknown fields are rejected. The SigNoz API validates server-side; on error, read the message and resubmit a corrected dashboard. Locked dashboards are rejected.",
		),
		rawInputSchema(updateDashboardSchema),
	)

	addTool(s, updateDashboardTool, h.handleUpdateDashboard)

	patchDashboardTool := mcp.NewTool(
		"signoz_patch_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Apply a partial update to a v2 dashboard using an RFC 6902 JSON Patch, without re-sending the whole dashboard. "+
				"Supply the dashboard 'id' and 'patch' (an array of {op, path, value} operations). Paths are JSON Pointers into the dashboard's postable shape, "+
				"e.g. /spec/display/name, /spec/panels/<panelId>, /spec/panels/<panelId>/spec/queries/0, /spec/variables/0, /tags/-. "+
				"Prefer this over signoz_update_dashboard for targeted changes (renaming, adding/editing one panel or query, tweaking a variable) — it is far cheaper than rebuilding the full dashboard. "+
				"Apply is lenient (remove on a missing path is a no-op; add creates missing parents) but the result is still validated; locked dashboards are rejected.",
		),
		rawInputSchema(patchDashboardSchema),
	)

	addTool(s, patchDashboardTool, h.handlePatchDashboard)

	deleteDashboardTool := mcp.NewTool("signoz_delete_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Delete a dashboard by its ID. This action is irreversible. Use signoz_list_dashboards to find dashboard IDs."),
		mcp.WithString("id", mcp.Description("Dashboard UUID to delete. Required.")),
	)

	addTool(s, deleteDashboardTool, h.handleDeleteDashboard)

	importDashboardTool := mcp.NewTool(
		"signoz_import_dashboard",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Create a new SigNoz dashboard from a curated template hosted in the SigNoz/dashboards GitHub repo. "+
				"Takes a single 'path' argument (e.g. 'hostmetrics/hostmetrics.json' or 'postgresql/postgresql.json') "+
				"that points to a template file on the main branch. The server fetches the JSON "+
				"and creates the dashboard in one call — the client does not need to inline the template body. "+
				"To discover the available paths, call signoz_list_dashboard_templates first and let the model pick the best match. "+
				"For custom dashboards, use signoz_create_dashboard.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Template path within the SigNoz/dashboards repo, e.g. 'hostmetrics/hostmetrics.json'.")),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
	)

	addTool(s, importDashboardTool, h.handleImportDashboard)

	listTemplatesTool := mcp.NewTool(
		"signoz_list_dashboard_templates",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription(
			"List all curated SigNoz dashboard templates bundled with this server. "+
				"Returns the full catalog as a JSON array — each entry includes 'id', 'title', 'path', 'description', 'category', and 'keywords'. "+
				"Use this to discover which template fits the user's intent, then pass the chosen 'path' to signoz_import_dashboard. "+
				"The catalog is small enough to read in full; let the model decide the best match rather than relying on keyword scoring.",
		),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
	)

	addTool(s, listTemplatesTool, h.handleListDashboardTemplates)

	// resources for create and update dashboard
	h.registerDashboardResources(s)
}

func (h *Handler) handleListDashboards(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_dashboards")
	// No client-side clamp (unlike the ParseParamsClamped tools): the v2 API
	// paginates and bounds limit server-side, so we forward the raw value.
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resultJSON, err := client.ListDashboards(ctx, limit, offset)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list dashboards", logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	// Inject a webUrl deep link into each "dashboards" entry (keyed by "id").
	// Fails open: any parse problem or missing base URL leaves result unchanged.
	if base, hasURL := util.GetSigNozURL(ctx); hasURL {
		var resp map[string]any
		if err := json.Unmarshal(resultJSON, &resp); err == nil {
			if list, ok := resp["dashboards"].([]any); ok {
				for _, item := range list {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					id, _ := m["id"].(string)
					if webURL, ok := util.ResourceWebURL(base, "dashboard", id); ok {
						m["webUrl"] = webURL
					}
				}
				if out, err := json.Marshal(resp); err == nil {
					resultJSON = out
				}
			}
		}
	}

	return structuredResult(resultJSON), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.GetDashboard(ctx, uuid)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get dashboard", slog.String("uuid", uuid), logpkg.ErrAttr(err))
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

	// Default to a server-generated name: if no "name" was supplied, set
	// generateName=true so the v2 API derives a valid DNS-1123 name from
	// spec.display.name. If a name is given, it is left as-is (generateName stays unset).
	if name, _ := rawConfig["name"].(string); name == "" {
		rawConfig["generateName"] = true
	}

	// Pass-through: the v2 API is the validator. Marshal the model's object and
	// forward it to POST /api/v2/dashboards verbatim.
	cleanJSON, err := json.Marshal(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to encode dashboard payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Dashboard encode error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_dashboard")
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.CreateDashboardRaw(ctx, cleanJSON)

	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create dashboard in SigNoz", logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	return structuredResult(data), nil
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
		h.logger.ErrorContext(ctx, "Failed to fetch dashboard template", slog.String("path", path), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Template fetch error: %s", err.Error())), nil
	}

	var rawConfig map[string]any
	if err := json.Unmarshal(body, &rawConfig); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse template JSON", slog.String("path", path), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Template parse error: %s", err.Error())), nil
	}
	if len(rawConfig) == 0 {
		return mcp.NewToolResultError("Template is empty after parsing."), nil
	}

	// Pass-through (mirrors handleCreateDashboard): the v2 API is the validator,
	// so forward the fetched template verbatim — no local validation/normalization.
	// Default to a server-generated name when the template carries none, so the
	// derived DNS-1123 name comes from spec.display.name.
	if name, _ := rawConfig["name"].(string); name == "" {
		rawConfig["generateName"] = true
	}
	cleanJSON, err := json.Marshal(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to encode template payload", slog.String("path", path), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Template encode error: %s", err.Error())), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.CreateDashboardRaw(ctx, cleanJSON)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create dashboard from template", slog.String("path", path), logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	return structuredResult(data), nil
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
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode templates: %s", err.Error())), nil
	}
	return structuredResult(body), nil
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
	delete(rawConfig, "uuid")
	delete(rawConfig, "id")
	delete(rawConfig, "searchContext")

	// Pass-through: forward the post-update body to PUT /api/v2/dashboards/{uuid};
	// the v2 API validates it.
	body, err := json.Marshal(rawConfig)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to encode dashboard payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Dashboard encode error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.UpdateDashboardRaw(ctx, uuid, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to update dashboard in SigNoz", logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	return structuredResult(data), nil
}

func (h *Handler) handlePatchDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map.")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: provide an object with "id" and "patch".`), nil
	}

	uuid := readResourceID(rawConfig, "uuid")
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Use signoz_list_dashboards to find dashboard ids.`), nil
	}

	patch, ok := rawConfig["patch"]
	if !ok {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "patch" is required and must be an array of RFC 6902 operations.`), nil
	}

	// Forward the JSON Patch op array to PATCH /api/v2/dashboards/{id}.
	body, err := json.Marshal(patch)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode patch: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_patch_dashboard", slog.String("id", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.PatchDashboardRaw(ctx, uuid, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to patch dashboard in SigNoz", logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	return structuredResult(data), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}
	err = client.DeleteDashboard(ctx, uuid)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to delete dashboard", slog.String("uuid", uuid), logpkg.ErrAttr(err))
		return upstreamError(err), nil
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
		mcp.WithResourceDescription("SigNoz v6 (Perses-schema) dashboard basics: display (title/tags/description), the grid layout (positions + the content.$ref panel linkage), and variable configuration (prefer DynamicVariable, $var reference, and the rule to always ask which panels a variable applies to). For WHICH panel/query type to choose, see signoz://dashboard/widgets-instructions."),
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
		"Dashboard Panels & Queries Guide (v6)",
		mcp.WithResourceDescription("Conceptual guidance for SigNoz dashboards: which query type (Query Builder / ClickHouse SQL / PromQL) and which panel type to choose for a given intent, plus legend and layout conventions. Structure-agnostic — for the exact v6 (Perses) JSON shape see signoz://dashboard/instructions."),
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
		mcp.WithResourceDescription("Worked v6 (Perses) widget examples — real round-tripped panel payloads (timeseries, list, pie, table, value/number) to copy the structure from; one panel per example. For the rules behind them see signoz://dashboard/instructions and signoz://dashboard/widgets-instructions."),
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
