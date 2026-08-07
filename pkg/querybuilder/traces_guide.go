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

== FIELD DISCOVERY ==

Unknown keys hard-error. Built-in span columns are stable, but resource and span/tag attributes depend
on this workspace's instrumentation. Do not guess unfamiliar field names. Discover keys, then confirm
their observed values before filtering:
  signoz_get_field_keys(signal="traces", fieldContext="span")
  signoz_get_field_keys(signal="traces", fieldContext="resource")
  signoz_get_field_keys(signal="traces", fieldContext="attribute")
  signoz_get_field_values(signal="traces", name="<field>", fieldContext="<matching context>")

The fieldContext value "tag" is accepted as an alias for "attribute" by the discovery tools; Query
Builder selectFields and groupBy use "tag". The resource and tag names below are common examples, not
a complete catalog for every workspace.

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
smaller limit only when the user asks for top N; use a larger limit when completeness
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

== AI / LLM TRACE QUERIES (builder_ai_query) ==

Use envelope type "builder_ai_query" instead of "builder_query" when the user asks about LLM/GenAI
activity: token usage, LLM cost, model/tool calls, prompts and responses, or "AI traces". It scopes
results to traces containing at least one gen_ai span (LLM, tool, or agent) and requires a SigNoz
version that supports it — older servers reject the type with an "invalid query type" error.

The spec shape is the same as a traces builder_query, but "signal" may be omitted (it is always
traces). Four request types are supported:

  requestType "raw"         — individual gen_ai span rows; behaves like a normal raw traces query with
                              the gen_ai scope ANDed into the filter. Order defaults to timestamp desc.
  requestType "trace"       — ONE ROW PER TRACE with computed AI columns (below). This is the mode for
                              "top traces by tokens/cost", per-conversation summaries, prompt/response
                              previews.
  requestType "scalar"      — aggregate statistics, one row per group: "total tokens by model",
                              "average cost per trace", "how many AI traces".
  requestType "time_series" — the same aggregates bucketed over time; requires stepInterval.

Per-trace columns returned in "trace" mode ([o] = orderable — usable in order and in trace-level
filter conditions; others are output-only):

  llm_call_count [o]         tool_call_count [o]      distinct_tool_count [o]
  input_tokens [o]           output_tokens [o]        total_tokens [o]
  estimated_total_cost [o]   max_llm_duration_nano [o]  last_activity_time [o]
  error_count                span_count               trace_duration_nano
  start_time                 end_time                 root_span_name
  service.name               input (first prompt)     output (last response)

In "trace" mode the filter expression may mix span-level conditions (service.name, gen_ai.* attributes)
with trace-level conditions on orderable columns, written bare or with a trace. prefix
(e.g. total_tokens > 10000 or trace.total_tokens > 10000). Omit "order" to use the backend default
(last_activity_time desc), or order by any orderable column.

--- Aggregations ("scalar" and "time_series") ---

The trace. prefix picks the aggregation DOMAIN, per expression:

  bare keys        SPAN-level — aggregates the gen_ai spans themselves. A trace with 5 LLM calls
                   contributes 5 rows.
                     count()   avg(duration_nano)   sum(gen_ai.usage.output_tokens)
  trace.<column>   TRACE-level — aggregates one window-clipped value per trace, so every trace
                   contributes exactly once regardless of span count.
                     avg(trace.output_tokens)   sum(trace.estimated_total_cost)   count(trace.trace_id)

count(trace.trace_id) is the idiom for "how many traces". Plain count() counts gen_ai SPANS — for a
trace count it is almost always wrong.

Trace-level columns usable in aggregations (the orderable set above):
  llm_call_count   tool_call_count       distinct_tool_count    input_tokens   output_tokens
  total_tokens     estimated_total_cost  max_llm_duration_nano  last_activity_time

Rules — each is a hard error with a targeted message, never a silent fallback:
  - Do not mix domains: either every aggregation in the query is span-level, or every one is
    trace-level.
  - groupBy takes SPAN attributes only (service.name, gen_ai.request.model). Grouping by a
    trace-level column is rejected; use it in an aggregation or a filter condition instead.
  - Filter conditions split exactly as in "trace" mode: span conditions filter spans, trace-level
    conditions qualify whole traces first. The two may be AND-ed, never OR-ed together.
  - *If combinators over trace. columns (countIf(trace.output_tokens > 5)) are unsupported; put the
    condition in the filter expression.
  - An unknown trace. column errors and lists the usable columns.

