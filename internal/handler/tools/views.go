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
	"github.com/SigNoz/signoz-mcp-server/pkg/views"
)

// validSourcePages is the allow-list for the explorer-views sourcePage param.
// "meter" is the Cost Meter Explorer — a distinct page in the SigNoz product
// (its own Meter Explorer route), even though its queries run against the
// metrics signal with spec.source="meter".
var validSourcePages = map[string]struct{}{
	"traces":  {},
	"logs":    {},
	"metrics": {},
	"meter":   {},
}

func validateSourcePage(sp string) error {
	if sp == "" {
		return fmt.Errorf(`%s "sourcePage" is required. Must be one of: "traces", "logs", "metrics", "meter"`, validationErrorPrefix)
	}
	if _, ok := validSourcePages[sp]; !ok {
		return fmt.Errorf(`%s "sourcePage" must be one of: "traces", "logs", "metrics", "meter" (got %q)`, validationErrorPrefix, sp)
	}
	return nil
}

// RegisterViewHandlers registers the unified saved-views CRUD tools plus
// the signoz://view/instructions and signoz://view/examples resources.
func (h *Handler) RegisterViewHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering view handlers")

	listTool := mcp.NewTool("signoz_list_views",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants to discover saved Explorer views or find a view UUID for one Logs, Traces, Metrics, or Cost Meter page. A view stores an Explorer query; it is not a multi-widget dashboard. Apply name/category filters before pagination, and follow pagination.nextOffset while pagination.hasMore is true before concluding a view is absent. Use signoz_get_view for one full definition."),
		mcp.WithString("sourcePage", mcp.Required(), mcp.Enum("traces", "logs", "metrics", "meter"), mcp.Description(`Explorer whose views to list: "traces", "logs", "metrics", or "meter". Use "meter" for Cost Meter, not "metrics".`)),
		mcp.WithString("name", mcp.Description("Partial, server-side match on the saved-view name. Omit to include every name.")),
		mcp.WithString("category", mcp.Description("Partial, server-side match on the saved-view category. Omit to include every category.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of views to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of results to skip before returning results. Use 'pagination.nextOffset' from the previous page. Default: 0.")),
	)
	h.addTool(s, listTool, h.handleListViews)

	getTool := mcp.NewTool("signoz_get_view",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants the complete definition of one known saved Explorer view. Use signoz_list_views first when its UUID is unknown. The returned data object is the required base for signoz_update_view because updates fully replace a view. Do not use this for multi-widget dashboards; use signoz_get_dashboard."),
		// Not mcp.Required(): the legacy alias "viewId" must remain a valid call
		// for schema-aware clients. The handler validates id/viewId presence.
		mcp.WithString("id", mcp.Description("Saved view UUID. Use signoz_list_views to discover IDs. Required.")),
	)
	h.addTool(s, getTool, h.handleGetView)

	createTool := mcp.NewTool("signoz_create_view",
		withCreateToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants to save one reusable Explorer query for Logs, Traces, Metrics, or Cost Meter; use signoz_create_dashboard for a multi-widget dashboard. Before composing any payload, you must read both signoz://view/instructions and signoz://view/examples. Cost Meter views use sourcePage=\"meter\" while each builder spec uses signal=\"metrics\" and source=\"meter\". Do not send server-populated IDs or timestamps.",
		),
		mcp.WithString("name", mcp.Required(), mcp.Description("Display name of the view.")),
		mcp.WithString("sourcePage", mcp.Required(), mcp.Enum("traces", "logs", "metrics", "meter"), mcp.Description(`Which Explorer this view belongs to. One of: "traces", "logs", "metrics", "meter". Use "meter" for Cost Meter views (queried as metrics with source "meter").`)),
		mcp.WithObject("compositeQuery", mcp.Required(), mcp.AdditionalProperties(true), mcp.Description("The Query Builder payload as an object (not a string). Must contain queryType plus matching sub-query. See signoz://view/instructions and signoz://view/examples.")),
		mcp.WithString("category", mcp.Description("Optional free-form grouping label.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Optional free-form tags.")),
		mcp.WithString("extraData", mcp.Description("Optional UI-controlled options as a JSON-encoded string (safe to leave empty).")),
	)
	h.addTool(s, createTool, h.handleCreateView)

	updateTool := mcp.NewTool("signoz_update_view",
		withUpdateToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants to change an existing saved Explorer view. This is a full replacement: call signoz_get_view first, modify its data object, preserve every unrequested field, and pass that full object as view. Read signoz://view/instructions and signoz://view/examples when changing sourcePage or compositeQuery; skip them for name-, category-, or tags-only changes when you already have the complete fetched view. Keep the UUID only in id; omit server-populated IDs and timestamps from view.",
		),
		mcp.WithString("id", mcp.Description("UUID of the view to replace. Required.")),
		mcp.WithObject("view",
			mcp.Required(),
			mcp.Properties(savedViewSchemaProperties()),
			mcp.AdditionalProperties(true),
			withRequiredFields("name", "sourcePage", "compositeQuery"),
			mcp.Description("Complete saved view after the requested changes. Start with the data returned by signoz_get_view and pass the full object here."),
		),
	)
	h.addTool(s, updateTool, h.handleUpdateView)

	deleteTool := mcp.NewTool("signoz_delete_view",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user has confirmed they want to permanently delete one saved Explorer view. The deletion is irreversible. Use signoz_list_views to discover the UUID when needed; do not use this for dashboards, which use signoz_delete_dashboard."),
		mcp.WithString("id", mcp.Description("UUID of the saved view to delete. Required; use signoz_list_views to discover it.")),
	)
	h.addTool(s, deleteTool, h.handleDeleteView)

	viewInstructions := mcp.NewResource(
		"signoz://view/instructions",
		"Saved View Instructions",
		mcp.WithResourceDescription("Read this before creating or updating a saved Explorer view. It explains view fields, sourcePage, Query Builder v5, Cost Meter views, and how to read a view before replacing it. It does not describe dashboards."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(views.Instructions))),
	)
	h.addResource(s, viewInstructions, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
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
		mcp.WithResourceDescription("Read this after signoz://view/instructions when composing a saved Explorer view. It provides complete Query Builder v5 payloads for traces, logs, metrics, and Cost Meter source pages."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(views.Examples))),
	)
	h.addResource(s, viewExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     views.Examples,
			},
		}, nil
	})
}

