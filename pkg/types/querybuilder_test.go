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

// Regression test for the codex review on PR #180: builder_formula envelopes
// were being decoded into QuerySpec, which has no `expression` or `legend`
// fields, so formulas like "A / B * 100" were silently dropped during the
// typed round-trip — exactly the same class of bug the PromQL fix addresses.
func TestQueryPayloadRoundTrip_PreservesBuilderFormula(t *testing.T) {
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[
				{"type":"builder_query","spec":{"name":"A","signal":"metrics","aggregations":[{"metricName":"http.requests","spaceAggregation":"sum"}],"stepInterval":60}},
				{"type":"builder_query","spec":{"name":"B","signal":"metrics","aggregations":[{"metricName":"http.errors","spaceAggregation":"sum"}],"stepInterval":60}},
				{"type":"builder_formula","spec":{"name":"C","expression":"A / B * 100","legend":"error_pct","disabled":false}}
			]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())

	formula, ok := payload.CompositeQuery.Queries[2].Spec.(FormulaSpec)
	require.True(t, ok, "expected FormulaSpec, got %T", payload.CompositeQuery.Queries[2].Spec)
	require.Equal(t, "A / B * 100", formula.Expression)
	require.Equal(t, "error_pct", formula.Legend)

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var roundTripped QueryPayload
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	require.Equal(t, "builder_formula", roundTripped.CompositeQuery.Queries[2].Type)
	rt, ok := roundTripped.CompositeQuery.Queries[2].Spec.(FormulaSpec)
	require.True(t, ok, "round-tripped spec is %T not FormulaSpec; output was: %s",
		roundTripped.CompositeQuery.Queries[2].Spec, string(out))
	require.Equal(t, "A / B * 100", rt.Expression,
		"formula expression did not survive round trip; output was: %s", string(out))
	require.Equal(t, "error_pct", rt.Legend)
}

// builder_trace_operator (and any future / less-common envelope type) must
// survive the round-trip byte-for-byte via the json.RawMessage fallback —
// its fields (expression, returnSpansFrom, etc.) are not in QuerySpec and
// would be dropped if we decoded into the wrong typed spec.
func TestQueryPayloadRoundTrip_PreservesTraceOperator(t *testing.T) {
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[
				{"type":"builder_trace_operator","spec":{"name":"T","expression":"A => B","returnSpansFrom":"A","disabled":false}}
			]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())

	raw, ok := payload.CompositeQuery.Queries[0].Spec.(json.RawMessage)
	require.True(t, ok, "expected json.RawMessage, got %T", payload.CompositeQuery.Queries[0].Spec)

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	// Re-parse and confirm the trace_operator-specific fields are still there.
	var roundTripped struct {
		CompositeQuery struct {
			Queries []struct {
				Type string         `json:"type"`
				Spec map[string]any `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	require.Equal(t, "builder_trace_operator", roundTripped.CompositeQuery.Queries[0].Type)
	require.Equal(t, "A => B", roundTripped.CompositeQuery.Queries[0].Spec["expression"],
		"trace_operator expression did not survive round trip; output was: %s", string(out))
	require.Equal(t, "A", roundTripped.CompositeQuery.Queries[0].Spec["returnSpansFrom"])
	// Sanity: no leaked builder-only zero fields were injected.
	_, hasSignal := roundTripped.CompositeQuery.Queries[0].Spec["signal"]
	require.False(t, hasSignal, "spec leaked builder-only `signal` field; output was: %s", string(out))
	_, hasOrder := roundTripped.CompositeQuery.Queries[0].Spec["order"]
	require.False(t, hasOrder, "spec leaked builder-only `order` field; output was: %s", string(out))

	// Sanity on the in-memory raw: it should be the original spec bytes.
	require.Contains(t, string(raw), `"expression":"A => B"`)
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

// Regression test for issue #176: the `source` field (e.g. "meter" for Cost Meter
// queries) is a sibling of "name" and "signal" inside the builder_query spec object,
// NOT a top-level QueryPayload field. It must survive the unmarshal → Validate →
// re-marshal round trip performed by signoz_execute_builder_query, and must be
// absent from the marshaled output when empty (omitempty).
func TestQueryPayloadRoundTrip_PreservesSource(t *testing.T) {
	input := `{
		"schemaVersion":"v1",
		"start":1700000000,
		"end":1700003600,
		"requestType":"time_series",
		"compositeQuery":{
			"queries":[{
				"type":"builder_query",
				"spec":{"name":"A","signal":"metrics","source":"meter","aggregations":[{"metricName":"signoz_db_samples_ingested","spaceAggregation":"sum"}]}
			}]
		}
	}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))

	spec, ok := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "expected QuerySpec, got %T", payload.CompositeQuery.Queries[0].Spec)
	require.Equal(t, "meter", spec.Source)

	require.NoError(t, payload.Validate())

	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var roundTripped QueryPayload
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	rt, ok := roundTripped.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "round-tripped spec is %T not QuerySpec; output was: %s",
		roundTripped.CompositeQuery.Queries[0].Spec, string(out))
	require.Equal(t, "meter", rt.Source,
		"source field did not survive round trip; output was: %s", string(out))

	// Verify omitempty: a spec with empty source must not emit the field.
	spec.Source = ""
	payload.CompositeQuery.Queries[0].Spec = spec
	outEmpty, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NotContains(t, string(outEmpty), `"source"`,
		"empty source must be omitted from JSON; got: %s", string(outEmpty))
}

