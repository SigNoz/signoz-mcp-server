package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// metricMetadata holds the parsed metadata from signoz_list_metrics response.
type metricMetadata struct {
	MetricType  string
	IsMonotonic bool
	Temporality string
}

func (h *Handler) handleQueryMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.Params.Arguments.(map[string]any)

	mqr, err := parseMetricsQueryArgs(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_query_metrics",
		slog.String("metricName", mqr.MetricName),
		slog.String("metricType", mqr.MetricType))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Track all decisions for the response
	var decisions []string
	decisions = append(decisions, fmt.Sprintf("metricName: %s", mqr.MetricName))

	// Auto-fetch metric metadata if not provided
	if mqr.MetricType == "" {
		meta, fetchErr := h.fetchMetricMetadata(ctx, client, mqr.MetricName)
		if fetchErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"Failed to auto-fetch metric metadata for %q: %s\n"+
					"Please provide metricType, temporality, and isMonotonic manually "+
					"(get them from signoz_list_metrics).",
				mqr.MetricName, fetchErr.Error())), nil
		}
		if meta != nil {
			mqr.MetricType = meta.MetricType
			mqr.IsMonotonic = meta.IsMonotonic
			mqr.Temporality = meta.Temporality
			decisions = append(decisions, fmt.Sprintf("metricType: %s (auto-fetched via signoz_list_metrics)", mqr.MetricType))
			decisions = append(decisions, fmt.Sprintf("temporality: %s (auto-fetched)", mqr.Temporality))
			decisions = append(decisions, fmt.Sprintf("isMonotonic: %t (auto-fetched)", mqr.IsMonotonic))
		} else {
			return mcp.NewToolResultError(fmt.Sprintf(
				"Metric %q not found via signoz_list_metrics. "+
					"Check the metric name or provide metricType manually.",
				mqr.MetricName)), nil
		}
	} else {
		decisions = append(decisions, fmt.Sprintf("metricType: %s (caller-provided)", mqr.MetricType))
		if mqr.Temporality != "" {
			decisions = append(decisions, fmt.Sprintf("temporality: %s (caller-provided)", mqr.Temporality))
		}
	}

	// Resolve timestamps
	startTime, endTime, err := resolveTimestamps(args, mqr.TimeRange)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Step interval: use caller-provided value or let the backend decide
	stepInterval := mqr.StepInterval
	callerProvidedStep := stepInterval > 0
	if callerProvidedStep {
		decisions = append(decisions, fmt.Sprintf("stepInterval: %ds (caller-provided)", stepInterval))
	}

	// Apply defaults for primary query
	resolved, err := metricsrules.ApplyDefaults(metricsrules.MetricQueryParams{
		MetricType:       mqr.MetricType,
		IsMonotonic:      mqr.IsMonotonic,
		Temporality:      mqr.Temporality,
		TimeAggregation:  mqr.TimeAggregation,
		SpaceAggregation: mqr.SpaceAggregation,
		ReduceTo:         mqr.ReduceTo,
	}, mqr.RequestType)
	if err != nil {
		return mcp.NewToolResultError(formatValidationError(err)), nil
	}

	decisions = append(decisions, resolved.Decisions...)
	decisions = append(decisions, fmt.Sprintf("requestType: %s", mqr.RequestType))

	// Build query specs
	var querySpecs []types.MetricsQuerySpec

	// Primary query is always "A"
	primaryGroupBy := buildGroupByFields(mqr.GroupBy)
	if len(primaryGroupBy) > 0 {
		var gbNames []string
		for _, g := range primaryGroupBy {
			gbNames = append(gbNames, fmt.Sprintf("%s [%s]", g.Name, g.FieldContext))
		}
		decisions = append(decisions, fmt.Sprintf("groupBy: %s", strings.Join(gbNames, ", ")))
	}

	querySpecs = append(querySpecs, types.MetricsQuerySpec{
		Name: "A",
		Aggregation: types.MetricAggregation{
			MetricName:       mqr.MetricName,
			Temporality:      mqr.Temporality,
			TimeAggregation:  resolved.TimeAggregation,
			SpaceAggregation: resolved.SpaceAggregation,
			ReduceTo:         resolved.ReduceTo,
		},
		Filter:  mqr.Filter,
		GroupBy: primaryGroupBy,
	})

	// Formula sub-queries
	for _, fq := range mqr.FormulaQueries {
		subResolved, subErr := resolveFormulaSubQuery(ctx, h, client, fq, mqr.RequestType, &decisions)
		if subErr != nil {
			return mcp.NewToolResultError(subErr.Error()), nil
		}

		subGroupBy := buildGroupByFields(fq.GroupBy)
		querySpecs = append(querySpecs, types.MetricsQuerySpec{
			Name: fq.Name,
			Aggregation: types.MetricAggregation{
				MetricName:       fq.MetricName,
				Temporality:      fq.Temporality,
				TimeAggregation:  subResolved.TimeAggregation,
				SpaceAggregation: subResolved.SpaceAggregation,
				ReduceTo:         subResolved.ReduceTo,
			},
			Filter:  fq.Filter,
			GroupBy: subGroupBy,
		})
	}

	// Formula query spec
	if mqr.Formula != "" {
		querySpecs = append(querySpecs, types.MetricsQuerySpec{
			Name:       formulaName(querySpecs),
			IsFormula:  true,
			Expression: mqr.Formula,
			Legend:     "formula_result",
		})
		decisions = append(decisions, fmt.Sprintf("formula: %s", mqr.Formula))
	}

	// Build and execute
	queryJSON, err := types.BuildMetricsQueryPayloadJSON(startTime, endTime, stepInterval, querySpecs, mqr.RequestType)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to build query payload: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Executing metrics query", slog.String("payload", string(queryJSON)))

	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logger.ErrorContext(ctx, "Metrics query failed", logpkg.ErrAttr(err))
		return mcp.NewToolResultError(fmt.Sprintf("Query execution failed: %s", err.Error())), nil
	}

	// Extract backend-determined stepInterval from response if caller didn't provide one
	if !callerProvidedStep {
		if si := extractStepInterval(result); si > 0 {
			decisions = append(decisions, fmt.Sprintf("stepInterval: %ds (backend-determined)", si))
		}
	}

	// Build response with decisions block
	var response strings.Builder
	response.WriteString("[Decisions applied]\n")
	for _, d := range decisions {
		response.WriteString(fmt.Sprintf("  %s\n", d))
	}
	for _, w := range resolved.Warnings {
		response.WriteString(fmt.Sprintf("  WARNING: %s\n", w))
	}
	response.WriteString("---\n")
	response.WriteString(string(result))

	return mcp.NewToolResultText(response.String()), nil
}

