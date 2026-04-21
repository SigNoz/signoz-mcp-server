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
| id              | string (UUID)     | No (server-assigned) | Path param on get/update/delete |
| name            | string            | Yes                 | Display name |
| category        | string            | No                  | Free-form grouping label |
| sourcePage      | string            | Yes                 | One of: "traces", "logs", "metrics" |
| tags            | string[]          | No                  | Free-form tags |
| compositeQuery  | object            | Yes                 | See below |
| extraData       | string            | No                  | UI-controlled options (JSON string) |
| createdAt / by, updatedAt / by | — | Server-populated | Do not send |

## compositeQuery

This is the same Query Builder object used throughout SigNoz. It has
three top-level sub-queries keyed by query type, plus metadata:

    {
      "queryType":  "builder" | "promql" | "clickhouse_sql",
      "panelType":  "list" | "graph" | "table" | "value",
      "builder":        { "queryData": [...], "queryFormulas": [...], "queryTraceOperator": [...] },
      "promql":         [{ "name":"A","query":"...","legend":"","disabled":false }],
      "clickhouse_sql": [{ "name":"A","legend":"","disabled":false,"query":"..." }],
      "unit": "",
      "id":   "<uuid>"
    }

Rules:
- "queryType" determines which sub-query is active. The other two are
  typically empty skeletons but must still be present.
- For "queryType":"builder", "builder.queryData[*].dataSource" must
  equal the view's "sourcePage" ("traces"/"logs"/"metrics").
- For time-series panels use "panelType":"graph"; for tabular results
  use "list" or "table"; for a single number use "value".
- The full Query Builder v5 schema is documented at
  https://signoz.io/docs/userguide/query-builder-v5/ — refer to it for
  filter expressions, group-by, aggregations, etc.

## Update flow

signoz_update_view **replaces** the view (upstream is HTTP PUT). To
rename or tweak one field:

1. Call signoz_get_view with the view's id. It returns
   {"status":"success","data":{...SavedView...}}.
2. Take the **data** object from that response.
3. Modify the field(s) you want to change.
4. Call signoz_update_view with { "viewId": "<id>", "view": <modified data> }.

Never hand-craft an update body from memory — you will lose fields.

## Minimal create body

    {
      "name": "my view",
      "sourcePage": "traces",
      "compositeQuery": { ... }
    }

See signoz://view/examples for complete payloads per sourcePage.
`
