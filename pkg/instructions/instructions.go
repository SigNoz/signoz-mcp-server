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

3. **Clarify the signal before querying.** If it is not clear which signal to use, ask the user whether to start with metrics, traces, or logs.

4. **Choose the right filter operator for the intent and data type.**

   **By query intent:**
   | Intent | Operator | Example |
   |---|---|---|
   | Field exists / is present | EXISTS | trace_id EXISTS |
   | Field is absent | NOT EXISTS | k8s.pod.name NOT EXISTS |
   | Exact match | = | service.name = 'frontend' |
   | Exclude (field must exist) | EXISTS AND != | service.name EXISTS AND service.name != 'redis' |
   | One of several values | IN | severity_text IN ('ERROR', 'WARN', 'FATAL') |
   | Substring / pattern | LIKE | name LIKE '%payment%' |
   | Case-insensitive pattern | ILIKE | body ILIKE '%timeout%' |
   | Simple text containment | CONTAINS | body CONTAINS 'timeout' |
   | Regex | REGEXP | name REGEXP '^grpc\.' |

   **By data type:**
   | Data type | Safe operators |
   |---|---|
   | bool | = , != , EXISTS, NOT EXISTS |
   | int64 | = , != , > , >= , < , <= , IN, EXISTS, NOT EXISTS |
   | string | = , != , LIKE, ILIKE, CONTAINS, REGEXP, IN, NOT IN, EXISTS, NOT EXISTS |

   **Important:** Negative operators (!=, NOT LIKE, NOT IN, NOT CONTAINS, NOT REGEXP) also match records where the field is **absent**. To exclude a value while requiring the field, combine with EXISTS:
   ` + "`" + `service.name EXISTS AND service.name != 'redis'` + "`" + `

5. **Never convert Unix timestamps manually.** All SigNoz timestamps (start, end, and time series values) are Unix milliseconds. When presenting timestamps to the user, always use a programmatic method (e.g., a date conversion tool or function) to convert them to human-readable format. Do NOT attempt mental arithmetic or manual offset calculations — this is error-prone and leads to incorrect times being reported.

6. **Do not generate SigNoz frontend links.** Never guess or build a clickable SigNoz URL from IDs, names, or the base domain. Only return a link if a tool or resource explicitly provides the full frontend URL; otherwise say MCP cannot generate a reliable link.
`
