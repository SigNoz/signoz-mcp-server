package tools

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func (h *Handler) RegisterTopMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering top metrics handlers")

	tool := mcp.NewTool("signoz_get_top_metrics",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription(
			"Return top 100 metrics ranked by ingested sample volume with pre-computed percentages. "+
				"Use this for questions like 'which metrics cost the most?', 'top metrics by sample count', "+
				"'what is driving my metrics ingestion volume?', 'metrics by ingestion cost'. "+
				"Wraps POST /api/v2/metrics/treemap."),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithString("timeRange", mcp.Description("Relative time range to analyze: 24h, 3d, 7d. Default: 7d. Ignored when both start and end are provided.")),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds. When both start and end are provided, they override timeRange.")),
	)

	addTool(s, tool, h.handleGetTopMetrics)
}

func (h *Handler) handleGetTopMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	startTime, endTime, err := resolveTimestamps(args, "7d")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_top_metrics",
		slog.Int64("start", startTime),
		slog.Int64("end", endTime))

	result, err := client.GetTopMetrics(ctx, startTime, endTime, 100)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get top metrics", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}
