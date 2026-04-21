package views

// Examples is the body of signoz://view/examples. Three complete
// SavedView payloads — one per sourcePage — that can be sent directly
// to signoz_create_view. All use Query Builder v5 shape
// ({queryType, panelType, queries[{type, spec}]}).
const Examples = `# Saved View Examples (Query Builder v5 shape)

All payloads below were round-tripped against a live SigNoz instance.
They work verbatim with signoz_create_view.

## Example 1 — Traces list view (panelType: list)

    {
      "name": "slow-checkout-traces",
      "category": "latency",
      "sourcePage": "traces",
      "tags": ["team:checkout"],
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
            "filter": { "expression": "service.name = 'checkoutservice' AND duration_nano > 500000000" },
            "having": { "expression": "" }
          }
        }]
      },
      "extraData": "{\"selectColumns\":[{\"name\":\"service.name\",\"signal\":\"traces\"},{\"name\":\"name\",\"signal\":\"traces\"},{\"name\":\"duration_nano\",\"signal\":\"traces\"}]}"
    }

## Example 1b — Error traces view (panelType: list)

    {
      "name": "Error Traces",
      "category": "errors",
      "sourcePage": "traces",
      "tags": ["debugging", "errors"],
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
            "filter": { "expression": "hasError = true" },
            "having": { "expression": "" }
          }
        }]
      },
      "extraData": "{\"selectColumns\":[{\"name\":\"service.name\",\"signal\":\"traces\"},{\"name\":\"name\",\"signal\":\"traces\"},{\"name\":\"duration_nano\",\"signal\":\"traces\"},{\"name\":\"status_code\",\"signal\":\"traces\"}]}"
    }

## Example 2 — Logs search view (panelType: list)

    {
      "name": "payment-errors",
      "sourcePage": "logs",
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "list",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "logs",
            "source": "",
            "stepInterval": 0,
            "filter": { "expression": "service.name = 'paymentservice' AND severity_text = 'ERROR'" },
            "having": { "expression": "" }
          }
        }]
      }
    }

## Example 3 — Metrics time-series view (panelType: graph)

    {
      "name": "http-request-rate",
      "sourcePage": "metrics",
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "graph",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "source": "",
            "stepInterval": 60,
            "filter": { "expression": "" },
            "having": { "expression": "" },
            "aggregations": [{
              "metricName": "http_requests_total",
              "timeAggregation": "rate",
              "spaceAggregation": "sum"
            }]
          }
        }]
      }
    }

## Notes

- "signal" inside each spec MUST match the view's sourcePage.
- stepInterval is 0 for list panels, typically 60 for minute-resolution graphs.
- For PromQL or raw ClickHouse views, set "queryType" accordingly and use
  "promql_query" / "clickhouse_query" entries in "queries":

      // promql
      "queries": [{
        "type": "promql_query",
        "spec": { "name": "A", "query": "rate(http_requests_total[5m])", "legend": "", "disabled": false }
      }]

      // clickhouse_sql
      "queries": [{
        "type": "clickhouse_query",
        "spec": { "name": "A", "query": "SELECT ...", "legend": "", "disabled": false }
      }]
`
