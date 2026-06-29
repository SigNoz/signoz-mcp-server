package dashboard

const WidgetExamples = `
Worked v6 (Perses) dashboard examples. Every payload below is a real widget created against a live SigNoz instance and read back (GET), so each field is server-accepted, not guessed. Each block is one panel — the object you place in the spec.panels map under a panel id (shown unkeyed here). Copy the structure, not the literal attribute names.

For the rules behind these — which panel and query type to choose, layout, legends, the one-query-per-panel constraint, and variables — read signoz://dashboard/instructions and signoz://dashboard/widgets-instructions. For the exact field set, use the create/update tool's JSON Schema.

Quick reference (all illustrated below):
- Aggregations: count(), p50/p95/p99(field), avg/sum/min/max(field). Name a column with the aggregation's "alias" field (NOT "as '...'" inside the expression). Multiple aggregations = multiple entries in the aggregations[] array.
- Filters: a single filter.expression string. Operators include =, !=, IN, AND, EXISTS. Reference variables as $name (omit the $ and it matches the literal string instead).

=== EXAMPLES ===

--- timeseries Widgets ---

Example: Token Usage (Timeseries with a formula over disabled base queries)

A single widget — one entry from spec.panels (keyed by a panel id, e.g. "token-usage"); a layout item links to it via content.$ref. It is a signoz/TimeSeriesPanel whose signoz/CompositeQuery sums prompt + completion tokens: two builder_query entries (A, B) marked "disabled": true and a builder_formula F1 ("A + B") left enabled, so only the summed series renders (the pattern for any computed series). Filters reference $service_name / $language / $llm_model, which are DynamicVariables declared at the dashboard level — see signoz://dashboard/instructions for variable wiring.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Token Usage", "description": "Total LLM tokens (prompt + completion) over time" },
    "plugin": {
      "kind": "signoz/TimeSeriesPanel",
      "spec": { "legend": { "position": "bottom", "customColors": { "F1": "#eccd03" } } }
    },
    "queries": [
      {
        "kind": "time_series",
        "spec": {
          "name": "token_usage",
          "plugin": {
            "kind": "signoz/CompositeQuery",
            "spec": {
              "queries": [
                {
                  "type": "builder_query",
                  "spec": {
                    "signal": "traces",
                    "name": "A",
                    "disabled": true,
                    "aggregations": [ { "expression": "sum(llm.token_count.prompt)", "alias": "prompt_tokens" } ],
                    "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model" }
                  }
                },
                {
                  "type": "builder_query",
                  "spec": {
                    "signal": "traces",
                    "name": "B",
                    "disabled": true,
                    "aggregations": [ { "expression": "sum(llm.token_count.completion)", "alias": "completion_tokens" } ],
                    "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model" }
                  }
                },
                {
                  "type": "builder_formula",
                  "spec": { "name": "F1", "expression": "A + B", "legend": "F1", "disabled": false }
                }
              ]
            }
          }
        }
      }
    ]
  }
}

Example: Latency (P95) (single builder query — direct, no CompositeQuery)

A timeseries widget with just one query and no formula, so the panel's single query sets its plugin directly to signoz/BuilderQuery — no signoz/CompositeQuery wrapper (contrast Example 1, which uses CompositeQuery to combine two builder queries + a formula). Shows a p95(duration_nano) aggregation with an alias, a static per-query legend ("P95"), and the y-axis unit via the panel plugin's formatting.unit ("ns"). Filters reference the same dashboard-level DynamicVariables as Example 1.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Latency (P95)", "description": "95th-percentile request duration over time" },
    "plugin": {
      "kind": "signoz/TimeSeriesPanel",
      "spec": { "legend": { "position": "bottom" }, "formatting": { "unit": "ns" } }
    },
    "queries": [
      {
        "kind": "time_series",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "p95(duration_nano)", "alias": "p95_latency" } ],
              "legend": "P95",
              "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model" }
            }
          }
        }
      }
    ]
  }
}

Example: Number of Requests (single builder query with groupBy — per-series legend)

A timeseries widget that groups a count() of trace spans by the service.name resource attribute, so each service renders as its own line. Like Example 2 it has a single query and no formula, so the query's plugin is signoz/BuilderQuery directly (no signoz/CompositeQuery wrapper). The groupBy entry uses "name" (the v6 field — NOT the old "key") alongside fieldContext/fieldDataType/signal, and legend "{{service.name}}" templates each series label from that groupBy key (set legend whenever a series-producing panel has a groupBy, else SigNoz shows raw query ids). The filter combines dashboard-level DynamicVariables ($service_name / $language / $llm_model) with a static condition (llm.provider = 'anthropic'). Legend sits at the bottom via the panel plugin; y-axis unit and soft min/max are left to server defaults.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Number of Requests", "description": "Request count over time, grouped by service (Anthropic LLM calls)" },
    "plugin": {
      "kind": "signoz/TimeSeriesPanel",
      "spec": { "legend": { "position": "bottom" } }
    },
    "queries": [
      {
        "kind": "time_series",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "count()" } ],
              "groupBy": [ { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" } ],
              "legend": "{{service.name}}",
              "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model AND llm.provider = 'anthropic'" }
            }
          }
        }
      }
    ]
  }
}

--- list Widgets ---

Example: Errors (list panel — raw trace rows, newest first)

A list widget rendering raw error spans with no aggregation: the panel plugin is signoz/ListPanel and the query's request type is "raw" (not time_series). selectFields on the ListPanel spec defines the displayed columns; the raw builder query selects the same fields, filters has_error together with the $service_name DynamicVariable, orders by timestamp desc, and caps rows with limit (the old "pageSize"). The old columnWidths / selectedLogFields / soft min-max / yAxisUnit have no v6 equivalent here and are dropped.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Errors", "description": "Recent error spans (Anthropic API)" },
    "plugin": {
      "kind": "signoz/ListPanel",
      "spec": {
        "selectFields": [
          { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" },
          { "name": "name", "fieldContext": "span", "fieldDataType": "string", "signal": "traces" },
          { "name": "duration_nano", "fieldContext": "span", "signal": "traces" },
          { "name": "http_method", "fieldContext": "span", "signal": "traces" },
          { "name": "response_status_code", "fieldContext": "span", "signal": "traces" }
        ]
      }
    },
    "queries": [
      {
        "kind": "raw",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "filter": { "expression": "has_error = true AND service.name IN $service_name" },
              "selectFields": [
                { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" },
                { "name": "name", "fieldContext": "span", "fieldDataType": "string", "signal": "traces" },
                { "name": "duration_nano", "fieldContext": "span", "signal": "traces" },
                { "name": "http_method", "fieldContext": "span", "signal": "traces" },
                { "name": "response_status_code", "fieldContext": "span", "signal": "traces" }
              ],
              "order": [ { "key": { "name": "timestamp" }, "direction": "desc" } ],
              "limit": 10
            }
          }
        }
      }
    ]
  }
}

Example: Errors (list panel — same shape, structured filter collapsed to an expression)

Identical in structure to the previous list example. Worth noting: the v1 source carried BOTH a free-text filter and a structured filters.items block; v6 has only filter.expression, so the two conditions (has_error and the $service_name DynamicVariable) collapse into one expression. Display columns and the raw query mirror the previous example.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Errors", "description": "Recent error spans (Autogen)" },
    "plugin": {
      "kind": "signoz/ListPanel",
      "spec": {
        "selectFields": [
          { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" },
          { "name": "name", "fieldContext": "span", "fieldDataType": "string", "signal": "traces" },
          { "name": "duration_nano", "fieldContext": "span", "signal": "traces" },
          { "name": "http_method", "fieldContext": "span", "signal": "traces" },
          { "name": "response_status_code", "fieldContext": "span", "signal": "traces" }
        ]
      }
    },
    "queries": [
      {
        "kind": "raw",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "filter": { "expression": "service.name IN $service_name AND has_error = true" },
              "selectFields": [
                { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" },
                { "name": "name", "fieldContext": "span", "fieldDataType": "string", "signal": "traces" },
                { "name": "duration_nano", "fieldContext": "span", "signal": "traces" },
                { "name": "http_method", "fieldContext": "span", "signal": "traces" },
                { "name": "response_status_code", "fieldContext": "span", "signal": "traces" }
              ],
              "order": [ { "key": { "name": "timestamp" }, "direction": "desc" } ],
              "limit": 10
            }
          }
        }
      }
    ]
  }
}

Example: Logs (list panel on the logs signal — multi-key ordering)

A list widget over logs rather than traces: signal is "logs" and selectFields use fieldContext "log" (timestamp, body). The raw query orders by two keys — timestamp desc then id desc — to keep ordering stable when timestamps tie. Filter references the $service_name DynamicVariable (declared with signal "logs" so its dropdown derives from log data).

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Logs", "description": "Recent logs (Autogen)" },
    "plugin": {
      "kind": "signoz/ListPanel",
      "spec": {
        "selectFields": [
          { "name": "timestamp", "fieldContext": "log", "signal": "logs" },
          { "name": "body", "fieldContext": "log", "fieldDataType": "string", "signal": "logs" }
        ]
      }
    },
    "queries": [
      {
        "kind": "raw",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "logs",
              "name": "A",
              "filter": { "expression": "service.name IN $service_name" },
              "selectFields": [
                { "name": "timestamp", "fieldContext": "log", "signal": "logs" },
                { "name": "body", "fieldContext": "log", "fieldDataType": "string", "signal": "logs" }
              ],
              "order": [
                { "key": { "name": "timestamp" }, "direction": "desc" },
                { "key": { "name": "id" }, "direction": "desc" }
              ],
              "limit": 10
            }
          }
        }
      }
    ]
  }
}

--- pie Widgets ---

Example: Model Distribution (pie panel — scalar count grouped by an attribute)

A pie widget showing each model's share of requests. The panel plugin is signoz/PieChartPanel and the query's request type is "scalar" (one aggregated value per group — contrast time_series for graphs and raw for lists). count() is grouped by the llm.model_name attribute (fieldContext "attribute" — the v6 mapping of the old "tag" type), and legend "{{llm.model_name}}" labels each slice. The filter keeps the $service_name DynamicVariable plus static llm.provider and an EXISTS guard so requests without a model name are excluded (the v1 source had a duplicated/garbled EXISTS that collapses to one).

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Model Distribution", "description": "Share of requests per model (Anthropic API)" },
    "plugin": {
      "kind": "signoz/PieChartPanel",
      "spec": { "legend": { "position": "bottom" } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "count()" } ],
              "groupBy": [ { "name": "llm.model_name", "fieldContext": "attribute", "fieldDataType": "string", "signal": "traces" } ],
              "legend": "{{llm.model_name}}",
              "filter": { "expression": "service.name IN $service_name AND llm.provider = 'anthropic' AND llm.model_name EXISTS" }
            }
          }
        }
      }
    ]
  }
}

Example: Model Distribution (pie panel — different grouping attribute)

Same pie shape as the previous example, but Autogen records the model under the gen_ai.request.model attribute, so both the groupBy key and the legend template change accordingly. Filter keeps $service_name and an EXISTS guard on the grouping attribute.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Model Distribution", "description": "Share of requests per model (Autogen)" },
    "plugin": {
      "kind": "signoz/PieChartPanel",
      "spec": { "legend": { "position": "bottom" } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "count()" } ],
              "groupBy": [ { "name": "gen_ai.request.model", "fieldContext": "attribute", "fieldDataType": "string", "signal": "traces" } ],
              "legend": "{{gen_ai.request.model}}",
              "filter": { "expression": "service.name IN $service_name AND gen_ai.request.model EXISTS" }
            }
          }
        }
      }
    ]
  }
}

Example: Model Distribution (pie panel — Azure OpenAI, same llm.model_name attribute)

Same pie shape again. Azure OpenAI also records the model under llm.model_name, so this matches the Anthropic example minus the provider-specific static filter — just $service_name plus an EXISTS guard on llm.model_name.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Model Distribution", "description": "Share of requests per model (Azure OpenAI API)" },
    "plugin": {
      "kind": "signoz/PieChartPanel",
      "spec": { "legend": { "position": "bottom" } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "count()" } ],
              "groupBy": [ { "name": "llm.model_name", "fieldContext": "attribute", "fieldDataType": "string", "signal": "traces" } ],
              "legend": "{{llm.model_name}}",
              "filter": { "expression": "service.name IN $service_name AND llm.model_name EXISTS" }
            }
          }
        }
      }
    ]
  }
}

--- table Widgets ---

Example: Services and Languages (table panel — multi-key groupBy, aliased aggregation, no variables)

A table widget counting spans per (service.name, SDK language) pair. The panel plugin is signoz/TablePanel and the query's request type is "scalar". The single aggregation carries an alias via the {expression, alias} object (count() → "Count of Spans") instead of the v1 "count() as '…'" string. groupBy lists both columns; the old "isColumn" flag has no v6 equivalent and is dropped. This panel takes no dashboard variables.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Services and Languages", "description": "Span counts per service and SDK language (Anthropic API)" },
    "plugin": {
      "kind": "signoz/TablePanel",
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
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "count()", "alias": "Count of Spans" } ],
              "groupBy": [
                { "name": "service.name", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" },
                { "name": "telemetry.sdk.language", "fieldContext": "resource", "fieldDataType": "string", "signal": "traces" }
              ],
              "filter": { "expression": "llm.provider = 'anthropic' AND telemetry.sdk.language EXISTS" }
            }
          }
        }
      }
    ]
  }
}

Example: Agents (table panel — two aggregations in one query + column units)

A table widget with two aggregations per row: count() aliased "Requests" and avg(duration_nano) aliased "Latency". In v6 these are TWO entries in the aggregations array (the v1 source crammed both into one "… as … … as …" string). Column units live under the panel plugin's formatting.columnUnits, keyed by the aggregation ALIAS ("Latency": "ns"). groupBy is the gen_ai.agent.name attribute, and the filter combines $service_name with EXISTS and an operation guard.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Agents", "description": "Per-agent request count and average latency (Autogen)" },
    "plugin": {
      "kind": "signoz/TablePanel",
      "spec": { "formatting": { "columnUnits": { "Latency": "ns" } } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [
                { "expression": "count()", "alias": "Requests" },
                { "expression": "avg(duration_nano)", "alias": "Latency" }
              ],
              "groupBy": [ { "name": "gen_ai.agent.name", "fieldContext": "attribute", "fieldDataType": "string", "signal": "traces" } ],
              "filter": { "expression": "service.name IN $service_name AND gen_ai.agent.name EXISTS AND gen_ai.operation.name = 'invoke_agent'" }
            }
          }
        }
      }
    ]
  }
}

Example: Tools (table panel — same two-aggregation shape, tool dimension)

Same table shape as Agents. Autogen records tool calls under the gen_ai.tool.name attribute and the operation gen_ai.operation.name = 'execute_tool', so only the groupBy key and the filter's EXISTS/operation conditions change; the two aliased aggregations and the "Latency": "ns" column unit are identical.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Tools", "description": "Per-tool request count and average latency (Autogen)" },
    "plugin": {
      "kind": "signoz/TablePanel",
      "spec": { "formatting": { "columnUnits": { "Latency": "ns" } } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "A",
          "plugin": {
            "kind": "signoz/BuilderQuery",
            "spec": {
              "signal": "traces",
              "name": "A",
              "aggregations": [
                { "expression": "count()", "alias": "Requests" },
                { "expression": "avg(duration_nano)", "alias": "Latency" }
              ],
              "groupBy": [ { "name": "gen_ai.tool.name", "fieldContext": "attribute", "fieldDataType": "string", "signal": "traces" } ],
              "filter": { "expression": "service.name IN $service_name AND gen_ai.tool.name EXISTS AND gen_ai.operation.name = 'execute_tool'" }
            }
          }
        }
      }
    ]
  }
}

--- value Widgets ---

Example: Input Tokens (value panel — single scalar aggregation)

A value/KPI widget reducing a series to one number. The panel plugin is signoz/NumberPanel and the query's request type is "scalar". A single sum(llm.token_count.prompt) aggregation, filtered by the three DynamicVariables ($service_name / $language / $llm_model). No legend or unit needed (count of tokens is unitless).

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Input Tokens", "description": "Total prompt tokens (Anthropic API)" },
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
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "sum(llm.token_count.prompt)" } ],
              "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model" }
            }
          }
        }
      }
    ]
  }
}

Example: Output Tokens (value panel — same shape, completion tokens)

Identical to the Input Tokens value panel, summing llm.token_count.completion instead of llm.token_count.prompt. Same NumberPanel, scalar request type, and the same three DynamicVariables in the filter.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Output Tokens", "description": "Total completion tokens (Anthropic API)" },
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
              "signal": "traces",
              "name": "A",
              "aggregations": [ { "expression": "sum(llm.token_count.completion)" } ],
              "filter": { "expression": "service.name IN $service_name AND telemetry.sdk.language IN $language AND llm.model_name IN $llm_model" }
            }
          }
        }
      }
    ]
  }
}

Example: Error Rate (value panel — composite query with a ratio formula)

A value widget computing errored ÷ total as a single percentage. Because it combines two queries with a formula, the query uses signoz/CompositeQuery (like the Token Usage graph): builder_query A (errored, disabled) and B (non-errored, disabled), plus builder_formula F1 = "A / (A + B)" left enabled so only the ratio renders. The panel is a signoz/NumberPanel with formatting.unit "percentunit". has_error is a boolean, so the filters use unquoted true/false.

{
  "kind": "Panel",
  "spec": {
    "display": { "name": "Error Rate", "description": "Share of errored spans (Anthropic API)" },
    "plugin": {
      "kind": "signoz/NumberPanel",
      "spec": { "formatting": { "unit": "percentunit" } }
    },
    "queries": [
      {
        "kind": "scalar",
        "spec": {
          "name": "error_rate",
          "plugin": {
            "kind": "signoz/CompositeQuery",
            "spec": {
              "queries": [
                {
                  "type": "builder_query",
                  "spec": {
                    "signal": "traces",
                    "name": "A",
                    "disabled": true,
                    "aggregations": [ { "expression": "count()" } ],
                    "filter": { "expression": "has_error = true AND service.name IN $service_name" }
                  }
                },
                {
                  "type": "builder_query",
                  "spec": {
                    "signal": "traces",
                    "name": "B",
                    "disabled": true,
                    "aggregations": [ { "expression": "count()" } ],
                    "filter": { "expression": "has_error = false AND service.name IN $service_name" }
                  }
                },
                {
                  "type": "builder_formula",
                  "spec": { "name": "F1", "expression": "A / (A + B)", "legend": "Error Rate", "disabled": false }
                }
              ]
            }
          }
        }
      }
    ]
  }
}

`
