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
	DefaultRawQueryLimit          = 100
	DefaultAggregateQueryLimit    = 100
	MaxQueryLimit                 = 10000
	DefaultFormulaInputQueryLimit = MaxQueryLimit
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
	return &apitypes.Querybuildertypesv5Having{Expression: new("")}
}

func filterOf(expression string) *apitypes.Querybuildertypesv5Filter {
	return &apitypes.Querybuildertypesv5Filter{Expression: new(expression)}
}

// LogsSearch builds the raw-row request signoz_search_logs sends. Rows order by
// timestamp then id, both descending, so offset pagination stays stable.
func LogsSearch(start, end int64, filter string, limit, offset int) (Request, error) {
	envelope, err := builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
		return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5LogAggregation(logQuery{
			Name:     new("A"),
			Disabled: new(false),
			Filter:   filterOf(filter),
			Limit:    new(limit),
			Offset:   new(offset),
			Order:    &[]orderBy{newOrderBy("timestamp", "desc"), newOrderBy("id", "desc")},
			Having:   emptyHaving(),
		})
	})
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, "raw", []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// TracesSearch builds the raw span-row request signoz_search_traces and
// signoz_get_trace_details send, newest span first.
func TracesSearch(start, end int64, filter string, limit, offset int, selectFields []FieldKey) (Request, error) {
	envelope, err := builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
		query := traceQuery{
			Name:     new("A"),
			Disabled: new(false),
			Filter:   filterOf(filter),
			Limit:    new(limit),
			Offset:   new(offset),
			Order:    &[]orderBy{newOrderBy("timestamp", "desc")},
			Having:   emptyHaving(),
		}
		if len(selectFields) > 0 {
			query.SelectFields = new(append([]FieldKey(nil), selectFields...))
		}
		return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5TraceAggregation(query)
	})
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, "raw", []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
}

