package querybuilder

const TracesQueryBuilderGuide = `
=== SIGNOZ QUERY BUILDER V5 — TRACES GUIDE ===

== FILTER EXPRESSION FORMAT ==

Filters are a STRING expression in filter.expression — NOT a structured {op, items} object.

CORRECT:   "filter": {"expression": "has_error = true AND k8s.namespace.name = 'my-ns'"}
INCORRECT: "filter": {"op": "AND", "items": [...]}

Operators: =  !=  >  >=  <  <=  IN  NOT IN  LIKE  NOT LIKE  ILIKE  NOT ILIKE  CONTAINS  NOT CONTAINS  REGEXP  NOT REGEXP  BETWEEN  NOT BETWEEN  EXISTS  NOT EXISTS
Combine:   AND  OR  (use parentheses for precedence)

Examples:
  has_error = true
  duration_nano > 500000000
  status_code_string = 'Error'
  name LIKE '%payment%'
  has_error = true AND k8s.namespace.name = 'prod'
  (has_error = true OR duration_nano > 1000000000) AND service.name = 'checkout'

== FIELD NAMES — THREE CATEGORIES ==

--- 1. Built-in span columns (snake_case, fieldContext: "span") ---

Use these directly by name in filter expressions. In selectFields, use fieldContext "span"
to make the built-in/span-column intent explicit:

  has_error            bool      — whether the span has an error
  status_message       string    — OTel status message
  status_code_string   string    — span status: 'Ok', 'Error', or 'Unset' (prefer has_error = true for errors)
  status_code          number    — numeric OTel status code
  duration_nano        number    — span duration in nanoseconds
  trace_id             string    — trace identifier
  span_id              string    — span identifier
  parent_span_id       string    — parent span identifier
  name                 string    — span/operation name
  timestamp            number    — span start timestamp
  http_method          string    — HTTP method
  http_url             string    — HTTP URL
  kind_string          string    — 'Server', 'Client', 'Internal', 'Producer', 'Consumer'
  response_status_code string    — HTTP response status as string; do not use for numeric comparisons

For numeric HTTP status comparisons, use a numeric span/tag attribute such as
attribute.http.response.status_code after verifying it exists for your data.

selectFields entry:
  {"name": "has_error", "fieldDataType": "bool", "signal": "traces", "fieldContext": "span"}
  {"name": "duration_nano", "fieldDataType": "number", "signal": "traces", "fieldContext": "span"}
  {"name": "name", "fieldDataType": "string", "signal": "traces", "fieldContext": "span"}

--- 2. Resource attributes (dot notation, fieldContext: "resource") ---

OTel resource attributes set by the SDK/collector on the originating entity:

  service.name              string — service name
  k8s.namespace.name        string — Kubernetes namespace
  k8s.pod.name              string — Kubernetes pod name
  k8s.node.name             string — Kubernetes node name
  k8s.cluster.name          string — Kubernetes cluster name
  signoz.deployment.tier    string — custom deployment tier tag
  host.name                 string — host name

selectFields entry (fieldContext: "resource" required):
  {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"}
  {"name": "k8s.namespace.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"}

--- 3. Span/tag attributes (dot notation, fieldContext: "tag") ---

OTel span attributes set per-span:

  http.response.status_code number — HTTP status code attribute
  db.system          string — database system (e.g. "postgresql")
  db.operation       string — database operation
  rpc.method         string — RPC method name
  messaging.system   string — messaging system (e.g. "kafka")

selectFields entry (fieldContext: "tag" required):
  {"name": "http.response.status_code", "fieldDataType": "number", "signal": "traces", "fieldContext": "tag"}

== RESULT BOUNDS AND ORDERING ==

Every builder_query must include a positive limit and explicit order.

  raw / trace: limit 100; order by timestamp desc
  scalar:      limit 100 groups; order by the primary aggregation desc unless the task needs another order
  time_series: limit 100 groups; order by the primary aggregation desc unless the task needs another order

For time_series queries with groupBy, the limit selects top groups using the ordering across the ENTIRE
time range, not each time bucket. A short-lived spike can fall outside the selected groups. Use an explicit
smaller positive limit only when the user asks for top N; use a larger positive override when completeness
matters more than response size.

== COMPLETE WORKING EXAMPLES ==

--- Example 1: Raw error spans (requestType: "raw") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "name": "A",
          "signal": "traces",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
          "having": {"expression": ""},
          "filter": {"expression": "has_error = true AND k8s.namespace.name = 'prod'"},
          "selectFields": [
            {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
            {"name": "name",          "fieldDataType": "string", "signal": "traces", "fieldContext": "span"},
            {"name": "has_error",     "fieldDataType": "bool",   "signal": "traces", "fieldContext": "span"},
            {"name": "duration_nano", "fieldDataType": "number", "signal": "traces", "fieldContext": "span"},
            {"name": "status_message","fieldDataType": "string", "signal": "traces", "fieldContext": "span"},
            {"name": "trace_id",      "fieldDataType": "string", "signal": "traces", "fieldContext": "span"},
            {"name": "span_id",       "fieldDataType": "string", "signal": "traces", "fieldContext": "span"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 2: Aggregation query — error count grouped by service (requestType: "scalar") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "scalar",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "name": "A",
          "signal": "traces",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "having": {"expression": ""},
          "filter": {"expression": "has_error = true"},
          "aggregations": [
            {"expression": "count()"}
          ],
          "groupBy": [
            {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"}
          ],
          "order": [{"key": {"name": "count()"}, "direction": "desc"}]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 3: Time series — P99 latency over time (requestType: "time_series") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "name": "A",
          "signal": "traces",
          "disabled": false,
          "stepInterval": 60,
          "limit": 100,
          "offset": 0,
          "order": [{"key": {"name": "p99(duration_nano)"}, "direction": "desc"}],
          "having": {"expression": ""},
          "filter": {"expression": "service.name = 'checkout'"},
          "aggregations": [
            {"expression": "p99(duration_nano)"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

== TIMESTAMP FORMAT ==

The top-level "start" and "end" request fields are Unix milliseconds (13-digit), e.g. 1756386047000.
Prefer start/end to bound the time window. The built-in "timestamp" COLUMN is nanosecond-scale
(DateTime64(9)), so do NOT put a millisecond value in an inline "timestamp" filter — use start/end instead.

== QUICK REFERENCE ==

| Need                        | Field              | fieldContext  |
|-----------------------------|--------------------|---------------|
| Is error span?              | has_error          | span          |
| Span duration               | duration_nano      | span          |
| Operation name              | name               | span          |
| Trace ID                    | trace_id           | span          |
| Span ID                     | span_id            | span          |
| Service name                | service.name       | resource      |
| Kubernetes namespace        | k8s.namespace.name | resource      |
| HTTP response code (attr)   | http.response.status_code | tag   |
`
