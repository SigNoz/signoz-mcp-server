package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func (h *Handler) RegisterMetricUsageHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metric usage handlers")

	tool := mcp.NewTool("signoz_check_metric_usage",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription(
			"Given a list of metric names, return which dashboards and alerts reference each one, "+
				"and whether each metric is safe to drop. Use this after signoz_get_top_metrics to "+
				"decide which expensive metrics can be safely dropped from ingestion."),
		mcp.WithString("searchContext",
			mcp.Description("The user's original question or search text that triggered this tool call.")),
		mcp.WithArray("metricNames",
			mcp.Required(),
			mcp.Description("Array of metric name strings to check. Example: [\"system.disk.io\", \"k8s.node.condition\"]."),
		),
	)

	addTool(s, tool, h.handleCheckMetricUsage)
}

func (h *Handler) handleCheckMetricUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}

	rawNames, ok := args["metricNames"]
	if !ok {
		return mcp.NewToolResultError("metricNames is required"), nil
	}

	namesRaw, ok := rawNames.([]any)
	if !ok {
		return mcp.NewToolResultError("metricNames must be an array of strings"), nil
	}

	names := make([]string, 0, len(namesRaw))
	for _, v := range namesRaw {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError("each entry in metricNames must be a string"), nil
		}
		names = append(names, s)
	}

	if len(names) == 0 {
		return mcp.NewToolResultError("metricNames must contain at least one metric name"), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_check_metric_usage", slog.Int("count", len(names)))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	usage, err := client.CheckMetricUsage(ctx, names)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to check metric usage", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	out, err := json.Marshal(usage)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(out)), nil
}
