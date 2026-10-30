package tools

// Parameter descriptions for the query tools (logs, traces, and metrics) and
// the one every tool shares, kept in one file so a teaching change lands in
// one place and shared wording stays identical across tools. Client-visible
// text: see docs/client-visible-writing-style.md.

// Shared by every tool.
const (
	searchContextParamDescription = "Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."
)

// Shared by the query tools: time window, buckets, result shape, and result shaping.
const (
	startParamDescription        = "Start time in unix milliseconds (optional). When both start and end are provided, they override timeRange."
	endParamDescription          = "End time in unix milliseconds (optional). When both start and end are provided, they override timeRange."
	stepIntervalParamDescription = "Time bucket size in seconds for time_series mode (optional). " +
		"When omitted, the backend auto-selects an appropriate interval. " +
		"Only set this if the user explicitly requests a specific granularity. " +
		"Examples: '60' (1 min), '3600' (1 hour), '86400' (1 day)."
	aggregateRequestTypeParamDescription = `Result shape. "scalar" (default) returns one value or a grouped/ranked table over the full time range; use it for totals, percentiles, and top lists. "time_series" returns time-bucketed values, with one series per group when grouped; use it for spikes, trends, changes over time, or questions about when something happened.`
	aggregateLimitParamDescription       = "Maximum number of groups to return (default: 100, max: 10000; higher values are clamped). For time_series queries, groups are ranked across the entire time range, so a short-lived spike can fall outside the selected top groups."
	havingParamDescription               = "Condition on the aggregation results, in the filter syntax, applied after grouping. " +
		"Reference an aggregation by its expression, for example \"count() > 100\" or \"p99(duration_nano) > 500000000\". SigNoz validates it."
	functionsParamDescription = "Post-query transforms applied in order to each time_series result, as an array of {name, args} objects, or a JSON-encoded string of that array. " +
		"SigNoz applies them to time_series results only and returns scalar results unchanged, so set requestType to time_series. " +
		"Names are the Query Builder function names, for example cutOffMin, cutOffMax, clampMin, clampMax, absolute, runningDiff, cumulativeSum, ewma3, median5, timeShift, or fillZero; " +
		"args carries the function's parameters, for example [{\"value\": 10}] for cutOffMin. SigNoz validates names and args."
)

