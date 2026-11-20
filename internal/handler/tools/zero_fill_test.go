package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// Bucket starts on 2026-11-12 (UTC, ms) for a 600 s step.
const (
	at2000 = int64(1794513600000)
	at2040 = int64(1794516000000)
	at2130 = int64(1794519000000)
	at2200 = int64(1794520800000)
)

// aggregateAgainst calls an aggregate tool against a fake SigNoz that answers
// query_range with body, and returns the result's text blocks.
func aggregateAgainst(t *testing.T, logger *slog.Logger, tool, body string, args map[string]any) []string {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	h := &Handler{logger: logger, clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "k", "SIGNOZ-API-KEY", nil)}
	handle := map[string]mcp.ToolHandlerFunc{
		"signoz_aggregate_logs":   h.handleAggregateLogs,
		"signoz_aggregate_traces": h.handleAggregateTraces,
	}[tool]
	result, err := handle(testCtx(), makeToolRequest(tool, args))
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	var texts []string
	for _, content := range result.Content {
		if block, ok := mcp.AsTextContent(content); ok {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

type point struct {
	Timestamp int64   `json:"timestamp"`
	Partial   bool    `json:"partial"`
	Value     float64 `json:"value"`
}

// seriesPoints returns each aggregation's series points, keyed by aggregation index.
func seriesPoints(t *testing.T, payload string) map[int][][]point {
	t.Helper()
	var body struct {
		Data struct {
			Data struct {
				Results []struct {
					Aggregations []struct {
						Index  int `json:"index"`
						Series []struct {
							Values []point `json:"values"`
						} `json:"series"`
					} `json:"aggregations"`
				} `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &body))
	out := map[int][][]point{}
	for _, result := range body.Data.Data.Results {
		for _, aggregation := range result.Aggregations {
			for _, series := range aggregation.Series {
				out[aggregation.Index] = append(out[aggregation.Index], series.Values)
			}
		}
	}
	return out
}

// A count and a p99 over the same buckets; SigNoz lists points out of order.
const countAndP99Body = `{"status":"success","data":{"type":"time_series","meta":{"rowsScanned":76,"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[` +
	`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794519000000,"value":8},{"timestamp":1794516000000,"value":50}]}]},` +
	`{"index":1,"alias":"__result_1","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794519000000,"value":50120529},{"timestamp":1794516000000,"value":283365415}]}]}]}]}}}`

func TestCountTimeSeriesGetsZeroForEachEmptyBucketInTheWindow(t *testing.T) {
	windows := []struct {
		name         string
		start, end   int64
		first, final int64
		partialEdges bool
	}{
		// Partial first and last buckets: 20:03:20 to 22:06:40 covers parts of 20:00 and 22:00.
		{name: "unaligned window", start: at2000 + 200_000, end: at2200 + 400_000, first: at2000, final: at2200, partialEdges: true},
		// The bucket that starts at end lies outside [start, end).
		{name: "aligned window", start: at2000, end: at2200, first: at2000, final: at2200 - 600_000},
	}
	for _, tool := range []string{"signoz_aggregate_logs", "signoz_aggregate_traces"} {
		for _, window := range windows {
			t.Run(tool+"/"+window.name, func(t *testing.T) {
				texts := aggregateAgainst(t, logpkg.New("error"), tool, countAndP99Body, map[string]any{
					"aggregations": []any{"count()", "p99(duration_nano)"},
					"requestType":  "time_series",
					"start":        fmt.Sprint(window.start),
					"end":          fmt.Sprint(window.end),
				})

				var want []point
				for ts := window.first; ts <= window.final; ts += 600_000 {
					value := 0.0
					switch ts {
					case at2040:
						value = 50
					case at2130:
						value = 8
					}
					partial := window.partialEdges && (ts == window.first || ts == window.final)
					want = append(want, point{Timestamp: ts, Partial: partial, Value: value})
				}
				assert.Equal(t, [][]point{want}, seriesPoints(t, texts[0])[0],
					"the count series has every bucket of the window in order, SigNoz's values kept, and zeros at partial edges marked partial")
				assert.True(t, strings.HasPrefix(texts[0], `{"status":"success","data":{"type":"time_series","meta":{"rowsScanned":76,"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[`),
					"members before the filled values stay as SigNoz sent them: %s", texts[0])
				assert.True(t, strings.HasSuffix(texts[0], `]}]},{"index":1,"alias":"__result_1","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794519000000,"value":50120529},{"timestamp":1794516000000,"value":283365415}]}]}]}]}}}`),
					"the p99 series has no value in an empty bucket and stays as SigNoz sent it: %s", texts[0])
				assert.Contains(t, texts, "note: __result_0 (count()) is 0 for each 600s bucket in the window with no matching rows. "+
					"Other aggregations are unchanged; a bucket missing from their series has no value.")
			})
		}
	}
}

func TestCountTimeSeriesWithTimeShiftFillsTheShiftedBuckets(t *testing.T) {
	// For 20:00 to 22:00 with a 17-minute timeShift, SigNoz buckets 19:43 to 21:43 on
	// 10-minute steps, then moves each point 17 minutes forward: 19:57, 20:07, ..., 21:57.
	at1957, at2047, at2127, at2157 := at2000-180_000, at2000+2_820_000, at2000+5_220_000, at2200-180_000
	body := `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[` +
		fmt.Sprintf(`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":%d,"value":4},{"timestamp":%d,"value":2}]}]}]}]}}}`, at2047, at2127)

	texts := aggregateAgainst(t, logpkg.New("error"), "signoz_aggregate_traces", body, map[string]any{
		"aggregations": "count()", "requestType": "time_series",
		"functions": []any{map[string]any{"name": "timeShift", "args": []any{map[string]any{"value": 1020}}}},
		"start":     fmt.Sprint(at2000), "end": fmt.Sprint(at2200),
	})

	var want []point
	for ts := at1957; ts <= at2157; ts += 600_000 {
		value := map[int64]float64{at2047: 4, at2127: 2}[ts]
		want = append(want, point{Timestamp: ts, Partial: ts == at1957 || ts == at2157, Value: value})
	}
	assert.Equal(t, [][]point{want}, seriesPoints(t, texts[0])[0], "13 shifted buckets, the two edge zeros marked partial")
}

func TestOnlyZeroWhenEmptyAggregationsGetZeroBuckets(t *testing.T) {
	body := `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[` +
		`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794516000000,"value":3}]}]}]}]}}}`
	base := map[string]any{"requestType": "time_series", "start": fmt.Sprint(at2000), "end": fmt.Sprint(at2200)}
	cases := []struct {
		name   string
		args   map[string]any
		filled bool
	}{
		{"count", map[string]any{"aggregations": "count()"}, true},
		{"countIf with a parenthesis in a quoted value", map[string]any{"aggregations": "countIf(name = 'a)b')"}, true},
		{"count_distinct", map[string]any{"aggregations": "count_distinct(name)"}, true},
		{"sum", map[string]any{"aggregations": "sum(duration_nano)"}, true},
		{"rate", map[string]any{"aggregations": "rate()"}, true},
		{"timeShift keeps the count a count", map[string]any{"aggregations": "count()", "functions": []any{map[string]any{"name": "timeShift", "args": []any{map[string]any{"value": 86400}}}}}, true},
		{"avg", map[string]any{"aggregations": "avg(duration_nano)"}, false},
		{"max", map[string]any{"aggregations": "max(duration_nano)"}, false},
		{"p99", map[string]any{"aggregations": "p99(duration_nano)"}, false},
		{"ratio of two counts", map[string]any{"aggregations": "countIf(has_error = true) / count()"}, false},
		{"having can drop a bucket that had rows", map[string]any{"aggregations": "count()", "having": "count() > 1"}, false},
		{"cumulativeSum is no longer a count", map[string]any{"aggregations": "count()", "functions": []any{map[string]any{"name": "cumulativeSum"}}}, false},
		{"a timeShift of part of a second", map[string]any{"aggregations": "count()", "functions": []any{map[string]any{"name": "timeShift", "args": []any{map[string]any{"value": 1020.5}}}}}, false},
		{"two timeShifts", map[string]any{"aggregations": "count()", "functions": []any{
			map[string]any{"name": "timeShift", "args": []any{map[string]any{"value": 600}}},
			map[string]any{"name": "timeShift", "args": []any{map[string]any{"value": 600}}},
		}}, false},
		{"scalar", map[string]any{"aggregations": "count()", "requestType": "scalar"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{}
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.args {
				args[k] = v
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			texts := aggregateAgainst(t, logger, "signoz_aggregate_traces", body, args)
			if !tc.filled {
				assert.Equal(t, body, texts[0], "the payload must stay as SigNoz returned it")
				assert.NotContains(t, strings.Join(texts[1:], "\n"), "bucket")
				assert.Empty(t, logs.String(), "a request the fill does not cover is not a contract problem")
				return
			}
			assert.Len(t, seriesPoints(t, texts[0])[0][0], 12, "20:00 to 21:50 in 600 s buckets")
		})
	}
}

func TestZeroFillSkipsAGroupedResultThatWouldGrowTooMuch(t *testing.T) {
	// 20 groups with one point each, 120 one-minute buckets: 20 * 119 zeros.
	var series []string
	for group := range 20 {
		series = append(series, fmt.Sprintf(`{"labels":[{"key":{"name":"name"},"value":"op-%d"}],"values":[{"timestamp":%d,"value":1}]}`, group, at2040))
	}
	body := `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":60}},"data":{"results":[{"queryName":"A","aggregations":[` +
		`{"index":0,"alias":"__result_0","meta":{},"series":[` + strings.Join(series, ",") + `]}]}]}}}`

	texts := aggregateAgainst(t, logpkg.New("error"), "signoz_aggregate_traces", body, map[string]any{
		"aggregations": "count()", "groupBy": "name", "requestType": "time_series",
		"start": fmt.Sprint(at2000), "end": fmt.Sprint(at2200),
	})

	assert.Equal(t, body, texts[0], "nothing is filled when the zeros would exceed the bound")
	assert.Contains(t, strings.Join(texts[1:], "\n"), "note: empty buckets are left out, not filled with 0: filling them would add 2380 points")
	assert.Contains(t, strings.Join(texts[1:], "\n"), "raise stepInterval, lower limit, or narrow the filter or time range")
}

func TestZeroFillLeavesAnUnexpectedResponseAloneAndWarns(t *testing.T) {
	bodies := map[string]string{
		"no step interval": `{"status":"success","data":{"type":"time_series","meta":{},"data":{"results":[{"queryName":"A","aggregations":[` +
			`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794516000000,"value":3}]}]}]}]}}}`,
		"point off the bucket grid": `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[` +
			`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794516060000,"value":3}]}]}]}]}}}`,
		"two points in one bucket": `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":600}},"data":{"results":[{"queryName":"A","aggregations":[` +
			`{"index":0,"alias":"__result_0","meta":{},"series":[{"labels":[],"values":[{"timestamp":1794516000000,"value":3},{"timestamp":1794516000000,"value":5}]}]}]}]}}}`,
		"no results member": `{"status":"success","data":{"type":"time_series","meta":{"stepIntervals":{"A":600}},"data":{}}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			texts := aggregateAgainst(t, logger, "signoz_aggregate_logs", body, map[string]any{
				"aggregations": "count()", "requestType": "time_series",
				"start": fmt.Sprint(at2000), "end": fmt.Sprint(at2200),
			})
			assert.Equal(t, []string{body}, texts, "the payload passes through with no note")
			assert.Contains(t, logs.String(), "time series left unfilled: unexpected query_range response")
			assert.Contains(t, logs.String(), "signoz_aggregate_logs")
		})
	}
}
