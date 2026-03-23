package instructions

// ServerInstructions is sent to MCP clients during the initialize response.
// It contains cross-cutting rules that apply to all tool usage.
const ServerInstructions = `# SigNoz MCP Server — Instructions

## Rules

1. **Avoid redundant queries.** Only fetch additional data if the first result is insufficient or if the user explicitly asks for more. Do not make multiple overlapping calls when one would suffice.

2. **Always prefer resource attributes in filters.** Resource attributes (e.g., service.name, k8s.namespace.name, host.name) significantly speed up backend queries. If the user does not provide a resource attribute filter:
   a. Call signoz_get_field_keys with fieldContext=resource to discover available resource attributes.
   b. Optionally call signoz_get_field_values to suggest concrete values for those attributes.
   c. Ask the user to pick a resource attribute filter before running the query.
   If the user already provides resource attributes, proceed directly without extra prompting.
`
