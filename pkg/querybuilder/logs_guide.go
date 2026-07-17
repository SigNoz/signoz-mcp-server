package querybuilder

const LogsQueryBuilderGuide = `
=== SIGNOZ QUERY BUILDER V5 - LOGS GUIDE ===

== FILTER EXPRESSION FORMAT ==

Filters are a STRING expression in filter.expression - NOT a structured {op, items} object.

CORRECT:   "filter": {"expression": "severity_text = 'ERROR' AND body CONTAINS 'timeout'"}
INCORRECT: "filter": {"op": "AND", "items": [...]}

Operators: =  !=  >  >=  <  <=  IN  NOT IN  LIKE  NOT LIKE  ILIKE  NOT ILIKE  CONTAINS  NOT CONTAINS  REGEXP  NOT REGEXP  BETWEEN  NOT BETWEEN  EXISTS  NOT EXISTS
Combine:   AND  OR  (use parentheses for precedence)

Examples:
  severity_text = 'ERROR'
  body CONTAINS 'timeout'
  service.name = 'checkout' AND severity_text IN ('ERROR', 'FATAL')   <- only if this workspace's logs carry service.name (see FIELD NAMES AND CONTEXTS)
  body.user.id = '12345'
  body.error.message ILIKE '%connection refused%'
  trace_id = 'abc123def456'
  (severity_text = 'ERROR' OR body CONTAINS 'panic') AND k8s.namespace.name = 'prod'

== FIELD NAMES AND CONTEXTS ==

Unknown keys hard-error. Do not guess field names. If unsure, call:
  signoz_get_field_keys(signal="logs", fieldContext="resource")
  signoz_get_field_keys(signal="logs", fieldContext="attribute")
  signoz_get_field_values(signal="logs", name="<field>")

Log keys are workspace-specific: logs have no spec-mandated resource attributes, so even
service.name exists only when the log pipeline sets it. A filter on a key this workspace's
logs never carried fails with "key ... not found" — discover valid keys as above and use
an existing one (e.g. k8s.deployment.name) instead of retrying the same filter.

If a key matches BOTH the resource and attribute contexts, SigNoz defaults to the resource context (and warns). For other multi-context matches, every matching context is ORed together.
Disambiguate explicitly in filter expressions with resource.<key> or attribute.<key> (scope.<key> also exists).

--- 1. Built-in log columns ---

Use these directly by name in filter expressions:

  timestamp       datetime  - log timestamp (stored as Unix NANOSECONDS; see TIMESTAMP FORMAT)
  body            string    - rendered log body
  severity_text   string    - DEBUG, INFO, WARN, ERROR, FATAL, etc.
  severity_number number    - numeric severity when present
  trace_id        string    - linked trace identifier
  span_id         string    - linked span identifier
  trace_flags     number    - W3C trace flags
  id              string    - unique log record id (also the default secondary sort key)
  scope_name      string    - instrumentation scope / logger name (fieldContext: "scope")
  scope_version   string    - instrumentation scope version (fieldContext: "scope")

Examples:
  severity_text = 'ERROR'
  trace_id = '4bf92f3577b34da6a3ce929d0e0e4736'
  trace_flags = 1

--- 2. Resource attributes (dot notation, fieldContext: "resource") ---

OTel resource attributes describe the emitting service, host, pod, or deployment.
Discover real keys first with signoz_get_field_keys(signal="logs", fieldContext="resource").

Common examples:
  service.name
  k8s.namespace.name
  k8s.pod.name
  k8s.cluster.name
  host.name
  deployment.environment

Filter examples:
  service.name = 'checkout'
  k8s.namespace.name = 'prod'
  resource.service.name = 'checkout'

groupBy/selectFields entry:
  {"name": "service.name", "fieldDataType": "string", "signal": "logs", "fieldContext": "resource"}

--- 3. Log attributes (fieldContext: "attribute") ---

Log attributes are key-value fields attached to individual log records.
They vary by application and collector pipeline, so discover them before use:
  signoz_get_field_keys(signal="logs", fieldContext="attribute")

Common examples when instrumented:
  http.method
  http.status_code
  exception.type
  exception.message
  workflow_run_id
  user.id

Filter examples:
  http.status_code >= 500
  attribute.http.status_code >= 500
  workflow_run_id = 'wr_123'
  attribute.workflow_run_id = 'wr_123'

groupBy/selectFields entry:
  {"name": "workflow_run_id", "fieldDataType": "string", "signal": "logs", "fieldContext": "attribute"}

--- 4. Body text and body JSON path search ---

Use body for full rendered-message text search:
  body CONTAINS 'timeout'
  body ILIKE '%connection refused%'

Use body.<json path> only when the log body is JSON and you need a nested field. If a record's body is not
valid JSON, body.<path> (and has(...)) match nothing for that row — prefer body CONTAINS / ILIKE when you
are unsure the body is JSON:
  body.user.id = '12345'
  body.error.code = 'E_CONN_RESET'
  body.request.method = 'POST'
  body.latency_ms >= 1000

For arrays inside JSON bodies, mark the path as an array with the [*] suffix and use has():
  has(body.tags[*], 'production')

== RESULT BOUNDS AND ORDERING ==

Every builder_query must include a positive limit and explicit order.

  raw:        limit 100; order by timestamp desc, then id desc for stable offset pagination
  scalar:     limit 100 groups; order by the primary aggregation desc unless the task needs another order
  time_series: limit 100 groups; order by the primary aggregation desc unless the task needs another order

For time_series queries with groupBy, the limit selects top groups using the ordering across the ENTIRE
time range, not each time bucket. A short-lived spike can fall outside the selected groups. Use an explicit
smaller positive limit only when the user asks for top N; use a larger positive override when completeness
matters more than response size.

== COMPLETE WORKING EXAMPLES ==

--- Example 1: Raw error logs for a service (requestType: "raw") ---

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
          "signal": "logs",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "order": [
            {"key": {"name": "timestamp"}, "direction": "desc"},
            {"key": {"name": "id"}, "direction": "desc"}
          ],
          "having": {"expression": ""},
          "filter": {"expression": "service.name = 'checkout' AND severity_text = 'ERROR' AND body CONTAINS 'timeout'"}
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 2: Raw logs matching JSON body fields (requestType: "raw") ---

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
          "signal": "logs",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "order": [
            {"key": {"name": "timestamp"}, "direction": "desc"},
            {"key": {"name": "id"}, "direction": "desc"}
          ],
          "having": {"expression": ""},
          "filter": {"expression": "body.user.id = '12345' AND body.request.method = 'POST'"}
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 3: Aggregation - error count grouped by service (requestType: "scalar") ---

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
          "signal": "logs",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "having": {"expression": ""},
          "filter": {"expression": "severity_text IN ('ERROR', 'FATAL')"},
          "aggregations": [
            {"expression": "count()"}
          ],
          "groupBy": [
            {"name": "service.name", "fieldDataType": "string", "signal": "logs", "fieldContext": "resource"}
          ],
          "order": [{"key": {"name": "count()"}, "direction": "desc"}]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example 4: Time series - error logs per minute (requestType: "time_series") ---

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
          "signal": "logs",
          "disabled": false,
          "stepInterval": 60,
          "limit": 100,
          "offset": 0,
          "order": [{"key": {"name": "count()"}, "direction": "desc"}],
          "having": {"expression": ""},
          "filter": {"expression": "service.name = 'checkout' AND severity_text = 'ERROR'"},
          "aggregations": [
            {"expression": "count()"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

== TIMESTAMP FORMAT ==

The top-level "start" and "end" request fields are Unix milliseconds (13-digit), e.g. 1756386047000;
the backend auto-scales them to nanoseconds. Prefer start/end to bound the time window.
The built-in "timestamp" COLUMN stores Unix NANOSECONDS, so an inline filter like "timestamp >= ..." must
use a nanosecond value (e.g. 1756386047000000000), not milliseconds — otherwise it silently matches everything.

== QUICK REFERENCE ==

| Need                         | Field example            | Context in select/groupBy |
|------------------------------|--------------------------|---------------------------|
| Log severity                 | severity_text            | log (built-in)            |
| Rendered message text        | body                     | log (built-in)            |
| JSON field inside body       | body.error.code          | body                      |
| Trace correlation            | trace_id                 | log (built-in)            |
| Service name                 | service.name             | resource                  |
| Kubernetes namespace         | k8s.namespace.name       | resource                  |
| Application log attribute    | workflow_run_id          | attribute                 |
`
