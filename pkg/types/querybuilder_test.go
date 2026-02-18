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

