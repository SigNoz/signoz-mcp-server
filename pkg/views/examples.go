package views

// Examples is the body of signoz://view/examples. Complete SavedView
// payloads covering each sourcePage (traces, logs, metrics, meter) that can
// be sent directly to signoz_create_view. All use Query Builder v5 shape
// ({queryType, panelType, queries[{type, spec}]}).
const Examples = `# Saved View Examples (Query Builder v5 shape)

The traces, logs, and metrics payloads below were round-tripped against a live
SigNoz instance and work verbatim with signoz_create_view. The Cost Meter
(meter) payload follows the same encoding the SigNoz product persists for
Meter Explorer views (sourcePage "meter" + signal "metrics" + source "meter").

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
            "filter": { "expression": "has_error = true" },
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

## Example 4 — Cost Meter usage view (sourcePage: meter, panelType: graph)

A Cost Meter view lives on its own Explorer page (` + "`sourcePage:\"meter\"`" + `) but
is queried as metrics: every builder spec sets ` + "`signal:\"metrics\"`" + ` AND
` + "`source:\"meter\"`" + `. The backend rolls Cost Meter data up hourly, so use
` + "`stepInterval: 3600`" + `.

    {
      "name": "log-ingestion-volume",
      "category": "cost",
      "sourcePage": "meter",
      "tags": ["cost-meter", "billing"],
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "graph",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "source": "meter",
            "stepInterval": 3600,
            "filter": { "expression": "" },
            "having": { "expression": "" },
            "aggregations": [{
              "metricName": "signoz.meter.log.size",
              "timeAggregation": "increase",
              "spaceAggregation": "sum"
            }]
          }
        }]
      }
    }

## Notes

- Explorer saved views are **builder-query only**. Set "queryType" to "builder"
  and use "builder_query" entries in "queries". PromQL / raw ClickHouse query
  types are not supported for Explorer saved views.
- "signal" inside each spec MUST match the view's sourcePage for
  traces/logs/metrics. A **Cost Meter** view is sourcePage "meter" with
  signal "metrics" + source "meter" (see Example 4).
- stepInterval is 0 for list panels, typically 60 for minute-resolution graphs,
  and 3600 for Cost Meter (meter) views, which the backend aggregates hourly.
`
