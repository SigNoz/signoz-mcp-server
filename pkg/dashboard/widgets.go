package dashboard

const WidgetsInstructions = `
Conceptual guidance only: WHICH query type and panel type to choose, and how to think about legends and layout. For the exact JSON shape — the panels map, plugin 'kind's, layouts, and variables — see signoz://dashboard/instructions and the create/update tool's JSON Schema.

Query Type Selection [CRITICAL]:
Choose the appropriate query type based on your data source and requirements:

1. Query Builder (Recommended):
   - Use for: Most common use cases with logs, traces, and metrics
   - Best for: Filtering, aggregation, grouping with UI-driven query construction
   - Advantages: Auto-completion, validation, easier to maintain, supports trace operators
   - Supports: All signal types (logs, traces, metrics)
   - When to use: Default choice unless you need advanced SQL features or PromQL-specific functions

2. ClickHouse SQL:
   - Use for: Advanced queries requiring complex SQL operations
   - Best for: Custom aggregations, window functions, CTEs, joins, histogram quantiles, rate calculations
   - Advantages: Full SQL power, complex transformations, performance optimization
   - Supports: Logs, traces, metrics (via different schemas)
   - When to use: Query Builder limitations, complex calculations, custom time bucketing, advanced filtering
   - Required: Must reference appropriate schema (logs/metrics/traces) and examples

3. PromQL:
   - Use for: Metrics queries with Prometheus-style syntax
   - Best for: Rate calculations, range queries, aggregations over time, histogram quantiles
   - Advantages: Familiar to Prometheus users, concise for time-series operations
   - Supports: Metrics only
   - When to use: Prometheus migration, metric-specific operations, OpenTelemetry metrics with dots in names
   - Required: Must follow new format syntax (wrap metric names in curly braces, quote names with dots)
   - Reference: Read signoz://promql/instructions — covers the Prometheus 3.x UTF-8 quoted-selector form for dotted OTel names, anti-pattern table, dotted labels in by()/on()/ignoring(), and common patterns by metric type.

Selection Guidelines:
- Start with Query Builder for simplicity and maintainability
- Use ClickHouse SQL when you need: CTEs, window functions, complex joins, custom bucketing, advanced rate calculations
- Use PromQL when you need: Prometheus compatibility, metric-specific functions, range vector operations
- Avoid mixing query types within a single dashboard unless necessary for specific requirements

Query Resources [REQUIRED]:
- Query Builder: signoz://dashboard/query-builder-example
- ClickHouse logs: signoz://dashboard/clickhouse-schema-for-logs and signoz://dashboard/clickhouse-logs-example
- ClickHouse metrics: signoz://dashboard/clickhouse-schema-for-metrics and signoz://dashboard/clickhouse-metrics-example
- ClickHouse traces: signoz://dashboard/clickhouse-schema-for-traces and signoz://dashboard/clickhouse-traces-example
- PromQL: signoz://promql/instructions

Field Discovery:
- When a Query Builder field name is not already known, call signoz_get_field_keys with the widget's signal and fieldContext before composing the query; use signoz_get_field_values when observed values help verify the filter.
- Do not invent tenant-specific attributes from an example. Adapt each example to fields present in the target tenant.

One query per panel [CRITICAL]:
A panel holds exactly ONE query. Putting more than one fails backend validation (not caught by the JSON Schema): "panel must have one query". To plot multiple series or compute a formula, nest them inside that single query as one signoz/CompositeQuery — each builder query and each formula an entry inside it. When a panel needs only one query and no formula, prefer setting that query's plugin directly (e.g. signoz/BuilderQuery) over wrapping a lone query in signoz/CompositeQuery — simpler and equivalent; reserve CompositeQuery for combining multiple builder queries and/or formulas.

Legend Formatting [CRITICAL]:
- Query Builder syntax: use {{attribute_name}} placeholders that exactly match groupBy keys.
- ALWAYS set legend when groupBy is used on series-producing charts. Without legend, SigNoz shows raw query identifiers.
- Single-key example: groupBy service.name -> legend {{service.name}}
- Multi-key example: groupBy service.name and http.method -> legend {{service.name}} - {{http.method}}
- Panel rules: timeseries/graph, bar, pie, histogram require legend when the query has groupBy.
- Table panels with groupBy should usually set legend, but the columns are often self-labeling so it is recommended rather than required.
- Value/number and list panels do not need legend formatting.
- PromQL legend syntax uses labels: {{label_name}}
- ClickHouse SQL legend syntax uses result columns: {{column_name}}
- In update flows, preserve existing legends unless you are intentionally improving them.

Panel/widgets types in dashboards [CRITICAL]:
1. Bar Chart: categorical comparisons.
2. Histogram: value distribution.
3. List Chart: ranked or enumerated items.
4. Pie Chart: proportional breakdowns.
5. Table: multi-column data inspection.
6. Timeseries: time-indexed metrics.
7. Value (the "Number" panel): single aggregated metric.

Layout (concepts):
- Panels sit on a column-based grid: each panel has a position (where it starts) and a size (how wide and tall it is).
- Give charts with legends enough height that the legend stays fully visible — treat legend space as a vertical requirement.
- Keep consistent sizing within a row of similar panels, group related panels together, and don't let panels overlap.
- Use full width for tables and wide timeseries; split a row for side-by-side comparisons.
- For the exact layout fields and how a panel links to its grid position, see signoz://dashboard/instructions and this tool's JSON Schema.

Bar chart panel [CRITICAL]:
Note: This panel is best used when you need to compare discrete categories (e.g. service names, status codes) or track count/metric values over categories in an easy-to-read manner.
- Bar Chart displays frequency or aggregated values for one or more categories over time or across categories.
- It supports data from logs, traces, or metrics.
- You can configure the Y-axis unit, and optionally set "Soft Min/Max" to control vertical scale so small values aren't exaggerated.
- You can add thresholds to highlight important limits: SigNoz colors the threshold label or tints the panel background when the condition holds — it does NOT support a Grafana-style line marker.

Histogram panel [CRITICAL]:
Note: This panel is best used to understand distribution patterns, detect skew, and analyze how values cluster across ranges.
- Histogram displays frequency distribution by grouping numeric values into buckets, revealing shape, spread, and skew of the data.
- It supports time-series inputs from logs, traces, or metrics.
- Each bar represents a numeric range rather than a discrete category; bucket count controls bin granularity, and bin width is auto-calculated unless overridden.
- Multiple series can be plotted separately or merged into a single aggregated histogram using the "merge all series into one" option.
- Configuration allows controlling number of buckets, bucket width, and series-merging behavior.

List chart panel [CRITICAL]:
Note: This panel is best used when the goal is to surface unaggregated events—such as errors, warnings, or individual spans—in a compact, navigable format.
- List Chart displays raw values as a scrollable list, ideal for presenting log lines or spans directly in a dashboard panel.
- It supports logs and traces, rendering each entry as an item in a continuous, searchable list.
- The panel provides infinite scrolling and built-in search for rapid inspection.
- No additional configuration options are available.

Pie chart panel [CRITICAL]:
Note: This panel is best used when you need to visualize categorical proportions—such as request distribution across services—in a compact, high-level breakdown.
- Pie Chart displays proportional composition across categories, showing how a whole is divided among a small set of groups.
- It supports time-series inputs from logs, traces, or metrics.
- Each slice represents a category's share of the total, making relative comparison straightforward when category count is low.
- The panel has no configuration options.

Table panel [CRITICAL]:
Note: This panel is best used when detailed numeric inspection, multi-field comparison, or exact value visibility is required.
- Table displays data in a structured, row-and-column format, suitable for inspecting exact values across multiple fields.
- It supports time-series outputs from logs, traces, or metrics.
- Each column represents a field or aggregation, allowing precise comparison not feasible in graphical panels.
- Configuration supports assigning column units to render numeric values in readable formats (e.g., bytes, durations).

Timeseries panel [CRITICAL]:
Note: This panel is best used for any metric whose meaning depends on temporal evolution—throughput, latency, error rate, resource consumption, saturation, or any continuous operational signal.
- Timeseries Chart plots values against time to reveal trends, seasonality, spikes, degradations, and long-term patterns.
- It supports any time-series output derived from logs, traces, or metrics, making it the primary panel for operational and performance timelines.
- It renders each series as a continuous line, enabling comparison across services, endpoints, or resource metrics.
- Fill Gaps converts missing timestamps into zeros, useful when sparse data must be interpreted as absence of activity rather than missing samples.
- Y-axis Unit formats numerical values for readability and domain correctness (bytes, durations, percentages, counts).
- Soft Min/Max constrains the y-axis so small fluctuations aren't visually amplified or drowned out, stabilizing interpretation across charts.
- Thresholds highlight limits, SLOs, warning levels, or expected baselines: SigNoz colors the threshold label or tints the panel background — it does NOT render a horizontal line at the threshold value.

Value panel [CRITICAL]:
- Value Panel reduces a time series to a single representative number, exposing a point-in-time or aggregated metric such as current throughput, average latency, error count, or any computed summary.
- It supports logs, traces, and metrics, as long as the underlying data can be aggregated into one value.
- It surfaces high-salience indicators that benefit from immediate readability, functioning as a KPI-style snapshot.
- Configuration requires selecting the signal type, selecting the metric/log/trace source, and defining the reduction function that collapses the series into a single output.
  This panel is best used for top-level KPIs, summary statistics, or health indicators where only the final aggregated value—not the trend—is required.
`
