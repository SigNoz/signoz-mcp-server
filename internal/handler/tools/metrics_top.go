package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (h *Handler) RegisterTopMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering top metrics handlers")

	tool := mcp.NewTool("signoz_get_top_metrics",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to know which metrics drive ingestion volume or cost. It returns a fixed top 100 ranked by ingested sample count with pre-computed percentages for the requested window. Do not use it for metric values or trends (signoz_query_metrics), dashboard or alert dependencies (signoz_check_metric_usage), or label cardinality (signoz_check_metric_cardinality). This ranking has no offset pagination."),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithString("timeRange", mcp.DefaultString("7d"), mcp.Description(timeRangeDesc("Defaults to '7d' (a cost-analysis window); if the query times out, retry with '3d', then '24h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds. When both start and end are provided, they override timeRange.")),
	)

	h.addTool(s, tool, h.handleGetTopMetrics)
}

func (h *Handler) handleGetTopMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	startTime, endTime, err := resolveTimestamps(args, "7d")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
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
		h.logUpstreamFailure(ctx, "Failed to get top metrics", err)
		return upstreamError(err), nil
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
