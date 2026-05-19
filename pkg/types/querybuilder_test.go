package types

import (
	"encoding/json"
	"strings"
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
	spec, ok := q.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "expected QuerySpec, got %T", q.CompositeQuery.Queries[0].Spec)
	require.NotNil(t, spec.StepInterval)
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
	spec, ok := q.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "expected QuerySpec, got %T", q.CompositeQuery.Queries[0].Spec)
	require.Nil(t, spec.StepInterval)
}

// Regression test for #179: PromQL query strings must survive the
// unmarshal → Validate → re-marshal round trip that the
// signoz_execute_builder_query handler performs. We re-unmarshal the
// marshaled output and compare typed values rather than asserting on
// substrings — otherwise the test could pass even if the query string
// landed under the wrong path.
func TestQueryPayloadRoundTrip_PreservesPromQLQuery(t *testing.T) {
	const promQLExpr = `{"http.server.duration_count"}`
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[{
				"type":"promql",
				"spec":{"name":"A","query":` + jsonString(promQLExpr) + `,"step":60,"legend":"http_requests"}
			}]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))

	spec, ok := payload.CompositeQuery.Queries[0].Spec.(PromQLSpec)
	require.True(t, ok, "expected PromQLSpec, got %T", payload.CompositeQuery.Queries[0].Spec)
	require.Equal(t, promQLExpr, spec.Query)
	require.Equal(t, "A", spec.Name)
	require.Equal(t, "http_requests", spec.Legend)

	require.NoError(t, payload.Validate())

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var roundTripped QueryPayload
	require.NoError(t, json.Unmarshal(out, &roundTripped),
		"marshaled output failed to re-parse: %s", string(out))
	require.Equal(t, "promql", roundTripped.CompositeQuery.Queries[0].Type)
	rt, ok := roundTripped.CompositeQuery.Queries[0].Spec.(PromQLSpec)
	require.True(t, ok, "round-tripped spec is %T not PromQLSpec; output was: %s",
		roundTripped.CompositeQuery.Queries[0].Spec, string(out))
	require.Equal(t, promQLExpr, rt.Query,
		"PromQL query string did not survive round trip; output was: %s", string(out))
	require.Equal(t, "A", rt.Name)
	require.Equal(t, "http_requests", rt.Legend)
}

// Mirrors the PromQL round-trip test, with the duration-string form for step
// (the backend accepts either a number-of-seconds or a Go duration string).
func TestQueryPayloadRoundTrip_PreservesPromQLWithStepString(t *testing.T) {
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[{
				"type":"promql",
				"spec":{"name":"A","query":"up","step":"60s"}
			}]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var roundTripped QueryPayload
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	rt, ok := roundTripped.CompositeQuery.Queries[0].Spec.(PromQLSpec)
	require.True(t, ok)
	require.Equal(t, "up", rt.Query)
	require.Equal(t, "60s", rt.Step,
		"step duration-string did not survive round trip; output was: %s", string(out))
}

func TestQueryPayloadRoundTrip_PreservesClickHouseSQL(t *testing.T) {
	const sql = `SELECT count() FROM signoz_traces.signoz_index_v3 WHERE service.name = 'frontend'`
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"scalar",
		"compositeQuery":{
			"queries":[{
				"type":"clickhouse_sql",
				"spec":{"name":"A","query":` + string(mustJSON(t, sql)) + `,"legend":"frontend_count"}
			}]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())

	spec, ok := payload.CompositeQuery.Queries[0].Spec.(ClickHouseSQLSpec)
	require.True(t, ok, "expected ClickHouseSQLSpec, got %T", payload.CompositeQuery.Queries[0].Spec)
	require.Equal(t, sql, spec.Query)

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var roundTripped QueryPayload
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	require.Equal(t, "clickhouse_sql", roundTripped.CompositeQuery.Queries[0].Type)
	rt, ok := roundTripped.CompositeQuery.Queries[0].Spec.(ClickHouseSQLSpec)
	require.True(t, ok, "round-tripped spec is %T not ClickHouseSQLSpec; output was: %s",
		roundTripped.CompositeQuery.Queries[0].Spec, string(out))
	require.Equal(t, sql, rt.Query,
		"ClickHouse SQL query did not survive round trip; output was: %s", string(out))
	require.Equal(t, "frontend_count", rt.Legend)
}

// Malformed spec payloads should produce a clear error from Query.UnmarshalJSON
// rather than silently producing a zero-valued spec.
func TestQueryUnmarshalJSON_MalformedPromQLSpec(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name:    "spec is a string, not an object",
			input:   `{"type":"promql","spec":"not-an-object"}`,
			wantMsg: "invalid promql spec",
		},
		{
			name:    "query field is a number",
			input:   `{"type":"promql","spec":{"name":"A","query":123}}`,
			wantMsg: "invalid promql spec",
		},
		{
			name:    "clickhouse_sql with non-object spec",
			input:   `{"type":"clickhouse_sql","spec":[1,2,3]}`,
			wantMsg: "invalid clickhouse_sql spec",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var q Query
			err := json.Unmarshal([]byte(tc.input), &q)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg, "got: %v", err)
		})
	}
}

func TestQueryPayloadValidate_PromQLRequiresQuery(t *testing.T) {
	q := &QueryPayload{
		Start: 1,
		End:   2,
		CompositeQuery: CompositeQuery{
			Queries: []Query{{
				Type: "promql",
				Spec: PromQLSpec{Name: "A"},
			}},
		},
	}
	err := q.Validate()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "missing query string"), "got: %v", err)
}

func TestQueryPayloadValidate_PromQLDefaultsRequestType(t *testing.T) {
	q := &QueryPayload{
		Start: 1,
		End:   2,
		CompositeQuery: CompositeQuery{
			Queries: []Query{{
				Type: "promql",
				Spec: PromQLSpec{Name: "A", Query: `up`},
			}},
		},
	}
	require.NoError(t, q.Validate())
	require.Equal(t, "time_series", q.RequestType)
}

func TestQueryPayloadRoundTrip_MixedBuilderAndPromQL(t *testing.T) {
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[
				{"type":"builder_query","spec":{"name":"A","signal":"metrics","aggregations":[{"metricName":"k8s.pod.cpu","spaceAggregation":"sum"}],"stepInterval":60}},
				{"type":"promql","spec":{"name":"B","query":"up"}}
			]
		}
	}`
	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())

	_, ok := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "first query: expected QuerySpec, got %T", payload.CompositeQuery.Queries[0].Spec)
	prom, ok := payload.CompositeQuery.Queries[1].Spec.(PromQLSpec)
	require.True(t, ok, "second query: expected PromQLSpec, got %T", payload.CompositeQuery.Queries[1].Spec)
	require.Equal(t, "up", prom.Query)
}

// jsonString JSON-encodes s and returns the result as a Go string (including
// the surrounding double quotes).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
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
