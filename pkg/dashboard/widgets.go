package dashboard

const WidgetsInstructions = `
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

Selection Guidelines:
- Start with Query Builder for simplicity and maintainability
- Use ClickHouse SQL when you need: CTEs, window functions, complex joins, custom bucketing, advanced rate calculations
- Use PromQL when you need: Prometheus compatibility, metric-specific functions, range vector operations
- Avoid mixing query types within a single dashboard unless necessary for specific requirements

Panel/widgets types in dashboards [CRITICAL]:
1. Bar Chart: categorical comparisons.
2. Histogram: value distribution.
3. List Chart: ranked or enumerated items.
4. Pie Chart: proportional breakdowns.
5. Table: multi-column data inspection.
6. Timeseries: time-indexed metrics.
7. Value: single aggregated metric.

Layout [CRITICAL]:
- Use X, Y, W, and H as fixed grid coordinates and dimensions.
- Keep I as the stable unique identifier for each layout entry.
- Enforce minimum widget size with MinW and MinH to prevent collapse.
- Apply MaxH only when bounding height is necessary.
- Mark widgets static only when they must not move.
- Enable dragging only when intentional to avoid accidental layout shifts.
- Allocate sufficient height for any chart with legends.
- Treat legend space as a mandatory vertical requirement and size H accordingly to keep legends fully visible.

Bar chart panel [CRITICAL]:
Note: This panel is best used when you need to compare discrete categories (e.g. service names, status codes) or track count/metric values over categories in an easy-to-read manner.
- Bar Chart displays frequency or aggregated values for one or more categories over time or across categories.
- It supports data from logs, traces, or metrics.
- You can configure the Y-axis unit, and optionally set "Soft Min/Max" to control vertical scale so small values aren't exaggerated.
- You can add thresholds (value + optional color) to draw reference lines on the Y-axis, useful for highlighting important limits.

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
- Thresholds add horizontal reference lines for limits, SLOs, warning levels, or expected baselines, improving anomaly recognition.

Value panel [CRITICAL]:
- Value Panel reduces a time series to a single representative number, exposing a point-in-time or aggregated metric such as current throughput, average latency, error count, or any computed summary.
- It supports logs, traces, and metrics, as long as the underlying data can be aggregated into one value.
- It surfaces high-salience indicators that benefit from immediate readability, functioning as a KPI-style snapshot.
- Configuration requires selecting the signal type, selecting the metric/log/trace source, and defining the reduction function (avg, sum, max, min, latest) that collapses the series into a single output.
  This panel is best used for top-level KPIs, summary statistics, or health indicators where only the final aggregated value—not the trend—is required.
`
