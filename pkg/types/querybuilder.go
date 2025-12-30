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
	Signal       string        `json:"signal,omitempty"` // For builder_query
	StepInterval *int64        `json:"stepInterval,omitempty"`
	Disabled     bool          `json:"disabled"`
	Filter       *Filter       `json:"filter,omitempty"`       // For builder_query
	Limit        int           `json:"limit,omitempty"`        // For builder_query
	Offset       int           `json:"offset,omitempty"`       // For builder_query
	Order        []Order       `json:"order,omitempty"`        // For builder_query
	Having       *Having       `json:"having,omitempty"`       // For builder_query
	SelectFields []SelectField `json:"selectFields,omitempty"` // For builder_query
	Aggregations []any         `json:"aggregations,omitempty"` // For builder_query
	// PromQL-specific fields
	Query  string `json:"query,omitempty"`  // For promql - the PromQL query string
	Legend string `json:"legend,omitempty"` // For promql - legend format (e.g., "{{host}}")
	Stats  bool   `json:"stats,omitempty"`  // For promql
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
		q.SchemaVersion = "v1"
	}

	if q.Start == 0 || q.End == 0 {
		return fmt.Errorf("missing start or end timestamp")
	}
	if len(q.CompositeQuery.Queries) == 0 {
		return fmt.Errorf("missing or empty compositeQuery.queries")
	}

	for i, query := range q.CompositeQuery.Queries {
		queryName := query.Spec.Name
		if queryName == "" {
			queryName = fmt.Sprintf("query at position %d", i+1)
		}

		spec := query.Spec

		switch query.Type {
		case "builder_query":
			signal := spec.Signal
			if signal == "" {
				return fmt.Errorf("%s: builder_query requires signal field", queryName)
			}

			switch signal {
			case "metrics":
				if q.RequestType != "time_series" {
					q.RequestType = "time_series"
				}
				if spec.StepInterval == nil || *spec.StepInterval <= 0 {
					def := int64(60)
					spec.StepInterval = &def
				}

			case "traces":
				if q.RequestType != "raw" && q.RequestType != "trace" {
					q.RequestType = "raw"
				}
				spec.StepInterval = nil

			case "logs":
				if q.RequestType != "raw" {
					q.RequestType = "raw"
				}
				spec.StepInterval = nil

			default:
				return fmt.Errorf("%s: unknown signal type '%s'", queryName, signal)
			}

		case "promql":
			// PromQL queries require a query string
			if spec.Query == "" {
				return fmt.Errorf("%s: promql query requires 'query' field with PromQL query string", queryName)
			}
			// PromQL queries should use time_series request type
			if q.RequestType != "time_series" {
				q.RequestType = "time_series"
			}
			// Clear builder_query-only fields to avoid sending them to SigNoz
			spec.Signal = ""
			spec.Filter = nil
			spec.Limit = 0
			spec.Offset = 0
			spec.Order = nil
			spec.Having = nil
			spec.SelectFields = nil
			spec.Aggregations = nil
			spec.StepInterval = nil

		default:
			// Unknown query type - allow it to pass through (might be supported by SigNoz)
			continue
		}

		q.CompositeQuery.Queries[i].Spec = spec
	}

	if q.RequestType == "" {
		q.RequestType = "raw"
	}

	return nil
}

// BuildLogsQueryPayload creates a QueryPayload for logs queries
func BuildLogsQueryPayload(startTime, endTime int64, filterExpression string, limit int, offset int) *QueryPayload {
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
						Offset:   offset,
						Order: []Order{
							{Key: Key{Name: "timestamp"}, Direction: "desc"},
						},
						Having: &Having{Expression: ""},
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

func BuildTracesQueryPayload(startTime, endTime int64, filterExpression string, limit int) *QueryPayload {
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
						Signal:   "traces",
						Disabled: false,
						Filter:   &Filter{Expression: filterExpression},
						Limit:    limit,
						Offset:   0,
						Order: []Order{
							{Key: Key{Name: "timestamp"}, Direction: "desc"},
						},
						Having: &Having{Expression: ""},
						SelectFields: []SelectField{
							{Name: "traceID", FieldDataType: "string", Signal: "traces"},
							{Name: "spanID", FieldDataType: "string", Signal: "traces"},
							{Name: "parentSpanID", FieldDataType: "string", Signal: "traces"},
							{Name: "service.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "name", FieldDataType: "string", Signal: "traces"},
							{Name: "durationNano", FieldDataType: "int64", Signal: "traces"},
							{Name: "timestamp", FieldDataType: "string", Signal: "traces"},
							{Name: "hasError", FieldDataType: "bool", Signal: "traces"},
							{Name: "statusCode", FieldDataType: "string", Signal: "traces"},
							{Name: "statusCodeString", FieldDataType: "string", Signal: "traces"},
							{Name: "httpMethod", FieldDataType: "string", Signal: "traces"},
							{Name: "httpUrl", FieldDataType: "string", Signal: "traces"},
							{Name: "spanKind", FieldDataType: "string", Signal: "traces"},
							{Name: "rpcMethod", FieldDataType: "string", Signal: "traces"},
							{Name: "kind", FieldDataType: "int32", Signal: "traces"},
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
