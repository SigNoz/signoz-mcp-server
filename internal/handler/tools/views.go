package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/views"
)

// validSourcePages is the allow-list for the explorer-views sourcePage param.
var validSourcePages = map[string]struct{}{
	"traces":  {},
	"logs":    {},
	"metrics": {},
}

func validateSourcePage(sp string) error {
	if sp == "" {
		return fmt.Errorf(`parameter validation failed: "sourcePage" is required. Must be one of: "traces", "logs", "metrics"`)
	}
	if _, ok := validSourcePages[sp]; !ok {
		return fmt.Errorf(`parameter validation failed: "sourcePage" must be one of: "traces", "logs", "metrics" (got %q)`, sp)
	}
	return nil
}

// RegisterViewHandlers registers the unified saved-views CRUD tools plus
// the signoz://view/instructions and signoz://view/examples resources.
func (h *Handler) RegisterViewHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering view handlers")

	listTool := mcp.NewTool("signoz_list_views",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List SigNoz saved Explorer views for a given sourcePage. A saved view is a reusable Explorer query (filters, aggregations, panel type). "+
			"IMPORTANT: Supports pagination via 'limit' and 'offset'. The response includes 'pagination' with 'total', 'hasMore', and 'nextOffset'. When searching for a specific view, ALWAYS check 'pagination.hasMore' — if true, continue paging with 'nextOffset' until you find the item or 'hasMore' is false. Never conclude a view doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("sourcePage", mcp.Required(), mcp.Description(`Required. Which Explorer to list views for. One of: "traces", "logs", "metrics".`)),
		mcp.WithString("name", mcp.Description("Optional partial-match filter on view name (applied server-side).")),
		mcp.WithString("category", mcp.Description("Optional partial-match filter on view category (applied server-side).")),
		mcp.WithString("limit", mcp.Description("Maximum number of views to return per page. Default: 50.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use 'pagination.nextOffset' from the previous page. Default: 0.")),
	)
	s.AddTool(listTool, h.handleListViews)

	getTool := mcp.NewTool("signoz_get_view",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Fetch a single SigNoz saved view by UUID. Use the returned object as the base for signoz_update_view — the update is a full-body replace."),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("Saved view UUID. Use signoz_list_views to discover IDs.")),
	)
	s.AddTool(getTool, h.handleGetView)

	createTool := mcp.NewTool("signoz_create_view",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Create a new SigNoz saved Explorer view.\n\n"+
				"CRITICAL: You MUST read these resources BEFORE composing a payload:\n"+
				"1. signoz://view/instructions — REQUIRED: SavedView field schema and sourcePage rules\n"+
				"2. signoz://view/examples — REQUIRED: full working payloads for traces/logs/metrics\n\n"+
				"Required fields: name, sourcePage (one of traces|logs|metrics), compositeQuery (object). "+
				"Optional: category, tags, extraData. Server populates id, createdAt/By, updatedAt/By — "+
				"do not send them.",
		),
		mcp.WithInputSchema[types.SavedViewInput](),
	)
	s.AddTool(createTool, h.handleCreateView)

	updateTool := mcp.NewTool("signoz_update_view",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithDescription(
			"Replace an existing SigNoz saved view (HTTP PUT — full body replace). "+
				"Pass the view's UUID as viewId and the full SavedView body as view. "+
				"ALWAYS call signoz_get_view first, modify the `data` object it returns, "+
				"and pass that under the `view` field here. Partial bodies will wipe unspecified fields. "+
				"See signoz://view/instructions for the SavedView schema. "+
				"Do not send id/createdAt/createdBy/updatedAt/updatedBy — the server ignores them.",
		),
		mcp.WithInputSchema[types.UpdateViewInput](),
	)
	s.AddTool(updateTool, h.handleUpdateView)

	deleteTool := mcp.NewTool("signoz_delete_view",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Permanently delete a SigNoz saved view by UUID. This cannot be undone."),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("UUID of the view to delete.")),
	)
	s.AddTool(deleteTool, h.handleDeleteView)

	viewInstructions := mcp.NewResource(
		"signoz://view/instructions",
		"Saved View Instructions",
		mcp.WithResourceDescription("SigNoz saved-view schema: SavedView fields, sourcePage values, compositeQuery rules, and the GET-then-PUT update flow."),
		mcp.WithMIMEType("text/markdown"),
	)
	s.AddResource(viewInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     views.Instructions,
			},
		}, nil
	})

	viewExamples := mcp.NewResource(
		"signoz://view/examples",
		"Saved View Examples",
		mcp.WithResourceDescription("Three complete SavedView payloads — one per sourcePage (traces, logs, metrics) — suitable for signoz_create_view."),
		mcp.WithMIMEType("text/markdown"),
	)
	s.AddResource(viewExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     views.Examples,
			},
		}, nil
	})
}

