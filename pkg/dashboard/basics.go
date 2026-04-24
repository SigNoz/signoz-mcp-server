package dashboard

const Basics = `
Follow these rules and examples; refer to the links below when required

Critical:
For full query patterns and signal-specific instructions, consult the schema resources, widget examples and dashboard-instruction resource.

Basics:
Dashboard Title: A descriptive name for your new dashboard
Tags: Create tags as short, purposeful descriptors that encode a dashboard's domain, environment, or ownership so they can be filtered, grouped, and retrieved without ambiguity.
Description: Write the description as a precise summary of the dashboard's intent, scope, and expected use so a reader can infer what questions it answers and when to rely on it.

Layout [Critical]:
- Use a 12-column grid system where X and Y define position, W and H define dimensions in grid units.
- Set X as the horizontal starting position (0-11, where 0 is leftmost).
- Set Y as the vertical starting position (0+, increments as rows stack downward).
- Set W as width in grid units (1-12, where 12 spans full width).
- Set H as height in grid units (minimum 2, typical range 2-10 depending on panel type).
- Keep I as the stable unique identifier for each layout entry; it must exactly match the corresponding widget ID.
- Enforce minimum widget size with MinW and MinH to prevent collapse; recommended MinW=2, MinH=2 for value panels, MinW=4, MinH=5 for charts.
- Apply MaxH only when bounding height is necessary to prevent excessive vertical expansion.
- Mark widgets static=true only when they must not move or resize; default to false for flexibility.
- Enable isDraggable=true only when intentional repositioning is allowed; default to false to avoid accidental layout shifts.
- Ensure no widgets overlap by verifying that no two layout items occupy the same grid coordinates.
- Allocate sufficient height for any chart with legends; treat legend space as a mandatory vertical requirement and size H accordingly to keep legends fully visible.
- Use consistent row heights within the same visual tier (e.g., all KPI widgets in a row should have identical H values).
- Plan Y-coordinate progression carefully; each new row's Y must start at or after the previous row's Y + H to avoid overlap.
- For full-width widgets (tables, wide timeseries), set W=12 and X=0.
- For side-by-side widgets, ensure X1 + W1 = X2 and W1 + W2 ≤ 12 (e.g., two widgets: X=0, W=6 and X=6, W=6).
- For three-column layouts, use W=4 for each widget (X=0, X=4, X=8).
- For four-column layouts, use W=3 for each widget (X=0, X=3, X=6, X=9).

Recommended Heights by Panel Type:
- Value panels (single metric): H=2 to H=3
- Timeseries with legend: H=6 to H=8
- Bar charts with legend: H=5 to H=7
- Pie charts: H=5 to H=6
- Histograms: H=5 to H=7
- Tables (depends on expected row count): H=6 to H=10
- List panels: H=8 to H=12

Common Layout Patterns:
- KPI Row (3-4 value widgets): Y=0, H=3, W=4 or W=3 each, spanning full width
- Timeseries Row (2 charts): Y=3, H=7, W=6 each, side-by-side
- Single Full-Width Chart: Y=10, H=7, W=12
- Table Row: Y=17, H=8, W=12
- Mixed Row (1 chart + 1 table): Y=25, chart W=6 H=8, table W=6 H=8

Layout Validation Checklist:
1. Every layout item I matches exactly one widget id
2. No two widgets share the same grid space (check X, Y, W, H for overlaps)
3. All W values are between 1 and 12
4. Sum of W values in any horizontal row does not exceed 12
5. All widgets have MinW and MinH set to prevent collapse
6. Charts with legends have H ≥ 6
7. Y coordinates progress logically without gaps or overlaps
8. Full-width widgets have X=0 and W=12

Variables [Critical]:
- Use variables to parameterize dashboards and eliminate hard-coded query values, enabling template reuse and context-driven panel updates.
- Keep Name stable and set Key identical to it for consistent reference.
- Write Description as a terse statement of what the variable controls.

Default to DYNAMIC Variables:
- ALWAYS prefer DYNAMIC type variables. They auto-derive dropdown values from OpenTelemetry resource/tag attributes using DynamicVariablesAttribute and DynamicVariablesSource, require no manual SQL, and stay in sync with backend state automatically.
- Set DynamicVariablesSource to one of: "Traces", "Logs", "Metrics", or "All telemetry" (matches the values written by the SigNoz UI dropdown). Note: GET responses may return these in any casing (e.g. "metrics") and older SigNoz versions used the legacy alias "all sources" in place of "All telemetry"; the server normalizes case and aliases on write so echoing a GET payload back is safe.
- Set DynamicVariablesAttribute to the attribute key (e.g., "service.name", "deployment.environment", "k8s.namespace.name").
- When you know the attribute (e.g., the user says "filter by service" → use service.name, "filter by environment" → use deployment.environment), create the DYNAMIC variable directly without extra API calls.
- When the right attribute is NOT obvious, call signoz_get_field_keys with the relevant signal and fieldContext=resource to discover available resource attributes, then pick the best match. Optionally call signoz_get_field_values to preview values and confirm the attribute is populated. Use the discovered attribute as DynamicVariablesAttribute.

Other Variable Types (use only when DYNAMIC is not suitable):
- CUSTOM: for short, fixed enumerations (comma-separated values). Use when the set of values is static and known upfront (e.g., "production,staging,development").
- TEXTBOX: for free-form input with optional defaults. Use when the user needs to type arbitrary values.
- QUERY: backed by ClickHouse SQL. Avoid due to performance and maintenance overhead; prefer DYNAMIC instead.

Variable Configuration:
- Use MultiSelect, ShowALLOption, AllSelected, and DefaultValue to fix selection semantics explicitly.
- Use Sort only to enforce predictable value ordering.
- Use Order to control strict UI placement.
- Store overrides only when needed using CustomValue or TextboxValue.
- Treat SelectedValue and HaveCustomValuesSelected as runtime-only, not design-time configuration.
- Interpret ALL in dynamic variables as no filter, not select everything.
- Reference variables using $var syntax across Query Builder, ClickHouse SQL, and PromQL; use IN for multi-select inputs.
- Chain variables by constraining one variable using another, but rely on dynamic variables for automatic dependency handling.

Applying Variables to Panels [Critical — MUST prompt the user]:
- Before wiring a new variable into panel queries, ALWAYS ask the user which panels it should apply to. Never silently inject a variable reference into every panel.
- Present the current panels as a list (id + title, grouped by row if rows exist) and offer two choices: (a) apply to all panels, or (b) pick a subset. Wait for the user's selection before generating any queries that reference the variable.
- Only inject $<var_name> into the filter expressions / queries of panels the user selected; leave the rest untouched.
- Rationale: variables often apply to a subset of panels (e.g., a service.name filter belongs on RED panels but not on a global latency heatmap). Silent auto-injection produces dashboards that look valid syntactically but over-filter unrelated panels.
- This applies to both signoz_create_dashboard (introducing a variable at creation time) and signoz_update_dashboard (adding a variable to an existing dashboard).

Links:
- Dashboard: https://signoz.io/docs/userguide/manage-dashboards/
- Variables: https://signoz.io/docs/userguide/manage-variables/
`