// savedViewSchemaProperties is kept manual because mcp.WithInputSchema cannot
// currently render the SavedView input structs used here without producing an
// empty effective schema for tools/list clients.
func savedViewSchemaProperties() map[string]any {
	return map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "Display name of the view.",
		},
		"sourcePage": map[string]any{
			"type":        "string",
			"enum":        []string{"traces", "logs", "metrics", "meter"},
			"description": `Which Explorer this view belongs to. One of: "traces", "logs", "metrics", "meter". Use "meter" for Cost Meter views (queried as metrics with source "meter").`,
		},
		"compositeQuery": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"description":          "The Query Builder payload as an object (not a string). Must contain queryType plus matching sub-query. See signoz://view/instructions and signoz://view/examples.",
		},
		"category": map[string]any{
			"type":        "string",
			"description": "Optional free-form grouping label.",
		},
		"tags": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional free-form tags.",
		},
		"extraData": map[string]any{
			"type":        "string",
			"description": "Optional UI-controlled options as a JSON-encoded string (safe to leave empty).",
		},
	}
}

func withRequiredFields(fields ...string) mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["required"] = fields
	}
}

// serverPopulatedViewFields are set by the SigNoz API on create/read and
// must not be echoed back on create or update — upstream ignores them at
// best and rejects them at worst.
var serverPopulatedViewFields = []string{
	"id", "createdAt", "createdBy", "updatedAt", "updatedBy",
}