// signoz_aggregate_logs and signoz_search_logs.
const (
	logsFilterParamDescription          = "Filter expression text in SigNoz search syntax, not {\"expression\": ...} (see signoz://logs/query-builder-guide). Combine conditions with AND, OR, and parentheses. An unknown key may not fail: no rows plus a `key ... not found in metadata` warning means the key is absent. Ambiguous keys default to resource; prefix attribute.<key> or resource.<key> to choose. Log keys are workspace-specific; even service.name exists only when the log pipeline sets it. Discover keys and values with signoz_get_field_keys and signoz_get_field_values first. For message text use body CONTAINS 'term'. Only when you don't know which field holds a term, use search('term') for every field or search('term', attribute); scopes (body, attribute, resource, log) are ORed. search() is slow and may be rejected over wide time ranges. There is no key:value, STARTS WITH, or IS NULL; use LIKE 'prefix%', and EXISTS or NOT EXISTS. Example: \"service.name = 'payment-svc' AND severity_text = 'ERROR'\"."
	aggregateLogsFilterParamDescription = logsFilterParamDescription + " Combined with service/severity params using AND."
	logsAggregationsParamDescription    = "Aggregation expressions to compute, written as the Query Builder writes them: count(), count_distinct(user_id), avg(duration), sum(bytes), min(x), max(x), p50(x) to p99(x), rate(), or a conditional such as countIf(severity_text = 'ERROR'). " +
		"One expression or an array; results come back as __result_0, __result_1, ... in that order. Example: [\"count()\", \"p99(duration)\"]. SigNoz validates the expressions."
	logsGroupByParamDescription = "Field names to group results by, as an array or a comma-separated string (e.g. [\"service.name\", \"severity_text\"] or 'service.name, severity_text'). Leave empty for a single aggregate value. " +
		"Group by keys signoz_get_field_keys returns; an unknown key can come back as one empty group. A body field (body.<path>) can be grouped only when signoz_get_field_keys(signal=\"logs\", fieldContext=\"body\") lists it; otherwise SigNoz rejects it with `can be filtered but not selected or grouped` or `field not found`, so filter on it instead."
	aggregateLogsOrderByParamDescription  = "How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration) asc'. Defaults to the first aggregation expression descending."
	aggregateLogsServiceParamDescription  = "Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter. When this workspace's logs lack service.name, the call usually returns no rows with a `key service.name not found in metadata` warning instead of failing; discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead."
	aggregateLogsSeverityParamDescription = "Shortcut filter for severity_text, matched in any case and as a prefix, so WARN also matches warning and Warning; pass the short form (ERR, WARN, INFO). Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover the workspace's values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\")."
	searchLogsServiceParamDescription     = "Optional service name to filter by (adds service.name = '<value>'). When this workspace's logs lack service.name, the call usually returns no rows with a `key service.name not found in metadata` warning instead of failing; discover keys with signoz_get_field_keys(signal=\"logs\", fieldContext=\"resource\") and filter on an available key instead."
	searchLogsSeverityParamDescription    = "Filter on severity_text, matched in any case and as a prefix, so WARN also matches warning and Warning; pass the short form (ERR, WARN, INFO). Common values include DEBUG, INFO, WARN, ERROR, and FATAL, but they are not an exhaustive enum. Discover the workspace's values with signoz_get_field_values(signal=\"logs\", name=\"severity_text\", fieldContext=\"log\")."
	logsSearchTextParamDescription        = "Literal text to find, escaped automatically. searchScope chooses where it matches; the default is the log body. Combines with filter using AND."
	logsSearchScopeParamDescription       = "Where searchText matches: body (default) for message text; attribute or resource for their keys and values; all for every field. Scopes other than body are slower, and SigNoz may reject them over wide time ranges. Use them only when you don't know which key holds the text; when you do, put a predicate such as attribute.<key> CONTAINS 'text' in filter instead. Ignored, with a note, when searchText is empty."
	searchLogsLimitParamDescription       = "Maximum number of logs to return (default: 100, max: 10000; higher values are clamped; paginate with offset)"
	searchLogsOffsetParamDescription      = "Offset for pagination (default: 0)"
	searchLogsOrderByParamDescription     = "Row order as '<field> <direction>', e.g. 'timestamp asc' or 'severity_number desc'. Defaults to timestamp desc (newest first); when ordering by timestamp, id breaks ties in the same direction."
	logsSelectFieldsParamDescription      = "Extra fields to return on each row, added to the default set: timestamp, id, body, severity_text, trace_id, span_id. Pass an array of names or a comma-separated string, at most 50. " +
		"Names can be resource attributes (service.name, k8s.pod.name), log attributes (http.route), or other log columns (severity_number); a resource., attribute., log., or body. prefix picks the context. Each field comes back as its own row key. " +
		"A body field (body.<path>) can be selected only when signoz_get_field_keys(signal=\"logs\", fieldContext=\"body\") lists it; otherwise SigNoz rejects it with `can be filtered but not selected or grouped` or `field not found`, so filter on it instead. " +
		"Discover names with signoz_get_field_keys (signal=\"logs\"). Example: [\"service.name\", \"http.route\"]."
)

