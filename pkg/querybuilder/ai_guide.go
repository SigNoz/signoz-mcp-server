package querybuilder

// AIQueryBuilderGuide is the signoz://ai/query-builder-guide resource. Its
// per-trace column list mirrors SigNoz's AI trace scope (aistatementbuilder).
const AIQueryBuilderGuide = `
=== SIGNOZ QUERY BUILDER V5: AI GUIDE ===

Read this for questions about AI agents and LLM calls: cost, tokens, models, tool calls, and
per-trace AI totals. It covers queryType on signoz_search_traces and signoz_aggregate_traces, and
builder_ai_query envelopes in signoz_execute_builder_query and alerts. Filter syntax
and span fields are in signoz://traces/query-builder-guide.

== WARNINGS ==

- Set queryType to builder_ai_query whenever aggregations, filter, or orderBy uses one of the eight
  per-trace keys below.
- AI trace rows (signoz_search_traces with requestType=trace) are built in two passes. Trace-level
  filters, orderBy, and the limit use values computed from the spans inside the time window, so they
  decide which traces match and make the page. The returned columns are then computed over all of
  each trace's spans, including spans outside the window, and the page is sorted by those values. A
  trace that crosses the window edge can therefore show a value that fails the filter, or sort in a
  different place than it ranked.
- Aggregations (signoz_aggregate_traces, scalar or time_series) count only spans inside the window,
  and a time series splits a trace across its buckets, so a trace's total there can be lower than
  in its trace row.
- trace.total_tokens is trace.input_tokens + trace.output_tokens. Input tokens include cache reads
  and writes (gen_ai.usage.cache_read.input_tokens, gen_ai.usage.cache_creation.input_tokens), as
  the OpenTelemetry GenAI conventions define them, so do not add those again. Cost prices the
  cached part at the model's cache rates.
- Cost is an estimate in USD. SigNoz stamps signoz.gen_ai.usage.tokens.cost on each span at ingest
  from the pricing rule active then. Spans of an unpriced model carry no cost, so a null or 0 cost
  means unpriced models, not missing calls; list them with signoz_get_ai_pricing
  (view=unpriced_models).
- builder_ai_query span rows (requestType=raw) are only the LLM, tool, and agent spans of AI
  traces, the spans with gen_ai.request.model, gen_ai.tool.name, or gen_ai.agent.name; other spans
  of those traces need builder_query with a trace_id filter.
- builder_ai_query sees only spans with gen_ai.* keys. At ingest, SigNoz maps common vendor keys
  (OpenInference, OpenLLMetry, Vercel AI SDK, and others; for example llm.model_name to
  gen_ai.request.model), so those spans count. A span whose keys no mapping covers is not an AI span
  to it: check the mappings with signoz_list_ai_attribute_mappings, suggest the user add a mapping
  for those keys on the Attribute Mapping tab of AI Observability, which applies to spans ingested
  afterwards, and query spans already stored with builder_query on their own keys.

== CHOOSING THE LEVEL ==

Per call (one span per LLM or tool call): builder_query on gen_ai.* span attributes.
  gen_ai.request.model                      string  model requested by the call
  gen_ai.provider.name                      string  model provider
  gen_ai.operation.name                     string  operation, such as chat or execute_tool
  gen_ai.agent.name                         string  agent name
  gen_ai.tool.name                          string  tool name on tool-call spans
  gen_ai.usage.input_tokens                 number  input tokens of the call
  gen_ai.usage.output_tokens                number  output tokens of the call
  gen_ai.usage.cache_read.input_tokens      number  cached input tokens read
  gen_ai.usage.cache_creation.input_tokens  number  input tokens written to the cache
  gen_ai.usage.reasoning.output_tokens      number  reasoning tokens
  signoz.gen_ai.usage.tokens.cost           number  estimated USD cost of the call

Per trace (one row or value per AI trace): builder_ai_query. An AI trace has at least one span with
gen_ai.request.model, gen_ai.tool.name, or gen_ai.agent.name.

== PER-TRACE KEYS (builder_ai_query only) ==

  trace.llm_call_count          spans with gen_ai.request.model
  trace.tool_call_count         spans with gen_ai.tool.name
  trace.distinct_tool_count     distinct gen_ai.tool.name values
  trace.input_tokens            sum of gen_ai.usage.input_tokens
  trace.output_tokens           sum of gen_ai.usage.output_tokens
  trace.total_tokens            input plus output tokens
  trace.estimated_total_cost    sum of signoz.gen_ai.usage.tokens.cost, in USD
  trace.max_llm_duration_nano   duration of the slowest LLM call

Where they work:
- Aggregations (signoz_aggregate_traces): wrap a per-trace key in a function, e.g.
  avg(trace.estimated_total_cost) or max(trace.tool_call_count); SigNoz computes each trace's value,
  then aggregates across traces. Rules SigNoz enforces:
  - one call is all trace-level (trace.) or all span-level aggregations, never both;
    avg(trace.estimated_total_cost) with p99(duration_nano) is rejected, so make two calls;
  - no sumIf-style conditions on trace. keys; put the condition in filter.
  count() counts spans and count_distinct(trace_id) counts AI traces; both are span-level, so
  never put them in the same call as a trace. key.
- Filters: with span rows and trace rows (signoz_search_traces) and with aggregations. Combine them
  with span-level conditions using a top-level AND; SigNoz rejects OR or NOT across the two levels.
  A span-level condition keeps the traces that have at least one matching span inside the time
  window.
- orderBy: only trace rows (signoz_search_traces, requestType=trace) order by a per-trace key, any
  of the eight or trace.last_activity_time, e.g. trace.input_tokens desc; they default to
  last_activity_time descending. Span rows (requestType=raw) order by span fields such as timestamp
  or duration_nano, as with builder_query. Aggregations order by one of the expressions in
  aggregations or a groupBy key, e.g. avg(trace.input_tokens) desc.
- Group by: never. Group by span attributes such as gen_ai.request.model or service.name. Each
  trace's values are then split by that attribute: a group counts only the trace's spans with its
  value, spans without the attribute fall in a null group, and one trace can appear in several groups.

AI trace rows also carry trace_id, service.name, root_span_name, span_count, trace_duration_nano,
start_time, end_time, last_activity_time, error_count, input, and output (the input and output
message JSON). Per-trace columns come back without the trace. prefix, such as llm_call_count.
error_count, input, and output are display only: they cannot be filtered or ordered. For failing
traces filter on the span-level has_error = true. Pass a trace_id to signoz_get_trace_details for
its spans.

== EXAMPLES ==

Most expensive traces (signoz_search_traces):
  {"queryType": "builder_ai_query", "requestType": "trace",
   "orderBy": "trace.estimated_total_cost desc", "limit": "10", "timeRange": "24h"}

Agent stuck in a loop (signoz_search_traces):
  {"queryType": "builder_ai_query", "requestType": "trace",
   "filter": "trace.tool_call_count > 10 AND trace.distinct_tool_count < 3",
   "orderBy": "trace.tool_call_count desc", "timeRange": "24h"}

Average per-trace spend on each model (signoz_aggregate_traces):
  {"queryType": "builder_ai_query", "aggregations": "avg(trace.estimated_total_cost)",
   "groupBy": "gen_ai.request.model", "timeRange": "24h"}

AI spend by model, per call (signoz_aggregate_traces, default queryType):
  {"aggregations": "sum(signoz.gen_ai.usage.tokens.cost)",
   "groupBy": "gen_ai.request.model", "timeRange": "24h"}

Failing tool calls (signoz_search_traces, default queryType):
  {"filter": "gen_ai.tool.name EXISTS AND has_error = true", "selectFields": ["gen_ai.tool.name"],
   "timeRange": "6h"}

Slowest LLM calls (signoz_search_traces, default queryType):
  {"filter": "gen_ai.request.model EXISTS", "orderBy": "duration_nano desc",
   "selectFields": ["gen_ai.request.model"], "timeRange": "1h"}

For a full query_range request (signoz_execute_builder_query, a script, an alert, or a dashboard),
read signoz://query-builder/examples and copy the matching ai_traces_* example instead of writing one:
ai_traces_scalar for a per-trace value by model, ai_traces_time_series for the same over time,
ai_traces_trace for AI trace rows, ai_traces_raw for span rows with every column, and
ai_traces_raw_select_fields for span rows with only the selected fields. The builder_ai_query
spec is a traces builder spec with "signal": "traces".
signoz_execute_builder_query fills an omitted limit on a builder_ai_query and inserts no order.

== ALERTS ==

An AI alert sets alertType to AI_TRACES_BASED_ALERT and puts the builder_ai_query envelope inside its
compositeQuery. With TRACES_BASED_ALERT it still evaluates, but its links open the traces explorer
instead of the AI explorer. Its aggregations accept the trace-level fields, such as
sum(trace.estimated_total_cost), and count only the spans inside each evaluation window, as the
aggregation warning above describes.
`
