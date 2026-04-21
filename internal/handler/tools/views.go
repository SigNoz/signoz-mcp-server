package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
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
		return fmt.Errorf(`Parameter validation failed: "sourcePage" is required. Must be one of: "traces", "logs", "metrics"`)
	}
	if _, ok := validSourcePages[sp]; !ok {
		return fmt.Errorf(`Parameter validation failed: "sourcePage" must be one of: "traces", "logs", "metrics" (got %q)`, sp)
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
		mcp.WithDescription("List SigNoz saved Explorer views for a given sourcePage. A saved view is a reusable Explorer query (filters, aggregations, panel type). Returns the raw server response as JSON."),
		mcp.WithString("sourcePage", mcp.Required(), mcp.Description(`Required. Which Explorer to list views for. One of: "traces", "logs", "metrics".`)),
		mcp.WithString("name", mcp.Description("Optional partial-match filter on view name.")),
		mcp.WithString("category", mcp.Description("Optional partial-match filter on view category.")),
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
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Create a new SigNoz saved Explorer view.\n\n"+
				"CRITICAL: You MUST read these resources BEFORE composing a payload:\n"+
				"1. signoz://view/instructions — REQUIRED: SavedView field schema and sourcePage rules\n"+
				"2. signoz://view/examples — REQUIRED: full working payloads for traces/logs/metrics\n\n"+
				"Required fields: name, sourcePage (one of traces|logs|metrics), compositeQuery. "+
				"Optional: category, tags, extraData. Server populates id, createdAt/By, updatedAt/By.",
		),
		mcp.WithInputSchema[types.SavedView](),
	)
	s.AddTool(createTool, h.handleCreateView)

	updateTool := mcp.NewTool("signoz_update_view",
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Replace an existing SigNoz saved view (HTTP PUT — full body replace). "+
				"ALWAYS call signoz_get_view first, modify the returned object, and pass the "+
				"full result here. Partial bodies will wipe unspecified fields. "+
				"See signoz://view/instructions for the schema.",
		),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("UUID of the view to replace. Find via signoz_list_views or signoz_get_view.")),
		mcp.WithInputSchema[types.SavedView](),
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

// marshalViewBody strips MCP-specific metadata and path parameters from
// the args map, then marshals the remainder as a SavedView body.
func marshalViewBody(args map[string]any) ([]byte, error) {
	delete(args, "searchContext")
	delete(args, "viewId")
	return json.Marshal(args)
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

	h.logger.DebugContext(ctx, "Tool called: signoz_list_views",
		slog.String("sourcePage", sourcePage),
		slog.String("name", name),
		slog.String("category", category),
	)

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.ListViews(ctx, sourcePage, name, category)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list views", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
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
		return mcp.NewToolResultError("Parameter validation failed: request body is empty or not an object"), nil
	}

	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "name" is required and cannot be empty.`), nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, present := args["compositeQuery"]; !present {
		return mcp.NewToolResultError(`Parameter validation failed: "compositeQuery" is required. Read signoz://view/instructions and signoz://view/examples for the schema.`), nil
	}
	if cq, ok := args["compositeQuery"].(string); ok {
		var tmp any
		if err := json.Unmarshal([]byte(cq), &tmp); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Parameter validation failed: "compositeQuery" is not valid JSON: %s`, err.Error())), nil
		}
		args["compositeQuery"] = tmp
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
		return mcp.NewToolResultError("Parameter validation failed: request body is empty or not an object"), nil
	}

	viewID, _ := args["viewId"].(string)
	if viewID == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Use signoz_list_views to find the UUID.`), nil
	}
	name, _ := args["name"].(string)
	if name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "name" is required. Call signoz_get_view first and pass the full body back.`), nil
	}
	sourcePage, _ := args["sourcePage"].(string)
	if err := validateSourcePage(sourcePage); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, present := args["compositeQuery"]; !present {
		return mcp.NewToolResultError(`Parameter validation failed: "compositeQuery" is required. Call signoz_get_view first and pass the full body back.`), nil
	}
	if cq, ok := args["compositeQuery"].(string); ok {
		var tmp any
		if err := json.Unmarshal([]byte(cq), &tmp); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Parameter validation failed: "compositeQuery" is not valid JSON: %s`, err.Error())), nil
		}
		args["compositeQuery"] = tmp
	}

	body, err := marshalViewBody(args)
	if err != nil {
		return mcp.NewToolResultError("failed to build request body: " + err.Error()), nil
	}
	h.logger.DebugContext(ctx, "Tool called: signoz_update_view", slog.String("viewId", viewID), slog.String("sourcePage", sourcePage))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
