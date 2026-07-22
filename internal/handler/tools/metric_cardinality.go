package tools

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (h *Handler) RegisterMetricCardinalityHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metric cardinality handlers")

	tool := mcp.NewTool("signoz_check_metric_cardinality",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants to find high-cardinality labels or attributes on one metric. It returns keys sorted by cardinality count with sample values, helping distinguish unbounded values such as UUIDs from bounded dimensions such as status codes. Do not use it to find dashboard or alert dependencies (signoz_check_metric_usage) or rank metric ingestion (signoz_get_top_metrics). This does not show whether the metric is used; check usage before recommending a drop."),
		mcp.WithString("searchContext",
			mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithString("metricName",
			mcp.Required(),
			mcp.Description("Name of the metric to inspect. Example: 'k8s.container.memory_limit'.")),
		mcp.WithString("timeRange", mcp.DefaultString("7d"),
			mcp.Description(timeRangeDesc("Defaults to '7d' (a cost-analysis window)."))),
		mcp.WithString("start", intOrStringType(),
			mcp.Description("Start time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(),
			mcp.Description("End time in unix milliseconds. When both start and end are provided, they override timeRange.")),
	)

	h.addTool(s, tool, h.handleCheckMetricCardinality)
}

func (h *Handler) handleCheckMetricCardinality(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	metricName, errResult := requireStringArg(args, "metricName")
	if errResult != nil {
		return errResult, nil
	}

	startTime, endTime, err := resolveTimestamps(args, "7d")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_check_metric_cardinality",
		slog.String("metricName", metricName))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	result, err := client.GetMetricCardinality(ctx, metricName, startTime, endTime)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to fetch metric cardinality", err, slog.String("metricName", metricName))
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}
