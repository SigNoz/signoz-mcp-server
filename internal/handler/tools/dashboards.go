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

// updatableDashboardFields are the PUT body fields (from the update schema);
// GET-only fields like createdAt/orgId/webUrl must be dropped or v2 rejects them.
var updatableDashboardFields = updatableFieldsFromSchema(updateDashboardSchema)

func updatableFieldsFromSchema(schemaJSON []byte) map[string]struct{} {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		panic("dashboards: cannot parse embedded update schema: " + err.Error())
	}
	fields := make(map[string]struct{}, len(s.Properties))
	for k := range s.Properties {
		switch k {
		case "id", "uuid", "searchContext": // envelope, not body
		default:
			fields[k] = struct{}{}
		}
	}
	return fields
}

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
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants to discover tenant dashboards, browse their summaries, or find a dashboard UUID. It returns names, descriptions, tags, timestamps, and a total count, not panel/query definitions; use signoz_get_dashboard for one full definition. Narrow the results with the optional filter expression (by name, description, tags, creator, timestamps, or locked state). When looking for a specific dashboard, page by raising offset by limit until you have covered total before concluding it is absent."),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum dashboard summaries per page. Default 50; values above 200 are clamped (the v2 API's server-side cap).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of dashboard summaries to skip. Default 0; raise by limit to page until you reach total.")),
		mcp.WithString("filter", mcp.Description("Optional server-side filter over dashboard metadata (name, description, tags, creator, timestamps, locked state); omit to list all. "+
			"Read signoz://dashboard/list-filter-guide for the filter DSL grammar, per-key operators, value formats, and examples. "+
			"Example: \"name CONTAINS 'overview' AND locked = true\".")),
		mcp.WithString("sort", mcp.Enum("updated_at", "created_at", "name"), mcp.Description("Sort field: 'updated_at' (default), 'created_at', or 'name'.")),
		mcp.WithString("order", mcp.Enum("asc", "desc"), mcp.Description("Sort order: 'asc' or 'desc' (default 'desc').")),
	)

	h.addTool(s, tool, h.handleListDashboards)

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants the complete definition of one known tenant dashboard, including its layout, variables, panels, and queries. Use signoz_list_dashboards first when the UUID is unknown. Do not use this to browse summaries or curated templates; use signoz_list_dashboards or signoz_list_dashboard_templates respectively."),
		// Not mcp.Required(): the legacy alias "uuid" must remain a valid call for
		// schema-aware clients. The handler validates id/uuid presence.
		mcp.WithString("id", mcp.Description("Known dashboard UUID. Required; use signoz_list_dashboards to discover it.")),
	)

	h.addTool(s, getDashboardTool, h.handleGetDashboard)

	createDashboardTool := mcp.NewTool(
		"signoz_create_dashboard",
		withCreateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants a custom SigNoz dashboard built from a complete title, layout, variables, and panel configuration; use signoz_import_dashboard instead when a curated template fits. "+
				"Use signoz_create_view instead to save one Explorer query. Before composing the payload, read signoz://dashboard/instructions, signoz://dashboard/widgets-instructions, signoz://dashboard/widgets-examples, and signoz://dashboard/examples (complete worked dashboards), then follow the query-specific resource linked by the widget guide.",
		),
		rawInputSchema(createDashboardSchema),
	)

	h.addTool(s, createDashboardTool, h.handleCreateDashboard)

	updateDashboardTool := mcp.NewTool(
		"signoz_update_dashboard",
		withUpdateToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to change an existing SigNoz dashboard. This is a full replacement, not a partial patch: fetch it with signoz_get_dashboard, merge only the requested changes, and preserve every other field. For small, targeted edits prefer signoz_patch_dashboard, which avoids re-sending the whole dashboard. "+
				"Use signoz_update_view instead for a saved Explorer query. Before composing changed panels, read signoz://dashboard/instructions, signoz://dashboard/widgets-instructions, and signoz://dashboard/widgets-examples, then follow the query-specific resource linked by the widget guide.",
		),
		rawInputSchema(updateDashboardSchema),
	)

	h.addTool(s, updateDashboardTool, h.handleUpdateDashboard)

	patchDashboardTool := mcp.NewTool(
		"signoz_patch_dashboard",
		withPatchToolAnnotations(),
		mcp.WithDescription(
			"Apply a partial update to a v2 dashboard using an RFC 6902 JSON Patch, without re-sending the whole dashboard. "+
				"Supply the dashboard 'id' and 'patch' (an array of {op, path, value} operations). Paths are JSON Pointers into the dashboard's postable shape, "+
				"e.g. /spec/display/name, /spec/panels/<panelId>, /spec/panels/<panelId>/spec/queries/0, /spec/variables/0, /tags/-. "+
				"Prefer this over signoz_update_dashboard for targeted changes (renaming, adding/editing one panel or query, tweaking a variable) — it is far cheaper than rebuilding the full dashboard. "+
				"Apply is lenient (remove on a missing path is a no-op; add creates missing parents) but the result is still validated; locked dashboards are rejected. "+
				"Read signoz://dashboard/patch-instructions for worked recipes and exact paths (e.g. adding a panel takes two ops).",
		),
		rawInputSchema(patchDashboardSchema),
	)

	h.addTool(s, patchDashboardTool, h.handlePatchDashboard)

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