Traces that clear the gen_ai gate but hold no LLM span (tool-only traces) have NULL token and cost
columns; avg/sum skip them rather than treating them as zero.

Common questions → query shape:

  "top N containers/services/users by tokens or cost"
      groupBy that attribute (resource attributes such as k8s.container.name work), aggregate
      sum(trace.total_tokens) or sum(trace.estimated_total_cost), order by it desc, limit N.
  "...only counting traces above X tokens"
      add "trace.total_tokens > X" to the same filter — it qualifies whole traces before grouping,
      so a group's count reflects qualifying traces only.
  "how many AI traces"
      count(trace.trace_id). count() would count gen_ai spans and overcount multi-call traces.
  "how many AI traces had errors"
      filter "has_error = true" with count(trace.trace_id) — that reads as "traces with a failing
      gen_ai span". trace.error_count is NOT available here (see below).
  "which prompt/input cost the most", "show me the most expensive conversations"
      this is a LIST question: requestType "trace", order by estimated_total_cost desc, limit N. The
      rows carry input/output previews next to cost and tokens. Add "trace.estimated_total_cost > X"
      to filter first. For a ranked total per distinct prompt instead, aggregate with
      groupBy gen_ai.input.messages and sum(trace.estimated_total_cost).
  "spans and traces side by side in one table"
      not expressible in one query (domain mixing); issue two queries.

The input and output columns are display-only previews of "trace" mode. In a filter or groupBy use
the underlying attributes gen_ai.input.messages / gen_ai.output.messages — the bare names input and
output resolve as ordinary span attributes, so they match nothing and group into a single NULL
bucket instead of erroring. trace.input / trace.output are rejected outright.

The "trace" mode columns that are not marked [o] — error_count, span_count, trace_duration_nano,
start_time, end_time, root_span_name — aggregate over EVERY span in the trace, while the
scalar/time_series scan sees only gen_ai spans. They are therefore unavailable both as trace.
aggregations and in trace-level filter conditions; this is deliberate, not a missing feature.

--- Example: top traces by token usage (requestType: "trace") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "trace",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_ai_query",
        "spec": {
          "name": "A",
          "disabled": false,
          "limit": 20,
          "offset": 0,
          "filter": {"expression": "gen_ai.request.model = 'gpt-4o' AND trace.total_tokens > 1000"},
          "order": [{"key": {"name": "total_tokens"}, "direction": "desc"}],
          "having": {"expression": ""}
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example: raw LLM span rows (requestType: "raw") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_ai_query",
        "spec": {
          "name": "A",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
          "having": {"expression": ""},
          "filter": {"expression": "has_error = true"},
          "selectFields": [
            {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
            {"name": "name",          "fieldDataType": "string", "signal": "traces", "fieldContext": "span"},
            {"name": "duration_nano", "fieldDataType": "number", "signal": "traces", "fieldContext": "span"},
            {"name": "trace_id",      "fieldDataType": "string", "signal": "traces", "fieldContext": "span"}
          ]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

--- Example: LLM cost and trace count per model (requestType: "scalar") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "scalar",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_ai_query",
        "spec": {
          "name": "A",
          "disabled": false,
          "limit": 100,
          "offset": 0,
          "having": {"expression": ""},
          "filter": {"expression": "service.name = 'checkout'"},
          "aggregations": [
            {"expression": "sum(trace.estimated_total_cost)"},
            {"expression": "count(trace.trace_id)"}
          ],
          "groupBy": [
            {"name": "gen_ai.request.model", "fieldDataType": "string", "signal": "traces", "fieldContext": "span"}
          ],
          "order": [{"key": {"name": "sum(trace.estimated_total_cost)"}, "direction": "desc"}]
        }
      }
    ]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

The next example's filter mixes a span condition (model) with a trace-level one (total_tokens): the
span part filters spans, the trace part qualifies whole traces.

--- Example: token usage over time, expensive traces only (requestType: "time_series") ---

{
  "schemaVersion": "v1",
  "start": 1756386047000,
  "end": 1756387847000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [
      {
        "type": "builder_ai_query",
        "spec": {
          "name": "A",
          "disabled": false,
          "stepInterval": 60,
          "limit": 100,
          "offset": 0,
          "having": {"expression": ""},
          "filter": {"expression": "gen_ai.request.model = 'gpt-4o' AND trace.total_tokens > 1000"},
          "aggregations": [
            {"expression": "sum(trace.total_tokens)"}
          ],
          "groupBy": [
            {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"}
          ],
          "order": [{"key": {"name": "sum(trace.total_tokens)"}, "direction": "desc"}]
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
