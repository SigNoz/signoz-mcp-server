package querybuilder

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var request map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	return request
}

func querySpec(t *testing.T, request map[string]any, index int) map[string]any {
	t.Helper()
	queries := request["compositeQuery"].(map[string]any)["queries"].([]any)
	return queries[index].(map[string]any)["spec"].(map[string]any)
}

func requireJSONEqual(t *testing.T, want string, got any) {
	t.Helper()
	body, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, want, string(body))
}

func TestApplyBounds_DefaultsByRequestType(t *testing.T) {
	tests := []struct {
		name        string
		requestType string
		spec        string
		wantLimit   int
		wantOrder   string
	}{
		{"raw logs", "raw", `{"name":"A","signal":"logs"}`, DefaultRawQueryLimit,
			`[{"key":{"name":"timestamp"},"direction":"desc"},{"key":{"name":"id"},"direction":"desc"}]`},
		{"raw traces", "raw", `{"name":"A","signal":"traces"}`, DefaultRawQueryLimit,
			`[{"key":{"name":"timestamp"},"direction":"desc"}]`},
		{"trace request", "trace", `{"name":"A","signal":"traces"}`, DefaultRawQueryLimit,
			`[{"key":{"name":"timestamp"},"direction":"desc"}]`},
		{"scalar logs", "scalar", `{"name":"A","signal":"logs","aggregations":[{"expression":"count()"}]}`, DefaultAggregateQueryLimit,
			`[{"key":{"name":"count()"},"direction":"desc"}]`},
		{"display alias stripped from the default order key", "scalar",
			`{"name":"A","signal":"logs","aggregations":[{"expression":"count() AS 'Requests'"}]}`, DefaultAggregateQueryLimit,
			`[{"key":{"name":"count()"},"direction":"desc"}]`},
		{"quoted as inside the aggregation survives alias stripping", "scalar",
			`{"name":"A","signal":"logs","aggregations":[{"expression":"countIf(message = 'read as write') AS 'Requests'"}]}`, DefaultAggregateQueryLimit,
			`[{"key":{"name":"countIf(message = 'read as write')"},"direction":"desc"}]`},
		{"time series traces", "time_series",
			`{"name":"A","signal":"traces","stepInterval":60,"aggregations":[{"expression":"p99(duration_nano)"}]}`, DefaultAggregateQueryLimit,
			`[{"key":{"name":"p99(duration_nano)"},"direction":"desc"}]`},
		{"scalar metrics", "scalar",
			`{"name":"A","signal":"metrics","aggregations":[{"metricName":"cpu","spaceAggregation":"sum"}]}`, DefaultAggregateQueryLimit,
			`[{"key":{"name":"__result"},"direction":"desc"}]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := decode(t, `{"requestType":"`+tc.requestType+`","compositeQuery":{"queries":[{"type":"builder_query","spec":`+tc.spec+`}]}}`)
			applied := ApplyBounds(request)
			spec := querySpec(t, request, 0)
			require.Equal(t, tc.wantLimit, spec["limit"])
			requireJSONEqual(t, tc.wantOrder, spec["order"])
			require.Len(t, applied, 1)
			require.True(t, applied[0].LimitDefaulted)
			require.True(t, applied[0].OrderDefaulted)
		})
	}
}

// SigNoz orders AI trace rows by last activity and accepts only per-trace keys
// there, so a builder_ai_query gets a reported default limit but no inserted order.
func TestApplyBounds_BuilderAIQueryGetsDefaultLimitAndNoOrder(t *testing.T) {
	for _, tc := range []struct {
		requestType string
		spec        string
		wantLimit   int
	}{
		{"trace", `{"name":"A","signal":"traces","filter":{"expression":"trace.tool_call_count > 10"}}`, DefaultRawQueryLimit},
		{"scalar", `{"name":"A","signal":"traces","limit":"0","aggregations":[{"expression":"avg(trace.estimated_total_cost)"}]}`, DefaultAggregateQueryLimit},
	} {
		t.Run(tc.requestType, func(t *testing.T) {
			request := decode(t, `{"requestType":"`+tc.requestType+`","compositeQuery":{"queries":[{"type":"builder_ai_query","spec":`+tc.spec+`}]}}`)
			applied := ApplyBounds(request)
			spec := querySpec(t, request, 0)
			require.Equal(t, tc.wantLimit, spec["limit"])
			require.NotContains(t, spec, "order", "a builder_ai_query must reach SigNoz without an inserted order")
			require.Equal(t, []AppliedBounds{{QueryIndex: 0, QueryName: `query "A"`, Limit: tc.wantLimit, LimitDefaulted: true}}, applied)
		})
	}
}

func TestApplyBounds_LeavesAuthoredBoundsUntouched(t *testing.T) {
	request := decode(t, `{"requestType":"time_series","compositeQuery":{"queries":[{"type":"builder_query","spec":{
		"name":"A","signal":"logs","limit":42,"stepInterval":60,"aggregations":[{"expression":"count()"}],
		"order":[{"key":{"name":"service.name","description":"Service name","unit":"short","signal":"logs","fieldContext":"resource","fieldDataType":"string"},"direction":"asc"}]
	}}]}}`)
	applied := ApplyBounds(request)
	spec := querySpec(t, request, 0)
	require.Equal(t, float64(42), spec["limit"])
	requireJSONEqual(t, `[{"key":{"name":"service.name","description":"Service name","unit":"short","signal":"logs","fieldContext":"resource","fieldDataType":"string"},"direction":"asc"}]`, spec["order"])
	require.Empty(t, applied)
}

func TestApplyBounds_LeavesFormulaInputsUnboundedAndBoundsFormulaResults(t *testing.T) {
	request := decode(t, `{"requestType":"time_series","compositeQuery":{"queries":[
		{"type":"builder_query","spec":{"name":"A","signal":"metrics","aggregations":[{"metricName":"errors","spaceAggregation":"sum"}]}},
		{"type":"builder_query","spec":{"name":"B","signal":"metrics","limit":42,"order":[{"key":{"name":"__result"},"direction":"desc"}],"aggregations":[{"metricName":"requests","spaceAggregation":"sum"}]}},
		{"type":"builder_query","spec":{"name":"AA","signal":"metrics","aggregations":[{"metricName":"unrelated","spaceAggregation":"sum"}]}},
		{"type":"builder_formula","spec":{"name":"F1","expression":"A / B","disabled":true}},
		{"type":"builder_formula","spec":{"name":"F2","expression":"A * 100","limit":"25","order":[{"key":{"name":"F2"},"direction":"asc"}]}}
	]}}`)
	applied := ApplyBounds(request)

	require.NotContains(t, querySpec(t, request, 0), "limit", "a formula input keeps every series, even under a disabled intermediate formula")
	require.NotContains(t, querySpec(t, request, 0), "order")
	require.Equal(t, float64(42), querySpec(t, request, 1)["limit"], "an authored input limit is kept")
	require.Equal(t, DefaultAggregateQueryLimit, querySpec(t, request, 2)["limit"], "AA is not referenced by A / B")
	require.Equal(t, DefaultAggregateQueryLimit, querySpec(t, request, 3)["limit"])
	requireJSONEqual(t, `[{"key":{"name":"__result"},"direction":"desc"}]`, querySpec(t, request, 3)["order"])
	require.Equal(t, 25, querySpec(t, request, 4)["limit"], "a numeric-string formula limit is normalized and kept")
	requireJSONEqual(t, `[{"key":{"name":"F2"},"direction":"asc"}]`, querySpec(t, request, 4)["order"])

	require.Len(t, applied, 2)
}

// Every field other than an inserted limit or order reaches SigNoz exactly as
// the caller sent it, so SigNoz, not this server, judges the request.
func TestApplyBounds_ForwardsEveryOtherFieldVerbatim(t *testing.T) {
	const body = `{
		"schemaVersion":"v1","start":1700000000000,"end":1700003600000,"requestType":"raw","noCache":true,"futureTopLevel":{"kept":1},
		"compositeQuery":{"queries":[
			{"type":"builder_query","spec":{"name":"A","signal":"logs","source":"meter","stepInterval":"60s","cursor":"c1",
				"filter":{"expression":"search('it\\'s', body) AND severity_text = 'ERROR'"},
				"selectFields":[{"name":"body","fieldContext":"log"}],"limitBy":{"keys":["service.name"],"value":"5"},
				"bucketOptions":{"kind":"linear"},"functions":[{"name":"ewma3"}],"legend":"{{service.name}}","orderBy":[{"columnName":"x"}]}},
			{"type":"promql","spec":{"name":"P","query":"rate(x[5m])","step":"1m"}},
			{"type":"clickhouse_sql","spec":{"name":"C","query":"SELECT 1"}},
			{"type":"builder_trace_operator","spec":{"name":"T","expression":"A => B","returnSpansFrom":"A"}}
		]},
		"formatOptions":{"formatTableResultForUI":false,"fillGaps":true},"variables":{"env":{"type":"query","value":"prod"}}
	}`
	request := decode(t, body)
	ApplyBounds(request)

	want := decode(t, body)
	spec := querySpec(t, want, 0)
	spec["limit"] = DefaultRawQueryLimit
	spec["order"] = jsonValue(defaultOrder(queryView{builder: true, signal: "logs"}, "raw"))
	requireJSONEqual(t, mustMarshal(t, want), request)
}

func TestApplyBounds_LeavesQueriesItCannotReadForSigNoz(t *testing.T) {
	const body = `{"requestType":"raw","compositeQuery":{"queries":[
		{"type":"builder_query","spec":{"name":"A","signal":"bogus"}},
		{"type":"builder_query","spec":{"name":"B","signal":"logs","limit":"lots"}},
		{"type":"builder_query"}
	]}}`
	request := decode(t, body)
	require.Empty(t, ApplyBounds(request))
	requireJSONEqual(t, body, request)
}

func TestApplyBounds_InsertsNoOrderWhereNoneIsSafe(t *testing.T) {
	request := decode(t, `{"requestType":"raw_stream","compositeQuery":{"queries":[{"type":"builder_query","spec":{"name":"A","signal":"logs"}}]}}`)
	applied := ApplyBounds(request)
	spec := querySpec(t, request, 0)
	require.Equal(t, DefaultAggregateQueryLimit, spec["limit"])
	require.NotContains(t, spec, "order")
	require.Len(t, applied, 1)
	require.False(t, applied[0].OrderDefaulted)
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return string(body)
}

var (
	plainGuideExamplePattern = regexp.MustCompile(`(?s)--- Example \d+:[^\n]*---\n\n(\{.*?\n\})\n`)
	markdownJSONPattern      = regexp.MustCompile("(?s)```json\\n(.*?)\\n```")
)

// The guides teach explicit bounds: every example already carries the limit and
// order this server would insert, so following an example never depends on the
// server's defaults.
func TestQueryBuilderGuideExamplesTeachTheInsertedBounds(t *testing.T) {
	tests := []struct {
		name    string
		guide   string
		pattern *regexp.Regexp
	}{
		{name: "logs", guide: LogsQueryBuilderGuide, pattern: plainGuideExamplePattern},
		{name: "traces", guide: TracesQueryBuilderGuide, pattern: plainGuideExamplePattern},
		{name: "metrics", guide: metricsrules.MetricsGuide, pattern: markdownJSONPattern},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			examples := tc.pattern.FindAllStringSubmatch(tc.guide, -1)
			require.NotEmpty(t, examples, "the example pattern no longer matches the guide; fix the pattern, or this test checks nothing")
			for index, match := range examples {
				example := decode(t, match[1])
				require.Emptyf(t, ApplyBounds(example), "example %d omits a limit or order the server would insert", index+1)

				stripped := decode(t, match[1])
				queries := stripped["compositeQuery"].(map[string]any)["queries"].([]any)
				for _, raw := range queries {
					if spec, ok := raw.(map[string]any)["spec"].(map[string]any); ok {
						delete(spec, "limit")
						delete(spec, "order")
					}
				}
				ApplyBounds(stripped)
				requireJSONEqual(t, mustMarshal(t, stripped), example)
			}
		})
	}
}
