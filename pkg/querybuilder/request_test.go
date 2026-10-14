package querybuilder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func builtSpec(t *testing.T, request Request, index int) map[string]any {
	t.Helper()
	body, err := json.Marshal(request)
	require.NoError(t, err)
	return querySpec(t, decode(t, string(body)), index)
}

func TestLogsSearch_OrdersByTimestampThenIDForStablePagination(t *testing.T) {
	request, err := LogsSearch(1, 2, "", DefaultRawQueryLimit, 0)
	require.NoError(t, err)
	spec := builtSpec(t, request, 0)
	require.Equal(t, "logs", spec["signal"])
	requireJSONEqual(t, `[{"key":{"name":"timestamp"},"direction":"desc"},{"key":{"name":"id"},"direction":"desc"}]`, spec["order"])
}

func TestTracesSearch_PropagatesOffsetAndSelectsCanonicalFields(t *testing.T) {
	request, err := TracesSearch(1000, 2000, "service.name = 'x'", 50, 25, TraceDetailSelectFields)
	require.NoError(t, err)
	spec := builtSpec(t, request, 0)
	require.Equal(t, float64(50), spec["limit"])
	require.Equal(t, float64(25), spec["offset"], "offset must propagate into the traces query")

	fields := map[string]map[string]any{}
	for _, raw := range spec["selectFields"].([]any) {
		field := raw.(map[string]any)
		fields[field["name"].(string)] = field
	}
	for _, deprecated := range []string{
		"traceID", "spanID", "parentSpanID", "durationNano", "hasError", "statusCode", "statusCodeString",
		"statusMessage", "spanKind", "httpMethod", "httpUrl", "responseStatusCode", "rpcMethod",
	} {
		require.NotContains(t, fields, deprecated, "deprecated trace field %q must not be selected", deprecated)
	}
	for name, want := range map[string]string{
		"trace_id":                  `{"name":"trace_id","fieldDataType":"string","signal":"traces","fieldContext":"span"}`,
		"duration_nano":             `{"name":"duration_nano","fieldDataType":"number","signal":"traces","fieldContext":"span"}`,
		"has_error":                 `{"name":"has_error","fieldDataType":"bool","signal":"traces","fieldContext":"span"}`,
		"kind":                      `{"name":"kind","fieldDataType":"number","signal":"traces","fieldContext":"span"}`,
		"service.name":              `{"name":"service.name","fieldDataType":"string","signal":"traces","fieldContext":"resource"}`,
		"rpc.method":                `{"name":"rpc.method","fieldDataType":"string","signal":"traces","fieldContext":"tag"}`,
		"http.response.status_code": `{"name":"http.response.status_code","fieldDataType":"number","signal":"traces","fieldContext":"tag"}`,
	} {
		requireJSONEqual(t, want, fields[name])
	}
}

func TestMetricsJSON_SetsSourceOnBuilderQueriesAndBoundsFormulas(t *testing.T) {
	queries := []MetricsQuery{
		{Name: "A", Aggregation: MetricAggregation("signoz.meter.log.size", "delta", "increase", "sum", "")},
		{Name: "B", Aggregation: MetricAggregation("signoz.meter.span.size", "delta", "increase", "sum", "")},
		{Name: "C", IsFormula: true, Expression: "A + B", Legend: "ingested_bytes"},
	}

	out, err := MetricsJSON(1700000000, 1700003600, 60, queries, "time_series", "meter")
	require.NoError(t, err)
	request := decode(t, string(out))
	for i := range 2 {
		spec := querySpec(t, request, i)
		require.Equal(t, "meter", spec["source"], "query %d: source must be set", i)
		require.Equal(t, float64(DefaultFormulaInputQueryLimit), spec["limit"])
		requireJSONEqual(t, `[{"key":{"name":"__result"},"direction":"desc"}]`, spec["order"])
	}
	formula := querySpec(t, request, 2)
	require.Equal(t, float64(DefaultAggregateQueryLimit), formula["limit"])
	requireJSONEqual(t, `[{"key":{"name":"__result"},"direction":"desc"}]`, formula["order"])
	require.Equal(t, 2, strings.Count(string(out), `"source":"meter"`), "source belongs on every builder_query, never on the formula: %s", out)

	outEmpty, err := MetricsJSON(1700000000, 1700003600, 60, queries, "time_series", "")
	require.NoError(t, err)
	require.NotContains(t, string(outEmpty), `"source"`, "an empty source must be omitted: %s", outEmpty)
}