// TestBuildMetricsQueryPayloadJSON_AppliesSource covers the signoz_query_metrics
// build path (distinct from the signoz_execute_builder_query round-trip above).
// It asserts the source argument lands on every builder_query spec, never on a
// builder_formula spec, and is omitted entirely when empty (omitempty) so existing
// payloads stay byte-for-byte unchanged.
func TestBuildMetricsQueryPayloadJSON_AppliesSource(t *testing.T) {
	// Two builder queries (A, B) + one formula (C) — so we can prove source lands on
	// EVERY builder_query, never on the builder_formula.
	queries := []MetricsQuerySpec{
		{
			Name: "A",
			Aggregation: MetricAggregation{
				MetricName:       "signoz.meter.log.size",
				Temporality:      "delta",
				TimeAggregation:  "increase",
				SpaceAggregation: "sum",
			},
		},
		{
			Name: "B",
			Aggregation: MetricAggregation{
				MetricName:       "signoz.meter.span.size",
				Temporality:      "delta",
				TimeAggregation:  "increase",
				SpaceAggregation: "sum",
			},
		},
		{
			Name:       "C",
			IsFormula:  true,
			Expression: "A + B",
			Legend:     "ingested_bytes",
		},
	}

	// source set → present on both builder_query specs, absent on the builder_formula.
	out, err := BuildMetricsQueryPayloadJSON(1700000000, 1700003600, 60, queries, "time_series", "meter")
	require.NoError(t, err)

	var payload QueryPayload
	require.NoError(t, json.Unmarshal(out, &payload))
	require.Len(t, payload.CompositeQuery.Queries, 3)

	for i := 0; i < 2; i++ {
		spec, ok := payload.CompositeQuery.Queries[i].Spec.(QuerySpec)
		require.True(t, ok, "query %d: expected QuerySpec, got %T", i, payload.CompositeQuery.Queries[i].Spec)
		require.Equal(t, "meter", spec.Source, "query %d: source must be set", i)
		require.Equal(t, DefaultAggregateQueryLimit, spec.Limit)
		require.Equal(t, resultDescendingOrder(), spec.Order)
	}

	formula, ok := payload.CompositeQuery.Queries[2].Spec.(FormulaSpec)
	require.True(t, ok, "expected FormulaSpec, got %T", payload.CompositeQuery.Queries[2].Spec)
	require.Equal(t, DefaultAggregateQueryLimit, formula.Limit)
	require.Equal(t, resultDescendingOrder(), formula.Order)

	// "source":"meter" appears exactly twice — once per builder_query, never on the formula.
	require.Equal(t, 2, strings.Count(string(out), `"source":"meter"`),
		"source must be set on every builder_query spec only; got: %s", string(out))

	// empty source → byte-for-byte identical to omitting the field, and "source" absent entirely.
	outEmpty, err := BuildMetricsQueryPayloadJSON(1700000000, 1700003600, 60, queries, "time_series", "")
	require.NoError(t, err)
	require.NotContains(t, string(outEmpty), `"source"`,
		"empty source must be omitted from JSON; got: %s", string(outEmpty))
	require.Equal(t, 0, strings.Count(string(outEmpty), `"source"`),
		"empty source must not emit the key at all; got: %s", string(outEmpty))
}