// fetchMetricMetadata calls ListMetrics to get type/temporality/isMonotonic for a metric.
func (h *Handler) fetchMetricMetadata(ctx context.Context, client interface {
	ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
}, metricName string) (*metricMetadata, error) {
	// Search with exact metric name, limit 10 to find it
	result, err := client.ListMetrics(ctx, 0, 0, 10, metricName, "")
	if err != nil {
		return nil, err
	}

	// The response is typically {"status":"success","data":{"metrics":[...]}}
	// or just an array. Try both formats.
	meta, err := parseMetricMetadataFromResponse(result, metricName)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// parseMetricMetadataFromResponse extracts metric metadata from the ListMetrics response.
func parseMetricMetadataFromResponse(data json.RawMessage, metricName string) (*metricMetadata, error) {
	// Try format: {"status":"success","data":{"metrics":[{...}]}}
	var wrapper struct {
		Status string `json:"status"`
		Data   struct {
			Metrics []struct {
				MetricName  string `json:"metricName"`
				Type        string `json:"type"`
				IsMonotonic bool   `json:"isMonotonic"`
				Temporality string `json:"temporality"`
			} `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Data.Metrics) > 0 {
		for _, m := range wrapper.Data.Metrics {
			if m.MetricName == metricName {
				return &metricMetadata{
					MetricType:  normalizeMetricType(m.Type),
					IsMonotonic: m.IsMonotonic,
					Temporality: m.Temporality,
				}, nil
			}
		}
		// If exact match not found, return the first result if search was specific
		m := wrapper.Data.Metrics[0]
		return &metricMetadata{
			MetricType:  normalizeMetricType(m.Type),
			IsMonotonic: m.IsMonotonic,
			Temporality: m.Temporality,
		}, nil
	}

	// Try format: [{"metricName":"...", "type":"...", ...}]
	var metrics []struct {
		MetricName  string `json:"metricName"`
		Type        string `json:"type"`
		IsMonotonic bool   `json:"isMonotonic"`
		Temporality string `json:"temporality"`
	}
	if err := json.Unmarshal(data, &metrics); err == nil && len(metrics) > 0 {
		for _, m := range metrics {
			if m.MetricName == metricName {
				return &metricMetadata{
					MetricType:  normalizeMetricType(m.Type),
					IsMonotonic: m.IsMonotonic,
					Temporality: m.Temporality,
				}, nil
			}
		}
		m := metrics[0]
		return &metricMetadata{
			MetricType:  normalizeMetricType(m.Type),
			IsMonotonic: m.IsMonotonic,
			Temporality: m.Temporality,
		}, nil
	}

	return nil, nil
}

func normalizeMetricType(t string) string {
	t = strings.ToLower(t)
	switch t {
	case "gauge", "sum", "histogram", "exponential_histogram", "exponentialhistogram":
		if t == "exponentialhistogram" {
			return "exponential_histogram"
		}
		return t
	default:
		return t
	}
}

// resolveFormulaSubQuery applies defaults for a formula sub-query, auto-fetching metadata if needed.
func resolveFormulaSubQuery(ctx context.Context, h *Handler, client interface {
	ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
}, fq formulaSubQuery, requestType string, decisions *[]string) (*metricsrules.ResolvedAggregation, error) {
	metricType := fq.MetricType
	isMonotonic := fq.IsMonotonic
	temporality := fq.Temporality

	// Auto-fetch if needed
	if metricType == "" {
		meta, err := h.fetchMetricMetadata(ctx, client, fq.MetricName)
		if err != nil {
			return nil, fmt.Errorf("failed to auto-fetch metadata for formula query %q (%s): %w", fq.Name, fq.MetricName, err)
		}
		if meta != nil {
			metricType = meta.MetricType
			isMonotonic = meta.IsMonotonic
			temporality = meta.Temporality
			*decisions = append(*decisions, fmt.Sprintf("query %s (%s): metricType=%s (auto-fetched)", fq.Name, fq.MetricName, metricType))
		} else {
			return nil, fmt.Errorf("metric %q not found for formula query %q. Check the metric name", fq.MetricName, fq.Name)
		}
	}

	resolved, err := metricsrules.ApplyDefaults(metricsrules.MetricQueryParams{
		MetricType:       metricType,
		IsMonotonic:      isMonotonic,
		Temporality:      temporality,
		TimeAggregation:  fq.TimeAggregation,
		SpaceAggregation: fq.SpaceAggregation,
	}, requestType)
	if err != nil {
		return nil, fmt.Errorf("validation error for formula query %q (%s): %w", fq.Name, fq.MetricName, err)
	}

	// Update fq temporality for the aggregation struct
	fq.Temporality = temporality

	for _, d := range resolved.Decisions {
		*decisions = append(*decisions, fmt.Sprintf("query %s: %s", fq.Name, d))
	}

	return &resolved, nil
}

func formulaName(existing []types.MetricsQuerySpec) string {
	// Find the next available letter after existing query names
	used := make(map[string]bool)
	for _, q := range existing {
		used[q.Name] = true
	}
	for c := 'A'; c <= 'Z'; c++ {
		name := string(c)
		if !used[name] {
			return name
		}
	}
	return "F"
}

func formatValidationError(err error) string {
	return fmt.Sprintf("[Validation Error]\n%s", err.Error())
}
