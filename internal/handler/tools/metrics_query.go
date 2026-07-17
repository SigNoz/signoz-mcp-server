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
//
// TemporalityMissing / IsMonotonicMissing flag that a matched row lacked the
// field; they drive the drift WARN and the "unknown/assumed" decision note.
type metricMetadata struct {
	MetricType         string
	IsMonotonic        bool
	Temporality        string
	TemporalityMissing bool
	IsMonotonicMissing bool
}

// metricMetadataDriftMarker is the static log marker for partial-field drift; grep it in prod logs.
const metricMetadataDriftMarker = "metric metadata partial-field drift"

func (h *Handler) handleQueryMetrics(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	mqr, err := parseMetricsQueryArgs(args)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
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
		meta, fetchErr := h.fetchMetricMetadata(ctx, client, mqr.MetricName, mqr.Source)
		if fetchErr != nil {
			return upstreamError(fmt.Errorf(
				"could not auto-fetch metric metadata for %q: %w. "+
					"Provide metricType, temporality, and isMonotonic manually "+
					"(get them from signoz_list_metrics)",
				mqr.MetricName, fetchErr)), nil
		}
		if meta != nil {
			mqr.MetricType = meta.MetricType
			mqr.IsMonotonic = meta.IsMonotonic
			mqr.Temporality = meta.Temporality
			decisions = append(decisions, fmt.Sprintf("metricType: %s (auto-fetched via signoz_list_metrics)", mqr.MetricType))
			// Absent fields are reported as unknown/assumed, not authoritative.
			if meta.TemporalityMissing {
				decisions = append(decisions, fmt.Sprintf("temporality: unknown (not returned by metadata; assumed %q)", mqr.Temporality))
			} else {
				decisions = append(decisions, fmt.Sprintf("temporality: %s (auto-fetched)", mqr.Temporality))
			}
			if meta.IsMonotonicMissing {
				decisions = append(decisions, fmt.Sprintf("isMonotonic: unknown (not returned by metadata; assumed %t)", mqr.IsMonotonic))
			} else {
				decisions = append(decisions, fmt.Sprintf("isMonotonic: %t (auto-fetched)", mqr.IsMonotonic))
			}
		} else {
			// User-correctable (wrong metric name); coded like the formula not-found path.
			return errorWithCode(CodeValidationFailed, fmt.Sprintf(
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
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	// Step interval: use caller-provided value or let the backend decide
	stepInterval := mqr.StepInterval
	callerProvidedStep := stepInterval > 0
	if callerProvidedStep {
		decisions = append(decisions, fmt.Sprintf("stepInterval: %ds (caller-provided)", stepInterval))
	} else if mqr.StepIntervalInvalid != "" {
		// Present but not a plain positive integer of seconds (e.g. "1h", "60s",
		// "abc"). Don't coerce it to a wrong bucket size — fall back to backend
		// auto-select and tell the caller why.
		decisions = append(decisions, fmt.Sprintf(
			"stepInterval: ignored invalid value %q (must be a positive integer count of seconds, e.g. 60); using backend auto-select",
			mqr.StepIntervalInvalid))
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
		return errorWithCode(CodeValidationFailed, formatValidationError(err)), nil
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
		subResolved, subErr := resolveFormulaSubQuery(ctx, h, client, fq, mqr.RequestType, mqr.Source, &decisions)
		if subErr != nil {
			// Upstream metadata-fetch failures get the uniform prefix; local
			// validation errors ("metric not found"/"validation error") stay raw.
			if res, ok := asUpstreamResult(subErr); ok {
				return res, nil
			}
			return errorWithCode(CodeValidationFailed, subErr.Error()), nil
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
	if mqr.Formula != "" {
		decisions = append(decisions, fmt.Sprintf("formula input bounds: limit=%d groups per query, order=__result desc", types.DefaultFormulaInputQueryLimit))
		decisions = append(decisions, fmt.Sprintf("formula result bounds: limit=%d groups, order=__result desc", types.DefaultAggregateQueryLimit))
	} else {
		decisions = append(decisions, fmt.Sprintf("result bounds: limit=%d groups, order=__result desc", types.DefaultAggregateQueryLimit))
	}
	if mqr.RequestType == "time_series" {
		decisions = append(decisions, "time-series selection: top groups are ranked across the entire time range; a short-lived spike can fall outside the selected groups")
	}

	// Build and execute
	queryJSON, err := types.BuildMetricsQueryPayloadJSON(startTime, endTime, stepInterval, querySpecs, mqr.RequestType, mqr.Source)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to build query payload: %s", err.Error())), nil
	}

	h.logger.DebugContext(ctx, "Executing metrics query", slog.String("payload", logpkg.TruncBody(queryJSON)))

	result, err := client.QueryBuilderV5(ctx, queryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Metrics query failed", err)
		return upstreamQueryError(err, "metrics"), nil
	}

	// Extract backend-determined stepInterval from response if caller didn't provide one
	if !callerProvidedStep {
		if si := extractStepInterval(result); si > 0 {
			decisions = append(decisions, fmt.Sprintf("stepInterval: %ds (backend-determined)", si))
		}
	}
	backendWarnings := extractBackendWarningMessages(result)
	warnBackendWarnings(ctx, h.logger, "signoz_query_metrics", backendWarnings)
	warnUnparsedWarningEnvelope(ctx, h.logger, "signoz_query_metrics", result, len(backendWarnings))

	// JSON-first: the raw backend payload is block 0 (matching the search/
	// aggregate siblings); decisions/warnings go into a SEPARATE note block
	// rather than prepended. query_metrics is a raw QB passthrough, so it stays
	// text-only (no structuredContent) — its upstream shape is variable.
	note := buildMetricsDecisionsNote(decisions, resolved.Warnings, backendWarnings)
	return resultWithNotes(result, note), nil
}

// buildMetricsDecisionsNote renders the decisions/warnings advisory block that
// query_metrics surfaces alongside (not prepended into) its JSON payload. It is
// emitted as a separate content block via resultWithNotes.
func buildMetricsDecisionsNote(decisions, defaultWarnings, backendWarnings []string) string {
	var b strings.Builder
	b.WriteString("[Decisions applied]\n")
	for _, d := range decisions {
		b.WriteString(fmt.Sprintf("  %s\n", d))
	}
	for _, w := range defaultWarnings {
		b.WriteString(fmt.Sprintf("  WARNING: %s\n", w))
	}
	for _, w := range backendWarnings {
		b.WriteString(fmt.Sprintf("  WARNING: backend: %s\n", w))
	}
	return strings.TrimRight(b.String(), "\n")
}

// fetchMetricMetadata calls ListMetrics to get type/temporality/isMonotonic for a metric.
// source is forwarded so that Cost Meter metrics (source="meter") are looked up in the
// correct store rather than the default metrics store.
func (h *Handler) fetchMetricMetadata(ctx context.Context, client interface {
	ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
}, metricName, source string) (*metricMetadata, error) {
	// Search with exact metric name, limit 10 to find it
	result, err := client.ListMetrics(ctx, 0, 0, 10, metricName, source)
	if err != nil {
		return nil, err
	}

	// The response is typically {"status":"success","data":{"metrics":[...]}}
	// or just an array. Try both formats.
	meta, err := parseMetricMetadataFromResponse(result, metricName)
	if err != nil {
		return nil, err
	}
	// Fail open, but never fail silent: a matched row missing a field signals
	// upstream drift before we apply a possibly-wrong default, so WARN on it.
	if meta != nil {
		if meta.TemporalityMissing {
			h.logger.WarnContext(ctx, metricMetadataDriftMarker,
				slog.String("metricName", metricName),
				slog.String("missingField", "temporality"),
				slog.String("metricType", meta.MetricType))
		}
		if meta.IsMonotonicMissing {
			h.logger.WarnContext(ctx, metricMetadataDriftMarker,
				slog.String("metricName", metricName),
				slog.String("missingField", "isMonotonic"),
				slog.String("metricType", meta.MetricType))
		}
	}
	return meta, nil
}

// metricMetadataRow mirrors one entry of a ListMetrics response. IsMonotonic and
// Temporality are pointers so an ABSENT field differs from a present empty/false
// value, enabling drift detection without flagging legitimate empty values.
type metricMetadataRow struct {
	MetricName  string  `json:"metricName"`
	Type        string  `json:"type"`
	IsMonotonic *bool   `json:"isMonotonic"`
	Temporality *string `json:"temporality"`
}

// metricMetadataFromRow builds metricMetadata from a matched row, recording
// missing fields. Drift flags only apply to a genuine match (non-empty type).
func metricMetadataFromRow(m metricMetadataRow) *metricMetadata {
	mt := normalizeMetricType(m.Type)
	isMono := m.IsMonotonic != nil && *m.IsMonotonic
	temporality := ""
	if m.Temporality != nil {
		temporality = *m.Temporality
	}
	meta := &metricMetadata{
		MetricType:  mt,
		IsMonotonic: isMono,
		Temporality: temporality,
	}
	if mt != "" {
		// Absent (nil), not present-but-empty: an explicit "" is a legitimate value.
		meta.TemporalityMissing = m.Temporality == nil
		// isMonotonic is only meaningful for sums; only its absence there is drift.
		meta.IsMonotonicMissing = mt == "sum" && m.IsMonotonic == nil
	}
	return meta
}

// parseMetricMetadataFromResponse extracts metric metadata from the ListMetrics response.
func parseMetricMetadataFromResponse(data json.RawMessage, metricName string) (*metricMetadata, error) {
	// Try format: {"status":"success","data":{"metrics":[{...}]}}
	var wrapper struct {
		Status string `json:"status"`
		Data   struct {
			Metrics []metricMetadataRow `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Data.Metrics) > 0 {
		for _, m := range wrapper.Data.Metrics {
			if m.MetricName == metricName {
				return metricMetadataFromRow(m), nil
			}
		}
		// If exact match not found, return the first result if search was specific
		return metricMetadataFromRow(wrapper.Data.Metrics[0]), nil
	}

	// Try format: [{"metricName":"...", "type":"...", ...}]
	var metrics []metricMetadataRow
	if err := json.Unmarshal(data, &metrics); err == nil && len(metrics) > 0 {
		for _, m := range metrics {
			if m.MetricName == metricName {
				return metricMetadataFromRow(m), nil
			}
		}
		return metricMetadataFromRow(metrics[0]), nil
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
}, fq formulaSubQuery, requestType, source string, decisions *[]string) (*metricsrules.ResolvedAggregation, error) {
	metricType := fq.MetricType
	isMonotonic := fq.IsMonotonic
	temporality := fq.Temporality

	// Auto-fetch if needed
	if metricType == "" {
		meta, err := h.fetchMetricMetadata(ctx, client, fq.MetricName, source)
		if err != nil {
			// Upstream (ListMetrics) failure — tag it so the caller surfaces the
			// uniform "SigNoz API error:" prefix. The "metric not found" and
			// "validation error" paths below are local and stay untagged.
			return nil, markUpstream(fmt.Errorf("failed to auto-fetch metadata for formula query %q (%s): %w", fq.Name, fq.MetricName, err))
		}
		if meta != nil {
			metricType = meta.MetricType
			isMonotonic = meta.IsMonotonic
			temporality = meta.Temporality
			*decisions = append(*decisions, fmt.Sprintf("query %s (%s): metricType=%s (auto-fetched)", fq.Name, fq.MetricName, metricType))
			// Mirror the primary path: absent fields are disclosed as assumed.
			if meta.TemporalityMissing {
				*decisions = append(*decisions, fmt.Sprintf("query %s (%s): temporality unknown (not returned by metadata; assumed %q)", fq.Name, fq.MetricName, temporality))
			}
			if meta.IsMonotonicMissing {
				*decisions = append(*decisions, fmt.Sprintf("query %s (%s): isMonotonic unknown (not returned by metadata; assumed %t)", fq.Name, fq.MetricName, isMonotonic))
			}
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
