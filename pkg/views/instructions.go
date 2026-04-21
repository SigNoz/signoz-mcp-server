// Package views holds the MCP resource content served at
// signoz://view/instructions and signoz://view/examples. The content
// teaches an LLM how to construct SavedView payloads before calling
// signoz_create_view / signoz_update_view.
package views

// Instructions is the body of signoz://view/instructions.
const Instructions = `# SigNoz Saved View Schema

A saved view is a reusable snapshot of an Explorer query. It maps 1:1
to the "Saved Views" feature in the SigNoz UI (traces-explorer,
logs-explorer, metrics-explorer).

## SavedView fields

| Field           | Type              | Required on create? | Notes |
|-----------------|-------------------|---------------------|-------|
| id              | string (UUID)     | No (server-assigned) | Path param on get/update/delete. Do not send on create/update. |
| name            | string            | Yes                 | Display name |
| category        | string            | No                  | Free-form grouping label |
| sourcePage      | string            | Yes                 | One of: "traces", "logs", "metrics" |
| tags            | string[]          | No                  | Free-form tags |
| compositeQuery  | object            | Yes                 | Query Builder v5 — see below |
| extraData       | string            | No                  | UI-controlled options (JSON string). Safe to leave "" |
| createdAt / by, updatedAt / by | — | Server-populated | Do not send |

## compositeQuery (Query Builder v5 shape)

CompositeQuery at the top level has exactly three fields:

    {
      "queryType": "builder" | "promql" | "clickhouse_sql",
      "panelType": "list" | "graph" | "table" | "value",
      "queries":   [ { "type": "...", "spec": { ... } }, ... ]
    }

**Do not send** legacy v3/v4 fields like ` + "`builder`" + `,
` + "`promql`" + `, ` + "`clickhouse_sql`" + ` (as sub-objects at the top level),
` + "`unit`" + `, ` + "`id`" + `, ` + "`queryFormulas`" + `, or ` + "`queryTraceOperator`" + `.
The server rejects them with HTTP 400 "failed to validate request body".

### Each entry in queries[]

    { "type": "builder_query" | "promql_query" | "clickhouse_query",
      "spec": { ...type-specific fields... } }

### builder_query spec fields (queryType: "builder")

| Field        | Type     | Notes |
|--------------|----------|-------|
| name         | string   | Reference name, e.g. "A" |
| signal       | string   | MUST match sourcePage: "traces" / "logs" / "metrics" |
| source       | string   | Usually "" |
| stepInterval | integer  | Seconds per bucket. 0 for list panels, e.g. 60 for graphs |
| filter       | object   | { "expression": "SigNoz filter expression" } |
| having       | object   | { "expression": "" } unless aggregating |
| aggregations | array    | Required for metrics graphs (see metrics example) |

### promql_query spec (queryType: "promql")

    { "name": "A", "query": "<PromQL>", "legend": "", "disabled": false }

### clickhouse_query spec (queryType: "clickhouse_sql")

    { "name": "A", "query": "<SQL>", "legend": "", "disabled": false }

## Rules

- **signal must equal sourcePage.** A ` + "`sourcePage:\"traces\"`" + ` view must use
  ` + "`\"signal\":\"traces\"`" + ` in every builder_query spec.
- **panelType by intent:** "list" for tabular spans/logs; "graph" for
  time-series; "table" for grouped tables; "value" for a single number.
- **Full Query Builder v5 spec:** https://signoz.io/docs/userguide/query-builder-v5/

## Update flow

signoz_update_view **replaces** the view (upstream is HTTP PUT). To
rename or tweak one field:

1. Call signoz_get_view with the view's id. It returns
   {"status":"success","data":{...SavedView...}}.
2. Take the **data** object from that response.
3. Strip server-populated fields (id, createdAt, createdBy, updatedAt, updatedBy).
4. Modify the field(s) you want to change.
5. Call signoz_update_view with { "viewId": "<id>", "view": <modified data> }.

(The MCP server strips server-populated fields for you if you forget, but
omitting them up front is clearer.)

## Minimal create body

    {
      "name": "my view",
      "sourcePage": "traces",
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "list",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "traces",
            "source": "",
            "stepInterval": 0,
            "filter":  { "expression": "service.name = 'checkoutservice'" },
            "having":  { "expression": "" }
          }
        }]
      }
    }

See signoz://view/examples for complete payloads per sourcePage.
`