// serverPopulatedViewFields are set by the SigNoz API on create/read and
// must not be echoed back on create or update — upstream ignores them at
// best and rejects them at worst.
var serverPopulatedViewFields = []string{
	"id", "createdAt", "createdBy", "updatedAt", "updatedBy",
}

// validateBuilderSignal enforces the documented rule: every builder_query
// spec's `signal` field must match the view's sourcePage. Upstream does not
// enforce this, and a mismatch silently saves an unusable view. Ignores
// non-builder queries (promql, clickhouse_sql) since they don't carry a
// signal field. Returns nil if no mismatches are found.
func validateBuilderSignal(compositeQuery any, sourcePage string) error {
	cq, ok := compositeQuery.(map[string]any)
	if !ok {
		return nil
	}
	queries, ok := cq["queries"].([]any)
	if !ok {
		return nil
	}
	for i, q := range queries {
		entry, ok := q.(map[string]any)
		if !ok {
			continue
		}
		if qt, _ := entry["type"].(string); qt != "builder_query" {
			continue
		}
		spec, ok := entry["spec"].(map[string]any)
		if !ok {
			continue
		}
		signal, _ := spec["signal"].(string)
		if signal == "" {
			continue
		}
		if signal != sourcePage {
			return fmt.Errorf(
				`Parameter validation failed: compositeQuery.queries[%d].spec.signal = %q but sourcePage = %q. They must match.`,
				i, signal, sourcePage,
			)
		}
	}
	return nil
}

// stripNonBodyFields removes MCP-specific metadata (searchContext, viewId)
// and server-populated SavedView fields from a map so what remains is a
// clean request body.
func stripNonBodyFields(m map[string]any) {
	delete(m, "searchContext")
	delete(m, "viewId")
	for _, k := range serverPopulatedViewFields {
		delete(m, k)
	}
}

// marshalViewBody strips non-body fields and marshals the remainder as a
// SavedView body.
func marshalViewBody(args map[string]any) ([]byte, error) {
	stripNonBodyFields(args)
	return json.Marshal(args)
}

// unwrapViewEnvelope handles callers who passed the full response of
// signoz_get_view (shape: {"status":"success","data":{...}}) straight
// into signoz_create_view / signoz_update_view. If the args look like
// that envelope — a `data` field holding an object, and no top-level
// `sourcePage` / `name` — replace args' contents with data's. The
// tool descriptions explicitly instruct this flow, so supporting it
// avoids a misleading "name is required" validation error.
func unwrapViewEnvelope(args map[string]any) {
	data, ok := args["data"].(map[string]any)
	if !ok {
		return
	}
	// Only unwrap when the outer object lacks the SavedView identity
	// fields; otherwise the caller is legitimately sending a view that
	// happens to have a `data` subfield (e.g. extraData).
	if _, hasName := args["name"]; hasName {
		return
	}
	if _, hasSourcePage := args["sourcePage"]; hasSourcePage {
		return
	}
	// Preserve searchContext and viewId (MCP-level fields) across the swap.
	preserved := map[string]any{}
	if v, ok := args["searchContext"]; ok {
		preserved["searchContext"] = v
	}
	if v, ok := args["viewId"]; ok {
		preserved["viewId"] = v
	}
	for k := range args {
		delete(args, k)
	}
	for k, v := range data {
		args[k] = v
	}
	for k, v := range preserved {
		args[k] = v
	}
}

func (h *Handler) handleListViews(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		h.logger.WarnContext(ctx, "list_views validation failed", slog.String("sourcePage", sourcePage))
		return mcp.NewToolResultError(err.Error()), nil
	}
	name, _ := args["name"].(string)
	category, _ := args["category"].(string)
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	h.logger.DebugContext(ctx, "Tool called: signoz_list_views",
		slog.String("sourcePage", sourcePage),
		slog.String("name", name),
		slog.String("category", category),
	)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListViews(ctx, sourcePage, name, category)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list views", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse views response", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}
	// Upstream returns `data: null`, omits `data`, or — on some deployments —
	// returns an empty object/scalar when there are no views. Treat any
	// non-array shape as zero rows rather than surfacing a format error.
	var data []any
	if raw, present := parsed["data"]; present && raw != nil {
		if arr, ok := raw.([]any); ok {
			data = arr
		} else {
			h.logger.DebugContext(ctx, "views response data was not an array; treating as empty",
				slog.String("data", logpkg.TruncAny(raw)))
		}
	}
	total := len(data)
	pagedData := paginate.Array(data, offset, limit)
	resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap views with pagination", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(resultJSON)), nil
}