// dashboardListMaxLimit is the v2 /api/v2/dashboards server-side page cap
// (MaxListLimit). Clamping to it keeps the requested limit equal to what the
// server returns, so paging by offset does not overshoot and silently skip rows.
const dashboardListMaxLimit = 200

func (h *Handler) handleListDashboards(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_dashboards")
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)
	// v2 caps the page size tighter than the shared MaxLimit; clamp to the
	// server cap so the requested limit equals what the server returns and
	// paging by offset does not overshoot total and silently skip rows.
	if limit > dashboardListMaxLimit {
		limit = dashboardListMaxLimit
		limitClamped = true
	}
	filter, sort, order := "", "", ""
	if args, ok := req.Params.Arguments.(map[string]any); ok {
		filter = strings.TrimSpace(stringArg(args, "filter"))
		sort = strings.TrimSpace(stringArg(args, "sort"))
		order = strings.TrimSpace(stringArg(args, "order"))
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	resultJSON, err := client.ListDashboards(ctx, limit, offset, filter, sort, order)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list dashboards", err)
		return upstreamError(err), nil
	}

	// Inject a webUrl deep link into each "dashboards" entry (keyed by "id").
	// Fails open: any parse problem or missing base URL leaves result unchanged.
	if base, hasURL := util.GetSigNozURL(ctx); hasURL {
		resultJSON = util.InjectListWebURL(resultJSON, base, "dashboard", "dashboards", "id")
	}

	if limitClamped {
		return structuredResultWithNotes(resultJSON, fmt.Sprintf(
			"Requested limit exceeded the maximum of %d and was clamped. Use offset to page through the rest.",
			dashboardListMaxLimit)), nil
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

// enrichCreatedDashboardWebURL injects webUrl into a create response whose id is
// only known from the body (the server generates it). It reads just the id
// (under a "data" envelope or at top level, "id" with a "uuid" fallback) with a
// targeted probe that does not touch the body, then delegates the actual
// injection to util.InjectWebURL (precision-preserving, fails open).
func enrichCreatedDashboardWebURL(ctx context.Context, data []byte) []byte {
	base, ok := util.GetSigNozURL(ctx)
	if !ok || base == "" {
		return data
	}
	var probe struct {
		ID   string `json:"id"`
		UUID string `json:"uuid"`
		Data struct {
			ID   string `json:"id"`
			UUID string `json:"uuid"`
		} `json:"data"`
	}
	_ = json.Unmarshal(data, &probe)
	id := probe.Data.ID
	for _, cand := range []string{probe.Data.UUID, probe.ID, probe.UUID} {
		if id == "" {
			id = cand
		}
	}
	if id == "" {
		return data
	}
	return util.InjectWebURL(data, base, "dashboard", id)
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
		return InternalErrorResult(fmt.Sprintf("Dashboard encode error: %s", err.Error())), nil
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

	data = enrichCreatedDashboardWebURL(ctx, data)
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
		return InternalErrorResult(fmt.Sprintf("Template encode error: %s", err.Error())), nil
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
		return InternalErrorResult(fmt.Sprintf("failed to encode templates: %s", err.Error())), nil
	}
	return structuredResult(body), nil
}

