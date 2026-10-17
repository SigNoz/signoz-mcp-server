package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
)

const (
	maxBuilderMetricMetadataLookups = 16
	builderMetricMetadataTimeout    = 30 * time.Second
	builderMetricReducerRecovery    = "supply reduceTo explicitly (sum, count, avg, min, max, last, or median)"
)

func (h *Handler) defaultBuilderMetricReducers(ctx context.Context, c client.Client, request map[string]any) ([]string, *mcp.CallToolResult) {
	requestType, _ := request["requestType"].(string)
	if strings.ToLower(strings.TrimSpace(requestType)) != string(apitypes.Querybuildertypesv5RequestTypeScalar) {
		return nil, nil
	}
	// Read through the generated contract, but edit the original object so unknown
	// fields and explicitly supplied values still reach SigNoz unchanged.
	body, err := json.Marshal(request)
	if err != nil {
		return nil, nil
	}
	var payload apitypes.Querybuildertypesv5QueryRangeRequest
	if err := json.Unmarshal(body, &payload); err != nil || payload.Start == nil || payload.End == nil || *payload.Start <= 0 || *payload.End <= 0 {
		return nil, nil
	}
	composite, _ := request["compositeQuery"].(map[string]any)
	queries, _ := composite["queries"].([]any)
	type pendingReducer struct {
		aggregation                     map[string]any
		metricName, source, name, field string
		index                           int
	}
	var pending []pendingReducer
	reducers := make(map[[2]string]string)
	for qi, raw := range queries {
		query, _ := raw.(map[string]any)
		rawSpec, _ := builderField(query, "spec")
		spec, _ := rawSpec.(map[string]any)
		queryType := strings.ToLower(strings.TrimSpace(builderString(query, "type")))
		signal := strings.ToLower(strings.TrimSpace(builderString(spec, "signal")))
		if queryType != string(apitypes.Querybuildertypesv5QueryTypeBuilderQuery) || signal != "metrics" {
			continue
		}
		source := strings.ToLower(strings.TrimSpace(builderString(spec, "source")))
		name := builderString(spec, "name")
		rawAggregations, _ := builderField(spec, "aggregations")
		aggregations, _ := rawAggregations.([]any)
		for ai, raw := range aggregations {
			agg, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, supplied := builderField(agg, "reduceTo"); supplied {
				continue
			}
			metricName := builderString(agg, "metricName")
			field := fmt.Sprintf("query.compositeQuery.queries[%d].spec.aggregations[%d].reduceTo", qi, ai)
			if metricName == "" {
				return nil, validationError(strings.TrimSuffix(field, "reduceTo")+"metricName", "is required to choose a default; find the metric with signoz_list_metrics")
			}
			reducers[[2]string{source, metricName}] = ""
			if len(reducers) > maxBuilderMetricMetadataLookups {
				return nil, validationError("query.compositeQuery.queries", fmt.Sprintf("requires more than %d metric metadata lookups; %s on the scalar metrics aggregations", maxBuilderMetricMetadataLookups, builderMetricReducerRecovery))
			}
			pending = append(pending, pendingReducer{agg, metricName, source, name, field, ai})
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	metadataCtx, cancel := context.WithTimeout(ctx, builderMetricMetadataTimeout)
	defer cancel()
	var decisions []string
	for _, item := range pending {
		key := [2]string{item.source, item.metricName}
		reducer := reducers[key]
		if reducer == "" {
			meta, err := h.fetchBuilderMetricMetadata(metadataCtx, c, int64(*payload.Start), int64(*payload.End), item.metricName, item.source)
			if err != nil {
				return nil, upstreamError(fmt.Errorf("could not choose reduceTo for metric %q: %w; %s to skip metadata lookup", item.metricName, err, builderMetricReducerRecovery))
			}
			if meta == nil {
				return nil, validationError(item.field, fmt.Sprintf("could not find metadata for metric %q in this source; check metricName and source with signoz_list_metrics, or %s if the name is correct", item.metricName, builderMetricReducerRecovery))
			}
			if meta.MetricName != item.metricName || meta.MetricType == "" || meta.IsMonotonicMissing {
				h.logger.WarnContext(ctx, "Cannot determine scalar metric reducer from metadata", slog.String("metricName", item.metricName), slog.String("source", item.source))
				return nil, validationError(item.field, "cannot be defaulted from the metric metadata; "+builderMetricReducerRecovery)
			}
			resolved, err := metricsrules.ApplyDefaults(metricsrules.MetricQueryParams{MetricType: meta.MetricType, IsMonotonic: meta.IsMonotonic}, "scalar")
			if err != nil {
				h.logger.WarnContext(ctx, "Unsupported metric metadata type for scalar reducer", slog.String("metricName", item.metricName), slog.String("source", item.source), slog.String("metricType", meta.MetricType))
				return nil, validationError(item.field, "cannot be defaulted for this metric type; "+builderMetricReducerRecovery)
			}
			reducer = resolved.ReduceTo
			reducers[key] = reducer
		}
		item.aggregation["reduceTo"] = reducer
		decisions = append(decisions, fmt.Sprintf("query %s aggregation #%d (%s): reduceTo=%s (metric-type default)", item.name, item.index+1, item.metricName, reducer))
	}
	return decisions, nil
}

func (h *Handler) fetchBuilderMetricMetadata(ctx context.Context, c client.Client, start, end int64, metricName, source string) (*metricMetadata, error) {
	meta, err := h.fetchMetricMetadata(ctx, c, start, end, metricName, source)
	if err != nil || (meta != nil && meta.MetricName == metricName) {
		return meta, err
	}
	// The catalog is a bounded substring search. Its first page can omit an exact name.
	if source == "meter" {
		// Meter queries align to hourly buckets outside short requested windows.
		// Its metadata fallback stays in the meter store and omits the window.
		data, err := c.ListMetrics(ctx, 0, 0, 5000, metricName, source)
		if err != nil {
			return nil, err
		}
		meta, err := parseMetricMetadataFromResponse(data, metricName)
		if err != nil {
			h.logger.WarnContext(ctx, "Cannot parse metric catalog metadata", slog.String("metricName", metricName), slog.String("source", source))
			return nil, err
		}
		if meta != nil && meta.MetricName != metricName {
			return nil, nil
		}
		return meta, nil
	}
	data, err := c.GetMetricMetadata(ctx, metricName)
	if err != nil {
		var statusErr *client.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	var response struct {
		Data *metricMetadataRow `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		h.logger.WarnContext(ctx, "Cannot parse exact metric metadata", slog.String("metricName", metricName))
		return nil, fmt.Errorf("invalid metric metadata response: %w", err)
	}
	if response.Data == nil || response.Data.Type == "" {
		h.logger.WarnContext(ctx, "Incomplete exact metric metadata response", slog.String("metricName", metricName))
		return nil, fmt.Errorf("invalid metric metadata response: missing data or metric type")
	}
	if response.Data.MetricName == "" {
		response.Data.MetricName = metricName
	}
	return metricMetadataFromRow(*response.Data), nil
}

func builderString(object map[string]any, name string) string {
	value, _ := builderField(object, name)
	text, _ := value.(string)
	return text
}

// JSON marshaling sorts map keys, and SigNoz's decoder takes the last
// case-insensitive match. Use the same value without rewriting caller fields.
func builderField(object map[string]any, name string) (any, bool) {
	var value any
	var matched string
	found := false
	for key, candidate := range object {
		if strings.EqualFold(key, name) && (!found || key > matched) {
			value, matched, found = candidate, key, true
		}
	}
	return value, found
}
