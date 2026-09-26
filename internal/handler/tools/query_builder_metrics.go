package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) defaultBuilderMetricReducers(ctx context.Context, client client.Client, payload *types.QueryPayload) ([]string, *mcp.CallToolResult) {
	if payload.RequestType != "scalar" {
		return nil, nil
	}
	var decisions []string
	reducers := make(map[[2]string]string)
	for qi, query := range payload.CompositeQuery.Queries {
		spec, ok := query.Spec.(types.QuerySpec)
		if !ok || query.Type != "builder_query" || spec.Signal != "metrics" {
			continue
		}
		for ai, raw := range spec.Aggregations {
			agg, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, supplied := agg["reduceTo"]; supplied {
				continue
			}
			metricName, _ := agg["metricName"].(string)
			field := fmt.Sprintf("query.compositeQuery.queries[%d].spec.aggregations[%d].reduceTo", qi, ai)
			if metricName == "" {
				return nil, validationError(field, "requires a metricName to choose a default; supply the metricName and reduceTo")
			}
			key := [2]string{spec.Source, metricName}
			reducer, cached := reducers[key]
			if !cached {
				meta, err := h.fetchMetricMetadata(ctx, client, payload.Start, payload.End, metricName, spec.Source)
				if err != nil {
					return nil, upstreamError(fmt.Errorf("could not choose reduceTo for metric %q: %w; supply reduceTo explicitly to skip metadata lookup", metricName, err))
				}
				if meta == nil || meta.MetricName != metricName || meta.MetricType == "" || meta.IsMonotonicMissing {
					h.logger.WarnContext(ctx, "Cannot determine scalar metric reducer from metadata", slog.String("metricName", metricName), slog.String("source", spec.Source))
					return nil, validationError(field, "cannot be defaulted from the metric metadata; supply sum, count, avg, min, max, last, or median explicitly")
				}
				resolved, err := metricsrules.ApplyDefaults(metricsrules.MetricQueryParams{MetricType: meta.MetricType, IsMonotonic: meta.IsMonotonic}, "scalar")
				if err != nil {
					return nil, validationError(field, "cannot be defaulted for this metric type; supply sum, count, avg, min, max, last, or median explicitly")
				}
				reducer = resolved.ReduceTo
				reducers[key] = reducer
			}
			agg["reduceTo"] = reducer
			decisions = append(decisions, fmt.Sprintf("query %s aggregation #%d (%s): reduceTo=%s (metric-type default)", spec.Name, ai+1, metricName, reducer))
		}
	}
	return decisions, nil
}