// validateBuilderSignal enforces the documented signal/source rules for a
// view's builder_query specs. Upstream enforces none of this, so missing,
// mismatched, or mis-filed values silently save unusable views.
//
//   - Every builder_query spec must set `signal`.
//   - Cost Meter views (sourcePage "meter") are a distinct Explorer page in
//     the SigNoz product but are queried against the metrics signal with
//     spec.source="meter". So a "meter" view must use signal "metrics" AND
//     source "meter" — omitting source="meter" would silently query the
//     default metrics store instead of the meter store.
//   - For the ordinary pages (traces/logs/metrics), `signal` must equal
//     sourcePage, and source must not be "meter" — a Cost Meter query belongs
//     on the dedicated "meter" page, not mis-filed under "metrics".
//
// Non-builder queries (promql, clickhouse_sql) don't carry these fields and
// are skipped.
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
		source, _ := spec["source"].(string)

		if sourcePage == "meter" {
			// Cost Meter views are queried as metrics against the meter store.
			if signal == "" {
				return fmt.Errorf(
					`%s compositeQuery.queries[%d].spec.signal is required for a "meter" view and must be "metrics"`,
					validationErrorPrefix, i,
				)
			}
			if signal != "metrics" {
				return fmt.Errorf(
					`%s compositeQuery.queries[%d].spec.signal = %q but a "meter" (Cost Meter) view must use signal "metrics"`,
					validationErrorPrefix, i, signal,
				)
			}
			if source != "meter" {
				return fmt.Errorf(
					`%s compositeQuery.queries[%d].spec.source = %q but a "meter" (Cost Meter) view must set source "meter"`,
					validationErrorPrefix, i, source,
				)
			}
			continue
		}

		if signal == "" {
			return fmt.Errorf(
				`%s compositeQuery.queries[%d].spec.signal is required and must equal sourcePage (%q)`,
				validationErrorPrefix, i, sourcePage,
			)
		}
		if signal != sourcePage {
			return fmt.Errorf(
				`%s compositeQuery.queries[%d].spec.signal = %q but sourcePage = %q, they must match`,
				validationErrorPrefix, i, signal, sourcePage,
			)
		}
		// A Cost Meter query (source="meter") must live on the "meter" page.
		if source == "meter" {
			return fmt.Errorf(
				`%s compositeQuery.queries[%d].spec.source = "meter" requires sourcePage "meter" (a Cost Meter view), but sourcePage = %q`,
				validationErrorPrefix, i, sourcePage,
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
		return notAJSONObjectError(), nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		h.logger.WarnContext(ctx, "list_views validation failed", slog.String("sourcePage", sourcePage))
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	name, _ := args["name"].(string)
	category, _ := args["category"].(string)
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	h.logger.DebugContext(ctx, "Tool called: signoz_list_views",
		slog.String("sourcePage", sourcePage),
		slog.String("name", name),
		slog.String("category", category),
	)

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.ListViews(ctx, sourcePage, name, category)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list views", err)
		return upstreamError(err), nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse views response", logpkg.ErrAttr(err))
		return upstreamResponseError("failed to parse response: " + err.Error()), nil
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
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleGetView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}
	viewID := readResourceID(args, "viewId")
	if viewID == "" {
		h.logger.WarnContext(ctx, "get_view missing id")
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a valid saved view UUID. Use signoz_list_views to see available views.`), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_get_view", slog.String("id", viewID))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.GetView(ctx, viewID)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get view", err, slog.String("viewId", viewID))
		return upstreamError(err), nil
	}
	return structuredResult(data), nil
}

func (h *Handler) handleCreateView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(args) == 0 {
		return notAConfigObjectError(), nil
	}
	unwrapViewEnvelope(args)

	name, errResult := requireStringArg(args, "name")
	if errResult != nil {
		return errResult, nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	cq, present := args["compositeQuery"]
	if !present {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "compositeQuery" is required. Read signoz://view/instructions and signoz://view/examples for the schema.`), nil
	}
	if err := validateBuilderSignal(cq, sourcePage); err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	body, err := marshalViewBody(args)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal view body", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to build request body: " + err.Error()), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_create_view", slog.String("name", name), slog.String("sourcePage", sourcePage))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.CreateView(ctx, body)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create view", err)
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleUpdateView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(args) == 0 {
		return notAConfigObjectError(), nil
	}

	viewID := readResourceID(args, "viewId")
	if viewID == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Use signoz_list_views to find the UUID.`), nil
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
			// Skip the MCP-level fields and the top-level id/viewId path param;
			// the SavedView body's own id is server-populated and stripped later.
			if k == "id" || k == "viewId" || k == "searchContext" || k == "view" {
				continue
			}
			view[k] = v
		}
		unwrapViewEnvelope(view)
	}
	if len(view) == 0 {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "view" is required. Pass the SavedView body under "view". Call signoz_get_view first and use the "data" field it returns.`), nil
	}

	name, _ := view["name"].(string)
	if name == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "view.name" is required. Call signoz_get_view first and pass its data field back as "view".`), nil
	}
	sourcePage, _ := view["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}
	cq, present := view["compositeQuery"]
	if !present {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "view.compositeQuery" is required. Call signoz_get_view first and pass its data field back as "view".`), nil
	}
	if err := validateBuilderSignal(cq, sourcePage); err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	stripNonBodyFields(view)
	body, err := json.Marshal(view)
	if err != nil {
		return InternalErrorResult("failed to build request body: " + err.Error()), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_update_view", slog.String("viewId", viewID), slog.String("sourcePage", sourcePage))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
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
			return errorWithCode(CodeValidationFailed, fmt.Sprintf(
				`Parameter validation failed: cannot change sourcePage on update (existing=%q, new=%q). Saved views are scoped to an Explorer; delete and re-create to move across Explorers.`,
				probe.Data.SourcePage, sourcePage,
			)), nil
		}
	}
	data, err := client.UpdateView(ctx, viewID, body)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to update view", err, slog.String("viewId", viewID))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleDeleteView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}
	viewID := readResourceID(args, "viewId")
	if viewID == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Use signoz_list_views to find the UUID.`), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_delete_view", slog.String("id", viewID))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.DeleteView(ctx, viewID)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete view", err, slog.String("viewId", viewID))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
