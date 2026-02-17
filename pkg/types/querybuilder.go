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
	StepInterval *int64        `json:"stepInterval,omitempty"`
	Disabled     bool          `json:"disabled"`
	Filter       *Filter       `json:"filter,omitempty"`
	Limit        int           `json:"limit"`
	Offset       int           `json:"offset"`
	Order        []Order       `json:"order"`
	Having       Having        `json:"having"`
	SelectFields []SelectField `json:"selectFields"`
	Aggregations []any         `json:"aggregations,omitempty"`
	GroupBy      []SelectField `json:"groupBy,omitempty"`
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

// QueryAggregation represents an aggregation expression for QB v5 queries (logs, traces).
// Example expressions: "count()", "avg(duration)", "p99(durationNano)", "count_distinct(user_id)"
type QueryAggregation struct {
	Expression string `json:"expression"`
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
		if query.Type != "builder_query" {
			continue
		}

		spec := query.Spec
		signal := spec.Signal
		queryName := spec.Name
		if queryName == "" {
			queryName = fmt.Sprintf("query at position %d", i+1)
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
			// Traces support both raw queries and time series aggregations.
			// Don't force requestType=raw, since that breaks aggregation queries.
			if q.RequestType == "" {
				q.RequestType = "raw"
			}
			switch q.RequestType {
			case "raw", "trace":
				spec.StepInterval = nil
			case "scalar":
				spec.StepInterval = nil
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for scalar traces query", queryName)
				}
			case "time_series":
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for time_series traces query", queryName)
				}
				if spec.StepInterval == nil || *spec.StepInterval <= 0 {
					def := int64(60)
					spec.StepInterval = &def
				}
			default:
				return fmt.Errorf("%s: unsupported requestType '%s' for traces", queryName, q.RequestType)
			}

		case "logs":
			// Logs support both raw queries and time series aggregations.
			// Don't force requestType=raw, since that breaks count()/groupBy queries.
			if q.RequestType == "" {
				q.RequestType = "raw"
			}
			switch q.RequestType {
			case "raw":
				spec.StepInterval = nil
			case "scalar":
				spec.StepInterval = nil
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for scalar logs query", queryName)
				}
			case "time_series":
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for time_series logs query", queryName)
				}
				if spec.StepInterval == nil || *spec.StepInterval <= 0 {
					def := int64(60)
					spec.StepInterval = &def
				}
			default:
				return fmt.Errorf("%s: unsupported requestType '%s' for logs", queryName, q.RequestType)
			}

		default:
			return fmt.Errorf("%s: unknown signal type '%s'", queryName, signal)
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

// BuildAggregateQueryPayload creates a QueryPayload for aggregation queries, signal is "logs" or "traces".
// aggregationExpr is a QB v5 expression like "count()", "avg(duration)", "p99(durationNano)".
// groupBy is a list of fields to group by.
// orderByExpr is the expression to order by (e.g. "count()"), orderDir is "asc" or "desc".
func BuildAggregateQueryPayload(signal string, startTime, endTime int64, aggregationExpr string, filterExpression string, groupBy []SelectField, orderByExpr string, orderDir string, limit int) *QueryPayload {
	return &QueryPayload{
		SchemaVersion: "v1",
		Start:         startTime,
		End:           endTime,
		RequestType:   "scalar",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:     "A",
						Signal:   signal,
						Disabled: false,
						Filter:   &Filter{Expression: filterExpression},
						Limit:    limit,
						Offset:   0,
						Order: []Order{
							{Key: Key{Name: orderByExpr}, Direction: orderDir},
						},
						Having:       Having{Expression: ""},
						GroupBy:      groupBy,
						Aggregations: []any{QueryAggregation{Expression: aggregationExpr}},
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
						Having: Having{Expression: ""},
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
