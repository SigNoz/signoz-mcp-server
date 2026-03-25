package tools

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterQueryBuilderV5Handlers(s *server.MCPServer) {
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription(
			"Execute a SigNoz Query Builder v5 query.\n\n"+
				"REQUIRED: Read signoz://traces/query-builder-guide BEFORE building any query. "+
				"It documents filter expression syntax, correct field names (camelCase vs dot notation), "+
				"and complete working examples.\n\n"+
				"See docs: https://signoz.io/docs/userguide/query-builder-v5/",
		),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	s.AddTool(executeQuery, h.handleExecuteBuilderQuery)

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
	log := h.tenantLogger(ctx)
	log.Debug("Tool called: signoz_execute_builder_query")

	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		log.Warn("Invalid arguments payload type", zap.Any("type", req.Params.Arguments))
		return mcp.NewToolResultError("invalid arguments payload"), nil
	}

	queryObj, ok := args["query"].(map[string]any)
	if !ok {
		log.Warn("Invalid query parameter type", zap.Any("type", args["query"]))
		return mcp.NewToolResultError("query parameter must be a JSON object"), nil
	}

	queryJSON, err := json.Marshal(queryObj)
	if err != nil {
		log.Error("Failed to marshal query object", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
	}

	var queryPayload types.QueryPayload
	if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
		log.Error("Failed to unmarshal query payload", zap.Error(err))
		return mcp.NewToolResultError("invalid query payload structure: " + err.Error()), nil
	}

	if err := queryPayload.Validate(); err != nil {
		log.Error("Query validation failed", zap.Error(err))
		return mcp.NewToolResultError("query validation error: " + err.Error()), nil
	}

	finalQueryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		log.Error("Failed to marshal validated query payload", zap.Error(err))
		return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
	if err != nil {
		log.Error("Failed to execute query builder v5", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	log.Debug("Successfully executed query builder v5")
	return mcp.NewToolResultText(string(data)), nil
}
