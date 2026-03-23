# Feature: Metrics Query Tool — Context & Discussion

## Original Prompt
> create relevant metrics tools after understanding docs and openapi specs mentioned at
> https://signoz.io/docs/metrics-management/query-range-api/. Always understand the description
> of metrics as per the response of signoz_list_metrics which has unit, temporality, type,
> isMonotonic, etc. Also read the docs at
> https://signoz.io/docs/metrics-management/types-and-aggregation/ and gather understanding from
> public data to prompt users to ask the right fields and questions to prepare the right query
> range payload. Extract examples and understanding from panels and dashboards at
> https://github.com/SigNoz/dashboards/tree/main/k8s-infra-metrics to generate and prompt users
> to create the right and relevant query helping them understand the nuances of spacial and
> temporal aggregations along with choosing the right aggregation for the right type of metric
> (gauge, counter, monotonic, histogram). After understanding the details, you can also think
> about suggesting a resource to client if the guide becomes too big

## Reference Links
- [Metrics Query Range API docs](https://signoz.io/docs/metrics-management/query-range-api/)
- [Metric Types & Aggregation docs](https://signoz.io/docs/metrics-management/types-and-aggregation/)
- [k8s Infra Metrics Dashboard examples](https://github.com/SigNoz/dashboards/tree/main/k8s-infra-metrics)

## Key Decisions & Discussion Log

### 2026-03-23 — Initial planning
- New tool `signoz_query_metrics` to be a high-level wrapper over `signoz_execute_builder_query`
- Always call `signoz_list_metrics` first to get type/temporality/isMonotonic before querying
- Aggregation validation and smart defaults driven by metric type metadata
- MCP resource `signoz://metrics-aggregation-guide` to serve the full rules guide (keeps tool description concise)
- MetricAggregation struct needed in `pkg/types/querybuilder.go`
- `BuildMetricsQueryPayload` helper to be added alongside existing `BuildLogsQueryPayload` / `BuildTracesQueryPayload`

## Open Questions
<!-- Add questions as they arise during brainstorming -->
- Should `signoz_query_metrics` support multiple metrics in a single query (multiple aggregations in one payload)?
- Should we auto-fetch metric metadata inside the tool (call ListMetrics internally) or require the caller to pass metricType/temporality?
- How should groupBy fields specify fieldContext (resource vs tag)? Auto-detect or explicit param?
