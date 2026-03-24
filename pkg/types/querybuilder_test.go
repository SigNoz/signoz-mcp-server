package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func int64ptr(v int64) *int64 { return &v }

func TestQueryPayloadValidate_AllowsLogsTimeSeries(t *testing.T) {
	q := &QueryPayload{
		SchemaVersion: "v1",
		Start:         1,
		End:           2,
		RequestType:   "time_series",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:         "A",
						Signal:       "logs",
						Disabled:     false,
						StepInterval: int64ptr(60),
						Aggregations: []any{map[string]any{"expression": "count()"}},
					},
				},
			},
		},
	}

	require.NoError(t, q.Validate())
	require.Equal(t, "time_series", q.RequestType)
	require.NotNil(t, q.CompositeQuery.Queries[0].Spec.StepInterval)
}

func TestQueryPayloadValidate_LogsRawClearsStepInterval(t *testing.T) {
	q := &QueryPayload{
		SchemaVersion: "v1",
		Start:         1,
		End:           2,
		RequestType:   "raw",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:         "A",
						Signal:       "logs",
						Disabled:     false,
						StepInterval: int64ptr(60),
					},
				},
			},
		},
	}

	require.NoError(t, q.Validate())
	require.Nil(t, q.CompositeQuery.Queries[0].Spec.StepInterval)
}

func TestBuildLogsQueryPayload_IncludesSelectFields(t *testing.T) {
	p := BuildLogsQueryPayload(1000, 2000, "severity_text = 'error'", 25, 0)
	require.Len(t, p.CompositeQuery.Queries, 1)

	fields := p.CompositeQuery.Queries[0].Spec.SelectFields
	require.NotEmpty(t, fields, "logs query must include SelectFields for rich log attributes")

	fieldNames := make(map[string]bool)
	for _, f := range fields {
		fieldNames[f.Name] = true
	}

	for _, expected := range []string{
		"body", "exception.stacktrace", "exception.message", "exception.type",
		"trace_id", "span_id", "service.name",
	} {
		require.True(t, fieldNames[expected], "expected SelectField %q to be present", expected)
	}
}

func TestQueryPayloadValidate_LogsTimeSeriesRequiresAggregations(t *testing.T) {
	q := &QueryPayload{
		SchemaVersion: "v1",
		Start:         1,
		End:           2,
		RequestType:   "time_series",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:         "A",
						Signal:       "logs",
						Disabled:     false,
						StepInterval: int64ptr(60),
						Aggregations: nil,
					},
				},
			},
		},
	}

	require.Error(t, q.Validate())
}

