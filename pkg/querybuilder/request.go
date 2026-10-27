package querybuilder

import (
	"encoding/json"
	"fmt"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// Result bounds this server applies to the Query Builder requests it builds or
// normalizes. MaxQueryLimit also caps the dedicated tools' limit parameters,
// because every returned row is buffered in this server's memory.
const (
	DefaultRawQueryLimit       = 100
	DefaultAggregateQueryLimit = 100
	MaxQueryLimit              = 10000
)

// Request is the upstream-generated Query Builder v5 range request.
type Request = apitypes.Querybuildertypesv5QueryRangeRequest

// FieldKey is the upstream-generated telemetry field key used for
// selectFields. Group-by keys share its shape; see GroupByKeys.
type FieldKey = apitypes.TelemetrytypesTelemetryFieldKey

type (
	logQuery    = apitypes.Querybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5LogAggregation
	traceQuery  = apitypes.Querybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5TraceAggregation
	metricQuery = apitypes.Querybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5MetricAggregation
	orderBy     = apitypes.Querybuildertypesv5OrderBy
)

func nonEmpty[T ~string](v T) *T {
	if v == "" {
		return nil
	}
	return &v
}

// NewFieldKey builds a field key, leaving unset each attribute passed empty.
func NewFieldKey(name, signal, fieldContext, fieldDataType string) FieldKey {
	return FieldKey{
		Name:          name,
		Signal:        nonEmpty(apitypes.TelemetrytypesSignal(signal)),
		FieldContext:  nonEmpty(apitypes.TelemetrytypesFieldContext(fieldContext)),
		FieldDataType: nonEmpty(apitypes.TelemetrytypesFieldDataType(fieldDataType)),
	}
}

// FieldContextOf returns a key's field context, or "" when it is unset.
func FieldContextOf(key FieldKey) string {
	if key.FieldContext == nil {
		return ""
	}
	return string(*key.FieldContext)
}

// GroupByKeys converts field keys to group-by keys. The two generated types
// carry the same fields, so this is a plain conversion.
func GroupByKeys(keys []FieldKey) *[]apitypes.Querybuildertypesv5GroupByKey {
	if len(keys) == 0 {
		return nil
	}
	groupBy := make([]apitypes.Querybuildertypesv5GroupByKey, len(keys))
	for i, key := range keys {
		groupBy[i] = apitypes.Querybuildertypesv5GroupByKey(key)
	}
	return &groupBy
}

// MetricAggregation builds a metric aggregation; metricName and
// spaceAggregation are always sent, the rest only when set.
func MetricAggregation(metricName, temporality, timeAggregation, spaceAggregation, reduceTo string) apitypes.Querybuildertypesv5MetricAggregation {
	return apitypes.Querybuildertypesv5MetricAggregation{
		MetricName:       new(metricName),
		Temporality:      nonEmpty(apitypes.MetrictypesTemporality(temporality)),
		TimeAggregation:  nonEmpty(apitypes.MetrictypesTimeAggregation(timeAggregation)),
		SpaceAggregation: new(apitypes.MetrictypesSpaceAggregation(spaceAggregation)),
		ReduceTo:         nonEmpty(apitypes.Querybuildertypesv5ReduceTo(reduceTo)),
	}
}

func newOrderBy(key, direction string) orderBy {
	return orderBy{
		Key:       &apitypes.Querybuildertypesv5OrderByKey{Name: key},
		Direction: new(apitypes.Querybuildertypesv5OrderDirection(direction)),
	}
}

func stepOf(seconds int64) (*apitypes.Querybuildertypesv5Step, error) {
	var step apitypes.Querybuildertypesv5Step
	if err := step.FromQuerybuildertypesv5Step1(float32(seconds)); err != nil {
		return nil, err
	}
	return &step, nil
}

func newRequest(start, end int64, requestType string, envelopes []apitypes.Querybuildertypesv5QueryEnvelope) Request {
	return Request{
		SchemaVersion:  new("v1"),
		Start:          new(int(start)),
		End:            new(int(end)),
		RequestType:    new(apitypes.Querybuildertypesv5RequestType(requestType)),
		CompositeQuery: &apitypes.Querybuildertypesv5CompositeQuery{Queries: &envelopes},
		FormatOptions: &apitypes.Querybuildertypesv5FormatOptions{
			FormatTableResultForUI: new(false),
			FillGaps:               new(false),
		},
		Variables: &map[string]apitypes.Querybuildertypesv5VariableItem{},
	}
}

func builderEnvelope(fill func(*apitypes.Querybuildertypesv5BuilderQuerySpec) error) (apitypes.Querybuildertypesv5QueryEnvelope, error) {
	var spec apitypes.Querybuildertypesv5BuilderQuerySpec
	if err := fill(&spec); err != nil {
		return apitypes.Querybuildertypesv5QueryEnvelope{}, err
	}
	var envelope apitypes.Querybuildertypesv5QueryEnvelope
	err := envelope.FromQuerybuildertypesv5QueryEnvelopeBuilder(apitypes.Querybuildertypesv5QueryEnvelopeBuilder{Spec: &spec})
	return envelope, err
}

func emptyHaving() *apitypes.Querybuildertypesv5Having {
	return havingOf("")
}

func filterOf(expression string) *apitypes.Querybuildertypesv5Filter {
	return &apitypes.Querybuildertypesv5Filter{Expression: new(expression)}
}

// RawSearchOptions are the optional row-shaping inputs of a raw search.
// An empty OrderKey keeps the signal's default order.
type RawSearchOptions struct {
	SelectFields []FieldKey
	OrderKey     string
	OrderDir     string
}

func selectFieldsOf(fields []FieldKey) *[]FieldKey {
	if len(fields) == 0 {
		return nil
	}
	return new(append([]FieldKey(nil), fields...))
}

// logsSearchOrder keeps id as a tie-breaker behind the order key, in the same
// direction, so offset pagination stays stable across rows that share a value.
func logsSearchOrder(opts RawSearchOptions) []orderBy {
	switch opts.OrderKey {
	case "":
		return []orderBy{newOrderBy("timestamp", "desc"), newOrderBy("id", "desc")}
	case "timestamp":
		return []orderBy{newOrderBy("timestamp", opts.OrderDir), newOrderBy("id", opts.OrderDir)}
	case "id":
		return []orderBy{newOrderBy("id", opts.OrderDir)}
	default:
		return []orderBy{newOrderBy(opts.OrderKey, opts.OrderDir), newOrderBy("id", opts.OrderDir)}
	}
}

// LogsSearch builds the raw-row request signoz_search_logs sends. Rows order by
// timestamp then id, both descending, unless opts names another order.
func LogsSearch(start, end int64, filter string, limit, offset int, opts RawSearchOptions) (Request, error) {
	envelope, err := builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
		return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5LogAggregation(logQuery{
			Name:         new("A"),
			Disabled:     new(false),
			Filter:       filterOf(filter),
			Limit:        new(limit),
			Offset:       new(offset),
			Order:        new(logsSearchOrder(opts)),
			Having:       emptyHaving(),
			SelectFields: selectFieldsOf(opts.SelectFields),
		})
	})
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, "raw", []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// Query envelope types the trace tools send. builder_ai_query runs over AI traces
// (traces with a gen_ai model, tool, or agent span).
const (
	QueryTypeBuilder   = string(apitypes.Querybuildertypesv5QueryTypeBuilderQuery)
	QueryTypeBuilderAI = string(apitypes.Querybuildertypesv5QueryTypeBuilderAiQuery)
)

