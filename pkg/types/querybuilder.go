package types

import "fmt"

// QueryPayload is struct used as payload the Query Builder v5 JSON schema
type QueryPayload struct {
	SchemaVersion  string         `json:"schemaVersion"`
	Start          int64          `json:"start"`
	End            int64          `json:"end"`
	RequestType    string         `json:"requestType"`
	CompositeQuery CompositeQuery `json:"compositeQuery"`
	FormatOptions  FormatOptions  `json:"formatOptions"`
	Variables      map[string]any `json:"variables"`
}

type CompositeQuery struct {
	Queries []Query `json:"queries"`
}

type Query struct {
	Type string    `json:"type"`
	Spec QuerySpec `json:"spec"`
}

type QuerySpec struct {
	Name         string        `json:"name"`
	Signal       string        `json:"signal"`
	StepInterval *string       `json:"stepInterval,omitempty"`
	Disabled     bool          `json:"disabled"`
	Filter       *Filter       `json:"filter,omitempty"`
	Limit        int           `json:"limit"`
	Offset       int           `json:"offset"`
	Order        []Order       `json:"order"`
	Having       Having        `json:"having"`
	SelectFields []SelectField `json:"selectFields"`
}

type Order struct {
	Key       Key    `json:"key"`
	Direction string `json:"direction"`
}

type Key struct {
	Name string `json:"name"`
}

type Having struct {
	Expression string `json:"expression"`
}

type Filter struct {
	Expression string `json:"expression"`
}

type SelectField struct {
	Name          string `json:"name"`
	FieldDataType string `json:"fieldDataType"`
	Signal        string `json:"signal"`
	FieldContext  string `json:"fieldContext,omitempty"`
}

type FormatOptions struct {
	FormatTableResultForUI bool `json:"formatTableResultForUI"`
	FillGaps               bool `json:"fillGaps"`
}

// Validate performs necessary validation for required fields
// this indirectly helps LLMs to build right payload.
// if there is an error LLM checks the error and fix.
func (q *QueryPayload) Validate() error {
	if q.SchemaVersion == "" {
		return fmt.Errorf("missing required field: schemaVersion")
	}
	if q.RequestType == "" {
		return fmt.Errorf("missing required field: requestType")
	}
	if len(q.CompositeQuery.Queries) == 0 {
		return fmt.Errorf("missing or empty compositeQuery.queries")
	}
	if q.Start == 0 {
		return fmt.Errorf("missing or zero start timestamp")
	}
	if q.End == 0 {
		return fmt.Errorf("missing or zero end timestamp")
	}
	return nil
}

// BuildQueryHelpText provides guidance for building SigNoz queries
func BuildQueryHelpText(signal, queryType string) string {
	switch signal {
	case "traces":
		return buildTracesHelpText(queryType)
	case "logs":
		return buildLogsHelpText(queryType)
	case "metrics":
		return buildMetricsHelpText(queryType)
	default:
		return buildGeneralHelpText(queryType)
	}
}