// signoz_aggregate_traces, signoz_search_traces, and signoz_get_trace_details.
const (
	tracesFilterParamDescription          = "Filter expression text in SigNoz search syntax, not {\"expression\": ...} (see signoz://traces/query-builder-guide). search() is logs-only; traces use field predicates. Combine conditions with AND, OR, and parentheses for precedence. An unknown key may not fail: SigNoz can match a guessed attribute and return a `key ... not found in metadata` warning with no rows, so read that warning as an absent key. Keys present in multiple contexts default to resource context; disambiguate with attribute.<key>, resource.<key>, or span.<key>. Every condition needs a key and an operator: there is no full-text search, key:value, STARTS WITH, or IS NULL; use LIKE 'prefix%' and EXISTS or NOT EXISTS. Discover valid keys with signoz_get_field_keys, then confirm values with signoz_get_field_values, before filtering. Examples: \"service.name = 'payment-svc' AND has_error = true\", \"http_method = 'POST' AND (has_error = true OR duration_nano > 1000000000)\"."
	aggregateTracesFilterParamDescription = tracesFilterParamDescription + " Combined with service/operation/error/duration params using AND."
	searchTracesFilterParamDescription    = tracesFilterParamDescription + " Combined with shortcut params using AND."
	tracesAggregationsParamDescription    = "Aggregation expressions to compute, written as the Query Builder writes them: count(), count_distinct(name), avg(duration_nano), sum(x), min(x), max(x), p50(duration_nano) to p99(duration_nano), rate(), or a conditional such as countIf(has_error = true). " +
		"duration_nano is span latency in nanoseconds. One expression or an array; results come back as __result_0, __result_1, ... in that order. Example: [\"count()\", \"p99(duration_nano)\"]. SigNoz validates the expressions."
	tracesGroupByParamDescription            = "Field names to group results by, as an array or a comma-separated string (e.g. [\"service.name\", \"name\"] or 'service.name, name'). Leave empty for a single aggregate value. For values over time, use requestType time_series; grouping by timestamp fails."
	aggregateTracesOrderByParamDescription   = "How to order results. Format: '<expression> <direction>', e.g. 'count() desc' or 'avg(duration_nano) asc'. Defaults to the first aggregation expression descending."
	aggregateTracesServiceParamDescription   = "Shortcut filter for service name. Equivalent to adding service.name = '<value>' to filter."
	aggregateTracesOperationParamDescription = "Shortcut filter for span/operation name. Equivalent to adding name = '<value>' to filter."
	aggregateTracesErrorParamDescription     = "Shortcut filter for error spans (true or false). Equivalent to adding has_error = true/false to filter."
	searchTracesServiceParamDescription      = "Optional service name to filter by."
	searchTracesOperationParamDescription    = "Operation/span name to filter by."
	searchTracesErrorParamDescription        = "Filter by error status (true or false)."
	minDurationParamDescription              = "Minimum span duration, such as '500ms' or '2s' (units ns, us, ms, s, m, h). A bare number is nanoseconds: 500 means 500ns, not 500ms."
	maxDurationParamDescription              = "Maximum span duration, such as '500ms' or '2s' (units ns, us, ms, s, m, h). A bare number is nanoseconds: 500 means 500ns, not 500ms."
	searchTracesLimitParamDescription        = "Maximum number of rows to return (default: 100, max: 10000; higher values are clamped; paginate with offset)."
	searchTracesOffsetParamDescription       = "Number of rows to skip for pagination (default: 0)."
	searchTracesOrderByParamDescription      = "Row order as '<expression> <direction>', e.g. 'duration_nano desc'; the direction defaults to desc. Span rows order by span fields and default to 'timestamp desc'. builder_ai_query trace rows order by a trace-level field or trace.last_activity_time, e.g. 'trace.estimated_total_cost desc', and default to latest activity first. builder_query trace rows always come back slowest first; SigNoz ignores orderBy for them."
	tracesSelectFieldsParamDescription       = "Extra fields to return on each span row, added to the default set: timestamp, trace_id, span_id, parent_span_id, name, service.name, kind_string, duration_nano, has_error, status_code_string, status_message, response_status_code, http_method. Pass an array of names or a comma-separated string, at most 50. Names can be span columns (db_name), resource attributes (k8s.pod.name), or span attributes (http.route); a resource., attribute., or span. prefix picks the context. Each field comes back as its own row key. Discover names with signoz_get_field_keys (signal=\"traces\"). Example: [\"http.route\", \"db.statement\"]."
	searchTracesQueryTypeParamDescription    = "'builder_query' (default) searches all traces. Set 'builder_ai_query' for AI observability queries: it searches AI traces (traces with an LLM, tool, or agent span), its span rows are only those LLM, tool, and agent spans, and filter also accepts the trace-level fields " + aiTraceKeys + "."
	aggregateTracesQueryTypeParamDescription = "'builder_query' (default) aggregates spans of all traces. Set 'builder_ai_query' for AI observability queries: it aggregates spans of AI traces (traces with an LLM, tool, or agent span), and aggregations, filter, and orderBy also accept the trace-level fields " + aiTraceKeys + ", e.g. avg(trace.estimated_total_cost). One call cannot mix trace-level and span-level aggregations; count() and count_distinct(trace_id) are span-level, so count AI traces with count_distinct(trace_id) in a separate call."
	searchTracesRequestTypeParamDescription  = "Row shape. raw (default) returns one row per span. trace returns one row per trace: span-level filters, including operation, error, minDuration, and maxDuration, keep the traces with at least one matching span, and selectFields does not apply. builder_query trace rows carry trace_id, the root span name, service.name, duration_nano, and span_count (the spans matching the filter). builder_ai_query trace rows carry the trace-level fields without the trace. prefix (such as llm_call_count), plus root_span_name, span_count, trace_duration_nano, error_count, and the input and output message JSON."
	traceIDParamDescription                  = "Known trace ID to retrieve. Discover it with signoz_search_traces when the user has not supplied one."
	includeSpansParamDescription             = "Include detailed span information (default: true)."
)

// aiTraceKeys are the per-trace AI columns SigNoz computes only for builder_ai_query.
const aiTraceKeys = "trace.llm_call_count, trace.tool_call_count, trace.distinct_tool_count, trace.input_tokens, trace.output_tokens, trace.total_tokens, trace.estimated_total_cost, trace.max_llm_duration_nano"

