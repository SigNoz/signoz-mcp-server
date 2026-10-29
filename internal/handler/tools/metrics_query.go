package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

// metricMetadata holds one metric's metadata from the exact-name lookup.
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
		return clientError(err), nil
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
			decisions = append(decisions, fmt.Sprintf("metricType: %s (auto-fetched)", mqr.MetricType))
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
			return errorWithCode(CodeValidationFailed, metricNotFoundMessage(mqr.MetricName, "")), nil
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
	var querySpecs []querybuilder.MetricsQuery

	// Primary query is always "A"
	primaryGroupBy := buildGroupByFields(mqr.GroupBy)
	if len(mqr.GroupBy) > 0 {
		decisions = append(decisions, fmt.Sprintf("groupBy: %s", strings.Join(mqr.GroupBy, ", ")))
	}

	primary := querybuilder.MetricsQuery{
		Name: "A",
		Aggregation: querybuilder.MetricAggregation(
			mqr.MetricName, mqr.Temporality, resolved.TimeAggregation, resolved.SpaceAggregation, resolved.ReduceTo,
		),
		Filter:    mqr.Filter,
		GroupBy:   primaryGroupBy,
		Functions: mqr.Functions,
		// having always filters the primary query: SigNoz accepts a having on
		// a formula but ignores it (pinned spec), so it is never sent there.
		Having: mqr.Having,
	}
	// limit and order shape the result the caller reads: the formula when
	// there is one, otherwise the primary query.
	if mqr.Formula == "" {
		primary.Limit, primary.OrderKey, primary.OrderDir = mqr.Limit, mqr.OrderKey, mqr.OrderDir
	}
	querySpecs = append(querySpecs, primary)

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
		querySpecs = append(querySpecs, querybuilder.MetricsQuery{
			Name: fq.Name,
			Aggregation: querybuilder.MetricAggregation(
				fq.MetricName, fq.Temporality, subResolved.TimeAggregation, subResolved.SpaceAggregation, subResolved.ReduceTo,
			),
			Filter:  fq.Filter,
			GroupBy: subGroupBy,
		})
	}

	// Formula query spec
	if mqr.Formula != "" {
		querySpecs = append(querySpecs, querybuilder.MetricsQuery{
			Name:       formulaName(querySpecs),
			IsFormula:  true,
			Expression: mqr.Formula,
			Legend:     "formula_result",
			Limit:      mqr.Limit,
			OrderKey:   mqr.OrderKey,
			OrderDir:   mqr.OrderDir,
		})
		decisions = append(decisions, fmt.Sprintf("formula: %s", mqr.Formula))
	}
	resultLimit, resultOrder := querybuilder.DefaultAggregateQueryLimit, "__result desc"
	if mqr.Limit > 0 {
		resultLimit = mqr.Limit
	}
	if mqr.OrderKey != "" {
		resultOrder = mqr.OrderKey + " " + mqr.OrderDir
	}
	if mqr.Formula != "" {
		decisions = append(decisions, "formula input bounds: none; every input series reaches the formula")
		decisions = append(decisions, fmt.Sprintf("formula result bounds: limit=%d groups, order=%s", resultLimit, resultOrder))
	} else {
		decisions = append(decisions, fmt.Sprintf("result bounds: limit=%d groups, order=%s", resultLimit, resultOrder))
	}
	if mqr.LimitClamped {
		decisions = append(decisions, fmt.Sprintf("limit clamped to %d groups to bound server memory", MaxRawResultLimit))
	}
	if len(mqr.Functions) > 0 && mqr.RequestType == "scalar" {
		decisions = append(decisions, "functions: not applied; SigNoz applies them to time_series results only and returns scalar results unchanged")
	}
	if mqr.Having != "" && mqr.Formula != "" {
		decisions = append(decisions, fmt.Sprintf("having (on query A, before the formula; SigNoz ignores a having on the formula result): %s", mqr.Having))
	} else if mqr.Having != "" {
		decisions = append(decisions, fmt.Sprintf("having: %s", mqr.Having))
	}
	if mqr.RequestType == "time_series" {
		decisions = append(decisions, "time-series selection: top groups are ranked across the entire time range; a short-lived spike can fall outside the selected groups")
	}

	// Build and execute
	queryJSON, err := querybuilder.MetricsJSON(startTime, endTime, stepInterval, querySpecs, mqr.RequestType, mqr.Source)
	if err != nil {
		return validationResult(fmt.Sprintf("Failed to build query payload: %s", err.Error())), nil
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
		fmt.Fprintf(&b, "  %s\n", d)
	}
	for _, w := range defaultWarnings {
		fmt.Fprintf(&b, "  WARNING: %s\n", w)
	}
	for _, w := range backendWarnings {
		fmt.Fprintf(&b, "  WARNING: backend: %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}

// metricMetadataClient is the part of the SigNoz client the metadata lookup uses.
type metricMetadataClient interface {
	ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
	GetMetricMetadata(ctx context.Context, metricName string) (json.RawMessage, error)
}

// fetchMetricMetadata looks up type, temporality, and isMonotonic for exactly
// metricName, or returns nil when SigNoz has no such metric. The metrics store
// has an exact-name endpoint; Cost Meter metrics (source="meter") are listed
// by substring and matched exactly here.
func (h *Handler) fetchMetricMetadata(ctx context.Context, client metricMetadataClient, metricName, source string) (*metricMetadata, error) {
	var meta *metricMetadata
	if source == "" {
		result, err := client.GetMetricMetadata(ctx, metricName)
		var statusErr *signozclient.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		meta = parseExactMetricMetadata(result)
		if meta == nil {
			// Fail open as "not found", but never silent: a 200 without a type is drift.
			h.logger.WarnContext(ctx, metricMetadataDriftMarker,
				slog.String("metricName", metricName),
				slog.String("missingField", "type"))
			return nil, nil
		}
	} else {
		result, err := client.ListMetrics(ctx, 0, 0, 10, metricName, source)
		if err != nil {
			return nil, err
		}
		if meta, err = parseMetricMetadataFromResponse(result, metricName); err != nil {
			return nil, err
		}
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

// parseExactMetricMetadata decodes a GetMetricMetadata body. It returns nil
// when the body carries no metric type.
func parseExactMetricMetadata(data json.RawMessage) *metricMetadata {
	var resp struct {
		Data struct {
			apitypes.MetricsexplorertypesMetricMetadata
			// Shadows the generated bool so an absent field differs from false.
			IsMonotonic *bool `json:"isMonotonic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.Data.Type == "" {
		return nil
	}
	metricType := normalizeMetricType(string(resp.Data.Type))
	return &metricMetadata{
		MetricType:         metricType,
		IsMonotonic:        resp.Data.IsMonotonic != nil && *resp.Data.IsMonotonic,
		Temporality:        string(resp.Data.Temporality),
		TemporalityMissing: resp.Data.Temporality == "",
		IsMonotonicMissing: metricType == "sum" && resp.Data.IsMonotonic == nil,
	}
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
		// The search is a substring match: only an exact name is this metric.
		for _, m := range wrapper.Data.Metrics {
			if m.MetricName == metricName {
				return metricMetadataFromRow(m), nil
			}
		}
		return nil, nil
	}

	// Try format: [{"metricName":"...", "type":"...", ...}]
	var metrics []metricMetadataRow
	if err := json.Unmarshal(data, &metrics); err == nil && len(metrics) > 0 {
		for _, m := range metrics {
			if m.MetricName == metricName {
				return metricMetadataFromRow(m), nil
			}
		}
		return nil, nil
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
func resolveFormulaSubQuery(ctx context.Context, h *Handler, client metricMetadataClient, fq formulaSubQuery, requestType, source string, decisions *[]string) (*metricsrules.ResolvedAggregation, error) {
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
			return nil, errors.New(metricNotFoundMessage(fq.MetricName, fq.Name))
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

// metricNotFoundMessage turns a failed exact-name lookup into a search
// instruction; agents most often guess a plausible name the workspace lacks.
func metricNotFoundMessage(metricName, formulaQuery string) string {
	subject := fmt.Sprintf("Metric %q", metricName)
	if formulaQuery != "" {
		subject = fmt.Sprintf("Metric %q in formula query %q", metricName, formulaQuery)
	}
	return subject + " was not found in this workspace. Search for the right name with signoz_list_metrics, " +
		"using part of the name as searchText (for example \"cpu\"), then retry with the exact name. " +
		"A histogram named X is stored as X.bucket."
}

func formulaName(existing []querybuilder.MetricsQuery) string {
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
