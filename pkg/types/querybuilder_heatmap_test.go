package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func heatmapPayloadJSON(queries string, formatOptions string) string {
	if formatOptions == "" {
		formatOptions = `{"formatTableResultForUI":false,"fillGaps":false}`
	}
	return `{"schemaVersion":"v1","start":1700000000000,"end":1700003600000,"requestType":"heatmap","compositeQuery":{"queries":[` + queries + `]},"formatOptions":` + formatOptions + `,"variables":{}}`
}

func unmarshalValidHeatmap(t *testing.T, body string) QueryPayload {
	t.Helper()
	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	require.NoError(t, payload.Validate())
	return payload
}

func TestQueryPayloadHeatmap_MetricQueryBucketVariantsRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		bucketJSON   string
		wantFiltered string
	}{
		{name: "absent options stays absent", bucketJSON: ``, wantFiltered: ``},
		{name: "log empty spec stays empty", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{}}`, wantFiltered: `"bucketOptions":{"kind":"log","spec":{}}`},
		{name: "log scale preserved", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{"scale":-2}}`, wantFiltered: `"bucketOptions":{"kind":"log","spec":{"scale":-2}}`},
		{name: "linear minimal", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10}}`, wantFiltered: `"bucketOptions":{"kind":"linear","spec":{"maxValue":10}}`},
		{name: "authored zero numBuckets preserved", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":0}}`, wantFiltered: `"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":0}}`},
		{name: "authored numBuckets preserved", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":512}}`, wantFiltered: `"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":512}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := heatmapPayloadJSON(`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":false,"aggregations":[{"metricName":"system.cpu.usage","spaceAggregation":"avg"}],"limit":25,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""}`+tt.bucketJSON+`}}`, "")
			payload := unmarshalValidHeatmap(t, body)
			out, err := json.Marshal(payload)
			require.NoError(t, err)
			if tt.wantFiltered == "" {
				require.NotContains(t, string(out), "bucketOptions")
			} else {
				require.Contains(t, string(out), tt.wantFiltered)
			}
			require.Contains(t, string(out), `"limit":25`, "authored limit must remain untouched")
		})
	}
}

func TestQueryPayloadHeatmap_FormulaBucketVariantsRoundTrip(t *testing.T) {
	// Disabled metric input feeds the enabled formula; per the released
	// contract its omitted limit defaults to 10000 while the formula result
	// defaults to 100, and both decisions are recorded in AppliedBounds.
	body := heatmapPayloadJSON(
		`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":true,"aggregations":[{"metricName":"system.cpu.usage","spaceAggregation":"avg"}]}},`+
			`{"type":"builder_formula","spec":{"name":"F","expression":"A","disabled":false,"bucketOptions":{"kind":"linear","spec":{"maxValue":5,"numBuckets":0}}}}`, "")
	payload := unmarshalValidHeatmap(t, body)
	require.Len(t, payload.AppliedBounds, 2)

	out, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Contains(t, string(out), `"bucketOptions":{"kind":"linear","spec":{"maxValue":5,"numBuckets":0}}`)

	var round QueryPayload
	require.NoError(t, json.Unmarshal(out, &round))
	input := round.CompositeQuery.Queries[0].Spec.(QuerySpec)
	formula := round.CompositeQuery.Queries[1].Spec.(FormulaSpec)
	require.Equal(t, 10000, input.Limit, "formula input gets the wide bound")
	require.Equal(t, 100, formula.Limit, "enabled formula output gets the 100-series default")
	require.Equal(t, "linear", formula.BucketOptions.Kind)
}