func (h *Handler) handleUpdateDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)

	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map from Claude.")
		return notAConfigObjectError(), nil
	}

	// A fetched dashboard may arrive wrapped in the v2 {status, data:{…}} envelope
	// (as signoz_get_dashboard returns it); operate on the inner object.
	source := rawConfig
	if inner, ok := rawConfig["data"].(map[string]any); ok {
		source = inner
	}

	// id may be a top-level param or the fetched dashboard's own id.
	uuid := readResourceID(rawConfig, "uuid")
	if uuid == "" {
		uuid = readResourceID(source, "uuid")
	}
	if uuid == "" {
		h.logger.WarnContext(ctx, "Empty id parameter")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
	}

	// Keep only updatable body fields so a fetched dashboard's read-only fields don't trip the v2 decoder.
	updatable := make(map[string]any, len(source))
	for k, v := range source {
		if _, ok := updatableDashboardFields[k]; ok {
			updatable[k] = v
		}
	}

	body, err := json.Marshal(updatable)
	if err != nil {
		h.logger.WarnContext(ctx, "Failed to encode dashboard payload", logpkg.ErrAttr(err))
		return InternalErrorResult(fmt.Sprintf("Dashboard encode error: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_update_dashboard", slog.String("uuid", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.UpdateDashboardRaw(ctx, uuid, body)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to update dashboard in SigNoz", err)
		return upstreamError(err), nil
	}

	data = enrichDashboardWebURL(ctx, data, uuid)
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
		return InternalErrorResult(fmt.Sprintf("failed to encode patch: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_patch_dashboard", slog.String("id", uuid))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.PatchDashboardRaw(ctx, uuid, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to patch dashboard in SigNoz", logpkg.ErrAttr(err))
		return upstreamError(err), nil
	}

	data = enrichDashboardWebURL(ctx, data, uuid)
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
		mcp.WithResourceDescription("Read this after the dashboard and widget instructions when building panels. It provides one worked, server-verified v6 panel payload per panel type (timeseries, list, pie, table, value/number) to copy structurally. Verify field names in the target SigNoz workspace."),
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

	listFilterGuide := mcp.NewResource(
		"signoz://dashboard/list-filter-guide",
		"Dashboard List Filter Guide",
		mcp.WithResourceDescription("Read this to build the optional filter argument of signoz_list_dashboards. It documents the server-side metadata filter DSL: grammar (terms, AND/OR/NOT, free-text search), the filterable keys (name, description, created_by, created_at, updated_at, locked, and tag keys) with their operators and value formats, and worked examples."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.ListFilterGuide))),
	)

	h.addResource(s, listFilterGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.ListFilterGuide,
			},
		}, nil
	})

	patchInstructions := mcp.NewResource(
		"signoz://dashboard/patch-instructions",
		"Dashboard Patch Instructions",
		mcp.WithResourceDescription("Read this before calling signoz_patch_dashboard. It gives worked RFC 6902 JSON Patch recipes with exact JSON Pointer paths for targeted edits (rename, add/edit/move/remove a panel, edit a query, variables, tags), including the two-op sequences the backend requires (adding a panel needs a panel op plus a grid-item op). For the panel/query/variable JSON to use as a patch value, also read signoz://dashboard/widgets-examples and signoz://dashboard/instructions."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.PatchInstructions))),
	)

	h.addResource(s, patchInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.PatchInstructions,
			},
		}, nil
	})

	dashboardExamples := mcp.NewResource(
		"signoz://dashboard/examples",
		"Dashboard Examples",
		mcp.WithResourceDescription("Read this when assembling a complete dashboard (panels + layout) with signoz_create_dashboard, or for a worked metrics Query Builder panel. It provides whole server-verified v6 create payloads: a timeseries grouped by an attribute, the same with a dynamic variable used as a filter, a number/value panel, and a multi-panel dashboard. For single-panel shapes see signoz://dashboard/widgets-examples; for layout and variable rules see signoz://dashboard/instructions."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(dashboard.DashboardExamples))),
	)

	h.addResource(s, dashboardExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     dashboard.DashboardExamples,
			},
		}, nil
	})
}
