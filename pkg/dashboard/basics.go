package dashboard

// Basics is the v6 (Perses-schema) dashboard basics guide served at
// signoz://dashboard/instructions: dashboard display (title/tags/description),
// the grid layout (positions + the content.$ref panel linkage), and variable
// configuration (incl. the rule to always ask which panels a variable applies to).
const Basics = `
Follow these rules and examples; refer to the links below when required

Critical:
For full query patterns and signal-specific instructions, consult the schema resources, widget examples and the panels-and-queries guide (signoz://dashboard/widgets-instructions).

Basics:
Dashboard Title: set spec.display.name to a descriptive name for your dashboard. Do NOT set the top-level "name" when creating a dashboard — it is an immutable machine label the server generates from spec.display.name; however it has to be set when updating a dashboard with the full payload. schemaVersion must be "v6". 
Tags: key/value descriptors (max 10) that encode a dashboard's domain, environment, or ownership so they can be filtered, grouped, and retrieved without ambiguity.
Description: set spec.display.description as a precise summary of the dashboard's intent, scope, and expected use so a reader can infer what questions it answers and when to rely on it.

Layout [Critical]:
- Position panels with a Grid layout in spec.layouts[]: a 12-column grid where x and y define position, width and height define dimensions in grid units.
- Set x as the horizontal starting position (0-11, where 0 is leftmost).
- Set y as the vertical starting position (0+, increments as rows stack downward).
- Set width in grid units (1-12, where 12 spans full width).
- Set height in grid units (minimum 2, typical range 2-10 depending on panel type).
- Link each grid item to its panel with content.$ref = "#/spec/panels/<panelId>". Panels live in the spec.panels MAP keyed by id; the grid item references that id (it is not repeated as a separate layout id).
- Ensure no panels overlap by verifying that no two grid items occupy the same grid coordinates.
- Allocate sufficient height for any chart with legends; treat legend space as a mandatory vertical requirement and size height accordingly to keep legends fully visible.
- Use consistent row heights within the same visual tier (e.g., all KPI panels in a row should have identical height values).
- Plan y-coordinate progression carefully; each new row's y must start at or after the previous row's y + height to avoid overlap.
- For full-width panels (tables, wide timeseries), set width=12 and x=0.
- For side-by-side panels, ensure x1 + width1 = x2 and width1 + width2 ≤ 12 (e.g., two panels: x=0, width=6 and x=6, width=6).
- For three-column layouts, use width=4 for each panel (x=0, x=4, x=8).
- For four-column layouts, use width=3 for each panel (x=0, x=3, x=6, x=9).

Recommended Heights by Panel Type:
- Value/Number panels (single metric): height 2 to 3
- Timeseries with legend: height 6 to 8
- Bar charts with legend: height 5 to 7
- Pie charts: height 5 to 6
- Histograms: height 5 to 7
- Tables (depends on expected row count): height 6 to 10
- List panels: height 8 to 12

Common Layout Patterns:
- KPI Row (3-4 value panels): y=0, height=3, width=4 or width=3 each, spanning full width
- Timeseries Row (2 charts): y=3, height=7, width=6 each, side-by-side
- Single Full-Width Chart: y=10, height=7, width=12
- Table Row: y=17, height=8, width=12
- Mixed Row (1 chart + 1 table): y=25, chart width=6 height=8, table width=6 height=8

Layout Validation Checklist:
1. Every grid item's content.$ref points to an existing panel id in spec.panels
2. Every panel in spec.panels has exactly one grid item
3. No two panels share the same grid space (check x, y, width, height for overlaps)
4. All width values are between 1 and 12
5. Sum of width values in any horizontal row does not exceed 12
6. Charts with legends have height ≥ 6
7. y coordinates progress logically without gaps or overlaps
8. Full-width panels have x=0 and width=12

Variables [Critical]:
- Use variables to parameterize dashboards and eliminate hard-coded query values, enabling template reuse and context-driven panel updates.
- Set the variable's name (how queries reference it as $name) and a clear display.name (its label).
- Write the display description as a terse statement of what the variable controls.

Default to DYNAMIC Variables:
- ALWAYS prefer a ListVariable whose plugin.kind is signoz/DynamicVariable. It auto-derives dropdown values from an OpenTelemetry resource/tag attribute, requires no manual SQL, and stays in sync with backend state automatically.
- Set the plugin spec's signal to one of: traces, logs, metrics.
- Set the plugin spec's name to the attribute key (e.g., "service.name", "deployment.environment", "k8s.namespace.name").
- When you know the attribute (e.g., the user says "filter by service" → use service.name, "filter by environment" → use deployment.environment), create the DynamicVariable directly without extra API calls.
- When the right attribute is NOT obvious, call signoz_get_field_keys with the relevant signal and fieldContext=resource to discover available resource attributes, then pick the best match. Optionally call signoz_get_field_values to preview values and confirm the attribute is populated. Use the discovered attribute as the plugin spec's name.

Other Variable Types (use only when DYNAMIC is not suitable):
- CustomVariable (plugin.kind signoz/CustomVariable): for short, fixed enumerations via a comma-separated customValue. Use when the set of values is static and known upfront (e.g., "production,staging,development").
- TextVariable (kind TextVariable): for free-form input with an optional default value. Use when the user needs to type arbitrary values.
- QueryVariable (plugin.kind signoz/QueryVariable): backed by ClickHouse SQL (queryValue). Avoid due to performance and maintenance overhead; prefer DynamicVariable instead.

Variable Configuration:
- Use allowMultiple, allowAllValue, and defaultValue to fix selection semantics explicitly.
- Use sort to enforce predictable value ordering.
- Variable order follows the position of entries in the spec.variables[] array.
- Interpret ALL in dynamic variables as no filter, not select everything.
- Reference variables using $var syntax across Query Builder, ClickHouse SQL, and PromQL; use IN for multi-select inputs.
- Chain variables by constraining one variable using another, but rely on dynamic variables for automatic dependency handling.

Applying Variables to Panels [Critical — MUST prompt the user]:
- Before wiring a new variable into panel queries, ALWAYS ask the user which panels it should apply to. Never silently inject a variable reference into every panel.
- Present the current panels as a list (id + title) and offer two choices: (a) apply to all panels, or (b) pick a subset. Wait for the user's selection before generating any queries that reference the variable.
- Only inject $<var_name> into the filter expressions / queries of panels the user selected; leave the rest untouched.
- Rationale: variables often apply to a subset of panels (e.g., a service.name filter belongs on RED panels but not on a global latency heatmap). Silent auto-injection produces dashboards that look valid syntactically but over-filter unrelated panels.
- This applies to both signoz_create_dashboard (introducing a variable at creation time) and signoz_update_dashboard (adding a variable to an existing dashboard).

Links:
- Dashboard: https://signoz.io/docs/userguide/manage-dashboards/
- Variables: https://signoz.io/docs/userguide/manage-variables/
`
