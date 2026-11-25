package instructions

import "github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"

// ServerInstructions is sent to MCP clients during the initialize response.
// The preamble before the first heading routes questions to tools, because
// clients that defer tool loading show the model only tool names and these
// instructions; the rest holds rules that span several tools.
const ServerInstructions = `SigNoz tools query the user's metrics, logs, traces, and LLM or AI-agent telemetry, and manage dashboards, alerts, and saved views; questions about model calls, tokens, cost, or prompts mean the user's app in SigNoz, not this chat. A service's request rate, error rate, or latency: signoz_list_services. Log lines: signoz_search_logs; log counts: signoz_aggregate_logs. Spans: signoz_search_traces; span statistics: signoz_aggregate_traces. Metric values: signoz_query_metrics. SigNoz docs: signoz_search_docs.

## Which tool answers what

- One service's operations: signoz_get_service_top_operations. Which services call or depend on a service: signoz_get_service_map.
- Custom counts, percentiles, or time series over spans: signoz_aggregate_traces. For a service's requests, filter on ` + querybuilder.EntrySpanFilter + `; without it every span of a request counts. The span metrics signoz_calls_total and signoz_latency also count every span.
- One trace by ID: signoz_get_trace_details. Find trace IDs or slow traces: signoz_search_traces.
- Metric names and metadata: signoz_list_metrics. Formulas across queries, PromQL, or ClickHouse SQL: signoz_execute_builder_query.
- Field names: signoz_get_field_keys. Field values: signoz_get_field_values.
- Firing alerts: signoz_list_alerts. Alert rules: signoz_list_alert_rules. When an alert fired: signoz_get_alert_history.
- Dashboards: signoz_list_dashboards and signoz_get_dashboard. Templates: signoz_list_dashboard_templates. Saved views: signoz_list_views.
- Which metrics drive ingestion cost: signoz_get_top_metrics. SigNoz usage and billing metrics: signoz_query_metrics with source="meter".
- A page of the SigNoz docs: signoz_fetch_doc after signoz_search_docs.
- If the question names no signal and these routes do not settle it, ask whether to use metrics, traces, or logs.

## Rules

1. **Query once when possible.** Fetch more only if the first result is insufficient or the user asks. Never repeat overlapping queries.

2. **Prefer resource attributes in filters.** Use service.name, k8s.namespace.name, or host.name when available. If none was supplied, call signoz_get_field_keys with fieldContext=resource, optionally signoz_get_field_values, then ask the user to choose. Keys vary by workspace/signal; logs may lack standard resource fields. On "key not found", discover valid keys, then retry with an existing key or remove that condition. Never retry the invalid filter.

3. **Match operators to intent and type.** Use EXISTS/NOT EXISTS for presence, = for exact matches, IN/NOT IN for sets, and LIKE/ILIKE/CONTAINS/REGEXP for string patterns. int64 also supports >, >=, <, and <=; bool supports =, !=, EXISTS, and NOT EXISTS.

4. **Guard negative filters.** !=, NOT LIKE, NOT IN, NOT CONTAINS, and NOT REGEXP also match missing fields. Require presence when excluding a value, for example: service.name EXISTS AND service.name != 'redis'.

5. **Convert timestamps programmatically.** Tool start/end take RFC 3339; query bodies and time-series timestamps are Unix ms. Never convert them mentally; use a date/time function.

6. **Use returned webUrl values verbatim.** Resource results include SigNoz deep links. Never construct links from IDs or the instance URL.

7. **Prepare mutations with reads.** Reuse reads from the same still-current prepared operation; do not repeat them. Otherwise resolve names/IDs and fetch replacement objects. Refresh if state may have changed; preserve unchanged fields.

8. **Reduce metrics over the window deliberately.** A scalar metric value reduces one value per step: reduceTo avg of a percentile is not the window's percentile, and reduceTo sum of a rate is not a count. For a count, use timeAggregation increase with reduceTo sum.

9. **Read result notes before reporting a number.** A note can say what the result measured, such as every span rather than every request; answer with that in mind.

10. **Send feedback.** Call signoz_submit_feedback once for an unexplained tool error, misleading result, missing capability, or the user's feedback about SigNoz, then continue.
`
