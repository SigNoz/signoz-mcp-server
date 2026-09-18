package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/stretchr/testify/require"
)

func heatmapToolQuery(t *testing.T, queries, formatOptions string) map[string]any {
	t.Helper()
	if formatOptions == "" {
		formatOptions = "{\"formatTableResultForUI\":false,\"fillGaps\":false}"
	}
	body := "{\"schemaVersion\":\"v1\",\"start\":1711123200000,\"end\":1711209600000,\"requestType\":\"heatmap\",\"compositeQuery\":{\"queries\":[" + queries + "]},\"formatOptions\":" + formatOptions + ",\"variables\":{}}"
	var query map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &query))
	return query
}

func executeHeatmap(t *testing.T, query map[string]any, response json.RawMessage) (*capturedHeatmapResult, error) {
	t.Helper()
	captured := &capturedHeatmapResult{}
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured.body = append([]byte(nil), body...)
			if response == nil {
				return json.RawMessage("{\"status\":\"success\",\"data\":{}}"), nil
			}
			return response, nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
	captured.result = result
	return captured, err
}

type capturedHeatmapResult struct {
	body   []byte
	result *mcp.CallToolResult
}

func TestHandleExecuteBuilderQuery_HeatmapEnabledOutputTypes(t *testing.T) {
	order := "[{\"key\":{\"name\":\"__result\"},\"direction\":\"desc\"}]"
	tests := []struct {
		name    string
		queries string
		check   func(*testing.T, types.QueryPayload)
	}{
		{
			name:    "metrics builder query",
			queries: "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"disabled\":false,\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}],\"limit\":37,\"order\":" + order + ",\"bucketOptions\":{\"kind\":\"log\",\"spec\":{\"scale\":-2}}}}",
			check: func(t *testing.T, payload types.QueryPayload) {
				spec := payload.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
				require.Equal(t, 37, spec.Limit)
				require.Equal(t, "log", spec.BucketOptions.Kind)
				scale := spec.BucketOptions.Spec.(types.LogBucketsSpec).Scale
				require.NotNil(t, scale)
				require.Equal(t, -2, *scale)
			},
		},
		{
			name: "formula over disabled metric input",
			queries: "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"disabled\":true,\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}],\"limit\":10000,\"order\":" + order + "}}," +
				"{\"type\":\"builder_formula\",\"spec\":{\"name\":\"F\",\"expression\":\"A\",\"disabled\":false,\"limit\":100,\"order\":" + order + ",\"bucketOptions\":{\"kind\":\"linear\",\"spec\":{\"maxValue\":5,\"numBuckets\":0}}}}",
			check: func(t *testing.T, payload types.QueryPayload) {
				formula := payload.CompositeQuery.Queries[1].Spec.(types.FormulaSpec)
				require.Equal(t, 100, formula.Limit)
				require.Equal(t, "linear", formula.BucketOptions.Kind)
				numBuckets := formula.BucketOptions.Spec.(types.LinearBucketsSpec).NumBuckets
				require.NotNil(t, numBuckets)
				require.Equal(t, 0, *numBuckets)
			},
		},
		{
			name:    "promql",
			queries: "{\"type\":\"promql\",\"spec\":{\"name\":\"P\",\"query\":\"up\",\"disabled\":false}}",
		},
		{
			name:    "clickhouse sql",
			queries: "{\"type\":\"clickhouse_sql\",\"spec\":{\"name\":\"C\",\"query\":\"SELECT 1\",\"disabled\":false}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured, err := executeHeatmap(t, heatmapToolQuery(t, tt.queries, ""), nil)
			require.NoError(t, err)
			require.False(t, captured.result.IsError)
			var payload types.QueryPayload
			require.NoError(t, json.Unmarshal(captured.body, &payload))
			require.Equal(t, "heatmap", payload.RequestType)
			if tt.check != nil {
				tt.check(t, payload)
			}
		})
	}
}

func TestHandleExecuteBuilderQuery_HeatmapRawResponseAndBucketCountsPreserved(t *testing.T) {
	response := json.RawMessage("{\"status\":\"success\",\"data\":{\"data\":{\"results\":[{\"series\":[{\"labels\":{\"host.name\":\"a\"},\"values\":[[1,2,3,4],[2,3,4,5]]},{\"labels\":{\"host.name\":\"b\"},\"values\":[[9,0,0,0],[9,0,0,0]]}]}],\"aggregations\":[{\"meta\":{\"buckets\":[0.1,1,10]},\"series\":[{\"values\":[[1,2,3,4],[2,3,4,5]]}]}]}}}")
	query := heatmapToolQuery(t, "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}],\"limit\":2,\"order\":[{\"key\":{\"name\":\"__result\"},\"direction\":\"desc\"}]}}", "")

	captured, err := executeHeatmap(t, query, response)
	require.NoError(t, err)
	result := captured.result
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1, "bucket counts must stay in the raw body, with no reshaping note")
	require.Equal(t, string(response), noteText(t, result, 0))
	assertHeatmapSeriesRankedByBucketSums(t, response)
	require.NotContains(t, string(response), "\"value\":", "heatmap is not a scalar response")
}