// TraceSearchOptions describes one span or trace search.
type TraceSearchOptions struct {
	Filter       string
	Limit        int
	Offset       int
	SelectFields []FieldKey
	// QueryType builder_ai_query selects the AI envelope; any other value builder_query.
	QueryType string
	// OrderExpr and OrderDir set the order; an empty OrderExpr keeps the default.
	OrderExpr string
	OrderDir  string
}

// SpanSearch builds the raw request signoz_search_traces and
// signoz_get_trace_details send: one row per span, newest first unless
// OrderExpr is set. With builder_ai_query the spans come from AI traces.
func SpanSearch(start, end int64, opts TraceSearchOptions) (Request, error) {
	order := newOrderBy("timestamp", "desc")
	if opts.OrderExpr != "" {
		order = newOrderBy(opts.OrderExpr, opts.OrderDir)
	}
	query := traceQuery{
		Name:     new("A"),
		Disabled: new(false),
		Filter:   filterOf(opts.Filter),
		Limit:    new(opts.Limit),
		Offset:   new(opts.Offset),
		Order:    &[]orderBy{order},
		Having:   emptyHaving(),
	}
	if len(opts.SelectFields) > 0 {
		query.SelectFields = new(append([]FieldKey(nil), opts.SelectFields...))
	}
	envelope, err := traceEnvelope(opts.QueryType, query)
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, "raw", []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// TraceList builds a trace request: one row per trace. SigNoz picks the order
// unless OrderExpr is set.
func TraceList(start, end int64, opts TraceSearchOptions) (Request, error) {
	query := traceQuery{
		Name:     new("A"),
		Disabled: new(false),
		Filter:   filterOf(opts.Filter),
		Limit:    new(opts.Limit),
		Offset:   new(opts.Offset),
		Having:   emptyHaving(),
	}
	if opts.OrderExpr != "" {
		query.Order = &[]orderBy{newOrderBy(opts.OrderExpr, opts.OrderDir)}
	}
	if len(opts.SelectFields) > 0 {
		query.SelectFields = new(append([]FieldKey(nil), opts.SelectFields...))
	}
	envelope, err := traceEnvelope(opts.QueryType, query)
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, "trace", []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// traceEnvelope wraps a trace query in a builder_ai_query envelope, whose spec
// needs the signal set, or otherwise in a builder_query envelope.
func traceEnvelope(queryType string, query traceQuery) (apitypes.Querybuildertypesv5QueryEnvelope, error) {
	if queryType == QueryTypeBuilderAI {
		query.Signal = apitypes.Querybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5TraceAggregationSignalTraces
		var envelope apitypes.Querybuildertypesv5QueryEnvelope
		err := envelope.FromQuerybuildertypesv5QueryEnvelopeBuilderAI(apitypes.Querybuildertypesv5QueryEnvelopeBuilderAI{Spec: &query})
		return envelope, err
	}
	return builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
		return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5TraceAggregation(query)
	})
}

