package instructions

// ServerInstructions is sent to MCP clients during the initialize response.
// It contains cross-cutting rules that apply to all tool usage.
const ServerInstructions = `Use SigNoz tools to investigate metrics, logs, traces, and exceptions; manage dashboards, alerts, and saved views; and consult SigNoz docs. Start with the user's signal; if none is clear, ask whether to use metrics, traces, or logs. For data queries, prioritize resource-attribute filters such as service.name and avoid redundant calls.

# SigNoz MCP Server — Instructions

## Rules

1. **Query once when possible.** Fetch more only if the first result is insufficient or the user asks. Never repeat overlapping queries.

2. **Prefer resource attributes in filters.** Use service.name, k8s.namespace.name, or host.name when available. If none was supplied, call signoz_get_field_keys with fieldContext=resource, optionally signoz_get_field_values, then ask the user to choose. Keys vary by workspace/signal; logs may lack standard resource fields. On "key not found", discover valid keys, then retry with an existing key or remove that condition—never retry the invalid filter.

3. **Match operators to intent and type.** Use EXISTS/NOT EXISTS for presence, = for exact matches, IN/NOT IN for sets, and LIKE/ILIKE/CONTAINS/REGEXP for string patterns. int64 also supports >, >=, <, and <=; bool supports =, !=, EXISTS, and NOT EXISTS.

4. **Guard negative filters.** !=, NOT LIKE, NOT IN, NOT CONTAINS, and NOT REGEXP also match missing fields. Require presence when excluding a value, for example: service.name EXISTS AND service.name != 'redis'.

5. **Convert timestamps programmatically.** SigNoz start/end and time-series timestamps are Unix milliseconds. Never convert them mentally; use a date/time function before presenting them.

6. **Use returned webUrl values verbatim.** Resource results include SigNoz deep links. Never construct links from IDs or the instance URL.
`
