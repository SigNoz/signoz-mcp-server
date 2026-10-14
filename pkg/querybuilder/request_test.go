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

func TestTracesSearch_PropagatesOffsetAndSelectsNoDeprecatedFields(t *testing.T) {
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
	require.NotEmpty(t, fields, "the traces query must carry the selected fields")
	for _, deprecated := range []string{
		"traceID", "spanID", "parentSpanID", "durationNano", "hasError", "statusCode", "statusCodeString",
		"statusMessage", "spanKind", "httpMethod", "httpUrl", "responseStatusCode", "rpcMethod",
	} {
		require.NotContains(t, fields, deprecated, "deprecated trace field %q must not be selected", deprecated)
	}
}

func TestMetricsJSON_SetsSourceOnBuilderQueriesAndAppliesBounds(t *testing.T) {
	queries := []MetricsQuery{
		{Name: "A", Aggregation: MetricAggregation("signoz.meter.log.size", "delta", "increase", "sum", "")},
		{Name: "B", Aggregation: MetricAggregation("signoz.meter.span.size", "delta", "increase", "sum", "")},
		{Name: "C", IsFormula: true, Expression: "A + B", Legend: "ingested_bytes"},
	}

	out, err := MetricsJSON(1700000000, 1700003600, 60, queries, "time_series", "meter")
	require.NoError(t, err)
	request := decode(t, string(out))
	for i := range 2 {
		require.Equal(t, "meter", querySpec(t, request, i)["source"], "query %d: source must be set", i)
	}
	require.Contains(t, querySpec(t, request, 2), "limit", "MetricsJSON applies the result bounds; ApplyBounds' tests own their values")
	require.Equal(t, 2, strings.Count(string(out), `"source":"meter"`), "source belongs on every builder_query, never on the formula: %s", out)

	outEmpty, err := MetricsJSON(1700000000, 1700003600, 60, queries, "time_series", "")
	require.NoError(t, err)
	require.NotContains(t, string(outEmpty), `"source"`, "an empty source must be omitted: %s", outEmpty)
}
