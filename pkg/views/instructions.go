// Package views holds the MCP resource content served at
// signoz://view/instructions and signoz://view/examples. The content
// teaches an LLM how to construct SavedView payloads before calling
// signoz_create_view / signoz_update_view.
package views

// Instructions is the body of signoz://view/instructions.
const Instructions = `# SigNoz Saved View Schema (v2 typed spec)

A saved view is a reusable snapshot of an Explorer query. It maps 1:1
to the "Saved Views" feature in the SigNoz UI (traces, logs, metrics,
and meter / Cost Meter explorers). The tools call the
/api/v2/saved_views endpoints; schemaVersion is "v2".

The tool's input schema describes the view and spec fields (name and generateName, source, panelType, requestType, selectedFields, display). The MCP server fills schemaVersion ("v2") in when omitted and strips the server-populated fields (id, createdAt, createdBy, updatedAt, updatedBy) from what it forwards. This resource covers the query envelopes inside spec.queries, which the schema shows as plain objects, and the workflow around them.

## spec.queries[] entries

Each entry is a Query Builder v5 envelope with type plus spec:

    { "type": "builder_query",
      "spec": { ...builder_query fields... } }

Only builder_query envelopes carry signal/source, and only they fall
under the rules below. promql and clickhouse_sql envelopes are
allowed but skipped by the signal check.

### builder_query spec fields

| Field        | Type     | Notes |
|--------------|----------|-------|
| name         | string   | Reference name, e.g. "A" |
| signal       | string   | Required. MUST match source for "traces"/"logs"/"metrics". For a "meter" view, signal MUST be "metrics" |
| source       | string   | Usually "". For a "meter" view it MUST be "meter". Do NOT set "meter" on a "metrics" (or other) source view; Cost Meter views belong on source "meter" |
| stepInterval | integer  | Seconds per bucket. 0 for list panels, e.g. 60 for graphs |
| filter       | object   | { "expression": "SigNoz filter expression" } |
| having       | object   | { "expression": "" } unless aggregating |
| aggregations | array    | Required for metrics graphs (see metrics example) |
| limit        | integer  | Result bound. Use 100 by default for list/raw and graph/table/value aggregate views |
| order        | array    | Query Builder v5 wire order. List logs: timestamp desc then id desc; list traces: timestamp desc; metrics/formulas: __result desc; log/trace aggregates: primary aggregation desc. Do not use dashboard orderBy |

## Rules

- **signal must equal source** for "traces"/"logs"/"metrics".
  A "source":"traces" view must use "signal":"traces" in every
  builder_query spec. SigNoz accepts a mismatch, but the Explorer
  renders only matching views, so the saved view would be unusable.
- **Cost Meter views are special.** A Cost Meter view is "source":"meter"
  (its own Explorer page) but is queried as metrics: every builder_query
  spec sets "signal":"metrics" AND "source":"meter". Do not file a Cost
  Meter view under "source":"metrics"; it will land in the wrong Explorer's
  list.
- **name is immutable.** Upstream rejects name on update; spec.displayName
  carries the visible label.
- **Changing source on update moves the view** to that Explorer's list.
  Keep the existing source unless the user asked to move the view.
- **panelType by intent:** "list" for tabular spans/logs; "graph" for
  time-series; "table" for grouped tables; "value" for a single number;
  "trace" for a trace waterfall.
- **Discover unknown fields before writing filters.** Call
  signoz_get_field_keys with the view's signal and fieldContext; use
  signoz_get_field_values when observed values help verify a predicate. Do not
  copy tenant-specific attributes from an example without checking them.
- **Always bound and order builder results:** every builder_query must include a
  positive limit and non-empty v5 order. For time-series views,
  the limit selects groups over the whole requested range, so a short-lived
  locally significant series can fall outside the returned top N.
- **Full Query Builder v5 spec:** https://signoz.io/docs/userguide/query-builder-v5/

## Update flow

signoz_update_view **replaces** the view (upstream is HTTP PUT). To
rename the label or tweak one field:

1. Call signoz_get_view with the view's id. It returns
   {"status":"success","data":{...SavedView...}}.
2. Take the **data** object from that response.
3. Strip server-populated fields (id, createdAt, createdBy, updatedAt, updatedBy) and drop name.
4. Modify the field(s) you want to change (e.g. spec.displayName).
5. Call signoz_update_view with { "id": "<id>", "view": <modified data> }.

(The MCP server strips server-populated fields and name for you if you
forget, but omitting them up front is clearer.)

## Minimal create body

    {
      "name": "slow-checkout-traces",
      "source": "traces",
      "schemaVersion": "v2",
      "spec": {
        "displayName": "Slow checkout traces",
        "panelType": "list",
        "requestType": "raw",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "traces",
            "source": "",
            "stepInterval": 0,
            "limit": 100,
            "order": [{"key":{"name":"timestamp"},"direction":"desc"}],
            "filter":  { "expression": "service.name = 'checkoutservice'" },
            "having":  { "expression": "" }
          }
        }],
        "selectedFields": [
          {"name": "service.name", "signal": "traces", "fieldContext": "attribute", "fieldDataType": "string"},
          {"name": "duration_nano", "signal": "traces", "fieldContext": "span", "fieldDataType": "int64"}
        ],
        "display": {"maxLines": 2, "fontSize": "small", "format": "table", "color": "default"}
      }
    }

See signoz://view/examples for complete payloads per source.
`