func TestQueryPayloadHeatmap_InvalidBucketOptions(t *testing.T) {
	tests := []struct {
		name       string
		bucketJSON string
		wantError  string
	}{
		{name: "unknown kind", bucketJSON: `,"bucketOptions":{"kind":"exponential","spec":{}}`, wantError: "invalid bucketOptions kind"},
		{name: "missing kind", bucketJSON: `,"bucketOptions":{"spec":{}}`, wantError: "invalid bucketOptions kind"},
		{name: "missing spec", bucketJSON: `,"bucketOptions":{"kind":"log"}`, wantError: "bucketOptions spec is required"},
		{name: "linear missing maxValue", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{}}`, wantError: "linear buckets need a finite maxValue greater than 0"},
		{name: "linear zero maxValue", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":0}}`, wantError: "linear buckets need a finite maxValue greater than 0"},
		{name: "linear negative maxValue", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":-1}}`, wantError: "linear buckets need a finite maxValue greater than 0"},
		{name: "linear numBuckets above max", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":513}}`, wantError: "numBuckets must be between 1 and 512"},
		{name: "linear negative numBuckets", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"numBuckets":-1}}`, wantError: "numBuckets must be between 1 and 512"},
		{name: "log scale above max", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{"scale":5}}`, wantError: "scale must be between -4 and 4"},
		{name: "log scale below min", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{"scale":-5}}`, wantError: "scale must be between -4 and 4"},
		{name: "unknown options field", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{},"buckets":8}`, wantError: "bucketOptions rejects unknown field"},
		{name: "unknown linear spec field", bucketJSON: `,"bucketOptions":{"kind":"linear","spec":{"maxValue":10,"minValue":0}}`, wantError: "linear buckets spec rejects unknown field"},
		{name: "unknown log spec field", bucketJSON: `,"bucketOptions":{"kind":"log","spec":{"scale":2,"base":10}}`, wantError: "log buckets spec rejects unknown field"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := heatmapPayloadJSON(`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":false,"aggregations":[{"metricName":"system.cpu.usage","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""}`+tt.bucketJSON+`}}`, "")
			var payload QueryPayload
			err := json.Unmarshal([]byte(body), &payload)
			if err == nil {
				err = payload.Validate()
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestQueryPayloadHeatmap_RequestMatrix(t *testing.T) {
	metric := func(name string, mods string) string {
		return `{"type":"builder_query","spec":{"name":"` + name + `","signal":"metrics","disabled":false,"aggregations":[{"metricName":"system.cpu.usage","spaceAggregation":"avg"}],` + mods + `"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""}}}`
	}

	tests := []struct {
		name      string
		queries   string
		options   string
		wantError string
	}{
		{
			name:      "fillGaps rejected",
			queries:   metric("A", ""),
			options:   `{"formatTableResultForUI":false,"fillGaps":true}`,
			wantError: "fillGaps is not supported for heatmap requests",
		},
		{
			name:      "functions rejected on disabled input",
			queries:   `{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":true,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"functions":[{"name":"cutOffMin"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""}}},` + metric("B", ""),
			wantError: "functions are not supported for heatmap requests",
		},
		{
			name:      "having rejected on disabled input",
			queries:   `{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":true,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":"count() > 5"}}},` + metric("B", ""),
			wantError: "having is not supported for heatmap requests",
		},
		{
			name:      "zero enabled rejected",
			queries:   `{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":true,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""}}}`,
			wantError: "a heatmap needs one enabled query",
		},
		{
			name:      "two enabled rejected",
			queries:   metric("A", "") + "," + metric("B", ""),
			wantError: "a heatmap renders one distribution",
		},
		{
			name:      "builder and promql both enabled rejected",
			queries:   metric("A", "") + `,{"type":"promql","spec":{"name":"P","query":"up"}}`,
			wantError: "a heatmap renders one distribution",
		},
		{
			name:      "logs signal rejected even disabled",
			queries:   `{"type":"builder_query","spec":{"name":"A","signal":"logs","disabled":true,"limit":10,"order":[{"key":{"name":"timestamp"},"direction":"desc"}],"having":{"expression":""}}},` + metric("B", ""),
			wantError: "heatmaps are not supported for the logs and traces signals yet",
		},
		{
			name:      "unknown envelope rejected",
			queries:   `{"type":"builder_sub_query","spec":{"name":"S","expression":"A"}}`,
			wantError: "heatmap requests support one metrics builder query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload QueryPayload
			err := json.Unmarshal([]byte(heatmapPayloadJSON(tt.queries, tt.options)), &payload)
			if err == nil {
				err = payload.Validate()
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestQueryPayloadHeatmap_NullSpecMatchesUpstream(t *testing.T) {
	t.Run("log null spec accepted as defaults", func(t *testing.T) {
		body := heatmapPayloadJSON(`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":false,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""},"bucketOptions":{"kind":"log","spec":null}}}`, "")
		payload := unmarshalValidHeatmap(t, body)
		spec := payload.CompositeQuery.Queries[0].Spec.(QuerySpec)
		require.Equal(t, LogBucketsSpec{}, spec.BucketOptions.Spec)
	})

	t.Run("linear null spec rejected when enabled", func(t *testing.T) {
		body := heatmapPayloadJSON(`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":false,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""},"bucketOptions":{"kind":"linear","spec":null}}}`, "")
		var payload QueryPayload
		require.NoError(t, json.Unmarshal([]byte(body), &payload))
		err := payload.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "linear buckets need a finite maxValue greater than 0")
	})

	t.Run("linear null spec passes structural decode when disabled", func(t *testing.T) {
		body := heatmapPayloadJSON(`{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":true,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""},"bucketOptions":{"kind":"linear","spec":null}}},`+
			`{"type":"builder_formula","spec":{"name":"F","expression":"A","disabled":false}}`, "")
		unmarshalValidHeatmap(t, body)
	})
}

func TestQueryPayload_NonHeatmapBucketOptions(t *testing.T) {
	builder := func(disabled bool) string {
		return `{"type":"builder_query","spec":{"name":"A","signal":"metrics","disabled":` + jsonBool(disabled) + `,"aggregations":[{"metricName":"m","spaceAggregation":"avg"}],"limit":10,"order":[{"key":{"name":"__result"},"direction":"desc"}],"having":{"expression":""},"bucketOptions":{"kind":"log","spec":{}}}}`
	}

	t.Run("enabled rejected outside heatmap", func(t *testing.T) {
		body := strings.Replace(heatmapPayloadJSON(builder(false), ""), `"requestType":"heatmap"`, `"requestType":"time_series"`, 1)
		var payload QueryPayload
		require.NoError(t, json.Unmarshal([]byte(body), &payload))
		err := payload.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "bucketOptions are only supported for heatmap requests")
	})

	t.Run("disabled ignored outside heatmap, matching upstream", func(t *testing.T) {
		body := strings.Replace(heatmapPayloadJSON(builder(true)+`,{"type":"builder_formula","spec":{"name":"F","expression":"A","disabled":false}}`, ""), `"requestType":"heatmap"`, `"requestType":"time_series"`, 1)
		var payload QueryPayload
		require.NoError(t, json.Unmarshal([]byte(body), &payload))
		require.NoError(t, payload.Validate())
	})
}

func TestQueryPayload_SearchExpressionsPassThroughUnchanged(t *testing.T) {
	expression := `search('timeout') AND NOT search('panic', body) AND (search('it\'s', body, resource) OR search('C:\\logs', 'attribute')) AND severity_text = 'ERROR'`
	body := `{"schemaVersion":"v1","start":1700000000000,"end":1700003600000,"requestType":"raw","compositeQuery":{"queries":[{"type":"builder_query","spec":{"name":"A","signal":"logs","disabled":false,"limit":50,"offset":7,"order":[{"key":{"name":"timestamp"},"direction":"desc"}],"having":{"expression":""},"filter":{"expression":` + jsonString(expression) + `}}}]},"formatOptions":{"formatTableResultForUI":false,"fillGaps":false},"variables":{}}`

	var payload QueryPayload
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	require.NoError(t, payload.Validate())
	out, err := json.Marshal(payload)
	require.NoError(t, err)

	var round struct {
		CompositeQuery struct {
			Queries []struct {
				Spec struct {
					Filter struct {
						Expression string `json:"expression"`
					} `json:"filter"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	require.NoError(t, json.Unmarshal(out, &round))
	require.Equal(t, expression, round.CompositeQuery.Queries[0].Spec.Filter.Expression,
		"caller-authored search() bytes must survive validation and remarshal unchanged")
}

func jsonBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
