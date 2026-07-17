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
			"Return label/attribute keys for a single metric with their cardinality counts and sample "+
				"values, sorted highest-cardinality first. The values field on each attribute entry contains a "+
				"sample of actual label values, which helps determine whether high cardinality is real "+
				"(e.g. UUIDs, pod IDs) or bounded (e.g. namespace names, status codes). Note: if the metric "+
				"is not referenced in any dashboard or alert, dropping it outright eliminates its ingestion "+
				"cost entirely — more impactful than trimming its labels."),
		mcp.WithString("searchContext",
			mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
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
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := client.GetMetricCardinality(ctx, metricName, startTime, endTime)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to fetch metric cardinality", err, slog.String("metricName", metricName))
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}
