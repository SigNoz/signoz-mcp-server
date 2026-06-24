package tools

import (
	"context"
	"fmt"
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
				"'what is driving my metrics ingestion volume?', 'metrics by ingestion cost'."),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithString("timeRange", mcp.Description("Relative time range to analyze (e.g. 24h, 3d, 7d, 30d). Default: 7d. Ignored when both start and end are provided.")),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds. When both start and end are provided, they override timeRange.")),
	)

	addTool(s, tool, h.handleGetTopMetrics)
}

func (h *Handler) handleGetTopMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

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

	const topMetricsLimit = 100
	result, err := client.GetTopMetrics(ctx, startTime, endTime, topMetricsLimit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get top metrics", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Completeness signal: this tool returns a fixed top-N (no offset paging), so
	// the note tells the caller whether the list was truncated at the cap rather
	// than suggesting offset pagination.
	returnedRows, rowsKnown := countDataArrayRows(result, "samples")
	var note string
	if rowsKnown && returnedRows >= topMetricsLimit {
		note = fmt.Sprintf(
			"note: showing the top %d metrics by ingested sample volume; more metrics exist beyond this cap (hasMore=true). Narrow the time range to compare a different window.",
			topMetricsLimit)
	} else if rowsKnown {
		note = fmt.Sprintf(
			"note: returned %d metrics (top %d cap) — all ranked metrics returned for this window (hasMore=false).",
			returnedRows, topMetricsLimit)
	} else {
		note = fmt.Sprintf("note: showing up to the top %d metrics by ingested sample volume.", topMetricsLimit)
	}
	return resultWithNotes(result, note), nil
}
