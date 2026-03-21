# Ensuring Legend Formatting in SigNoz Dashboards

When language models (LLMs) or scripts create or update dashboards in SigNoz via the MCP interface, it is crucial they utilize proper legend formatting to ensure the resulting widgets are fully informative and human-readable.

## Problem Context
By default, pie charts and graphs may fail to label slices properly or leave them blank, causing "No data" or single unlabelled slice issues. In the case of `groupBy`, without a valid `legend` definition, the charts won't have the appropriate labels.

Reference: [SigNoz Query Builder V5 - Legend Formatting](https://signoz.io/docs/userguide/query-builder-v5/#legend-formatting)

## How to Set Legend Format Correctly

The `Legend` field is an essential part of the query structure, typically defined within `PromQL`, `ClickHouseSQL`, or `BuilderQuery` structs inside a `WidgetQuery` in `pkg/types/dashboard.go`.

When setting `legend`, LLMs should correctly use variables matching the attributes or standard tags grouped in the query. For example:

### Common Formats
* **Service Name**: `{{service.name}}`
* **Host Name**: `{{host.name}}`
* **Custom Attributes**: `{{llm.model_name}}`, `{{gen_ai.request.model}}`

### Go Struct Examples

In `BuilderQuery`:
```go
queryData := []BuilderQuery{
{
QueryName:  "A",
DataSource: DataSourceTraces,
Expression: "A",
Aggregations: []Aggregation{
{Expression: "count()"},
},
GroupBy: []AttributeKey{
{Key: "llm.model_name", DataType: "string", Type: "tag"},
},
// The key line for correct Legend formatting
Legend: "{{llm.model_name}}",
},
}
```

## Dashboard Guidelines Enforced

1. **JSON Schema Descriptions**: The underlying `dashboard.go` (and derived MCP payloads) include direct `jsonschema_extras` descriptions for `Legend` fields, explicitly steering clients to use the double curly bracing `{{ ... }}`.
2. **Chart Sizing**: As documented in `basics.go` and `widgets.go`, any chart including a legend requires a `H` (height) ≥ 6 to ensure the legend text is fully visible.
