package dashboard

const WidgetExamples = `
=== SIGNOZ DASHBOARD CREATION GUIDELINES ===

CRITICAL: Use NEW Query Builder Format ONLY DO NOT mix old and new query builder formats in the same dashboard.

=== QUERY BUILDER FORMAT ===

--- CORRECT Format (NEW - V5) ---

Use this format for ALL widgets:

queryData:

queryName: A dataSource: traces expression: A stepInterval: 60 aggregations:
expression: count() filter: expression: service.name in $service_name groupBy:
key: service.name dataType: string type: resource
--- INCORRECT Format (OLD - DEPRECATED) ---

DO NOT USE these fields: aggregateOperator: count aggregateAttribute: key: duration_nano dataType: float64

ERROR: If you use aggregateOperator or aggregateAttribute, the dashboard may not work correctly. SOLUTION: Always use aggregations array with expression strings instead.

=== FIELD NAMING CONVENTIONS ===

--- selectColumns (for list panels) ---

CORRECT: selectColumns: - name: service.name # Use 'name' not 'key' fieldContext: resource # REQUIRED fieldDataType: string # REQUIRED signal: traces # REQUIRED - name: duration_nano fieldContext: span signal: traces

INCORRECT: selectColumns: - key: service.name # ERROR: Should be 'name' dataType: string

ERROR: Using 'key' instead of 'name' in selectColumns causes frontend crash. ERROR: Missing fieldContext or signal causes rendering errors. SOLUTION: Always use 'name' field and include fieldContext, signal for selectColumns.

--- groupBy (for pie, table panels) ---

CORRECT: groupBy: - key: llm.model_name # Use 'key' not 'name' dataType: string type: tag

INCORRECT: groupBy: - name: llm.model_name # ERROR: Should be 'key' dataType: string

ERROR: Using 'name' instead of 'key' in groupBy causes query errors. SOLUTION: Always use 'key' field in groupBy.

--- filters.items (structured filters) ---

CORRECT: filters: op: AND items: - key: key: service.name # Use 'key' not 'name' dataType: string op: IN value: $service_name

INCORRECT: filters: items: - key: name: service.name # ERROR: Should be 'key' op: IN value: $service_name

ERROR: Using 'name' in filter items causes filter parsing errors. SOLUTION: Always use 'key' field in filters.items[].key.

=== PANEL TYPE REQUIREMENTS ===

--- graph Panel ---

REQUIRED FIELDS:

aggregations
filter or filters
queryName
dataSource
expression
OPTIONAL FIELDS:

groupBy (for multiple series)
legend (for series labels)
EXAMPLE: panelTypes: graph query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: p95(duration_nano) filter: expression: service.name in $service_name

ERROR: Missing aggregations causes "No data" error. ERROR: Missing filter may show unfiltered data.

--- list Panel ---

REQUIRED FIELDS:

aggregations
selectColumns (CRITICAL!)
orderBy
pageSize
queryName
dataSource
expression
EXAMPLE: panelTypes: list query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() filter: expression: has_error = true orderBy: - columnName: timestamp order: desc pageSize: 10 selectColumns: - name: service.name fieldContext: resource fieldDataType: string signal: traces - name: duration_nano fieldContext: span signal: traces

ERROR: Missing selectColumns causes FRONTEND CRASH when opening dashboard editor. ERROR: selectColumns with 'key' instead of 'name' causes rendering errors. ERROR: Missing fieldContext or signal in selectColumns causes column display errors. ERROR: Missing orderBy causes unpredictable row ordering. SOLUTION: ALWAYS include selectColumns with proper structure for list panels.

--- pie Panel ---

REQUIRED FIELDS:

aggregations
groupBy (CRITICAL!)
legend
queryName
dataSource
expression
EXAMPLE: panelTypes: pie query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() filter: expression: service.name in $service_name groupBy: - key: llm.model_name dataType: string type: tag legend: {{llm.model_name}}

ERROR: Missing groupBy causes "No data" or single slice. ERROR: Missing legend causes unlabeled slices. ERROR: Using 'name' instead of 'key' in groupBy causes query errors. SOLUTION: Always include groupBy with 'key' field and legend with template variables.

--- table Panel ---

REQUIRED FIELDS:

aggregations
groupBy (CRITICAL!)
queryName
dataSource
expression
EXAMPLE: panelTypes: table query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() as 'Requests' avg(duration_nano) as 'Latency' filter: expression: service.name in $service_name groupBy: - key: service.name dataType: string type: resource isColumn: true

ERROR: Missing groupBy causes single row output. ERROR: Missing 'as' aliases in aggregations causes unclear column names. SOLUTION: Always include groupBy and use aliases in aggregation expressions.

--- value Panel ---

REQUIRED FIELDS:

aggregations
queryName
dataSource
expression
EXAMPLE: panelTypes: value query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: sum(llm.token_count.prompt) filter: expression: service.name in $service_name

ERROR: Missing aggregations causes "No data". ERROR: Using groupBy causes multiple values instead of single value. SOLUTION: Do not use groupBy for value panels.

--- row Panel ---

REQUIRED FIELDS:

title
EXAMPLE: panelTypes: row title: Performance Metrics

NOTE: Row panels are just section headers, no query needed.

=== AGGREGATION EXPRESSIONS ===

--- Supported Expressions ---

Count: expression: count()

Percentiles: expression: p50(duration_nano) expression: p95(duration_nano) expression: p99(duration_nano)

Statistical: expression: avg(duration_nano) expression: sum(llm.token_count.prompt) expression: min(duration_nano) expression: max(duration_nano)

With Aliases (for tables): expression: count() as 'Total Requests' expression: avg(duration_nano) as 'Avg Latency'

Multiple in One Query (for tables): expression: count() as 'Requests' avg(duration_nano) as 'Latency'

ERROR: Invalid function names cause query errors. ERROR: Missing parentheses causes syntax errors. ERROR: Typos in field names cause "field not found" errors.

=== FILTER EXPRESSIONS ===

--- String Expression Format (Recommended) ---

Simple: filter: expression: service.name in $service_name

Multiple Conditions: filter: expression: service.name in $service_name AND has_error = true

With Variables: filter: expression: service.name in $service_name telemetry.sdk.language in $language

Existence Check: filter: expression: llm.model_name EXISTS

--- Structured Filter Format (Optional) ---

filters: op: AND items: - key: key: service.name dataType: string op: IN value: $service_name - key: key: has_error op: = value: true

NOTE: You can use BOTH filter.expression AND filters.items together for compatibility.

ERROR: Invalid operators cause filter errors. ERROR: Missing $ prefix for variables causes literal string matching. ERROR: Typo in field names causes no data.

=== FORMULA QUERIES ===

For calculated metrics (like error rate):

builder: queryData: - queryName: A dataSource: traces expression: A disabled: true # Disable base queries aggregations: - expression: count() filter: expression: has_error = 'true' - queryName: B dataSource: traces expression: B disabled: true aggregations: - expression: count() filter: expression: has_error = 'false' queryFormulas: - queryName: F1 expression: A / (A+B)

ERROR: Not disabling base queries shows multiple series. ERROR: Invalid formula syntax causes calculation errors. SOLUTION: Always set disabled: true for base queries when using formulas.

=== COMMON ERRORS AND SOLUTIONS ===

--- Error: Frontend Crash on Dashboard Editor ---

CAUSE: Missing selectColumns in list panel SYMPTOM: Dashboard loads but crashes when clicking "Edit" SOLUTION: Add selectColumns with name, fieldContext, signal fields

INCORRECT: panelTypes: list queryData: - aggregations: [...] # Missing selectColumns

CORRECT: panelTypes: list queryData: - aggregations: [...] selectColumns: - name: service.name fieldContext: resource signal: traces

--- Error: No Data Displayed ---

CAUSE 1: Missing aggregations SOLUTION: Add aggregations array with expression

CAUSE 2: Missing groupBy for pie/table panels SOLUTION: Add groupBy array

CAUSE 3: Invalid filter expression SOLUTION: Check field names and operators

CAUSE 4: Wrong dataSource SOLUTION: Verify dataSource matches signal type (traces/logs/metrics)

--- Error: Wrong Column Names in Table ---

CAUSE: Missing aliases in aggregation expressions SOLUTION: Use 'as' to name columns

INCORRECT: aggregations: - expression: count()

CORRECT: aggregations: - expression: count() as 'Total Requests'

--- Error: Multiple Values in Value Panel ---

CAUSE: Using groupBy in value panel SOLUTION: Remove groupBy for value panels

--- Error: Query Parsing Errors ---

CAUSE 1: Using 'name' instead of 'key' in groupBy SOLUTION: Use 'key' field in groupBy

CAUSE 2: Using 'key' instead of 'name' in selectColumns SOLUTION: Use 'name' field in selectColumns

CAUSE 3: Invalid aggregation function SOLUTION: Use supported functions (count, avg, sum, p50, p95, p99, min, max)

--- Error: Variables Not Working ---

CAUSE: Missing $ prefix SOLUTION: Use $variable_name format

INCORRECT: filter: expression: service.name in service_name

CORRECT: filter: expression: service.name in $service_name

=== VALIDATION CHECKLIST ===

Before creating a dashboard, verify:

[ ] Using aggregations array (NOT aggregateOperator) [ ] Using filter.expression for filters [ ] List panels have selectColumns with name, fieldContext, signal [ ] Pie/table panels have groupBy with key field [ ] Value panels do NOT have groupBy [ ] All variables use $ prefix [ ] All field names are spelled correctly [ ] All aggregation functions are valid [ ] stepInterval is present (can be null) [ ] queryName, dataSource, expression are present

=== COMPLETE WORKING EXAMPLES ===

--- Graph: Latency P95 ---

panelTypes: graph title: Latency (P95) yAxisUnit: ns query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: p95(duration_nano) filter: expression: service.name in $service_name

--- List: Error Traces ---

panelTypes: list title: Error Traces query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() filter: expression: has_error = true orderBy: - columnName: timestamp order: desc pageSize: 10 selectColumns: - name: service.name fieldContext: resource fieldDataType: string signal: traces - name: name fieldContext: span fieldDataType: string signal: traces - name: duration_nano fieldContext: span signal: traces

--- Pie: Service Distribution ---

panelTypes: pie title: Service Distribution query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() groupBy: - key: service.name dataType: string type: resource legend: {{service.name}}

--- Table: Service Metrics ---

panelTypes: table title: Service Metrics query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() as 'Requests' avg(duration_nano) as 'Latency' groupBy: - key: service.name dataType: string type: resource isColumn: true

--- Value: Total Requests ---

panelTypes: value title: Total Requests query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A aggregations: - expression: count() filter: expression: service.name in $service_name

--- Value: Error Rate (with Formula) ---

panelTypes: value title: Error Rate yAxisUnit: percentunit query: queryType: builder builder: queryData: - queryName: A dataSource: traces expression: A disabled: true aggregations: - expression: count() filter: expression: has_error = 'true' - queryName: B dataSource: traces expression: B disabled: true aggregations: - expression: count() filter: expression: has_error = 'false' queryFormulas: - queryName: F1 expression: A / (A+B)


=== WIDGET TYPES ===

--- graph Widgets ---

Example: Token Usage (from Anthropic API)
  bucketCount: 30
  customLegendColors:
    F1: #eccd03
  legendPosition: bottom
  panelTypes: graph
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: sum(llm.token_count.prompt) ) )
          dataSource: traces
          disabled: true
          expression: A
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model
          queryName: A
        -
          aggregations:
            -
              expression: sum(llm.token_count.completion)
          dataSource: traces
          disabled: true
          expression: B
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model
          queryName: B
      queryFormulas:
        -
          expression: A + B
          queryName: F1
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Token Usage
  yAxisUnit: none

Example: Latency (P95) (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: graph
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: p95(duration_nano)
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Latency (P95)
  yAxisUnit: ns

Example: Number of Requests (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: graph
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model llm.provider = 'anthropic'
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Number of Requests
  yAxisUnit: none

--- list Widgets ---

Example: Errors (from Anthropic API)
  bucketCount: 30
  columnWidths:
    date: 145
    duration_nano: 145
    http_method: 145
    name: 145
    response_status_code: 145
    service.name: 145
  legendPosition: bottom
  panelTypes: list
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: has_error = true service.name IN $service_name
          orderBy:
            -
              columnName: timestamp
              order: desc
          pageSize: 10
          queryName: A
          selectColumns:
            -
              fieldContext: resource
              fieldDataType: string
              name: service.name
              signal: traces
            -
              fieldContext: span
              fieldDataType: string
              name: name
              signal: traces
            -
              fieldContext: span
              name: duration_nano
              signal: traces
            -
              fieldContext: span
              name: http_method
              signal: traces
            -
              fieldContext: span
              name: response_status_code
              signal: traces
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Errors
  yAxisUnit: none

Example: Errors (from Autogen)
  bucketCount: 30
  columnWidths:
    date: 145
    duration_nano: 145
    http_method: 145
    name: 145
    response_status_code: 145
    service.name: 145
  legendPosition: bottom
  panelTypes: list
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: has_error = true service.name IN $service_name
          filters:
            items:
              -
                key:
                  dataType: string
                  key: service.name
                op: IN
                value: $service_name
              -
                key:
                  key: has_error
                op: =
                value: true
            op: AND
          orderBy:
            -
              columnName: timestamp
              order: desc
          pageSize: 10
          queryName: A
          selectColumns:
            -
              fieldContext: resource
              fieldDataType: string
              name: service.name
              signal: traces
            -
              fieldContext: span
              fieldDataType: string
              name: name
              signal: traces
            -
              fieldContext: span
              name: duration_nano
              signal: traces
            -
              fieldContext: span
              name: http_method
              signal: traces
            -
              fieldContext: span
              name: response_status_code
              signal: traces
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Errors
  yAxisUnit: none

Example: Logs (from Autogen)
  bucketCount: 30
  columnWidths:
    body: 350
    timestamp: 100
  legendPosition: bottom
  panelTypes: list
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: logs
          expression: A
          filter:
            expression: service.name IN $service_name
          orderBy:
            -
              columnName: timestamp
              order: desc
            -
              columnName: id
              order: desc
          pageSize: 10
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Logs
  yAxisUnit: none

--- pie Widgets ---

Example: Model Distribution (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: pie
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: llm.model_name EXISTS service.name in $service_name AND llm.provider = 'anthropic' AND llm.model_name EXISTS
          groupBy:
            -
              dataType: string
              key: llm.model_name
              type: tag
          legend: {{llm.model_name}}
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Model Distribution
  yAxisUnit: none

Example: Model Distribution (from Autogen)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: pie
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name gen_ai.request.model EXISTS
          groupBy:
            -
              dataType: string
              key: gen_ai.request.model
              type: tag
          legend: {{gen_ai.request.model}}
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Model Distribution
  yAxisUnit: none

Example: Model Distribution (from Azure OpenAI API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: pie
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          expression: A
          filter:
            expression: llm.model_name EXISTS service.name in $service_name AND llm.model_name EXISTS
          groupBy:
            -
              dataType: string
              key: llm.model_name
              type: tag
          legend: {{llm.model_name}}
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Model Distribution
  yAxisUnit: none

--- row Widgets ---

Example: Machine (from Fly.io — Fly App (SigNoz))
  panelTypes: row
  title: Machine

Example: TCP (from Fly.io — Fly App (SigNoz))
  panelTypes: row
  title: TCP

Example: Memory (from Fly.io — Fly App (SigNoz))
  panelTypes: row
  title: Memory

--- table Widgets ---

Example: Services and Languages (from Anthropic API)
  bucketCount: 30
  columnWidths:
    A: 145
    service.name: 145
    telemetry.sdk.language: 145
  legendPosition: bottom
  panelTypes: table
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count() as 'Count of Spans'
          dataSource: traces
          expression: A
          filter:
            expression: llm.provider = 'anthropic' AND telemetry.sdk.language EXISTS
          groupBy:
            -
              dataType: string
              isColumn: true
              key: service.name
              type: resource
            -
              dataType: string
              key: telemetry.sdk.language
              type: resource
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Services and Languages
  yAxisUnit: none

Example: Agents (from Autogen)
  bucketCount: 30
  columnUnits:
    A.avg(duration_nano): ns
  columnWidths:
    A.avg(duration_nano): 145
    A.count(): 145
    gen_ai.agent.name: 145
  legendPosition: bottom
  panelTypes: table
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count() as 'Requests' avg(duration_nano) as 'Latency'
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name gen_ai.agent.name EXISTS gen_ai.operation.name = 'invoke_agent'
          groupBy:
            -
              dataType: string
              key: gen_ai.agent.name
              type: tag
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Agents
  yAxisUnit: none

Example: Tools (from Autogen)
  bucketCount: 30
  columnUnits:
    A.avg(duration_nano): ns
  columnWidths:
    A.avg(duration_nano): 145
    A.count(): 145
    gen_ai.tool.name: 145
  legendPosition: bottom
  panelTypes: table
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count() as 'Requests' avg(duration_nano) as 'Latency'
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name gen_ai.tool.name EXISTS gen_ai.operation.name = 'execute_tool'
          groupBy:
            -
              dataType: string
              key: gen_ai.tool.name
              type: tag
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Tools
  yAxisUnit: none

--- value Widgets ---

Example: Input Tokens (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: value
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: sum(llm.token_count.prompt) )
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Input Tokens
  yAxisUnit: none

Example: Output Tokens (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: value
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: sum(llm.token_count.completion) )
          dataSource: traces
          expression: A
          filter:
            expression: service.name in $service_name telemetry.sdk.language in $language llm.model_name in $llm_model
          queryName: A
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Output Tokens
  yAxisUnit: none

Example: Error Rate (from Anthropic API)
  bucketCount: 30
  legendPosition: bottom
  panelTypes: value
  query:
    builder:
      queryData:
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          disabled: true
          expression: A
          filter:
            expression: has_error = 'true' service.name in $service_name
          queryName: A
        -
          aggregations:
            -
              expression: count()
          dataSource: traces
          disabled: true
          expression: B
          filter:
            expression: has_error = 'false' service.name in $service_name
          queryName: B
      queryFormulas:
        -
          expression: A / (A+B)
          queryName: F1
    queryType: builder
  selectedLogFields:
    -
      fieldContext: log
      name: timestamp
      signal: logs
      type: log
    -
      fieldContext: log
      name: body
      signal: logs
      type: log
  selectedTracesFields:
    -
      fieldContext: resource
      fieldDataType: string
      name: service.name
      signal: traces
    -
      fieldContext: span
      fieldDataType: string
      name: name
      signal: traces
    -
      fieldContext: span
      name: duration_nano
      signal: traces
    -
      fieldContext: span
      name: http_method
      signal: traces
    -
      fieldContext: span
      name: response_status_code
      signal: traces
  softMax: 0
  softMin: 0
  title: Error Rate
  yAxisUnit: percentunit

`
