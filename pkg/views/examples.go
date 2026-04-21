package views

// Examples is the body of signoz://view/examples. Three complete
// SavedView payloads — one per sourcePage — that can be sent directly
// to signoz_create_view.
const Examples = `# Saved View Examples

## Example 1 — Traces list view (panelType: list)

    {
      "name": "slow-checkout-traces",
      "category": "latency",
      "sourcePage": "traces",
      "tags": ["team:checkout"],
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "list",
        "builder": {
          "queryData": [{
            "dataSource": "traces",
            "queryName": "A",
            "aggregateOperator": "noop",
            "aggregateAttribute": {"id":"----","dataType":"","key":"","type":""},
            "filter": {"expression": "service.name = 'checkoutservice' AND durationNano > 500000000"},
            "filters": {"items": [], "op": "AND"},
            "expression": "A",
            "disabled": false,
            "having": {"expression": ""},
            "orderBy": [{"columnName":"timestamp","order":"desc"}],
            "groupBy": [],
            "limit": 100,
            "name": "A",
            "signal": "traces"
          }],
          "queryFormulas": [],
          "queryTraceOperator": []
        },
        "promql":        [{"name":"A","query":"","legend":"","disabled":false}],
        "clickhouse_sql":[{"name":"A","legend":"","disabled":false,"query":""}],
        "unit": ""
      },
      "extraData": "{\"selectColumns\":[{\"name\":\"service.name\",\"signal\":\"traces\"},{\"name\":\"name\",\"signal\":\"traces\"},{\"name\":\"duration_nano\",\"signal\":\"traces\"}]}"
    }

## Example 2 — Logs search view (panelType: list)

    {
      "name": "payment-errors",
      "sourcePage": "logs",
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "list",
        "builder": {
          "queryData": [{
            "dataSource": "logs",
            "queryName": "A",
            "aggregateOperator": "noop",
            "filter": {"expression": "service.name = 'paymentservice' AND severity_text = 'ERROR'"},
            "filters": {"items": [], "op": "AND"},
            "expression": "A",
            "disabled": false,
            "orderBy": [{"columnName":"timestamp","order":"desc"}],
            "limit": 200
          }],
          "queryFormulas": [],
          "queryTraceOperator": []
        },
        "promql":        [{"name":"A","query":"","legend":"","disabled":false}],
        "clickhouse_sql":[{"name":"A","legend":"","disabled":false,"query":""}],
        "unit": ""
      }
    }

## Example 3 — Metrics time-series view (panelType: graph)

    {
      "name": "http-request-rate",
      "sourcePage": "metrics",
      "compositeQuery": {
        "queryType": "builder",
        "panelType": "graph",
        "builder": {
          "queryData": [{
            "dataSource": "metrics",
            "queryName": "A",
            "aggregateAttribute": {"key":"http_requests_total","dataType":"","type":""},
            "aggregateOperator": "rate",
            "timeAggregation": "rate",
            "spaceAggregation": "sum",
            "groupBy": [{"key":"service.name"}],
            "filter": {"expression": ""},
            "filters": {"items": [], "op": "AND"},
            "expression": "A",
            "disabled": false,
            "stepInterval": 60
          }],
          "queryFormulas": [],
          "queryTraceOperator": []
        },
        "promql":        [{"name":"A","query":"","legend":"","disabled":false}],
        "clickhouse_sql":[{"name":"A","legend":"","disabled":false,"query":""}],
        "unit": "ops"
      }
    }
`
