package tools

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
)

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listMetricsTool := mcp.NewTool("signoz_list_metrics",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user needs to discover metric names or inspect catalog metadata such as type, temporality, unit, and monotonicity. It lists metrics active in the requested window; searchText filters names by substring. Do not use it for metric values or trends—use signoz_query_metrics, which can query a known exact name directly and auto-fetch missing metadata. Use source=\"meter\" only for Cost Meter metrics. Results have a limit but no offset pagination, so narrow the search if the cap is reached."),
		mcp.WithString("searchText", mcp.Description("Filter metrics by name substring (optional). Example: 'cpu', 'memory', 'http_requests'.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of metrics to return (optional). Default: 50.")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional). When both start and end are provided, they override timeRange.")),
		mcp.WithString("source", mcp.Description("Optional data-source filter. Use \"meter\" to list Cost Meter metrics — the usage/billing metrics SigNoz meters on (currently telemetry ingestion volume). Omit for the default SigNoz metrics store.")),
	)

	h.addTool(s, listMetricsTool, h.handleListMetrics)

	// signoz_query_metrics — smart metrics query tool with aggregation validation and defaults
	queryMetricsTool := mcp.NewTool("signoz_query_metrics",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this when the user wants metric values, trends, breakdowns, or formulas. It returns scalar or time-series results, applies metric-aware aggregation defaults, and reports the decisions used. If the exact metricName is known, call this directly: when metricType is omitted, type, temporality, and monotonicity are auto-fetched together. Use signoz_list_metrics only to discover names or inspect catalog metadata; use signoz_execute_builder_query for complex multi-query requests this tool cannot express. Standalone and formula results use top 100; formula inputs use 10000, and grouped time-series top-N is ranked over the whole window. Read signoz://metrics-aggregation-guide for rules and examples."),
		mcp.WithString("metricName", mcp.Required(), mcp.Description("Name of the metric to query. Example: 'container.cpu.utilization', 'http_requests_total'.")),
		mcp.WithString("metricType", mcp.Description("Metric type: gauge, sum, histogram, or exponential_histogram. Omit to auto-fetch it with temporality and monotonicity.")),
		mcp.WithBoolean("isMonotonic", boolOrStringType(), mcp.Description("Whether a type=sum metric is monotonically increasing. Auto-fetched when metricType is omitted; otherwise provide the correct value for sum metrics.")),
		mcp.WithString("temporality", mcp.Description("Metric temporality: cumulative, delta, or unspecified. Auto-fetched when metricType is omitted; otherwise provide the metric's value when it affects aggregation.")),
		mcp.WithString("timeAggregation", mcp.Description("Aggregation over time buckets. Auto-defaulted based on metricType. Valid: latest, sum, avg, min, max, count, count_distinct, rate, increase (type-dependent).")),
		mcp.WithString("spaceAggregation", mcp.Description("Aggregation across series/dimensions. Auto-defaulted based on metricType. Valid: sum, avg, min, max, count, p50, p75, p90, p95, p99 (type-dependent).")),
		mcp.WithString("groupBy", stringOrStringArrayType(), mcp.Description("Comma-separated field names or an array of field names. Context is inferred as resource for k8s.*, container.*, host.*, cloud.*, deployment.*, process.*, service.*, telemetry.*, and os.*; all other names use attribute context.")),
		mcp.WithString("filter", mcp.Description("Filter expression. Example: \"k8s.cluster.name = 'prod' AND service.name = 'frontend'\".")),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeDesc("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds. When both start and end are provided, they override timeRange.")),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description("Step interval in seconds for time_series mode (optional). When omitted, the backend auto-selects an appropriate interval (~300 data points, min 60s). Only set this if the user explicitly requests a specific granularity. Examples: '60' (1 min), '3600' (1 hour), '86400' (1 day).")),
		mcp.WithString("requestType", mcp.DefaultString("time_series"), mcp.Enum("scalar", "time_series"), mcp.Description("Response format: \"time_series\" (default) returns one value per time bucket; \"scalar\" returns a single reduced value per series.")),
		mcp.WithString("reduceTo", mcp.Description("For requestType=scalar only. Reduces time series to a single value: sum, count, avg, min, max, last, median. Auto-defaulted by metricType.")),
		mcp.WithString("formula", mcp.Description("Formula expression over named queries. Example: 'A / B * 100'. The primary metric becomes query 'A'. Additional queries are defined in formulaQueries.")),
		mcp.WithString("formulaQueries", stringOrArrayType(), mcp.Description("JSON array, or JSON-encoded array string, of additional named metric queries for formula. Each object supports {name, metricName, metricType, isMonotonic, temporality, timeAggregation, spaceAggregation, groupBy, filter}; name and metricName are required.")),
		mcp.WithString("source", mcp.Description("Optional data-source filter forwarded to the backend. Use \"meter\" to query Cost Meter data. Omit for the default SigNoz metrics store.")),
	)

	h.addTool(s, queryMetricsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return h.handleQueryMetrics(ctx, req)
	})

	// Register metrics aggregation guide as MCP resource
	metricsGuideResource := mcp.NewResource(
		"signoz://metrics-aggregation-guide",
		"Metrics Aggregation Guide",
		mcp.WithResourceDescription("Read this before choosing aggregations for signoz_query_metrics or building metrics Query Builder JSON. It explains timeAggregation, spaceAggregation, and reduceTo by metric type; formulas and limits; how groupBy field context is inferred; top-N selection across the full window; step intervals; examples; and Cost Meter queries."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(metricsrules.MetricsGuide))),
	)

	h.addResource(s, metricsGuideResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     metricsrules.MetricsGuide,
			},
		}, nil
	})
}

func (h *Handler) handleListMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	searchText, _ := args["searchText"].(string)
	source, _ := args["source"].(string)

	limit, err := intArg(args, "limit", 50)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	// Route timestamps through the shared helper: standard 1h default window,
	// magnitude auto-detect, and string-typed start/end. Returns canonical ms.
	start, end, err := resolveTimestamps(args, "1h")
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_list_metrics", slog.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListMetrics(ctx, start, end, limit, searchText, source)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list metrics", err, slog.String("searchText", searchText))
		return upstreamError(err), nil
	}

	// Completeness signal: list_metrics is a raw passthrough with a limit but NO
	// offset paging — so the note must advise narrowing rather than claim
	// offset pagination (a caller following an offset hint would loop the page).
	returnedRows, rowsKnown := countDataArrayRows(result, "metrics")
	note := limitOnlyCompletenessNote(returnedRows, limit, rowsKnown, `searchText, a tighter time range, or source`)
	return resultWithNotes(result, note), nil
}