func buildTracesHelpText(queryType string) string {
	switch queryType {
	case "fields":
		return `Available trace fields:
- service.name (resource context)
- operation (span name)
- duration_nano (span duration in nanoseconds)
- http_method (span attribute)
- http_status_code (span attribute)
- http_url (span attribute)
- error (boolean, true if span has error)
- trace_id (trace identifier)
- span_id (span identifier)
- parent_span_id (parent span identifier)
- timestamp (span timestamp)
- resource attributes: service.version, deployment.environment, etc.
- span attributes: http.method, http.status_code, db.system, etc.`
	case "structure":
		return `Trace query structure:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "traces",
        "disabled": false,
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": ""},
        "selectFields": [
          {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
          {"name": "operation", "fieldDataType": "string", "signal": "traces"},
          {"name": "duration_nano", "fieldDataType": "int64", "signal": "traces", "fieldContext": "span"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	case "examples":
		return `Example trace queries:

1. Recent slow traces:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "traces",
        "disabled": false,
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "duration_nano"}, "direction": "desc"}],
        "having": {"expression": "duration_nano > 1000000000"},
        "selectFields": [
          {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
          {"name": "operation", "fieldDataType": "string", "signal": "traces"},
          {"name": "duration_nano", "fieldDataType": "int64", "signal": "traces", "fieldContext": "span"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

2. Error traces:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "traces",
        "disabled": false,
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": "error = true"},
        "selectFields": [
          {"name": "service.name", "fieldDataType": "string", "signal": "traces", "fieldContext": "resource"},
          {"name": "operation", "fieldDataType": "string", "signal": "traces"},
          {"name": "http_status_code", "fieldDataType": "int32", "signal": "traces", "fieldContext": "span"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	default:
		return buildTracesHelpText("fields") + "\n\n" + buildTracesHelpText("structure") + "\n\n" + buildTracesHelpText("examples")
	}
}

func buildLogsHelpText(queryType string) string {
	switch queryType {
	case "fields":
		return `Available log fields:
- timestamp (log timestamp)
- severity_text (log level: DEBUG, INFO, WARN, ERROR, FATAL)
- body (log message)
- service.name (resource attribute)
- trace_id (trace identifier if linked)
- span_id (span identifier if linked)
- resource attributes: service.version, deployment.environment, etc.
- log attributes: custom key-value pairs`
	case "structure":
		return `Log query structure:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "logs",
        "disabled": false,
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": ""},
        "selectFields": [
          {"name": "timestamp", "fieldDataType": "string", "signal": "logs"},
          {"name": "severity_text", "fieldDataType": "string", "signal": "logs"},
          {"name": "body", "fieldDataType": "string", "signal": "logs"},
          {"name": "service.name", "fieldDataType": "string", "signal": "logs", "fieldContext": "resource"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	case "examples":
		return `Example log queries:

1. Error logs:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "logs",
        "disabled": false,
        "filter": {"expression": "severity_text = 'ERROR'"},
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": ""},
        "selectFields": [
          {"name": "timestamp", "fieldDataType": "string", "signal": "logs"},
          {"name": "severity_text", "fieldDataType": "string", "signal": "logs"},
          {"name": "body", "fieldDataType": "string", "signal": "logs"},
          {"name": "service.name", "fieldDataType": "string", "signal": "logs", "fieldContext": "resource"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}

2. Service-specific logs with text search:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "raw",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "logs",
        "disabled": false,
        "filter": {"expression": "service.name in ['my-service'] AND body CONTAINS 'error'"},
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": ""},
        "selectFields": [
          {"name": "timestamp", "fieldDataType": "string", "signal": "logs"},
          {"name": "severity_text", "fieldDataType": "string", "signal": "logs"},
          {"name": "body", "fieldDataType": "string", "signal": "logs"},
          {"name": "service.name", "fieldDataType": "string", "signal": "logs", "fieldContext": "resource"}
        ]
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	default:
		return buildLogsHelpText("fields") + "\n\n" + buildLogsHelpText("structure") + "\n\n" + buildLogsHelpText("examples")
	}
}

func buildMetricsHelpText(queryType string) string {
	switch queryType {
	case "fields":
		return `Available metric fields:
- timestamp (metric timestamp)
- value (metric value)
- metric_name (name of the metric)
- service.name (resource attribute)
- resource attributes: service.version, deployment.environment, etc.
- metric attributes: custom labels and tags`
	case "structure":
		return `Metric query structure:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "metrics",
        "disabled": false,
        "limit": 10,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "desc"}],
        "having": {"expression": ""},
        "selectFields": [
          {"name": "value", "fieldDataType": "float64", "signal": "metrics"},
          {"name": "metric_name", "fieldDataType": "string", "signal": "metrics"}
        ],
        "stepInterval": "60s"
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	case "examples":
		return `Example metric queries:

1. CPU usage over time:
{
  "schemaVersion": "v1",
  "start": 1704067200000,
  "end": 1758758400000,
  "requestType": "time_series",
  "compositeQuery": {
    "queries": [{
      "type": "builder_query",
      "spec": {
        "name": "A",
        "signal": "metrics",
        "disabled": false,
        "limit": 100,
        "offset": 0,
        "order": [{"key": {"name": "timestamp"}, "direction": "asc"}],
        "having": {"expression": "metric_name = 'cpu_usage'"},
        "selectFields": [
          {"name": "value", "fieldDataType": "float64", "signal": "metrics"},
          {"name": "timestamp", "fieldDataType": "string", "signal": "metrics"}
        ],
        "stepInterval": "60s"
      }
    }]
  },
  "formatOptions": {"formatTableResultForUI": false, "fillGaps": false},
  "variables": {}
}`
	default:
		return buildMetricsHelpText("fields") + "\n\n" + buildMetricsHelpText("structure") + "\n\n" + buildMetricsHelpText("examples")
	}
}

func buildGeneralHelpText(queryType string) string {
	return `SigNoz Query Builder v5 Help

Available signals: traces, logs, metrics

Common query parameters:
- schemaVersion: Always "v1"
- start/end: Timestamps in milliseconds since epoch
- requestType: "raw" for point-in-time data, "time_series" for time-series data
- compositeQuery.queries[].spec.signal: "traces", "logs", or "metrics"
- compositeQuery.queries[].spec.limit: Number of results to return
- compositeQuery.queries[].spec.order: Sort order for results
- compositeQuery.queries[].spec.selectFields: Fields to include in results
- compositeQuery.queries[].spec.having: Filter expression for aggregated results

Use signoz_query_helper with specific signal type for detailed field information.`
}

// BuildLogsQueryPayload creates a QueryPayload for logs queries
func BuildLogsQueryPayload(startTime, endTime int64, filterExpression string, limit int) *QueryPayload {
	return &QueryPayload{
		SchemaVersion: "v1",
		Start:         startTime,
		End:           endTime,
		RequestType:   "raw",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:     "A",
						Signal:   "logs",
						Disabled: false,
						Filter:   &Filter{Expression: filterExpression},
						Limit:    limit,
						Offset:   0,
						Order: []Order{
							{Key: Key{Name: "timestamp"}, Direction: "desc"},
						},
						Having: Having{Expression: ""},
						SelectFields: []SelectField{
							{Name: "timestamp", FieldDataType: "string", Signal: "logs"},
							{Name: "severity_text", FieldDataType: "string", Signal: "logs"},
							{Name: "body", FieldDataType: "string", Signal: "logs"},
							{Name: "service.name", FieldDataType: "string", Signal: "logs", FieldContext: "resource"},
						},
					},
				},
			},
		},
		FormatOptions: FormatOptions{
			FormatTableResultForUI: false,
			FillGaps:               false,
		},
		Variables: map[string]any{},
	}
}
