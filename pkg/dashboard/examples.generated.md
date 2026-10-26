# Dashboard request examples

Source: the request examples published by the SigNoz OpenAPI spec.

## Create a dashboard with signoz_create_dashboard

### 1. logs_table_with_runbook — Logs dashboard with a text panel and a table panel

A `signoz/TextPanel` renders markdown and must send `queries: []`. The table counts error logs per service as a scalar result, orders by the `errors` alias and colors cells above 1000 errors; the threshold names the column by its key `A` (the query name), not by the alias.

```json
{
  "name": "error-logs-by-service",
  "schemaVersion": "v6",
  "spec": {
    "display": {
      "description": "",
      "name": "Error Logs by Service"
    },
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "content": {
                "$ref": "#/spec/panels/runbook"
              },
              "height": 6,
              "width": 4,
              "x": 0,
              "y": 0
            },
            {
              "content": {
                "$ref": "#/spec/panels/errors-by-service"
              },
              "height": 6,
              "width": 8,
              "x": 4,
              "y": 0
            }
          ]
        }
      }
    ],
    "links": [],
    "panels": {
      "errors-by-service": {
        "kind": "Panel",
        "spec": {
          "display": {
            "description": "",
            "name": "Error logs per service"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TablePanel",
            "spec": {
              "formatting": {
                "decimalPrecision": "0"
              },
              "thresholds": [
                {
                  "color": "#F1575F",
                  "columnName": "A",
                  "format": "background",
                  "operator": "above",
                  "value": 1000
                }
              ]
            }
          },
          "queries": [
            {
              "kind": "scalar",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "aggregations": [
                      {
                        "alias": "errors",
                        "expression": "count()"
                      }
                    ],
                    "filter": {
                      "expression": "severity_text = 'ERROR'"
                    },
                    "groupBy": [
                      {
                        "fieldContext": "resource",
                        "fieldDataType": "string",
                        "name": "service.name"
                      }
                    ],
                    "limit": 20,
                    "name": "A",
                    "order": [
                      {
                        "direction": "desc",
                        "key": {
                          "name": "errors"
                        }
                      }
                    ],
                    "signal": "logs"
                  }
                }
              }
            }
          ]
        }
      },
      "runbook": {
        "kind": "Panel",
        "spec": {
          "display": {
            "description": "What to do when errors spike",
            "name": "Runbook"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TextPanel",
            "spec": {
              "headerOptions": {
                "hide": false
              },
              "mode": "markdown",
              "presentation": {
                "textAlign": "left",
                "verticalAlign": "top"
              },
              "text": "## Error logs\n\n1. Find the service with the most errors in the table.\n2. Open its logs and group by `code.function`."
            }
          },
          "queries": []
        }
      }
    },
    "variables": []
  },
  "tags": []
}
```

### 2. service_overview — Service overview with a variable, a time series panel and a number panel

A `$service` list variable fed by the observed `service.name` values filters both panels. The time series panel counts spans per service; the number panel renders a single scalar and turns red above 100 errors. `generateName` lets the server derive the name from the display name.

```json
{
  "generateName": true,
  "schemaVersion": "v6",
  "spec": {
    "display": {
      "description": "Request rate and errors per service",
      "name": "Service Overview"
    },
    "duration": "1h",
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "content": {
                "$ref": "#/spec/panels/request-rate"
              },
              "height": 6,
              "width": 8,
              "x": 0,
              "y": 0
            },
            {
              "content": {
                "$ref": "#/spec/panels/error-count"
              },
              "height": 6,
              "width": 4,
              "x": 8,
              "y": 0
            }
          ]
        }
      }
    ],
    "links": [],
    "panels": {
      "error-count": {
        "kind": "Panel",
        "spec": {
          "display": {
            "description": "Spans with an error status",
            "name": "Errors"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/NumberPanel",
            "spec": {
              "thresholds": [
                {
                  "color": "#F1575F",
                  "format": "background",
                  "operator": "above",
                  "value": 100
                }
              ]
            }
          },
          "queries": [
            {
              "kind": "scalar",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "aggregations": [
                      {
                        "alias": "errors",
                        "expression": "count()"
                      }
                    ],
                    "filter": {
                      "expression": "has_error = true AND service.name IN $service"
                    },
                    "name": "A",
                    "signal": "traces"
                  }
                }
              }
            }
          ]
        }
      },
      "request-rate": {
        "kind": "Panel",
        "spec": {
          "display": {
            "description": "Spans per service",
            "name": "Requests"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TimeSeriesPanel",
            "spec": {
              "formatting": {
                "decimalPrecision": "0"
              },
              "legend": {
                "position": "bottom"
              }
            }
          },
          "queries": [
            {
              "kind": "time_series",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "aggregations": [
                      {
                        "alias": "requests",
                        "expression": "count()"
                      }
                    ],
                    "filter": {
                      "expression": "service.name IN $service"
                    },
                    "groupBy": [
                      {
                        "fieldContext": "resource",
                        "fieldDataType": "string",
                        "name": "service.name"
                      }
                    ],
                    "legend": "{{service.name}}",
                    "name": "A",
                    "signal": "traces"
                  }
                }
              }
            }
          ]
        }
      }
    },
    "variables": [
      {
        "kind": "ListVariable",
        "spec": {
          "allowAllValue": true,
          "allowMultiple": true,
          "display": {
            "name": "Service"
          },
          "name": "service",
          "plugin": {
            "kind": "signoz/DynamicVariable",
            "spec": {
              "name": "service.name",
              "signal": "traces"
            }
          },
          "sort": "alphabetical-asc"
        }
      }
    ]
  },
  "tags": [
    {
      "key": "team",
      "value": "platform"
    }
  ]
}
```