func TestHandleExecuteBuilderQuery_HeatmapAppliedBounds(t *testing.T) {
	t.Run("standalone query uses 100 and result order", func(t *testing.T) {
		query := heatmapToolQuery(t, "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}]}}", "")
		captured, err := executeHeatmap(t, query, nil)
		require.NoError(t, err)
		result := captured.result
		require.False(t, result.IsError)
		require.Len(t, result.Content, 2)
		require.Contains(t, noteText(t, result, 1), "limit=100")
		require.Contains(t, noteText(t, result, 1), "__result desc")

		var payload types.QueryPayload
		require.NoError(t, json.Unmarshal(captured.body, &payload))
		spec := payload.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
		require.Equal(t, 100, spec.Limit)
		require.Equal(t, "__result", spec.Order[0].Key.Name)
	})

	t.Run("formula input and output bounds support bucket-sum ranking upstream", func(t *testing.T) {
		queries := "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"disabled\":true,\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}]}}," +
			"{\"type\":\"builder_formula\",\"spec\":{\"name\":\"F\",\"expression\":\"A\",\"disabled\":false}}"
		captured, err := executeHeatmap(t, heatmapToolQuery(t, queries, ""), nil)
		require.NoError(t, err)
		result := captured.result
		require.False(t, result.IsError)
		note := noteText(t, result, 1)
		require.Contains(t, note, `query "A": limit=10000 (formula-input default`)
		require.Contains(t, note, `query "F": limit=100 (request-type default)`)

		var payload types.QueryPayload
		require.NoError(t, json.Unmarshal(captured.body, &payload))
		input := payload.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
		formula := payload.CompositeQuery.Queries[1].Spec.(types.FormulaSpec)
		require.Equal(t, types.DefaultFormulaInputQueryLimit, input.Limit)
		require.Equal(t, types.DefaultAggregateQueryLimit, formula.Limit)
	})
}

func assertHeatmapSeriesRankedByBucketSums(t *testing.T, response json.RawMessage) {
	t.Helper()
	var body struct {
		Data struct {
			Data struct {
				Results []struct {
					Series []struct {
						Labels map[string]string
						Values [][]float64
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(response, &body))
	require.NotEmpty(t, body.Data.Data.Results)
	var sums []float64
	for _, series := range body.Data.Data.Results[0].Series {
		var sum float64
		for _, values := range series.Values {
			for _, count := range values {
				sum += count
			}
		}
		sums = append(sums, sum)
	}
	require.NotEmpty(t, sums)
	for i := 1; i < len(sums); i++ {
		require.GreaterOrEqual(t, sums[i-1], sums[i], "heatmap series must remain ranked by summed bucket counts")
	}
}

func TestHandleExecuteBuilderQuery_HeatmapInvalidRequestIsCoded(t *testing.T) {
	tests := []struct {
		name    string
		queries string
		options string
		want    string
	}{
		{name: "fill gaps", queries: heatmapBuilderQuery(""), options: "{\"formatTableResultForUI\":false,\"fillGaps\":true}", want: "fillGaps is not supported"},
		{name: "nonheatmap options", queries: heatmapBuilderQuery(",\"bucketOptions\":{\"kind\":\"log\",\"spec\":{}}"), want: "bucketOptions are only supported"},
		{name: "two enabled", queries: heatmapBuilderQuery("") + "," + heatmapBuilderQuery(""), want: "one distribution"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := heatmapToolQuery(t, tt.queries, tt.options)
			if tt.name == "nonheatmap options" {
				query["requestType"] = "time_series"
			}
			captured, err := executeHeatmap(t, query, nil)
			require.NoError(t, err)
			result := captured.result
			require.True(t, result.IsError)
			require.Contains(t, noteText(t, result, 0), tt.want)
			require.Equal(t, CodeValidationFailed, resultCode(t, result))
		})
	}
}

func heatmapBuilderQuery(bucketOptions string) string {
	order := "[{\"key\":{\"name\":\"__result\"},\"direction\":\"desc\"}]"
	return "{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"metrics\",\"aggregations\":[{\"metricName\":\"system.cpu.usage\",\"spaceAggregation\":\"avg\"}],\"limit\":100,\"order\":" + order + bucketOptions + "}}"
}