func TestQueryPayloadValidate_DefaultsBoundsByRequestType(t *testing.T) {
	tests := []struct {
		name        string
		requestType string
		spec        QuerySpec
		wantLimit   int
		wantOrder   []Order
	}{
		{
			name:        "raw logs",
			requestType: "raw",
			spec:        QuerySpec{Name: "A", Signal: "logs"},
			wantLimit:   DefaultRawQueryLimit,
			wantOrder: []Order{
				{Key: Key{Name: "timestamp"}, Direction: "desc"},
				{Key: Key{Name: "id"}, Direction: "desc"},
			},
		},
		{
			name:        "raw traces",
			requestType: "raw",
			spec:        QuerySpec{Name: "A", Signal: "traces"},
			wantLimit:   DefaultRawQueryLimit,
			wantOrder:   []Order{{Key: Key{Name: "timestamp"}, Direction: "desc"}},
		},
		{
			name:        "trace request",
			requestType: "trace",
			spec:        QuerySpec{Name: "A", Signal: "traces"},
			wantLimit:   DefaultRawQueryLimit,
			wantOrder:   []Order{{Key: Key{Name: "timestamp"}, Direction: "desc"}},
		},
		{
			name:        "scalar logs",
			requestType: "scalar",
			spec: QuerySpec{Name: "A", Signal: "logs",
				Aggregations: []any{map[string]any{"expression": "count()"}}},
			wantLimit: DefaultAggregateQueryLimit,
			wantOrder: []Order{{Key: Key{Name: "count()"}, Direction: "desc"}},
		},
		{
			name:        "scalar logs strips display alias from default order",
			requestType: "scalar",
			spec: QuerySpec{Name: "A", Signal: "logs",
				Aggregations: []any{map[string]any{"expression": "count() AS 'Requests'"}}},
			wantLimit: DefaultAggregateQueryLimit,
			wantOrder: []Order{{Key: Key{Name: "count()"}, Direction: "desc"}},
		},
		{
			name:        "scalar logs preserves quoted as inside aggregation while stripping display alias",
			requestType: "scalar",
			spec: QuerySpec{Name: "A", Signal: "logs",
				Aggregations: []any{map[string]any{"expression": "countIf(message = 'read as write') AS 'Requests'"}}},
			wantLimit: DefaultAggregateQueryLimit,
			wantOrder: []Order{{Key: Key{Name: "countIf(message = 'read as write')"}, Direction: "desc"}},
		},
		{
			name:        "time series traces",
			requestType: "time_series",
			spec: QuerySpec{Name: "A", Signal: "traces", StepInterval: int64ptr(60),
				Aggregations: []any{map[string]any{"expression": "p99(duration_nano)"}}},
			wantLimit: DefaultAggregateQueryLimit,
			wantOrder: []Order{{Key: Key{Name: "p99(duration_nano)"}, Direction: "desc"}},
		},
		{
			name:        "scalar metrics",
			requestType: "scalar",
			spec: QuerySpec{Name: "A", Signal: "metrics",
				Aggregations: []any{map[string]any{"metricName": "cpu", "spaceAggregation": "sum"}}},
			wantLimit: DefaultAggregateQueryLimit,
			wantOrder: resultDescendingOrder(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := QueryPayload{
				Start:       1,
				End:         2,
				RequestType: tc.requestType,
				CompositeQuery: CompositeQuery{Queries: []Query{{
					Type: "builder_query",
					Spec: tc.spec,
				}}},
			}
			require.NoError(t, payload.Validate())
			got := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
			require.Equal(t, tc.wantLimit, got.Limit)
			require.Equal(t, tc.wantOrder, got.Order)
			require.Len(t, payload.AppliedBounds, 1)
			require.True(t, payload.AppliedBounds[0].LimitDefaulted)
			require.True(t, payload.AppliedBounds[0].OrderDefaulted)
		})
	}
}

func TestQueryPayloadValidate_PreservesAuthoredBounds(t *testing.T) {
	wantOrder := []Order{{Key: Key{
		Name:          "service.name",
		Description:   "Service name",
		Unit:          "short",
		Signal:        "logs",
		FieldContext:  "resource",
		FieldDataType: "string",
	}, Direction: "asc"}}
	payload := QueryPayload{
		Start:       1,
		End:         2,
		RequestType: "time_series",
		CompositeQuery: CompositeQuery{Queries: []Query{{
			Type: "builder_query",
			Spec: QuerySpec{
				Name:   "A",
				Signal: "logs",
				Limit:  42,
				Order: []Order{{Key: Key{
					Name:          " service.name ",
					Description:   "Service name",
					Unit:          "short",
					Signal:        "logs",
					FieldContext:  "resource",
					FieldDataType: "string",
				}, Direction: " ASC "}},
				StepInterval: int64ptr(60),
				Aggregations: []any{map[string]any{"expression": "count()"}},
			},
		}}},
	}
	require.NoError(t, payload.Validate())
	got := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.Equal(t, 42, got.Limit)
	require.Equal(t, wantOrder, got.Order)
	require.Empty(t, payload.AppliedBounds)
}

