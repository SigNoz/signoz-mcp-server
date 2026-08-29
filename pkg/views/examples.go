package views

// Examples is the body of signoz://view/examples. Complete SavedView
// payloads covering traces, logs, metrics, and Cost Meter sources that can
// be sent directly to signoz_create_view. All use the v2 typed spec shape
// ({name, source, schemaVersion, spec{...}}).
const Examples = `# Saved View Examples (v2 typed spec)

The traces, logs, and metrics payloads below match the v2 SavedView shape
the SigNoz server persists and work verbatim with signoz_create_view. The
Cost Meter payload follows the same encoding the SigNoz product persists
for Meter Explorer views (source "meter" + signal "metrics" + builder
spec source "meter").

## Example 1 — Traces list view

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
            "filter": { "expression": "service.name = 'checkoutservice' AND duration_nano > 500000000" },
            "having": { "expression": "" }
          }
        }],
        "selectedFields": [
          {"name": "service.name", "signal": "traces", "fieldContext": "resource", "fieldDataType": "string"},
          {"name": "name", "signal": "traces", "fieldContext": "span", "fieldDataType": "string"},
          {"name": "duration_nano", "signal": "traces", "fieldContext": "span", "fieldDataType": "float64"}
        ]
      }
    }

## Example 2 — Logs search view

    {
      "name": "payment-errors",
      "source": "logs",
      "schemaVersion": "v2",
      "spec": {
        "displayName": "Payment errors",
        "panelType": "list",
        "requestType": "raw",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "logs",
            "source": "",
            "stepInterval": 0,
            "limit": 100,
            "order": [
              {"key":{"name":"timestamp"},"direction":"desc"},
              {"key":{"name":"id"},"direction":"desc"}
            ],
            "filter": { "expression": "service.name = 'paymentservice' AND severity_text = 'ERROR'" },
            "having": { "expression": "" }
          }
        }],
        "selectedFields": [
          {"name": "service.name", "signal": "logs", "fieldContext": "resource", "fieldDataType": "string"},
          {"name": "severity_text", "signal": "logs", "fieldContext": "log", "fieldDataType": "string"}
        ]
      }
    }

## Example 3 — Metrics time-series view

    {
      "name": "http-request-rate",
      "source": "metrics",
      "schemaVersion": "v2",
      "spec": {
        "displayName": "HTTP request rate",
        "panelType": "graph",
        "requestType": "time_series",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "source": "",
            "stepInterval": 60,
            "limit": 100,
            "order": [{"key":{"name":"__result"},"direction":"desc"}],
            "filter": { "expression": "" },
            "having": { "expression": "" },
            "aggregations": [{
              "metricName": "http_requests_total",
              "timeAggregation": "rate",
              "spaceAggregation": "sum"
            }]
          }
        }],
        "selectedFields": []
      }
    }

## Example 4 — Cost Meter usage view (source "meter")

A Cost Meter view lives on its own Explorer page ("source":"meter") but
is queried as metrics: every builder spec sets "signal":"metrics" AND
"source":"meter". The backend rolls Cost Meter data up hourly, so use
"stepInterval": 3600.

    {
      "name": "log-ingestion-volume",
      "source": "meter",
      "schemaVersion": "v2",
      "spec": {
        "displayName": "Log ingestion volume",
        "panelType": "graph",
        "requestType": "time_series",
        "queries": [{
          "type": "builder_query",
          "spec": {
            "name": "A",
            "signal": "metrics",
            "source": "meter",
            "stepInterval": 3600,
            "limit": 100,
            "order": [{"key":{"name":"__result"},"direction":"desc"}],
            "filter": { "expression": "" },
            "having": { "expression": "" },
            "aggregations": [{
              "metricName": "signoz.meter.log.size",
              "timeAggregation": "increase",
              "spaceAggregation": "sum"
            }]
          }
        }],
        "selectedFields": []
      }
    }

## Notes

- name must be a DNS-1123 label (lowercase letters, digits, hyphens). To
  let the server generate one from spec.displayName, send
  "generateName": true with an empty name.
- The signal inside each builder_query spec MUST match the view's source for
  traces/logs/metrics/ai_observability. A Cost Meter view is source "meter"
  with signal "metrics" + builder spec source "meter" (see Example 4).
- spec.displayName is the visible label; it can differ from name.
- spec.selectedFields is the Explorer column choice. Use [] when the view
  has no column preference (e.g. graphs); each entry needs name plus
  signal/fieldContext/fieldDataType.
- stepInterval is 0 for list panels, typically 60 for minute-resolution
  graphs, and 3600 for Cost Meter views, which the backend aggregates
  hourly.
- Every builder query carries a limit and the v5 order field.
  The 100-group limit on a graph ranks groups over the whole selected window;
  a short-lived local spike may be outside the returned top N.
`