// Function is the upstream-generated post-query transform, forwarded verbatim
// for SigNoz to validate.
type Function = apitypes.Querybuildertypesv5Function

// AggregateSpec describes the grouped request signoz_aggregate_logs and
// signoz_aggregate_traces send. Signal is "logs" or "traces"; Aggregations are
// expressions such as "count()" or "p99(duration_nano)". An empty RequestType
// means "scalar"; a nil StepInterval lets SigNoz choose.
type AggregateSpec struct {
	Signal       string
	Start, End   int64
	Aggregations []string
	Filter       string
	GroupBy      []FieldKey
	OrderExpr    string
	OrderDir     string
	Limit        int
	RequestType  string
	StepInterval *int64
	Having       string
	Functions    []Function
	QueryType    string // builder_ai_query selects the AI envelope for traces
}

func havingOf(expression string) *apitypes.Querybuildertypesv5Having {
	return &apitypes.Querybuildertypesv5Having{Expression: new(expression)}
}

func functionsOf(functions []Function) *[]Function {
	if len(functions) == 0 {
		return nil
	}
	return new(append([]Function(nil), functions...))
}

// Aggregate builds the grouped request signoz_aggregate_logs and
// signoz_aggregate_traces send.
func Aggregate(spec AggregateSpec) (Request, error) {
	requestType := spec.RequestType
	if requestType == "" {
		requestType = "scalar"
	}
	var step *apitypes.Querybuildertypesv5Step
	if spec.StepInterval != nil {
		var err error
		if step, err = stepOf(*spec.StepInterval); err != nil {
			return Request{}, err
		}
	}
	order := &[]orderBy{newOrderBy(spec.OrderExpr, spec.OrderDir)}
	var envelope apitypes.Querybuildertypesv5QueryEnvelope
	var err error
	switch spec.Signal {
	case "logs":
		aggregations := make([]apitypes.Querybuildertypesv5LogAggregation, 0, len(spec.Aggregations))
		for _, expression := range spec.Aggregations {
			aggregations = append(aggregations, apitypes.Querybuildertypesv5LogAggregation{Expression: new(expression)})
		}
		envelope, err = builderEnvelope(func(builder *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
			return builder.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5LogAggregation(logQuery{
				Name:         new("A"),
				StepInterval: step,
				Disabled:     new(false),
				Filter:       filterOf(spec.Filter),
				Limit:        new(spec.Limit),
				Offset:       new(0),
				Order:        order,
				Having:       havingOf(spec.Having),
				GroupBy:      GroupByKeys(spec.GroupBy),
				Aggregations: &aggregations,
				Functions:    functionsOf(spec.Functions),
			})
		})
	case "traces":
		aggregations := make([]apitypes.Querybuildertypesv5TraceAggregation, 0, len(spec.Aggregations))
		for _, expression := range spec.Aggregations {
			aggregations = append(aggregations, apitypes.Querybuildertypesv5TraceAggregation{Expression: new(expression)})
		}
		envelope, err = traceEnvelope(spec.QueryType, traceQuery{
			Name:         new("A"),
			StepInterval: step,
			Disabled:     new(false),
			Filter:       filterOf(spec.Filter),
			Limit:        new(spec.Limit),
			Offset:       new(0),
			Order:        order,
			Having:       havingOf(spec.Having),
			GroupBy:      GroupByKeys(spec.GroupBy),
			Aggregations: &aggregations,
			Functions:    functionsOf(spec.Functions),
		})
	default:
		err = fmt.Errorf("aggregate requests support the logs and traces signals, not %q", spec.Signal)
	}
	if err != nil {
		return Request{}, err
	}
	return newRequest(spec.Start, spec.End, requestType, []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// MetricsQuery describes one metric query or formula in a signoz_query_metrics
// request.
type MetricsQuery struct {
	Name        string
	Aggregation apitypes.Querybuildertypesv5MetricAggregation
	Filter      string
	GroupBy     []FieldKey
	IsFormula   bool   // when true, Expression replaces Aggregation, Filter, and GroupBy
	Expression  string // formula expression such as "A / B * 100"
	Legend      string
	// Result shaping the caller asked for. Zero values leave the field to
	// ApplyBounds (limit and order) or omit it (having, functions).
	Limit     int
	OrderKey  string
	OrderDir  string
	Having    string
	Functions []Function
}

func (q MetricsQuery) limit() *int {
	if q.Limit <= 0 {
		return nil
	}
	return new(q.Limit)
}

func (q MetricsQuery) order() *[]orderBy {
	if q.OrderKey == "" {
		return nil
	}
	return &[]orderBy{newOrderBy(q.OrderKey, q.OrderDir)}
}

// MetricsJSON builds the signoz_query_metrics request and applies the same
// result bounds signoz_execute_builder_query applies. source selects a metrics
// store such as "meter" for Cost Meter; pass "" for the default store.
func MetricsJSON(start, end, stepInterval int64, queries []MetricsQuery, requestType, source string) ([]byte, error) {
	if requestType == "" {
		requestType = "time_series"
	}
	var step *apitypes.Querybuildertypesv5Step
	if stepInterval > 0 {
		var err error
		if step, err = stepOf(stepInterval); err != nil {
			return nil, err
		}
	}

	envelopes := make([]apitypes.Querybuildertypesv5QueryEnvelope, 0, len(queries))
	for _, q := range queries {
		if q.IsFormula {
			var envelope apitypes.Querybuildertypesv5QueryEnvelope
			if err := envelope.FromQuerybuildertypesv5QueryEnvelopeFormula(apitypes.Querybuildertypesv5QueryEnvelopeFormula{
				Spec: &apitypes.Querybuildertypesv5QueryBuilderFormula{
					Name:       new(q.Name),
					Expression: new(q.Expression),
					Legend:     nonEmpty(q.Legend),
					Disabled:   new(false),
					Limit:      q.limit(),
					Order:      q.order(),
					Functions:  functionsOf(q.Functions),
				},
			}); err != nil {
				return nil, err
			}
			envelopes = append(envelopes, envelope)
			continue
		}

		query := metricQuery{
			Name:         new(q.Name),
			Source:       nonEmpty(apitypes.TelemetrytypesSource(source)),
			StepInterval: step,
			Disabled:     new(false),
			Aggregations: &[]apitypes.Querybuildertypesv5MetricAggregation{q.Aggregation},
			GroupBy:      GroupByKeys(q.GroupBy),
			Having:       havingOf(q.Having),
			Limit:        q.limit(),
			Order:        q.order(),
			Functions:    functionsOf(q.Functions),
		}
		if q.Filter != "" {
			query.Filter = filterOf(q.Filter)
		}
		envelope, err := builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
			return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5MetricAggregation(query)
		})
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}

	request := newRequest(start, end, requestType, envelopes)
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	ApplyBounds(decoded)
	return json.Marshal(decoded)
}