func TestQueryPayloadRoundTrip_FormulaBoundsDefaultAndPreserve(t *testing.T) {
	input := `{
		"start":1,"end":2,"requestType":"time_series",
		"compositeQuery":{"queries":[
			{"type":"builder_query","spec":{"name":"A","signal":"metrics","aggregations":[{"metricName":"cpu","spaceAggregation":"sum"}]}},
			{"type":"builder_formula","spec":{"name":"F","expression":"A * 100","legend":"percent","having":{"expression":"F > 5"},"functions":[{"name":"clampMax","args":[{"name":"max","value":100}]}],"limit":"25","order":[{"key":{"name":"F","signal":"metrics","fieldContext":"metric","fieldDataType":"float64"},"direction":"asc"}]}}
		]}}
	`
	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(input), &payload))
	require.NoError(t, payload.Validate())
	formula := payload.CompositeQuery.Queries[1].Spec.(FormulaSpec)
	require.Equal(t, 25, formula.Limit)
	require.Equal(t, []Order{{Key: Key{Name: "F", Signal: "metrics", FieldContext: "metric", FieldDataType: "float64"}, Direction: "asc"}}, formula.Order)
	require.Equal(t, "percent", formula.Legend)
	require.Equal(t, &Having{Expression: "F > 5"}, formula.Having)
	require.Len(t, formula.Functions, 1)
	require.JSONEq(t, `{"name":"clampMax","args":[{"name":"max","value":100}]}`, string(formula.Functions[0]))

	roundTripped, err := json.Marshal(payload)
	require.NoError(t, err)
	var roundTrippedPayload QueryPayload
	require.NoError(t, json.Unmarshal(roundTripped, &roundTrippedPayload))
	roundTrippedFormula := roundTrippedPayload.CompositeQuery.Queries[1].Spec.(FormulaSpec)
	require.Equal(t, formula.Having, roundTrippedFormula.Having)
	require.Equal(t, formula.Order, roundTrippedFormula.Order)
	require.Equal(t, formula.Functions, roundTrippedFormula.Functions)

	payload.CompositeQuery.Queries[1].Spec = FormulaSpec{Name: "F", Expression: "A * 100"}
	require.NoError(t, payload.Validate())
	formula = payload.CompositeQuery.Queries[1].Spec.(FormulaSpec)
	require.Equal(t, DefaultAggregateQueryLimit, formula.Limit)
	require.Equal(t, resultDescendingOrder(), formula.Order)
}

func TestQuerySpecUnmarshal_NormalizesIntegerStringsAndRejectsInvalidShapes(t *testing.T) {
	var query Query
	require.NoError(t, json.Unmarshal([]byte(`{"type":"builder_query","spec":{"name":"A","signal":"logs","limit":"100","offset":"2"}}`), &query))
	spec := query.Spec.(QuerySpec)
	require.Equal(t, 100, spec.Limit)
	require.Equal(t, 2, spec.Offset)
	require.NoError(t, json.Unmarshal([]byte(`{"type":"builder_query","spec":{"name":"A","signal":"logs","limit":100.0,"offset":"2.0"}}`), &query))
	spec = query.Spec.(QuerySpec)
	require.Equal(t, 100, spec.Limit)
	require.Equal(t, 2, spec.Offset)
	require.NoError(t, json.Unmarshal([]byte(`{"type":"builder_formula","spec":{"name":"F","expression":"A","limit":"25.0"}}`), &query))
	require.Equal(t, 25, query.Spec.(FormulaSpec).Limit)

	invalid := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name:    "fractional limit",
			input:   `{"type":"builder_query","spec":{"name":"A","signal":"logs","limit":1.5}}`,
			wantMsg: "spec.limit must be an integer or numeric string",
		},
		{
			name:    "malformed formula limit",
			input:   `{"type":"builder_formula","spec":{"name":"F","expression":"A","limit":"many"}}`,
			wantMsg: "spec.limit must be an integer or numeric string",
		},
		{
			name:    "pathological numeric token",
			input:   `{"type":"builder_query","spec":{"name":"A","signal":"logs","limit":"1e1000000000000000000000000000000"}}`,
			wantMsg: "integer tokens must be at most 32 characters",
		},
		{
			name:    "dashboard orderBy in raw query",
			input:   `{"type":"builder_query","spec":{"name":"A","signal":"logs","orderBy":[{"columnName":"timestamp","order":"desc"}]}}`,
			wantMsg: "spec.orderBy is a dashboard/editor field",
		},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var got Query
			err := json.Unmarshal([]byte(tc.input), &got)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestQueryPayloadValidate_NegativeLimitHasRecoveryGuidance(t *testing.T) {
	payload := QueryPayload{
		Start:       1,
		End:         2,
		RequestType: "raw",
		CompositeQuery: CompositeQuery{Queries: []Query{{
			Type: "builder_query",
			Spec: QuerySpec{Name: "A", Signal: "logs", Limit: -1},
		}}},
	}
	err := payload.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "compositeQuery.queries[0].spec.limit")
	require.Contains(t, err.Error(), "omit/use 0")
	require.Contains(t, err.Error(), "signoz://logs/query-builder-guide")
}