// Aggregate builds the grouped request signoz_aggregate_logs and
// signoz_aggregate_traces send. signal is "logs" or "traces"; aggregationExpr
// is an expression such as "count()" or "p99(duration_nano)". An empty
// requestType means "scalar"; a nil stepInterval lets SigNoz choose.
func Aggregate(signal string, start, end int64, aggregationExpr, filter string, groupBy []FieldKey, orderExpr, orderDir string, limit int, requestType string, stepInterval *int64) (Request, error) {
	if requestType == "" {
		requestType = "scalar"
	}
	var step *apitypes.Querybuildertypesv5Step
	if stepInterval != nil {
		var err error
		if step, err = stepOf(*stepInterval); err != nil {
			return Request{}, err
		}
	}
	order := &[]orderBy{newOrderBy(orderExpr, orderDir)}
	envelope, err := builderEnvelope(func(spec *apitypes.Querybuildertypesv5BuilderQuerySpec) error {
		switch signal {
		case "logs":
			return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5LogAggregation(logQuery{
				Name:         new("A"),
				StepInterval: step,
				Disabled:     new(false),
				Filter:       filterOf(filter),
				Limit:        new(limit),
				Offset:       new(0),
				Order:        order,
				Having:       emptyHaving(),
				GroupBy:      GroupByKeys(groupBy),
				Aggregations: &[]apitypes.Querybuildertypesv5LogAggregation{{Expression: new(aggregationExpr)}},
			})
		case "traces":
			return spec.FromQuerybuildertypesv5QueryBuilderQueryGithubComSigNozSignozPkgTypesQuerybuildertypesQuerybuildertypesv5TraceAggregation(traceQuery{
				Name:         new("A"),
				StepInterval: step,
				Disabled:     new(false),
				Filter:       filterOf(filter),
				Limit:        new(limit),
				Offset:       new(0),
				Order:        order,
				Having:       emptyHaving(),
				GroupBy:      GroupByKeys(groupBy),
				Aggregations: &[]apitypes.Querybuildertypesv5TraceAggregation{{Expression: new(aggregationExpr)}},
			})
		default:
			return fmt.Errorf("aggregate requests support the logs and traces signals, not %q", signal)
		}
	})
	if err != nil {
		return Request{}, err
	}
	return newRequest(start, end, requestType, []apitypes.Querybuildertypesv5QueryEnvelope{envelope}), nil
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
			Having:       emptyHaving(),
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

// TraceDetailSelectFields is the column set signoz_get_trace_details requests.
var TraceDetailSelectFields = []FieldKey{
	// Top-level span fields
	NewFieldKey("trace_id", "traces", "span", "string"),
	NewFieldKey("span_id", "traces", "span", "string"),
	NewFieldKey("parent_span_id", "traces", "span", "string"),
	NewFieldKey("name", "traces", "span", "string"),
	NewFieldKey("duration_nano", "traces", "span", "number"),
	NewFieldKey("timestamp", "traces", "span", "number"),
	NewFieldKey("has_error", "traces", "span", "bool"),
	NewFieldKey("status_code", "traces", "span", "number"),
	NewFieldKey("status_code_string", "traces", "span", "string"),
	NewFieldKey("http_method", "traces", "span", "string"),
	NewFieldKey("http_url", "traces", "span", "string"),
	NewFieldKey("kind_string", "traces", "span", "string"),
	NewFieldKey("kind", "traces", "span", "number"),
	NewFieldKey("response_status_code", "traces", "span", "string"),
	NewFieldKey("status_message", "traces", "span", "string"),
	// Resource attributes
	NewFieldKey("service.name", "traces", "resource", "string"),
	NewFieldKey("cloud.account.id", "traces", "resource", "string"),
	NewFieldKey("cloud.platform", "traces", "resource", "string"),
	NewFieldKey("cloud.provider", "traces", "resource", "string"),
	NewFieldKey("cloud.region", "traces", "resource", "string"),
	NewFieldKey("deployment.environment", "traces", "resource", "string"),
	NewFieldKey("host.name", "traces", "resource", "string"),
	NewFieldKey("k8s.cluster.name", "traces", "resource", "string"),
	NewFieldKey("k8s.namespace.name", "traces", "resource", "string"),
	NewFieldKey("k8s.node.name", "traces", "resource", "string"),
	NewFieldKey("k8s.pod.name", "traces", "resource", "string"),
	NewFieldKey("k8s.pod.start_time", "traces", "resource", "string"),
	NewFieldKey("k8s.pod.uid", "traces", "resource", "string"),
	NewFieldKey("k8s.statefulset.name", "traces", "resource", "string"),
	NewFieldKey("service.version", "traces", "resource", "string"),
	NewFieldKey("signoz.deployment.tier", "traces", "resource", "string"),
	NewFieldKey("signoz.workload", "traces", "resource", "string"),
	NewFieldKey("signoz.workspace.key.id", "traces", "resource", "string"),
	// Span attributes
	NewFieldKey("client.address", "traces", "tag", "string"),
	NewFieldKey("http.request.method", "traces", "tag", "string"),
	NewFieldKey("http.response.body.size", "traces", "tag", "string"),
	NewFieldKey("http.response.status_code", "traces", "tag", "number"),
	NewFieldKey("http.route", "traces", "tag", "string"),
	NewFieldKey("rpc.method", "traces", "tag", "string"),
	NewFieldKey("network.peer.address", "traces", "tag", "string"),
	NewFieldKey("network.peer.port", "traces", "tag", "string"),
	NewFieldKey("network.protocol.version", "traces", "tag", "string"),
	NewFieldKey("server.address", "traces", "tag", "string"),
	NewFieldKey("url.path", "traces", "tag", "string"),
	NewFieldKey("url.scheme", "traces", "tag", "string"),
	NewFieldKey("db.operation", "traces", "tag", "string"),
	NewFieldKey("db.statement", "traces", "tag", "string"),
	NewFieldKey("db.system", "traces", "tag", "string"),
}
