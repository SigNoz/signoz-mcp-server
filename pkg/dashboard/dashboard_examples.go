package dashboard

// DashboardExamples is the complete-dashboard example guide served at
// signoz://dashboard/examples: whole v6 (Perses) create payloads (top-level
// schemaVersion/generateName/tags/spec) assembling panels + layouts, verified
// against a live SigNoz instance. Complements signoz://dashboard/widgets-examples,
// which shows single panels in isolation.
const DashboardExamples = `
Complete v6 (Perses) dashboard payloads for signoz_create_dashboard. Every example below was created against a live SigNoz instance and read back, so each field is server-accepted. Copy the structure, not the literal metric/attribute names. For single-panel structure and choosing a panel/query type, read signoz://dashboard/widgets-examples and signoz://dashboard/widgets-instructions; for layout and variable rules read signoz://dashboard/instructions.

Payload envelope: {schemaVersion:"v6", generateName:true, tags:[...], spec:{display, variables, panels, layouts, links:[]}}. Do NOT set the top-level "name" (generateName lets the server derive it). spec.links and every panel's spec.links are required (use []).

Metrics Query Builder shape (used by all examples below):
- aggregations[] entries are OBJECTS, not the {expression} string form used for logs/traces: {metricName, temporality, timeAggregation, spaceAggregation[, reduceTo]}.
  - metricName: the metric, e.g. system.cpu.time.
  - temporality: cumulative | delta | unspecified.
  - timeAggregation: per-series temporal rollup (rate, sum, avg, min, max, count, increase, latest).
  - spaceAggregation: cross-series combine (sum, avg, min, max, count, p50..p99).
  - reduceTo: value/number panels only — collapse the series to one scalar (sum, avg, last, min, max, count, median).
- groupBy[] entries: {name, fieldContext:"attribute", fieldDataType:"string", signal:"metrics"}; set legend "{{name}}".
- order key must be the composed aggregation expression spaceAggregation(timeAggregation(metricName)), e.g. sum(rate(system.cpu.time)) — NOT the bare metric name (rejected); __result or a groupBy key also work.
- Timeseries panels use query kind "time_series"; value/number and pie use kind "scalar".

=== EXAMPLES ===

--- 1. Timeseries grouped by an attribute ---
A single signoz/TimeSeriesPanel over system.cpu.time, grouped by the state attribute (one line per state via legend "{{state}}").

{
  "schemaVersion": "v6",
  "generateName": true,
  "tags": [
    {
      "key": "team",
      "value": "infra"
    }
  ],
  "spec": {
    "display": {
      "name": "System CPU Time",
      "description": "Example: system.cpu.time grouped by state"
    },
    "variables": [],
    "links": [],
    "panels": {
      "cpu-time": {
        "kind": "Panel",
        "spec": {
          "display": {
            "name": "System CPU Time",
            "description": "CPU time by state"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TimeSeriesPanel",
            "spec": {
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
                    "signal": "metrics",
                    "name": "A",
                    "aggregations": [
                      {
                        "metricName": "system.cpu.time",
                        "temporality": "cumulative",
                        "timeAggregation": "rate",
                        "spaceAggregation": "sum"
                      }
                    ],
                    "groupBy": [
                      {
                        "name": "state",
                        "fieldContext": "attribute",
                        "fieldDataType": "string",
                        "signal": "metrics"
                      }
                    ],
                    "legend": "{{state}}",
                    "order": [
                      {
                        "key": {
                          "name": "sum(rate(system.cpu.time))"
                        },
                        "direction": "desc"
                      }
                    ],
                    "limit": 100
                  }
                }
              }
            }
          ]
        }
      }
    },
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "x": 0,
              "y": 0,
              "width": 12,
              "height": 7,
              "content": {
                "$ref": "#/spec/panels/cpu-time"
              }
            }
          ]
        }
      }
    ]
  }
}

--- 2. Same panel with a dynamic variable used as a filter ---
Example 1 plus a dynamic $state variable (ListVariable / signoz/DynamicVariable, signal metrics, attribute "state") wired into the panel via filter.expression "state IN $state". The variable's spec.name is the $state handle; plugin.spec.name is the attribute key. allowMultiple + allowAllValue let it drive an IN filter.

{
  "schemaVersion": "v6",
  "generateName": true,
  "tags": [
    {
      "key": "team",
      "value": "infra"
    }
  ],
  "spec": {
    "display": {
      "name": "System CPU Time by State",
      "description": "Example: CPU time with dynamic state variable filter"
    },
    "variables": [
      {
        "kind": "ListVariable",
        "spec": {
          "name": "state",
          "display": {
            "name": "State",
            "description": "Filter by CPU state"
          },
          "allowMultiple": true,
          "allowAllValue": true,
          "plugin": {
            "kind": "signoz/DynamicVariable",
            "spec": {
              "name": "state",
              "signal": "metrics"
            }
          }
        }
      }
    ],
    "links": [],
    "panels": {
      "cpu-time": {
        "kind": "Panel",
        "spec": {
          "display": {
            "name": "System CPU Time",
            "description": "CPU time by state"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/TimeSeriesPanel",
            "spec": {
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
                    "signal": "metrics",
                    "name": "A",
                    "aggregations": [
                      {
                        "metricName": "system.cpu.time",
                        "temporality": "cumulative",
                        "timeAggregation": "rate",
                        "spaceAggregation": "sum"
                      }
                    ],
                    "groupBy": [
                      {
                        "name": "state",
                        "fieldContext": "attribute",
                        "fieldDataType": "string",
                        "signal": "metrics"
                      }
                    ],
                    "legend": "{{state}}",
                    "filter": {
                      "expression": "state IN $state"
                    },
                    "order": [
                      {
                        "key": {
                          "name": "sum(rate(system.cpu.time))"
                        },
                        "direction": "desc"
                      }
                    ],
                    "limit": 100
                  }
                }
              }
            }
          ]
        }
      }
    },
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "x": 0,
              "y": 0,
              "width": 12,
              "height": 7,
              "content": {
                "$ref": "#/spec/panels/cpu-time"
              }
            }
          ]
        }
      }
    ]
  }
}

--- 3. Number (value) panel reduced with sum ---
A single signoz/NumberPanel over system.network.errors. The scalar reduce is on the aggregation object as reduceTo:"sum" (there is no separate panel-level reduce), and the query kind is "scalar".

{
  "schemaVersion": "v6",
  "generateName": true,
  "tags": [
    {
      "key": "team",
      "value": "infra"
    }
  ],
  "spec": {
    "display": {
      "name": "Network Errors",
      "description": "Example: system.network.errors reduced to sum"
    },
    "variables": [],
    "links": [],
    "panels": {
      "net-errors": {
        "kind": "Panel",
        "spec": {
          "display": {
            "name": "Network Errors",
            "description": "Total network errors"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/NumberPanel",
            "spec": {}
          },
          "queries": [
            {
              "kind": "scalar",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "signal": "metrics",
                    "name": "A",
                    "aggregations": [
                      {
                        "metricName": "system.network.errors",
                        "temporality": "cumulative",
                        "timeAggregation": "rate",
                        "spaceAggregation": "sum",
                        "reduceTo": "sum"
                      }
                    ],
                    "order": [
                      {
                        "key": {
                          "name": "sum(rate(system.network.errors))"
                        },
                        "direction": "desc"
                      }
                    ],
                    "limit": 100
                  }
                }
              }
            }
          ]
        }
      }
    },
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "x": 0,
              "y": 0,
              "width": 6,
              "height": 3,
              "content": {
                "$ref": "#/spec/panels/net-errors"
              }
            }
          ]
        }
      }
    ]
  }
}

--- 4. Two panels: number + pie grouped by an attribute ---
The number panel from example 3 plus a signoz/PieChartPanel on system.network.errors grouped by device, laid out side by side (two non-overlapping grid items in one Grid layout).

{
  "schemaVersion": "v6",
  "generateName": true,
  "tags": [
    {
      "key": "team",
      "value": "infra"
    }
  ],
  "spec": {
    "display": {
      "name": "Network Errors Overview",
      "description": "Example: network errors total + by device"
    },
    "variables": [],
    "links": [],
    "panels": {
      "net-errors-total": {
        "kind": "Panel",
        "spec": {
          "display": {
            "name": "Network Errors",
            "description": "Total network errors"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/NumberPanel",
            "spec": {}
          },
          "queries": [
            {
              "kind": "scalar",
              "spec": {
                "name": "A",
                "plugin": {
                  "kind": "signoz/BuilderQuery",
                  "spec": {
                    "signal": "metrics",
                    "name": "A",
                    "aggregations": [
                      {
                        "metricName": "system.network.errors",
                        "temporality": "cumulative",
                        "timeAggregation": "rate",
                        "spaceAggregation": "sum",
                        "reduceTo": "sum"
                      }
                    ],
                    "order": [
                      {
                        "key": {
                          "name": "sum(rate(system.network.errors))"
                        },
                        "direction": "desc"
                      }
                    ],
                    "limit": 100
                  }
                }
              }
            }
          ]
        }
      },
      "net-errors-by-device": {
        "kind": "Panel",
        "spec": {
          "display": {
            "name": "Network Errors by Device",
            "description": "Errors grouped by device"
          },
          "links": [],
          "plugin": {
            "kind": "signoz/PieChartPanel",
            "spec": {
              "legend": {
                "position": "bottom"
              }
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
                    "signal": "metrics",
                    "name": "A",
                    "aggregations": [
                      {
                        "metricName": "system.network.errors",
                        "temporality": "cumulative",
                        "timeAggregation": "rate",
                        "spaceAggregation": "sum"
                      }
                    ],
                    "groupBy": [
                      {
                        "name": "device",
                        "fieldContext": "attribute",
                        "fieldDataType": "string",
                        "signal": "metrics"
                      }
                    ],
                    "legend": "{{device}}",
                    "order": [
                      {
                        "key": {
                          "name": "sum(rate(system.network.errors))"
                        },
                        "direction": "desc"
                      }
                    ],
                    "limit": 100
                  }
                }
              }
            }
          ]
        }
      }
    },
    "layouts": [
      {
        "kind": "Grid",
        "spec": {
          "items": [
            {
              "x": 0,
              "y": 0,
              "width": 6,
              "height": 3,
              "content": {
                "$ref": "#/spec/panels/net-errors-total"
              }
            },
            {
              "x": 6,
              "y": 0,
              "width": 6,
              "height": 6,
              "content": {
                "$ref": "#/spec/panels/net-errors-by-device"
              }
            }
          ]
        }
      }
    ]
  }
}
`
