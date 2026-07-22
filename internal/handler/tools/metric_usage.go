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
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user needs to know which dashboards and alerts reference known metric names, especially before dropping or reducing telemetry. It returns dashboards, alerts, and an error for each metric, with up to 50 unique names per call. Do not use it for ingestion ranking (signoz_get_top_metrics) or label cardinality (signoz_check_metric_cardinality). A non-empty per-metric error means that entry is partial and unreliable; never interpret it as proof that the metric is unused."),
		mcp.WithString("searchContext",
			mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
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
		return clientError(err), nil
	}

	usage, err := client.CheckMetricUsage(ctx, names)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to check metric usage", err)
		return upstreamError(err), nil
	}

	out, err := json.Marshal(usage)
	if err != nil {
		return InternalErrorResult(err.Error()), nil
	}

	return structuredResult(out), nil
}
