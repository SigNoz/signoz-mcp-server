package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterQueryBuilderV5Handlers(s *server.MCPServer) {
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Execute a SigNoz Query Builder v5 query.\n\n"+
				"REQUIRED: Read signoz://traces/query-builder-guide BEFORE building any query. "+
				"It documents filter expression syntax, correct field names (camelCase vs dot notation), "+
				"and complete working examples.\n\n"+
				"When compositeQuery.queryType=\"promql\" ALSO read signoz://promql/instructions — "+
				"OTel metric names with dots MUST use the Prometheus 3.x UTF-8 quoted-selector form ({\"metric.name.with.dots\"}). "+
				"Underscored / __name__ / bare-dotted forms silently return no data.\n\n"+
				"See docs: https://signoz.io/docs/userguide/query-builder-v5/",
		),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	addTool(s, executeQuery, h.handleExecuteBuilderQuery)

	tracesQueryBuilderGuide := mcp.NewResource(
		"signoz://traces/query-builder-guide",
		"Traces Query Builder Guide",
		mcp.WithResourceDescription("SigNoz Query Builder v5 traces guide: filter expression syntax (string, not structured object), built-in span column names (camelCase, no fieldContext), resource/tag attribute naming (dot notation + fieldContext), and complete working examples for raw, aggregation, and time series queries."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(tracesQueryBuilderGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     querybuilder.TracesQueryBuilderGuide,
			},
		}, nil
	})
}

func (h *Handler) handleExecuteBuilderQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_execute_builder_query")

	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid arguments payload type", slog.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError("invalid arguments payload"), nil
	}

	queryObj, ok := args["query"].(map[string]any)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid query parameter type", slog.Any("type", args["query"]))
		return mcp.NewToolResultError("query parameter must be a JSON object"), nil
	}

	queryJSON, err := json.Marshal(queryObj)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal query object", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
	}

	var queryPayload types.QueryPayload
	if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
		h.logger.ErrorContext(ctx, "Failed to unmarshal query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("invalid query payload structure: " + err.Error()), nil
	}

	if err := queryPayload.Validate(); err != nil {
		h.logger.ErrorContext(ctx, "Query validation failed", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("query validation error: " + err.Error()), nil
	}

	finalQueryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal validated query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to execute query builder v5", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Successfully executed query builder v5")
	return mcp.NewToolResultText(string(data)), nil
}
