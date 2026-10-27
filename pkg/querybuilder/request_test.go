package querybuilder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	"github.com/stretchr/testify/require"
)

func builtSpec(t *testing.T, request Request, index int) map[string]any {
	t.Helper()
	body, err := json.Marshal(request)
	require.NoError(t, err)
	return querySpec(t, decode(t, string(body)), index)
}

func TestLogsSearch_OrdersByTimestampThenIDForStablePagination(t *testing.T) {
	request, err := LogsSearch(1, 2, "", DefaultRawQueryLimit, 0, RawSearchOptions{})
	require.NoError(t, err)
	spec := builtSpec(t, request, 0)
	require.Equal(t, "logs", spec["signal"])
	requireJSONEqual(t, `[{"key":{"name":"timestamp"},"direction":"desc"},{"key":{"name":"id"},"direction":"desc"}]`, spec["order"])

	request, err = LogsSearch(1, 2, "", DefaultRawQueryLimit, 0, RawSearchOptions{OrderKey: "severity_number", OrderDir: "asc"})
	require.NoError(t, err)
	requireJSONEqual(t, `[{"key":{"name":"severity_number"},"direction":"asc"},{"key":{"name":"id"},"direction":"asc"}]`, builtSpec(t, request, 0)["order"])
}

func TestSpanSearch_PropagatesOffsetAndSelectsNoDeprecatedFields(t *testing.T) {
	request, err := SpanSearch(1000, 2000, TraceSearchOptions{Filter: "service.name = 'x'", Limit: 50, Offset: 25, SelectFields: TraceSearchCoreFields})
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

func TestMetricsJSON_ResultShapingTargetsTheFormulaWhenPresent(t *testing.T) {
	primary := MetricsQuery{
		Name:        "A",
		Aggregation: MetricAggregation("http_requests_total", "cumulative", "rate", "sum", ""),
		Functions:   []Function{{Name: new(apitypes.Querybuildertypesv5FunctionName("absolute"))}},
	}
	formula := MetricsQuery{Name: "C", IsFormula: true, Expression: "A / B", Limit: 3, OrderKey: "__result", OrderDir: "asc", Having: "__result > 0.5"}
	out, err := MetricsJSON(1, 2, 0, []MetricsQuery{primary, {Name: "B", Aggregation: primary.Aggregation}, formula}, "time_series", "")
	require.NoError(t, err)
	request := decode(t, string(out))
	require.NotContains(t, querySpec(t, request, 0), "limit", "formula inputs stay unbounded")
	requireJSONEqual(t, `[{"name":"absolute"}]`, querySpec(t, request, 0)["functions"])
	formulaSpec := querySpec(t, request, 2)
	require.Equal(t, float64(3), formulaSpec["limit"])
	requireJSONEqual(t, `[{"key":{"name":"__result"},"direction":"asc"}]`, formulaSpec["order"])
	// SigNoz accepts a formula having but ignores it, so the builder never sends one.
	require.NotContains(t, formulaSpec, "having")

	standalone := MetricsQuery{Name: "A", Aggregation: primary.Aggregation, Limit: 7, Having: "__result > 1"}
	out, err = MetricsJSON(1, 2, 0, []MetricsQuery{standalone}, "scalar", "")
	require.NoError(t, err)
	spec := querySpec(t, decode(t, string(out)), 0)
	require.Equal(t, float64(7), spec["limit"], "a caller limit must survive ApplyBounds")
	// An omitted order still gets ApplyBounds' default.
	requireJSONEqual(t, `[{"key":{"name":"__result"},"direction":"desc"}]`, spec["order"])
	requireJSONEqual(t, `{"expression":"__result > 1"}`, spec["having"])
}
