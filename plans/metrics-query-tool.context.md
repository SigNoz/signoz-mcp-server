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
- [x] Should `signoz_query_metrics` support multiple metrics in a single query? → v1: single primary metric + optional formula with additional named queries. Pure multi-metric without formula deferred.
- [x] Should we auto-fetch metric metadata inside the tool? → No. Caller provides metricType/temporality from prior `signoz_list_metrics`. If absent, apply conservative defaults and warn in decisions block.
- [x] How should groupBy fields specify fieldContext? → Auto-detect: known resource prefixes (`k8s.`, `container.`, `host.`, `cloud.`, `deployment.`, `process.`) → `resource`. Everything else → `attribute`.

### 2026-03-23 — Deep dive: OpenAPI spec + frontend/backend research

**API field name corrections (critical):**
- Actual field name is `timeAggregation` (not `temporalAggregation`)
- Actual field name is `spaceAggregation` (not `spatialAggregation`)
- Source: https://raw.githubusercontent.com/SigNoz/signoz/main/docs/api/openapi.yml

**New decisions:**
- `pkg/metricsrules/` dedicated package for validation + defaults (pure functions, unit-testable, no HTTP)
  - `rules.go` — `ApplyDefaults()` + `ValidateAggregation()` returning `ResolvedAggregation{Decisions, Warnings}`
  - `guide.go` — `MetricsGuide` markdown const → served as MCP resource `signoz://metrics-aggregation-guide`
- `requestType` is always `time_series` for metrics queries
- Step interval auto-calc: `max(60, (end-start)/300/1000)` seconds (targets ~300 data points)
- Formula support: multiple named `builder_query` specs + `builder_formula` with expression (e.g. "A/B*100")
- Every tool response **prepends a `[Decisions applied]` block** listing all defaults used — user's explicit requirement
- Validation errors are descriptive with suggested fix inline

**Payload examples added to plan:**
1. Gauge (CPU utilization) — `timeAggregation: avg`, `spaceAggregation: sum`
2. Counter/cumulative monotonic (HTTP requests) — `timeAggregation: rate`, `spaceAggregation: sum`
3. Histogram (latency p99) — `timeAggregation: ""`, `spaceAggregation: p99`
4. Formula (error rate %) — two builder_query + builder_formula

### 2026-03-23 — Design Q&A and implementation

**Resolved design questions:**
1. **Formula sub-queries** → Full params per query (name, metricName, metricType, isMonotonic, temporality, timeAggregation, spaceAggregation, groupBy, filter). Handles mixed-type formulas correctly.
2. **requestType** → Support both `time_series` (default) and `scalar`. For scalar, expose `reduceTo` param.
3. **Post-aggregation functions** (timeshift, anomaly, ewma) → Deferred to v2.
4. **Unknown metricType** → Auto-call `client.ListMetrics()` internally to fetch metadata. Report in decisions block.

**Implementation completed:**
- `pkg/metricsrules/rules.go` — ApplyDefaults + ValidateAggregation with all type/aggregation combos
- `pkg/metricsrules/guide.go` — MetricsGuide const with full markdown guide
- `pkg/types/querybuilder.go` — MetricAggregation, MetricsQuerySpec, BuildMetricsQueryPayload, BuildMetricsQueryPayloadJSON
- `internal/handler/tools/metrics_helper.go` — parseMetricsQueryArgs, buildGroupByFields, autoStepInterval
- `internal/handler/tools/metrics_query.go` — handleQueryMetrics with auto-fetch, formula support, decisions block
- `internal/handler/tools/handler.go` — signoz_query_metrics tool + signoz://metrics-aggregation-guide resource
- `manifest.json` + `README.md` updated
- Unit tests pass, `go build ./...` clean, `go vet ./...` clean
