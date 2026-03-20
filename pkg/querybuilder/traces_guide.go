package querybuilder

const TracesQueryBuilderGuide = `
=== SIGNOZ QUERY BUILDER V5 — TRACES GUIDE ===

== FILTER EXPRESSION FORMAT ==

Filters are a STRING expression in filter.expression — NOT a structured {op, items} object.

CORRECT:   "filter": {"expression": "hasError = true AND k8s.namespace.name = 'my-ns'"}
INCORRECT: "filter": {"op": "AND", "items": [...]}

Operators: =  !=  >  >=  <  <=  IN  NOT IN  LIKE  NOT LIKE  EXISTS  NOT EXISTS
Combine:   AND  OR  (use parentheses for precedence)

Examples:
  hasError = true
  durationNano > 500000000
  statusCodeString = 'STATUS_CODE_ERROR'
  name LIKE '%payment%'
  hasError = true AND k8s.namespace.name = 'prod'
  (hasError = true OR durationNano > 1000000000) AND service.name = 'checkout'

== FIELD NAMES — TWO CATEGORIES ==

--- 1. Built-in span columns (camelCase, NO fieldContext) ---

Use these directly by name in filter expressions and selectFields without fieldContext:

  hasError           bool      — whether the span has an error
  statusMessage      string    — OTel status message
  statusCodeString   string    — e.g. "STATUS_CODE_ERROR", "STATUS_CODE_OK"
  statusCode         int64     — numeric status code
  durationNano       int64     — span duration in nanoseconds
  traceID            string    — trace identifier
  spanID             string    — span identifier
  name               string    — span/operation name
  timestamp          datetime  — span start timestamp
  httpMethod         string    — HTTP method
  httpUrl            string    — HTTP URL
  spanKind           string    — SPAN_KIND_SERVER, SPAN_KIND_CLIENT, etc.
  responseStatusCode string    — HTTP response status as string

selectFields entry (no fieldContext needed):
  {"name": "hasError", "fieldDataType": "bool", "signal": "traces"}
  {"name": "durationNano", "fieldDataType": "int64", "signal": "traces"}
  {"name": "name", "fieldDataType": "string", "signal": "traces"}

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

  http.status_code   int64  — HTTP status code attribute
  db.system          string — database system (e.g. "postgresql")
  db.operation       string — database operation
  rpc.method         string — RPC method name
  messaging.system   string — messaging system (e.g. "kafka")

selectFields entry (fieldContext: "tag" required):
  {"name": "http.status_code", "fieldDataType": "int64", "signal": "traces", "fieldContext": "tag"}

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
          "limit": 10,
          "offset": 0,
          "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
          "having": {"expression": ""},
          "filter": {"expression": "hasError = true AND k8s.namespace.name = 'prod'"},
          "selectFields": [
            {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
            {"name": "name",         "fieldDataType": "string", "signal": "traces"},
            {"name": "hasError",     "fieldDataType": "bool",   "signal": "traces"},
            {"name": "durationNano", "fieldDataType": "int64",  "signal": "traces"},
            {"name": "statusMessage","fieldDataType": "string", "signal": "traces"},
            {"name": "traceID",      "fieldDataType": "string", "signal": "traces"},
            {"name": "spanID",       "fieldDataType": "string", "signal": "traces"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 2: Aggregation query — error count grouped by service (requestType: "aggregate") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "aggregate",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_query",
        "spec": {
          "name": "A",
          "signal": "traces",
          "disabled": false,
          "limit": 20,
          "offset": 0,
          "having": {"expression": ""},
          "filter": {"expression": "hasError = true"},
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
          "having": {"expression": ""},
          "filter": {"expression": "service.name = 'checkout'"},
          "aggregations": [
            {"expression": "p99(durationNano)"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

== TIMESTAMP FORMAT ==

"start" and "end" are Unix milliseconds (13-digit). Example: 1756386047000

== QUICK REFERENCE ==

| Need                        | Field              | fieldContext  |
|-----------------------------|--------------------|---------------|
| Is error span?              | hasError           | (none)        |
| Span duration               | durationNano       | (none)        |
| Operation name              | name               | (none)        |
| Service name                | service.name       | resource      |
| Kubernetes namespace        | k8s.namespace.name | resource      |
| HTTP response code (attr)   | http.status_code   | tag           |
`