## Replace a dashboard with signoz_update_dashboard

### 1. replace_dashboard — Replace a dashboard with a single CPU time series panel

Update is a full replacement: every field is written as sent, so fetch the dashboard first and resend the unchanged parts. `name` must equal the dashboard's current name.

```json
{
  "name": "host-cpu",
  "schemaVersion": "v6",
  "spec": {
    "display": {
      "description": "CPU time by state",
      "name": "Host CPU"
    },
    "duration": "3h",
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "content": {
                "$ref": "#/spec/panels/cpu-time"
              },
              "height": 7,
              "width": 12,
              "x": 0,
              "y": 0
            }
          ]
        }
      }
    ],
    "links": [],
    "panels": {
      "cpu-time": {
        "kind": "Panel",
        "spec": {
          "display": {
            "description": "Rate of system.cpu.time by state",
            "name": "CPU time"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TimeSeriesPanel",
            "spec": {
              "chartAppearance": {
                "fillMode": "gradient"
              },
              "legend": {
                "position": "right"
              }
            }
          },
          "queries": [
            {
              "kind": "time_series",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "aggregations": [
                      {
                        "metricName": "system.cpu.time",
                        "spaceAggregation": "sum",
                        "timeAggregation": "rate"
                      }
                    ],
                    "groupBy": [
                      {
                        "fieldContext": "attribute",
                        "fieldDataType": "string",
                        "name": "state"
                      }
                    ],
                    "legend": "{{state}}",
                    "name": "A",
                    "signal": "metrics"
                  }
                }
              }
            }
          ]
        }
      }
    },
    "refreshInterval": "30s",
    "variables": []
  },
  "tags": [
    {
      "key": "team",
      "value": "infra"
    }
  ]
}
```

## Patch a dashboard with signoz_patch_dashboard

### 1. add_panel — Add a panel and place it

Adding a panel takes two operations: one adds the panel under `/spec/panels/<id>`, the other appends a grid item that references it to the first layout. A panel that no layout places is accepted but not shown.

```json
[
  {
    "op": "add",
    "path": "/spec/panels/error-rate",
    "value": {
      "kind": "Panel",
      "spec": {
        "display": {
          "description": "Errored spans per time bucket",
          "name": "Error count"
        },
        "links": [],
        "plugin": {
          "kind": "signoz/TimeSeriesPanel",
          "spec": {}
        },
        "queries": [
          {
            "kind": "time_series",
            "spec": {
              "name": "A",
              "plugin": {
                "kind": "signoz/BuilderQuery",
                "spec": {
                  "aggregations": [
                    {
                      "expression": "count()"
                    }
                  ],
                  "filter": {
                    "expression": "has_error = true"
                  },
                  "name": "A",
                  "signal": "traces"
                }
              }
            }
          }
        ]
      }
    }
  },
  {
    "op": "add",
    "path": "/spec/layouts/0/spec/items/-",
    "value": {
      "content": {
        "$ref": "#/spec/panels/error-rate"
      },
      "height": 6,
      "width": 12,
      "x": 0,
      "y": 7
    }
  }
]
```

### 2. rename — Rename a dashboard

Replaces the display name without resending anything else.

```json
[
  {
    "op": "replace",
    "path": "/spec/display/name",
    "value": "Service Overview (production)"
  }
]
```