// signoz_list_metrics and signoz_query_metrics.
const (
	listMetricsSearchTextParamDescription = "Filter metrics by name substring (optional). Names separate words with dots or underscores (system.cpu.time, http_server_duration), so search one word, such as 'cpu' or 'memory'."
	listMetricsLimitParamDescription      = "Maximum number of metrics to return (optional). Default: 50."
	listMetricsSourceParamDescription     = "Optional data-source filter. Use \"meter\" to list Cost Meter metrics, the usage/billing metrics SigNoz meters on (currently telemetry ingestion volume). Omit for the default SigNoz metrics store."
	metricNameParamDescription            = "Name of the metric to query. Example: 'container.cpu.utilization', 'http_requests_total'."
	metricTypeParamDescription            = "Metric type: gauge, sum, histogram, exponential_histogram, or summary. Omit to auto-fetch it with temporality and monotonicity."
	isMonotonicParamDescription           = "Whether a type=sum metric is monotonically increasing. Auto-fetched when metricType is omitted; otherwise provide the correct value for sum metrics."
	temporalityParamDescription           = "Metric temporality: cumulative, delta, or unspecified. Auto-fetched when metricType is omitted; otherwise provide the metric's value when it affects aggregation."
	timeAggregationParamDescription       = "Aggregation within each series over a time bucket: latest, sum, avg, min, max, count, count_distinct, rate, or increase. Omit for the type's default: avg for gauges and non-monotonic sums, rate for counters, none for histogram percentiles. SigNoz validates the combination."
	spaceAggregationParamDescription      = "Aggregation across series: sum, avg, min, max, count, or p50, p75, p90, p95, p99 (histograms only). Omit for the type's default: sum, or p99 for histograms. Summary metrics have no defaults; set both aggregations."
	metricsGroupByParamDescription        = "Label names to group by, as an array or a comma-separated string (e.g. [\"service.name\", \"k8s.pod.name\"]). Metric labels hold resource and data-point attributes in one set, so names need no context prefix. Discover them with signoz_get_field_keys (signal=\"metrics\", metricName)."
	metricsFilterParamDescription         = "Filter expression, passed as the expression text, not {\"expression\": ...}. Example: \"k8s.cluster.name = 'prod' AND service.name = 'frontend'\"."
	metricsStartParamDescription          = "Start time in unix milliseconds. When both start and end are provided, they override timeRange."
	metricsEndParamDescription            = "End time in unix milliseconds. When both start and end are provided, they override timeRange."
	metricsStepIntervalParamDescription   = "Step interval in seconds for time_series mode (optional). When omitted, the backend auto-selects an appropriate interval (~300 data points, min 60s). Only set this if the user explicitly requests a specific granularity. Examples: '60' (1 min), '3600' (1 hour), '86400' (1 day)."
	metricsRequestTypeParamDescription    = "Response format: \"time_series\" (default) returns one value per time bucket; \"scalar\" returns a single value per series, reduced with reduceTo, which scalar requires."
	reduceToParamDescription              = "Required when requestType is scalar: how to reduce each series to one value. Choose from the question: last for the current value, max for the peak, avg for a typical value, or sum, min, count, median. For a total count over the window, use timeAggregation increase with reduceTo sum; summing rates is not a count. Ignored for time_series."
	formulaParamDescription               = "Formula expression over named queries. Example: 'A / B * 100'. The primary metric becomes query 'A'. Additional queries are defined in formulaQueries."
	formulaQueriesParamDescription        = "Additional named metric queries the formula references, as an array of objects (or a JSON-encoded string of one). name and metricName are required; the other fields mirror the top-level parameters and are auto-fetched or defaulted the same way."
	metricsLimitParamDescription          = "Maximum number of groups (series) in the result (default: 100, max: 10000; higher values are clamped). With a formula, limit applies to the formula result; formula inputs are not limited."
	metricsOrderByParamDescription        = "Result order as '<key> <direction>'. The key is __result (the aggregated value) or a groupBy field, e.g. '__result asc' for the smallest values first or 'service.name asc'. Defaults to __result desc."
	metricsHavingParamDescription         = "Condition on the primary query's aggregated values, applied after grouping, e.g. \"__result > 0.5\" or \"__result < 100\". With a formula it filters query A's series before the formula runs; SigNoz ignores a having on the formula result itself. SigNoz validates it."
	metricsSourceParamDescription         = "Optional data-source filter forwarded to the backend. Use \"meter\" to query Cost Meter data. Omit for the default SigNoz metrics store."
)

// timeRangeParamDescription builds the shared "timeRange" description. All time-windowed
// tools use one parser (timeutil.ParseTimeRange), so only the per-tool default
// window differs — defaultDesc is that trailing sentence (e.g. "Defaults to
// '1h'."). The full Go-duration grammar is accepted; we advertise the m/h/d
// subset because it covers every realistic observability window.
func timeRangeParamDescription(defaultDesc string) string {
	return "Relative time range. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). " +
		"Examples: '30m', '1h', '2h', '6h', '24h', '3d', '7d'. " +
		"Ignored when both start and end are provided. " + defaultDesc
}