// LogSearchCoreFields is the default column set for signoz_search_logs: the
// intrinsic log columns only, because logs carry no spec-mandated resource
// attributes and a default that names an absent key would warn on every call.
// Callers add attributes with selectFields; SigNoz always includes timestamp
// and id in an explicit selection.
var LogSearchCoreFields = []FieldKey{
	NewFieldKey("timestamp", "logs", "log", "number"),
	NewFieldKey("id", "logs", "log", "string"),
	NewFieldKey("body", "logs", "log", "string"),
	NewFieldKey("severity_text", "logs", "log", "string"),
	NewFieldKey("trace_id", "logs", "log", "string"),
	NewFieldKey("span_id", "logs", "log", "string"),
}

// TraceSearchCoreFields is the default column set for signoz_search_traces.
// Callers add fields with selectFields; SigNoz always adds timestamp, trace_id,
// and span_id to an explicit list, so rows stay flat.
var TraceSearchCoreFields = []FieldKey{
	NewFieldKey("timestamp", "traces", "span", "number"),
	NewFieldKey("trace_id", "traces", "span", "string"),
	NewFieldKey("span_id", "traces", "span", "string"),
	NewFieldKey("parent_span_id", "traces", "span", "string"),
	NewFieldKey("name", "traces", "span", "string"),
	NewFieldKey("service.name", "traces", "resource", "string"),
	NewFieldKey("kind_string", "traces", "span", "string"),
	NewFieldKey("duration_nano", "traces", "span", "number"),
	NewFieldKey("has_error", "traces", "span", "bool"),
	NewFieldKey("status_code_string", "traces", "span", "string"),
	NewFieldKey("status_message", "traces", "span", "string"),
	NewFieldKey("response_status_code", "traces", "span", "string"),
	NewFieldKey("http_method", "traces", "span", "string"),
}
