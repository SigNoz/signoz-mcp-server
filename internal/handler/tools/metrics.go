package tools

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
)

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listMetricsTool := mcp.NewTool("signoz_list_metrics",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Search and list available metrics from SigNoz. Supports filtering by name substring, time range, and source. Use searchText to find metrics by name."),
		mcp.WithString("searchText", mcp.Description("Filter metrics by name substring (optional). Example: 'cpu', 'memory', 'http_requests'.")),
		mcp.WithString("limit", mcp.Description("Maximum number of metrics to return (optional, default 50).")),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds (optional).")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds (optional).")),
		mcp.WithString("source", mcp.Description("Filter by source (optional).")),
	)

	s.AddTool(listMetricsTool, h.handleListMetrics)

	// signoz_query_metrics — smart metrics query tool with aggregation validation and defaults
	queryMetricsTool := mcp.NewTool("signoz_query_metrics",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Query metrics from SigNoz with smart aggregation defaults and validation. "+
				"Automatically applies the right timeAggregation and spaceAggregation based on metric type "+
				"(gauge, counter, histogram). If metricType is not provided, it is auto-fetched via signoz_list_metrics. "+
				"Every response includes a [Decisions applied] block showing all defaults used. "+
				"Read the signoz://metrics-aggregation-guide resource for full aggregation rules and examples. "+
				"TIP: Call signoz_list_metrics first to get the metric's type, temporality, and isMonotonic."),
		mcp.WithString("metricName", mcp.Required(), mcp.Description("Name of the metric to query. Example: 'container.cpu.utilization', 'http_requests_total'.")),
		mcp.WithString("metricType", mcp.Description("Metric type: gauge, sum, histogram, or exponential_histogram. Auto-fetched from signoz_list_metrics if not provided.")),
		mcp.WithString("isMonotonic", mcp.Description("Whether the metric is monotonically increasing (true/false). Only relevant for type=sum. Auto-fetched if not provided.")),
		mcp.WithString("temporality", mcp.Description("Metric temporality: cumulative, delta, or unspecified. Auto-fetched if not provided.")),
		mcp.WithString("timeAggregation", mcp.Description("Aggregation over time buckets. Auto-defaulted based on metricType. Valid: latest, sum, avg, min, max, count, count_distinct, rate, increase (type-dependent).")),
		mcp.WithString("spaceAggregation", mcp.Description("Aggregation across series/dimensions. Auto-defaulted based on metricType. Valid: sum, avg, min, max, count, p50, p75, p90, p95, p99 (type-dependent).")),
		mcp.WithString("groupBy", mcp.Description("Comma-separated field names to group by. fieldContext is auto-detected (k8s.*, container.*, host.* → resource; others → attribute). Example: 'k8s.namespace.name,k8s.pod.name'.")),
		mcp.WithString("filter", mcp.Description("Filter expression. Example: \"k8s.cluster.name = 'prod' AND service.name = 'frontend'\".")),
		mcp.WithString("timeRange", mcp.Description("Relative time range: 30m, 1h, 6h, 24h, 7d. Default: 1h. Ignored if start/end are provided.")),
		mcp.WithString("start", mcp.Description("Start time in unix milliseconds. Overrides timeRange.")),
		mcp.WithString("end", mcp.Description("End time in unix milliseconds. Overrides timeRange.")),
		mcp.WithString("stepInterval", mcp.Description("Step interval in seconds. Auto-calculated (~300 data points, min 60s) if not provided.")),
		mcp.WithString("requestType", mcp.Description("Response format: time_series (default) or scalar.")),
		mcp.WithString("reduceTo", mcp.Description("For requestType=scalar only. Reduces time series to a single value: sum, count, avg, min, max, last, median. Auto-defaulted by metricType.")),
		mcp.WithString("formula", mcp.Description("Formula expression over named queries. Example: 'A / B * 100'. The primary metric becomes query 'A'. Additional queries are defined in formulaQueries.")),
		mcp.WithString("formulaQueries", mcp.Description("JSON array of additional named metric queries for formula. Each object: {\"name\":\"B\", \"metricName\":\"...\", \"metricType\":\"...\", \"isMonotonic\":true, \"temporality\":\"...\", \"timeAggregation\":\"...\", \"spaceAggregation\":\"...\", \"groupBy\":[\"...\"], \"filter\":\"...\"}. All fields except name and metricName are optional.")),
	)

	s.AddTool(queryMetricsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return h.handleQueryMetrics(ctx, req)
	})

	// Register metrics aggregation guide as MCP resource
	metricsGuideResource := mcp.NewResource(
		"signoz://metrics-aggregation-guide",
		"Metrics Aggregation Guide",
		mcp.WithResourceDescription("Complete guide for metrics aggregation rules, defaults, payload examples, and common pitfalls. Covers gauge, counter, histogram, and exponential_histogram types with valid timeAggregation and spaceAggregation options."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(metricsGuideResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     metricsrules.MetricsGuide,
			},
		}, nil
	})
}

func (h *Handler) handleListMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.Params.Arguments.(map[string]any)

	searchText, _ := args["searchText"].(string)
	source, _ := args["source"].(string)

	var limit int
	if l, ok := args["limit"].(string); ok && l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit == 0 {
		limit = 50
	}

	var start, end int64
	if s, ok := args["start"].(string); ok && s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			start = v
		}
	}
	if e, ok := args["end"].(string); ok && e != "" {
		if v, err := strconv.ParseInt(e, 10, 64); err == nil {
			end = v
		}
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_list_metrics", slog.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListMetrics(ctx, start, end, limit, searchText, source)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list metrics", slog.String("searchText", searchText), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