func TestBuildLogsQueryPayload_UsesStablePaginationOrder(t *testing.T) {
	payload := BuildLogsQueryPayload(1, 2, "", DefaultRawQueryLimit, 0)
	spec := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.Equal(t, []Order{
		{Key: Key{Name: "timestamp"}, Direction: "desc"},
		{Key: Key{Name: "id"}, Direction: "desc"},
	}, spec.Order)
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

// TestBuildTracesQueryPayload_PropagatesOffset guards against a regression where
// the traces payload hardcoded Offset:0 and ignored the caller's offset, making
// signoz_search_traces pagination a silent no-op.
func TestBuildTracesQueryPayload_PropagatesOffset(t *testing.T) {
	payload := BuildTracesQueryPayload(1000, 2000, "service.name = 'x'", 50, 25)
	spec, ok := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "expected QuerySpec, got %T", payload.CompositeQuery.Queries[0].Spec)
	require.Equal(t, 50, spec.Limit)
	require.Equal(t, 25, spec.Offset, "offset must propagate into the traces query")
}

func TestBuildTracesQueryPayload_UsesCanonicalTraceFields(t *testing.T) {
	payload := BuildTracesQueryPayload(1000, 2000, "service.name = 'x'", 50, 0)
	spec, ok := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
	require.True(t, ok, "expected QuerySpec, got %T", payload.CompositeQuery.Queries[0].Spec)

	fields := map[string]SelectField{}
	for _, field := range spec.SelectFields {
		fields[field.Name] = field
	}

	for _, deprecated := range []string{
		"traceID",
		"spanID",
		"parentSpanID",
		"durationNano",
		"hasError",
		"statusCode",
		"statusCodeString",
		"statusMessage",
		"spanKind",
		"httpMethod",
		"httpUrl",
		"responseStatusCode",
		"rpcMethod",
	} {
		_, found := fields[deprecated]
		require.False(t, found, "deprecated trace field %q must not be selected", deprecated)
	}

	expected := map[string]SelectField{
		"trace_id": {
			Name: "trace_id", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"span_id": {
			Name: "span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"parent_span_id": {
			Name: "parent_span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"name": {
			Name: "name", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"duration_nano": {
			Name: "duration_nano", FieldDataType: "number", Signal: "traces", FieldContext: "span",
		},
		"timestamp": {
			Name: "timestamp", FieldDataType: "number", Signal: "traces", FieldContext: "span",
		},
		"has_error": {
			Name: "has_error", FieldDataType: "bool", Signal: "traces", FieldContext: "span",
		},
		"status_code": {
			Name: "status_code", FieldDataType: "number", Signal: "traces", FieldContext: "span",
		},
		"status_code_string": {
			Name: "status_code_string", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"status_message": {
			Name: "status_message", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"http_method": {
			Name: "http_method", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"http_url": {
			Name: "http_url", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"kind_string": {
			Name: "kind_string", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"kind": {
			Name: "kind", FieldDataType: "number", Signal: "traces", FieldContext: "span",
		},
		"response_status_code": {
			Name: "response_status_code", FieldDataType: "string", Signal: "traces", FieldContext: "span",
		},
		"rpc.method": {
			Name: "rpc.method", FieldDataType: "string", Signal: "traces", FieldContext: "tag",
		},
		"http.response.status_code": {
			Name: "http.response.status_code", FieldDataType: "number", Signal: "traces", FieldContext: "tag",
		},
	}

	for name, want := range expected {
		require.Equal(t, want, fields[name], "select field %q", name)
	}
}
