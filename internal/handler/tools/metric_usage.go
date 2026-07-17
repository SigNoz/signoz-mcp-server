package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (h *Handler) RegisterMetricUsageHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metric usage handlers")

	tool := mcp.NewTool("signoz_check_metric_usage",
		mcp.WithOutputSchema[map[string]signozclient.MetricUsage](),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription(
			"Given a list of metric names, return which dashboards and alerts reference each one. "+
				"Accepts up to 50 metric names per call — split larger lists into batches of 50 and merge results. "+
				"Each result entry contains dashboards (list), alerts (list), and error (string). "+
				"When error is non-empty, the lookup for that metric failed (e.g. older SigNoz version or transient 5xx) "+
				"and any dashboards/alerts lists are partial and unreliable — do not treat the metric as unused. "+
				"Use this to understand metric dependencies before making drop or reduction decisions."),
		mcp.WithString("searchContext",
			mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithArray("metricNames",
			mcp.Required(),
			mcp.WithStringItems(),
			mcp.Description("Array of metric name strings to check. Example: [\"system.disk.io\", \"k8s.node.condition\"]."),
		),
	)

	h.addTool(s, tool, h.handleCheckMetricUsage)
}

func (h *Handler) handleCheckMetricUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		return validationError("metricNames", "is required"), nil
	}

	rawNames, ok := args["metricNames"]
	if !ok {
		return validationError("metricNames", "is required"), nil
	}

	namesRaw, ok := rawNames.([]any)
	if !ok {
		return validationError("metricNames", "must be an array of strings"), nil
	}

	names := make([]string, 0, len(namesRaw))
	seen := make(map[string]struct{}, len(namesRaw))
	uniqueNonEmpty := 0
	for i, v := range namesRaw {
		s, ok := v.(string)
		if !ok {
			return validationErrorf("metricNames", "entry %d must be a string", i), nil
		}
		names = append(names, s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			uniqueNonEmpty++
		}
	}

	if uniqueNonEmpty == 0 {
		return validationError("metricNames", "must contain at least one non-empty metric name"), nil
	}
	if uniqueNonEmpty > signozclient.MaxMetricUsageNames {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(
			"too many metric names: %d exceeds the per-call limit of %d - split into batches of %d and merge results",
			uniqueNonEmpty, signozclient.MaxMetricUsageNames, signozclient.MaxMetricUsageNames,
		)), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_check_metric_usage", slog.Int("count", len(names)))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	usage, err := client.CheckMetricUsage(ctx, names)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to check metric usage", err)
		return upstreamError(err), nil
	}

	out, err := json.Marshal(usage)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return structuredResult(out), nil
}