func (h *Handler) handleGetView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}
	viewID, _ := args["viewId"].(string)
	if viewID == "" {
		h.logger.WarnContext(ctx, "get_view missing viewId")
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Provide a valid saved view UUID. Use signoz_list_views to see available views.`), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_get_view", slog.String("viewId", viewID))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.GetView(ctx, viewID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get view", slog.String("viewId", viewID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleCreateView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(args) == 0 {
		return mcp.NewToolResultError("parameter validation failed: request body is empty or not an object"), nil
	}
	unwrapViewEnvelope(args)

	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "name" is required and cannot be empty.`), nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cq, present := args["compositeQuery"]
	if !present {
		return mcp.NewToolResultError(`Parameter validation failed: "compositeQuery" is required. Read signoz://view/instructions and signoz://view/examples for the schema.`), nil
	}
	if err := validateBuilderSignal(cq, sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := marshalViewBody(args)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal view body", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to build request body: " + err.Error()), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_create_view", slog.String("name", name), slog.String("sourcePage", sourcePage))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.CreateView(ctx, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create view", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleUpdateView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(args) == 0 {
		return mcp.NewToolResultError("parameter validation failed: request body is empty or not an object"), nil
	}

	viewID, _ := args["viewId"].(string)
	if viewID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Use signoz_list_views to find the UUID.`), nil
	}

	// The canonical shape (per input schema) wraps the body under "view".
	// Back-compat: also accept the SavedView fields flat at the top level
	// (pre-wrapper call sites), and the wrapped get_view envelope shape
	// ({status,data:{...}}) pasted straight under "view".
	var view map[string]any
	if v, ok := args["view"].(map[string]any); ok {
		view = v
		unwrapViewEnvelope(view)
	} else {
		view = map[string]any{}
		for k, v := range args {
			if k == "viewId" || k == "searchContext" || k == "view" {
				continue
			}
			view[k] = v
		}
		unwrapViewEnvelope(view)
	}
	if len(view) == 0 {
		return mcp.NewToolResultError(`Parameter validation failed: "view" is required. Pass the SavedView body under "view". Call signoz_get_view first and use the "data" field it returns.`), nil
	}

	name, _ := view["name"].(string)
	if name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "view.name" is required. Call signoz_get_view first and pass its data field back as "view".`), nil
	}
	sourcePage, _ := view["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cq, present := view["compositeQuery"]
	if !present {
		return mcp.NewToolResultError(`Parameter validation failed: "view.compositeQuery" is required. Call signoz_get_view first and pass its data field back as "view".`), nil
	}
	if err := validateBuilderSignal(cq, sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	stripNonBodyFields(view)
	body, err := json.Marshal(view)
	if err != nil {
		return mcp.NewToolResultError("failed to build request body: " + err.Error()), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_update_view", slog.String("viewId", viewID), slog.String("sourcePage", sourcePage))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Saved views are keyed to an Explorer; upstream PUT silently allows the
	// sourcePage to be switched, which effectively moves the view to a
	// different Explorer. Pre-fetch and reject the change — callers who truly
	// want a cross-Explorer view should delete and re-create.
	if existing, ferr := client.GetView(ctx, viewID); ferr == nil {
		var probe struct {
			Data struct {
				SourcePage string `json:"sourcePage"`
			} `json:"data"`
		}
		if jerr := json.Unmarshal(existing, &probe); jerr == nil && probe.Data.SourcePage != "" && probe.Data.SourcePage != sourcePage {
			return mcp.NewToolResultError(fmt.Sprintf(
				`Parameter validation failed: cannot change sourcePage on update (existing=%q, new=%q). Saved views are scoped to an Explorer; delete and re-create to move across Explorers.`,
				probe.Data.SourcePage, sourcePage,
			)), nil
		}
	}
	data, err := client.UpdateView(ctx, viewID, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to update view", slog.String("viewId", viewID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleDeleteView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}
	viewID, _ := args["viewId"].(string)
	if viewID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Use signoz_list_views to find the UUID.`), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_delete_view", slog.String("viewId", viewID))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.DeleteView(ctx, viewID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to delete view", slog.String("viewId", viewID), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
