package dashboard

const WidgetsInstructions = `
Conceptual guidance only: WHICH query type and panel type to choose, and how to think about legends and layout. For the exact JSON shape (the panels map, plugin 'kind's, layouts, and variables), see signoz://dashboard/instructions and the create/update tool's JSON Schema.

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
   - Reference: Read signoz://promql/instructions, which covers the Prometheus 3.x UTF-8 quoted-selector form for dotted OTel names, anti-pattern table, dotted labels in by()/on()/ignoring(), and common patterns by metric type.

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

Query count per panel [CRITICAL]:
- Query-backed panels hold exactly ONE query. Putting more than one fails backend validation. To plot multiple series, or to add a formula or trace operator, nest them with the queries they read inside that single query as one signoz/CompositeQuery.
- signoz/TextPanel is queryless and must send a non-null empty array: "queries": []. Do not invent a query or dry-run one for a TextPanel.
- Every panel must include queries. null is rejected, including for TextPanel.

Dry-run a panel query [CRITICAL]:
- Before saving a query panel, validate its one query with signoz_execute_builder_query. A saved query spec is already a Query Builder v5 execution spec, so translate only the envelope and keep the spec unchanged.
- Envelope per query plugin: signoz/BuilderQuery -> builder_query, signoz/AIBuilderQuery -> builder_ai_query, signoz/Formula -> builder_formula, signoz/TraceOperator -> builder_trace_operator, signoz/PromQLQuery -> promql, signoz/ClickHouseSQL -> clickhouse_sql. Each becomes {"type": <envelope>, "spec": <saved spec>}.
- signoz/CompositeQuery: its entries become sibling envelopes in compositeQuery.queries, in order, keeping disabled inputs and every formula.
- PromQL and ClickHouse specs hold name and query. PromQL also accepts disabled, step, stats, and legend; ClickHouse accepts disabled and legend. Example: {"type": "promql", "spec": {"name": "A", "query": "sum(rate({\"http.server.request.duration.count\"}[5m]))"}}.
- Tool arguments: {"query": {"schemaVersion": "v1", "start": <ms>, "end": <ms>, "requestType": <panel query kind>, "compositeQuery": {"queries": [...]}}}. start and end are absolute Unix milliseconds over a short recent window (usually the last 30 to 60 minutes), not the panel's display range. Omitting them fails with "missing start or end timestamp".
- requestType is always the panel query's kind, set explicitly (ClickHouse has no default): time_series for timeseries, area, bar, and histogram panels; scalar for table, pie, value, top list, scatter plot, and geo map panels; raw for list panels; heatmap for heatmap panels; trace only for an existing trace panel (author trace lists as signoz/ListPanel with raw). Never use aggregate, table, or timeseries.
- A dry-run validates execution for its window only. A PromQL range selector looks back from each evaluation point, so a long selector such as [12h] stays costly even over a short window.
- The executor does not expand dashboard variables. In the dry-run copy only, replace each $variable with one representative discovered value; keep $variable in the saved panel.
- Keep limit and order identical in the saved panel and the dry-run.
- An empty result does not prove telemetry is absent: widen the window to suit the signal's cadence and report each window tried.
- On a timeout, do not resend the identical request. Shrink the window, coarsen stepInterval (Builder) or step (PromQL), or reduce query cost first.
- Save the Perses panel, never the execution envelope. If the executor cannot represent a saved field, report the gap and leave the field in place; do not remove it to make the dry-run pass. Save such an unvalidated panel only after the user explicitly accepts it.

Legend Formatting [CRITICAL]:
- Query Builder syntax: use {{attribute_name}} placeholders that exactly match groupBy keys.
- ALWAYS set legend when groupBy is used on series-producing charts. Without legend, SigNoz shows raw query identifiers.
- Single-key example: groupBy service.name -> legend {{service.name}}
- Multi-key example: groupBy service.name and http.method -> legend {{service.name}} - {{http.method}}
- Panel rules: timeseries/graph, area, bar, pie, histogram require legend when the query has groupBy.
- Table panels with groupBy should usually set legend, but the columns are often self-labeling so it is recommended rather than required.
- Value/number and list panels do not need legend formatting.
- Top List labels each row with its group-by values when legend is unset; a legend template such as {{service.name}} replaces that label.
- Scatter Plot and Geo Map use the legend as a plain-text name for the query's value, such as "p95 latency". Do not put {{group-by}} placeholders there.
- PromQL legend syntax uses labels: {{label_name}}
- ClickHouse SQL legend syntax uses result columns: {{column_name}}
- In update flows, preserve existing legends unless you are intentionally improving them.

Panel/widgets types in dashboards [CRITICAL]:
1. Area Chart: time-indexed volume, or how parts add up to a total over time.
2. Bar Chart: categorical comparisons.
3. Histogram: value distribution.
4. List Chart: ranked or enumerated items.
5. Pie Chart: proportional breakdowns.
6. Table: multi-column data inspection.
7. Timeseries: time-indexed metrics.
8. Value (the "Number" panel): single aggregated metric.
9. Text: static Markdown notes, runbooks, links, and section context. Use plugin kind signoz/TextPanel with mode "markdown", text, presentation, headerOptions, and queries: [].
10. Heatmap: value distribution over time.
11. Top List: groups ranked by one value.
12. Scatter Plot: two values compared across groups.
13. Geo Map: values by location.

Layout (concepts):
- Panels sit on a column-based grid: each panel has a position (where it starts) and a size (how wide and tall it is).
- Give charts with legends enough height that the legend stays fully visible; treat legend space as a vertical requirement.
- Keep consistent sizing within a row of similar panels, group related panels together, and don't let panels overlap.
- Use full width for tables and wide timeseries; split a row for side-by-side comparisons.
- For the exact layout fields and how a panel links to its grid position, see signoz://dashboard/instructions and this tool's JSON Schema.

Area chart panel [CRITICAL]:
Best for: volume over time (requests, bytes, log lines) and how grouped series add up to a total, such as traffic split by service.
- Area Chart plots values against time like Timeseries and fills the area under each series. Use plugin kind signoz/AreaChartPanel with a time_series query.
- It supports logs, traces, and metrics through Query Builder, ClickHouse SQL, or PromQL queries.
- visualization.stack controls stacking: "none" (default) overlaps series, "normal" stacks them so the top edge is the total, and "percent" stacks each timestamp to 100% to show share.
- chartAppearance.fillMode is "solid" (default) or "gradient"; chartAppearance.fillOpacity (0 to 1) sets fill transparency.
- Prefer Timeseries for latency, percentiles, and ratios: summing them in a stack produces a meaningless total.
- Formatting, Soft Min/Max, legend, and thresholds behave as they do for Timeseries.

Bar chart panel [CRITICAL]:
Best for: comparing discrete categories (e.g. service names, status codes), or tracking count/metric values across categories.
- Bar Chart displays frequency or aggregated values for one or more categories over time or across categories.
- It supports data from logs, traces, or metrics.
- You can configure the Y-axis unit, and optionally set "Soft Min/Max" to control vertical scale so small values aren't exaggerated.
- You can add thresholds to highlight important limits. SigNoz colors the threshold label or tints the panel background when the condition holds. It does NOT support a Grafana-style line marker.

Geo Map panel [CRITICAL]:
Best for: values that differ by place, such as request count or error rate by country, region, or coordinates.
- Use plugin kind signoz/GeoMapPanel with a scalar query: signoz/BuilderQuery, signoz/ClickHouseSQL, or signoz/CompositeQuery. It rejects PromQL, signoz/AIBuilderQuery, and trace operators.
- Group by the location keys. Each result row is one place, and a row without a usable location is not drawn. Discover the location keys with signoz_get_field_keys; common ones are geo.country.iso_code and geo.region.iso_code, and coordinates need a latitude key and a longitude key.
- location.mode is auto (default, detects the mode from the group-by keys), country, subdivision, or coordinates. location.country, location.region, location.lat, and location.lon name location columns: group-by keys of a Query Builder query, or result column names of a signoz/ClickHouseSQL query. SigNoz rejects a Query Builder panel that names a key outside its groupBy.
- map.layer is regions (default, fills each shape), bubbles (a circle per region), or points (a circle per coordinate). map.view is world (default), fit, a continent (africa, asia, europe, north_america, oceania, south_america), or country with map.viewCountry set to an uppercase ISO 3166-1 alpha-2 code.
- Subdivision mode draws states or provinces only when map.view is country and map.viewCountry names a country SigNoz has subdivision shapes for.
- map.colorBy and map.sizeBy name value columns. For Query Builder queries, a value column is an enabled query's name (such as A), or queryName.expression (such as A.count()) for a query with several aggregations. For signoz/ClickHouseSQL, it is the SQL column alias. Empty colorBy uses the first value column.
- Omit order and use no limit, or one large enough to keep every place: a limit drops locations. A dry-run of a Query Builder query that no formula references and that has no limit applies and reports a 100-row limit, while the panel draws up to 10,000 places, so the dry-run checks the query, not location coverage.
- Every query in the panel must group by the same keys, because SigNoz joins their results into one row per place.

Heatmap panel [CRITICAL]:
Best for: how a value's distribution shifts over time, such as request latency per interval from a histogram metric.
- Use plugin kind signoz/HeatmapPanel with a query of kind heatmap: signoz/BuilderQuery, signoz/PromQLQuery, signoz/ClickHouseSQL, or signoz/CompositeQuery. It rejects signoz/AIBuilderQuery and trace operators.
- The panel draws one enabled query. In a signoz/CompositeQuery, disable every other query; a formula can stay enabled while the queries it reads are disabled. SigNoz rejects functions and having on heatmap queries.
- Build a signoz/BuilderQuery on the metrics signal; the dashboard editor offers only metrics for this panel.
- A histogram metric draws its own le buckets, so SigNoz rejects bucketOptions on it; the UI saves spaceAggregation count for it. A gauge, sum, or summary metric takes optional bucketOptions, linear with a required maxValue and optional numBuckets, or log with an optional scale; without them SigNoz uses a log-scale axis. Exponential histograms are not supported.
- A signoz/PromQLQuery must keep the le label, such as sum by (le) (rate(<histogram>_bucket[5m])). A signoz/ClickHouseSQL query returns one row per cell: a timestamp, the bucket bounds as __bucket_min and __bucket_max, and the count in a column named value.
- axes.y.scale spaces the bucket bounds: auto (default), linear, log, or symlog.

Histogram panel [CRITICAL]:
Best for: distribution patterns, skew, and how values cluster across ranges.
- Histogram groups numeric values into buckets and displays the frequency distribution, so you can see shape, spread, and skew.
- It supports time-series inputs from logs, traces, or metrics.
- Each bar represents a numeric range rather than a discrete category; bucket count controls bin granularity, and bin width is auto-calculated unless overridden.
- Multiple series can be plotted separately or merged into a single aggregated histogram using the "merge all series into one" option.
- Configuration allows controlling number of buckets, bucket width, and series-merging behavior.

List chart panel [CRITICAL]:
Best for: unaggregated events such as errors, warnings, or individual spans.
- List Chart displays raw values as a scrollable list, ideal for presenting log lines or spans directly in a dashboard panel.
- It supports logs and traces, rendering each entry as an item in a continuous, searchable list.
- The panel provides infinite scrolling and built-in search for rapid inspection.
- No additional configuration options are available.

Pie chart panel [CRITICAL]:
Best for: categorical proportions, such as request distribution across services.
- Pie Chart displays proportional composition across categories, showing how a whole is divided among a small set of groups.
- It supports time-series inputs from logs, traces, or metrics.
- Each slice represents a category's share of the total. Relative comparison is easiest when the category count is low.
- The panel has no configuration options.

Scatter Plot panel [CRITICAL]:
Best for: comparing two values per group to find outliers or correlation, such as request rate against p95 latency per service.
- Use plugin kind signoz/ScatterPlotPanel with a scalar query: signoz/BuilderQuery, signoz/ClickHouseSQL, or signoz/CompositeQuery. It rejects PromQL, signoz/AIBuilderQuery, and trace operators.
- Each dot is one group. Put the x and y values in separate aggregations or queries.
- dimensions.x, dimensions.y, and dimensions.sizeBy name value columns, as map.colorBy does for Geo Map: an enabled query's name or queryName.expression for Query Builder queries, the SQL column alias for signoz/ClickHouseSQL. dimensions.colorBy lists group-by keys whose values color the dots.
- SigNoz rejects a Query Builder panel whose dimensions name a column or key its query does not produce. It does not check names for signoz/ClickHouseSQL.
- Every query in the panel must group by the same keys, because SigNoz joins their results row for row.
- axes.x and axes.y set the label, softMin, softMax, and scale (auto, linear, log, or symlog) of each axis. Thresholds draw reference lines across the x or y axis.

Table panel [CRITICAL]:
Best for: detailed numeric inspection, multi-field comparison, and exact values.
- Table displays data in a structured, row-and-column format, suitable for inspecting exact values across multiple fields.
- It supports time-series outputs from logs, traces, or metrics.
- Each column represents a field or aggregation, so you can compare exact values that graphical panels cannot show.
- Configuration supports assigning column units to render numeric values in readable formats (e.g., bytes, durations).

Timeseries panel [CRITICAL]:
Best for: any metric whose meaning depends on change over time, such as throughput, latency, error rate, resource consumption, or saturation.
- Timeseries Chart plots values against time to reveal trends, seasonality, spikes, degradations, and long-term patterns.
- It supports any time-series output derived from logs, traces, or metrics, and is the primary panel for operational and performance timelines.
- It renders each series as a continuous line, so you can compare services, endpoints, or resource metrics.
- Fill Gaps converts missing timestamps into zeros, useful when sparse data must be interpreted as absence of activity rather than missing samples.
- Y-axis Unit formats numerical values for readability and domain correctness (bytes, durations, percentages, counts).
- Soft Min/Max constrains the y-axis so small fluctuations aren't visually amplified or drowned out.
- Thresholds highlight limits, SLOs, warning levels, or expected baselines. SigNoz colors the threshold label or tints the panel background. It does NOT render a horizontal line at the threshold value.

Top List panel [CRITICAL]:
Best for: ranking groups by one value, such as the top services by request count or the slowest endpoints by p95 latency.
- Use plugin kind signoz/TopListPanel with a scalar query: signoz/BuilderQuery, signoz/ClickHouseSQL, or signoz/CompositeQuery. It rejects PromQL and signoz/AIBuilderQuery.
- Group by the keys that name each row. The panel ranks the first value column of the first enabled query or formula; it ignores other aggregations.
- Order by that value desc for the top N or asc for the bottom N, and set limit to the row count (the UI defaults to 10). SigNoz picks the N rows on the server.
- appearance.showRank numbers the rows, appearance.showShare shows each row's share of the listed total, and appearance.barColor is a hex color for every bar. Do not use showShare for avg, min, max, or percentile values, because they do not add up to a total.
- Thresholds recolor a row's text or background when its value crosses them.

Value panel [CRITICAL]:
Best for: top-level KPIs, summary statistics, or health indicators where only the final aggregated value matters, not the trend.
- Value Panel reduces a time series to a single number, such as current throughput, average latency, or error count.
- It supports logs, traces, and metrics, as long as the underlying data can be aggregated into one value.
- Configuration requires selecting the signal type, selecting the metric/log/trace source, and defining the reduction function that collapses the series into a single output.
`
