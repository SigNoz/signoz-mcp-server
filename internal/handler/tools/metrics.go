package tools

import (
	"context"
	"log/slog"
	"strconv"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

func (h *Handler) RegisterMetricsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering metrics handlers")

	listMetricsTool := mcp.NewTool("signoz_list_metrics",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user needs to discover metric names or inspect catalog metadata such as type, temporality, unit, and monotonicity. It lists metrics active in the requested window; searchText filters names by substring. Do not use it for metric values or trends; use signoz_query_metrics, which can query a known exact name directly and auto-fetch missing metadata. Use source=\"meter\" only for Cost Meter metrics. Results have a limit but no offset pagination, so narrow the search if the cap is reached."),
		mcp.WithString("searchText", mcp.Description(listMetricsSearchTextParamDescription)),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description(listMetricsLimitParamDescription)),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("source", mcp.Description(listMetricsSourceParamDescription)),
	)

	h.addTool(s, listMetricsTool, h.handleListMetrics)

	// signoz_query_metrics — metrics query tool that fills aggregation defaults
	queryMetricsTool := mcp.NewTool("signoz_query_metrics",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription(
			"Use this when the user wants metric values, trends, breakdowns, or formulas. It returns scalar or time-series results, applies metric-aware aggregation defaults, and reports the decisions used. If the exact metricName is known, call this directly: when metricType is omitted, type, temporality, and monotonicity are auto-fetched together. Use signoz_list_metrics only to discover names or inspect catalog metadata; use signoz_execute_builder_query for complex multi-query requests this tool cannot express. Standalone and formula results are capped at the top 100; formula inputs are not limited. Grouped time-series top-N is ranked over the whole window. Read signoz://metrics-aggregation-guide for rules and examples."),
		mcp.WithString("metricName", mcp.Required(), mcp.Description(metricNameParamDescription)),
		mcp.WithString("metricType", mcp.Description(metricTypeParamDescription)),
		mcp.WithBoolean("isMonotonic", boolOrStringType(), mcp.Description(isMonotonicParamDescription)),
		mcp.WithString("temporality", mcp.Description(temporalityParamDescription)),
		mcp.WithString("timeAggregation", mcp.Description(timeAggregationParamDescription)),
		mcp.WithString("spaceAggregation", mcp.Description(spaceAggregationParamDescription)),
		mcp.WithString("groupBy", stringOrStringArrayType(), mcp.Description(metricsGroupByParamDescription)),
		mcp.WithString("filter", mcp.Description(metricsFilterParamDescription)),
		mcp.WithString("timeRange", mcp.DefaultString("1h"), mcp.Description(timeRangeParamDescription("Defaults to '1h'."))),
		mcp.WithString("start", intOrStringType(), mcp.Description(metricsStartParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(metricsEndParamDescription)),
		mcp.WithString("stepInterval", intOrStringType(), mcp.Description(metricsStepIntervalParamDescription)),
		mcp.WithString("requestType", mcp.DefaultString("time_series"), mcp.Enum("scalar", "time_series"), mcp.Description(metricsRequestTypeParamDescription)),
		mcp.WithString("reduceTo", mcp.Description(reduceToParamDescription)),
		mcp.WithString("formula", mcp.Description(formulaParamDescription)),
		mcp.WithArray("formulaQueries", stringOrArrayType(), mcp.Items(formulaSubQuerySchema()), mcp.Description(formulaQueriesParamDescription)),
		mcp.WithString("limit", mcp.DefaultString(strconv.Itoa(querybuilder.DefaultAggregateQueryLimit)), intOrStringType(), mcp.Description(metricsLimitParamDescription)),
		mcp.WithString("orderBy", mcp.Description(metricsOrderByParamDescription)),
		mcp.WithString("having", mcp.Description(metricsHavingParamDescription)),
		functionsProperty(),
		mcp.WithString("source", mcp.Description(metricsSourceParamDescription)),
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
		return clientError(err), nil
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
