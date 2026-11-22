package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
)

// notesHandler runs the real client against a fake SigNoz that answers every
// query with an empty scalar result and metric metadata with the given body.
// Like SigNoz, it reports the step it used for the first query, by that
// query's name: its stepInterval, raised to 60s. It returns the handler and a
// reader for its counters.
func notesHandler(t *testing.T, metadata string) (*Handler, *sdkmetric.ManualReader) {
	return notesHandlerWithMeta(t, metadata, "")
}

// notesHandlerWithMeta is notesHandler with meta, when set, as the response's
// data.meta instead of the reported step.
func notesHandlerWithMeta(t *testing.T, metadata, meta string) (*Handler, *sdkmetric.ManualReader) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/metrics/metadata":
			_, _ = w.Write([]byte(`{"status":"success","data":` + metadata + `}`))
		case "/api/v5/query_range":
			var request struct {
				CompositeQuery struct {
					Queries []struct {
						Spec struct {
							Name         string  `json:"name"`
							StepInterval float64 `json:"stepInterval"`
						} `json:"spec"`
					} `json:"queries"`
				} `json:"compositeQuery"`
			}
			_ = json.Unmarshal(body, &request)
			reported := meta
			if reported == "" {
				name, step := "A", 60.0
				if queries := request.CompositeQuery.Queries; len(queries) > 0 {
					name = queries[0].Spec.Name
					step = max(step, queries[0].Spec.StepInterval)
				}
				reported = fmt.Sprintf(`{"stepIntervals":{%q:%d}}`, name, int(step))
			}
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"type":"scalar","meta":%s,"data":{"results":[]}}}`, reported)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(provider)
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{
		logger:         logpkg.New("error"),
		meters:         meters,
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}, reader
}

// resultNotes joins every text block after the payload.
func resultNotes(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool returned an error: %s", textContent(t, result))
	}
	var notes []string
	for _, content := range result.Content[1:] {
		if block, ok := mcp.AsTextContent(content); ok {
			notes = append(notes, block.Text)
		}
	}
	return strings.Join(notes, "\n")
}

// countedNotes returns the measurement-note count per "tool/rule".
func countedNotes(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	sum, found := oteltest.FindInt64SumMetric(data, "mcp.tool.result.measurement_notes")
	if !found {
		return counts
	}
	for _, point := range sum.DataPoints {
		tool, _ := point.Attributes.Value(attribute.Key("gen_ai.tool.name"))
		rule, _ := point.Attributes.Value(otelpkg.MCPResultNoteRuleKey)
		counts[tool.AsString()+"/"+rule.AsString()] += point.Value
	}
	return counts
}

func TestAggregateTraces_SaysWhenItCountsEverySpanOfAService(t *testing.T) {
	const note = `this aggregates every span of service "frontend" that matches the filter, not its requests`
	tests := []struct {
		name     string
		args     map[string]any
		wantNote bool
		wantFix  string
	}{
		{name: "count of one service", args: map[string]any{"service": "frontend", "aggregations": "count()"}, wantNote: true, wantFix: "set filter to isEntryPoint = true OR isRoot = true and keep the service argument"},
		{name: "p95 latency from the filter", args: map[string]any{"filter": "service.name = 'frontend'", "aggregations": []any{"p95(duration_nano)"}}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "server spans still nest", args: map[string]any{"service": "frontend", "filter": "kind_string = 'Server'", "aggregations": "rate()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "entry spans", args: map[string]any{"service": "frontend", "filter": "isEntryPoint = true OR isRoot = true", "aggregations": "count()"}},
		{name: "grouped by operation", args: map[string]any{"service": "frontend", "aggregations": "count()", "groupBy": "name"}},
		{name: "one operation", args: map[string]any{"service": "frontend", "operation": "GET /cart", "aggregations": "count()"}},
		{name: "client spans on purpose", args: map[string]any{"service": "frontend", "filter": "kind_string = 'Client'", "aggregations": "count()"}},
		{name: "distinct traces", args: map[string]any{"service": "frontend", "aggregations": "count_distinct(trace_id)"}},
		{name: "every service", args: map[string]any{"aggregations": "count()"}},
		{name: "two services", args: map[string]any{"filter": "service.name IN ('frontend', 'cart')", "aggregations": "count()"}},
		{name: "an OR that widens the scope", args: map[string]any{"filter": "service.name = 'frontend' OR has_error = true", "aggregations": "count()"}},
		{name: "a NOT in the filter", args: map[string]any{"filter": "service.name = 'frontend' AND http.route NOT LIKE '/health%'", "aggregations": "count()"}},
		{name: "a longer key that ends in service.name", args: map[string]any{"filter": "k8s.service.name = 'frontend'", "aggregations": "count()"}},
		{name: "the peer service a span calls", args: map[string]any{"filter": "attribute.peer.service.name = 'frontend'", "aggregations": "count()"}},
		{name: "service.name inside a quoted value", args: map[string]any{"filter": `attribute.message = "service.name = 'frontend'"`, "aggregations": "count()"}},
		{name: "every kind but server", args: map[string]any{"service": "frontend", "filter": "kind_string != 'Server'", "aggregations": "count()"}},
		{name: "an unquoted server kind", args: map[string]any{"service": "frontend", "filter": "kind_string = Server", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "server kind as a one-value list", args: map[string]any{"service": "frontend", "filter": "kind_string IN ('Server')", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "server and client kinds", args: map[string]any{"service": "frontend", "filter": "kind_string IN ('Server', 'Client')", "aggregations": "count()"}},
		{name: "the service in brackets, as the filter picker writes it", args: map[string]any{"filter": "service.name IN ['frontend']", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "an unquoted service with ==", args: map[string]any{"filter": "service.name == frontend", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "a span ID key in a quoted value", args: map[string]any{"filter": "service.name = 'frontend' AND http.route = '/lookup/:trace_id'", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
		{name: "one trace", args: map[string]any{"service": "frontend", "filter": "trace_id = 'abc'", "aggregations": "count()"}},
		{name: "the service beside a longer key", args: map[string]any{"filter": "k8s.service.name = 'web' AND service.name = 'frontend'", "aggregations": "count()"}, wantNote: true, wantFix: "add AND (isEntryPoint = true OR isRoot = true) to the filter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, reader := notesHandler(t, `{}`)
			result, err := h.handleAggregateTraces(testCtx(), makeToolRequest("signoz_aggregate_traces", tc.args))
			if err != nil {
				t.Fatal(err)
			}
			notes := resultNotes(t, result)
			if got := strings.Contains(notes, note); got != tc.wantNote {
				t.Fatalf("all-span note present = %t, want %t; notes:\n%s", got, tc.wantNote, notes)
			}
			if tc.wantNote && !strings.Contains(notes, tc.wantFix) {
				t.Fatalf("the note must say %q; notes:\n%s", tc.wantFix, notes)
			}
			want := map[string]int64{}
			if tc.wantNote {
				want["signoz_aggregate_traces/trace_all_spans"] = 1
			}
			if got := countedNotes(t, reader); !equalCounts(got, want) {
				t.Fatalf("counted notes = %v, want %v", got, want)
			}
		})
	}
}

func TestQueryMetrics_SaysWhatAScalarReductionMeasured(t *testing.T) {
	const (
		cumulativeHistogram = `{"type":"histogram","temporality":"cumulative","isMonotonic":false}`
		deltaHistogram      = `{"type":"histogram","temporality":"delta","isMonotonic":false}`
		cumulativeCounter   = `{"type":"sum","temporality":"cumulative","isMonotonic":true}`
		deltaCounter        = `{"type":"sum","temporality":"delta","isMonotonic":true}`
		upDownCounter       = `{"type":"sum","temporality":"cumulative","isMonotonic":false}`
	)
	base := map[string]any{"metricName": "http.server.duration.bucket", "requestType": "scalar", "timeRange": "1h"}
	tests := []struct {
		name     string
		metadata string
		args     map[string]any
		wantRule string
		alsoRule string
		want     []string
		absent   []string
	}{
		{
			name: "averaged percentile of a cumulative histogram", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "avg", "filter": "service.name = 'frontend'"},
			wantRule: "percentile_of_steps",
			want: []string{
				"reduceTo avg combined the p95 of each 60s step, so this is not the p95 over the whole window.",
				"query http.server.duration.bucket again with timeAggregation increase, spaceAggregation sum, reduceTo sum, groupBy le",
				"every bucket of every group", "95% of the total", "+Inf", "highest finite bound",
			},
			absent: []string{"PromQL", "within each group"},
		},
		{
			name: "default p99 of a delta histogram", metadata: deltaHistogram,
			args:     map[string]any{"reduceTo": "avg"},
			wantRule: "percentile_of_steps",
			want:     []string{"reduceTo avg combined the p99 of each 60s step", "groupBy le"},
		},
		{
			name: "a grouped percentile keeps its groups", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p90", "reduceTo": "median", "groupBy": "service.name"},
			wantRule: "percentile_of_steps",
			want:     []string{"groupBy service.name, le, and a limit that returns every bucket of every group", "interpolate within each group within the first bucket whose count reaches 90% of the total"},
		},
		{
			name: "an exponential histogram has no le buckets", metadata: `{"type":"exponentialhistogram","temporality":"delta","isMonotonic":false}`,
			args:     map[string]any{"metricName": "http.server.duration", "spaceAggregation": "p95", "reduceTo": "avg"},
			wantRule: "percentile_of_steps",
			want:     []string{"so this is not the p95 over the whole window."},
			absent:   []string{"groupBy"},
		},
		{
			name: "a step as long as a window that starts mid-step", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "avg", "stepInterval": "1200", "start": "2026-11-21T13:56:00Z", "end": "2026-11-21T14:16:00Z"},
			wantRule: "percentile_of_steps",
			alsoRule: "step_alignment",
			want: []string{
				"SigNoz starts steps at multiples of 1200s since the Unix epoch, so this 1200s step does not cover the window as one step: for this cumulative histogram the result starts at 2026-11-21T14:00:00Z and leaves out the window before it",
			},
		},
		{
			name: "a rate on a cumulative histogram reads the step before", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "timeAggregation": "rate", "reduceTo": "max", "stepInterval": "1200", "start": "2026-11-21T13:56:00Z", "end": "2026-11-21T14:16:00Z"},
			wantRule: "step_alignment",
			want:     []string{"the first step starts at 2026-11-21T13:40:00Z, before the window, and counts data from before it, or, for a cumulative histogram percentile, the result starts at 2026-11-21T14:00:00Z"},
		},
		{name: "one step that is the window", metadata: cumulativeHistogram, args: map[string]any{"spaceAggregation": "p95", "reduceTo": "avg", "stepInterval": "1200", "start": "2026-11-21T14:00:00Z", "end": "2026-11-21T14:20:00Z"}},
		{
			name: "metric type from the caller", metadata: `{}`,
			args:     map[string]any{"metricType": "histogram", "spaceAggregation": "p95", "reduceTo": "avg"},
			wantRule: "percentile_of_steps",
			want:     []string{"groupBy le, and a limit that returns every bucket of every group"},
		},
		{
			name: "a having filter keeps only what was measured", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "avg", "having": "__result > 100"},
			wantRule: "percentile_of_steps",
			want:     []string{"so this is not the p95 over the whole window."},
			absent:   []string{"groupBy"},
		},
		{name: "summed buckets of a histogram are counts", metadata: cumulativeHistogram, args: map[string]any{"timeAggregation": "rate", "spaceAggregation": "sum", "reduceTo": "sum", "groupBy": "le"}},
		{name: "peak percentile", metadata: cumulativeHistogram, args: map[string]any{"spaceAggregation": "p95", "reduceTo": "max"}},
		{
			name: "peak percentile over a window-long step", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "max", "stepInterval": "1200", "start": "2026-11-21T13:56:00Z", "end": "2026-11-21T14:16:00Z"},
			wantRule: "step_alignment",
			want:     []string{"the result starts at 2026-11-21T14:00:00Z", "Leave stepInterval unset", "at least 60s"},
			absent:   []string{"shorter", "can be empty"},
		},
		{
			name: "a window-long step whose next boundary is the window's end", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "max", "stepInterval": "1200", "start": "2026-11-21T13:56:00Z", "end": "2026-11-21T14:00:00Z"},
			wantRule: "step_alignment",
			want:     []string{"2026-11-21T14:00:00Z", "the result can be empty"},
			absent:   []string{"leaves out the window before it"},
		},
		{
			name: "a window-long step whose next boundary is the minute the end is floored to", metadata: cumulativeHistogram,
			args:     map[string]any{"spaceAggregation": "p95", "reduceTo": "max", "stepInterval": "1200", "start": "2026-11-21T13:56:00Z", "end": "2026-11-21T14:00:30Z"},
			wantRule: "step_alignment",
			want:     []string{"the result can be empty"},
			absent:   []string{"leaves out the window before it"},
		},
		{
			name: "a counter over a window-long step", metadata: cumulativeCounter,
			args:     map[string]any{"metricName": "http.requests", "timeAggregation": "increase", "reduceTo": "sum", "stepInterval": "3600", "start": "2026-11-21T13:30:00Z", "end": "2026-11-21T14:30:00Z"},
			wantRule: "step_alignment",
			want:     []string{"the first step starts at 2026-11-21T13:00:00Z, before the window, and counts data from before it."},
		},
		{name: "a counter over an aligned window-long step", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "timeAggregation": "increase", "reduceTo": "sum", "stepInterval": "3600", "start": "2026-11-21T13:00:00Z", "end": "2026-11-21T14:00:00Z"}},
		{
			name: "summed default rate", metadata: cumulativeCounter,
			args:     map[string]any{"metricName": "http.requests", "reduceTo": "sum"},
			wantRule: "rate_summed",
			want:     []string{"reduceTo sum added up the per-second rate of each 60s step", "use timeAggregation increase with reduceTo sum"},
		},
		{
			name: "the step SigNoz used, not the one asked for", metadata: cumulativeCounter,
			args:     map[string]any{"metricName": "http.requests", "reduceTo": "sum", "stepInterval": "10"},
			wantRule: "rate_summed",
			want:     []string{"the per-second rate of each 60s step"},
		},
		{name: "summed rate counted across series", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "spaceAggregation": "count", "reduceTo": "sum"}},
		{name: "summed rate after a function", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "reduceTo": "sum", "functions": []any{map[string]any{"name": "cutOffMin", "args": []any{map[string]any{"value": 0}}}}}},
		{name: "summed rate of a histogram typed in capitals", metadata: cumulativeCounter, args: map[string]any{"metricType": "Histogram", "temporality": "cumulative", "timeAggregation": "rate", "spaceAggregation": "sum", "reduceTo": "sum"}},
		{name: "summed increase", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "timeAggregation": "increase", "reduceTo": "sum"}},
		{name: "average rate", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "reduceTo": "avg"}},
		{
			name: "latest of a cumulative counter", metadata: cumulativeCounter,
			args:     map[string]any{"metricName": "http.requests", "timeAggregation": "latest", "spaceAggregation": "avg", "reduceTo": "sum"},
			wantRule: "counter_running_total",
			want: []string{
				"timeAggregation latest reads its running total",
				"timeAggregation increase, spaceAggregation sum, and reduceTo sum",
				"timeAggregation rate, spaceAggregation sum, and reduceTo avg",
			},
		},
		{
			name: "sample count of a cumulative counter", metadata: cumulativeCounter,
			args:     map[string]any{"metricName": "http.requests", "timeAggregation": "count", "reduceTo": "max", "requestType": "time_series"},
			wantRule: "counter_running_total",
			want:     []string{"timeAggregation count counts the counter's samples", "increase, and for the per-second rate, rate, each with spaceAggregation sum"},
			absent:   []string{"reduceTo"},
		},
		{name: "distinct values of a cumulative counter", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "timeAggregation": "count_distinct", "reduceTo": "max"}},
		{name: "latest of an up-down counter", metadata: upDownCounter, args: map[string]any{"metricName": "queue.size", "timeAggregation": "latest", "reduceTo": "last"}},
		{name: "max of a delta counter", metadata: deltaCounter, args: map[string]any{"metricName": "http.requests", "timeAggregation": "max", "reduceTo": "max"}},
		{
			name: "span metrics for one service", metadata: deltaCounter,
			args:     map[string]any{"metricName": "signoz_calls_total", "filter": "service.name = 'frontend'", "timeAggregation": "increase", "reduceTo": "sum"},
			wantRule: "span_metrics_all_spans",
			want:     []string{`counts every span of service "frontend"`, "signoz_list_services over the same window", "count() or rate()", "isEntryPoint = true OR isRoot = true, over the same window"},
			absent:   []string{"span fields", "time_series"},
		},
		{
			name: "span metrics over time", metadata: deltaCounter,
			args:     map[string]any{"metricName": "signoz_calls_total", "filter": "service.name = 'frontend'", "timeAggregation": "increase", "requestType": "time_series"},
			wantRule: "span_metrics_all_spans",
			want:     []string{"count() or rate()", "requestType time_series, the same window, and stepInterval 60"},
			absent:   []string{"signoz_list_services"},
		},
		{
			name: "span metrics for a service named like a label", metadata: deltaCounter,
			args:     map[string]any{"metricName": "signoz_calls_total", "filter": "service.name = 'operations-api'", "timeAggregation": "increase", "reduceTo": "sum"},
			wantRule: "span_metrics_all_spans",
			want:     []string{`counts every span of service "operations-api"`},
		},
		{
			name: "span metrics for one service and more", metadata: deltaCounter,
			args:     map[string]any{"metricName": "signoz_calls_total", "filter": "service.name = 'frontend' AND status.code = 'STATUS_CODE_ERROR'", "timeAggregation": "increase", "reduceTo": "sum"},
			wantRule: "span_metrics_all_spans",
			want:     []string{"(isEntryPoint = true OR isRoot = true) AND this query's other conditions", "Write those conditions as span fields", "status_code_string 'Error'"},
			absent:   []string{"signoz_list_services"},
		},
		{
			name: "span latency count for one service", metadata: deltaCounter,
			args:     map[string]any{"metricName": "signoz_latency.count", "filter": "service.name = 'frontend'", "timeAggregation": "increase", "reduceTo": "sum"},
			wantRule: "span_metrics_all_spans",
			want:     []string{`signoz_latency.count counts every span of service "frontend"`, "count() or rate()"},
		},
		{
			name: "span latency percentile for one service", metadata: deltaHistogram,
			args:     map[string]any{"metricName": "signoz_latency.bucket", "filter": "service.name = 'frontend'", "spaceAggregation": "p99", "reduceTo": "max"},
			wantRule: "span_metrics_all_spans",
			want:     []string{`signoz_latency.bucket records the duration of every span of service "frontend"`, "p99(duration_nano)", "isEntryPoint = true OR isRoot = true"},
			absent:   []string{"signoz_list_services", "span fields"},
		},
		{
			name: "span latency per environment", metadata: deltaHistogram,
			args:     map[string]any{"metricName": "signoz_latency.bucket", "filter": "resource.service.name = 'frontend'", "groupBy": "deployment.environment, le", "spaceAggregation": "p99", "reduceTo": "max"},
			wantRule: "span_metrics_all_spans",
			want:     []string{"grouped by the same keys", "Write those keys as span fields"},
			absent:   []string{"other conditions"},
		},
		{
			name: "span latency per bucket", metadata: deltaHistogram,
			args:     map[string]any{"metricName": "signoz_latency.bucket", "filter": "resource.service.name = 'frontend'", "groupBy": "le", "spaceAggregation": "p99", "reduceTo": "max"},
			wantRule: "span_metrics_all_spans",
			want:     []string{"p99(duration_nano)"},
			absent:   []string{"grouped by", "span fields"},
		},
		{name: "span latency sum", metadata: deltaCounter, args: map[string]any{"metricName": "signoz_latency.sum", "filter": "service.name = 'frontend'", "timeAggregation": "increase", "reduceTo": "sum"}},
		{name: "span metrics per operation", metadata: deltaCounter, args: map[string]any{"metricName": "signoz_calls_total", "filter": "service.name = 'frontend'", "groupBy": "operation", "timeAggregation": "increase", "reduceTo": "sum"}},
		{name: "a formula", metadata: cumulativeCounter, args: map[string]any{"metricName": "http.requests", "reduceTo": "sum", "formula": "A / B", "formulaQueries": []any{map[string]any{"name": "B", "metricName": "http.requests", "metricType": "sum", "isMonotonic": true, "temporality": "cumulative", "reduceTo": "sum"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, reader := notesHandler(t, tc.metadata)
			args := map[string]any{}
			for key, value := range base {
				args[key] = value
			}
			for key, value := range tc.args {
				args[key] = value
			}
			result, err := h.handleQueryMetrics(testCtx(), makeToolRequest("signoz_query_metrics", args))
			if err != nil {
				t.Fatal(err)
			}
			notes := resultNotes(t, result)
			for _, text := range tc.want {
				if !strings.Contains(notes, text) {
					t.Errorf("notes lack %q:\n%s", text, notes)
				}
			}
			for _, text := range tc.absent {
				if strings.Contains(notes, text) {
					t.Errorf("notes must not contain %q:\n%s", text, notes)
				}
			}
			want := map[string]int64{}
			for _, rule := range []string{tc.wantRule, tc.alsoRule} {
				if rule != "" {
					want["signoz_query_metrics/"+rule] = 1
				}
			}
			if got := countedNotes(t, reader); !equalCounts(got, want) {
				t.Fatalf("counted notes = %v, want %v; notes:\n%s", got, want, notes)
			}
		})
	}
}

// A note names the step SigNoz reported for the query, never the caller's
// stepInterval, which SigNoz can raise. Metadata that does not decode is a
// contract change and is WARN-logged; a step left out is not.
func TestQueryMetrics_NotesNameOnlyTheStepSigNozReported(t *testing.T) {
	tests := []struct {
		name     string
		meta     string
		wantWarn bool
	}{
		{name: "no step reported", meta: `{}`},
		{name: "no step for the query", meta: `{"stepIntervals":{}}`},
		{name: "step metadata that does not decode", meta: `{"stepIntervals":{"A":"300s"}}`, wantWarn: true},
		{name: "a null step", meta: `{"stepIntervals":{"A":null}}`, wantWarn: true},
		{name: "a negative step", meta: `{"stepIntervals":{"A":-300}}`, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := notesHandlerWithMeta(t, `{"type":"histogram","temporality":"cumulative","isMonotonic":false}`, tc.meta)
			var logs bytes.Buffer
			h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			result, err := h.handleQueryMetrics(testCtx(), makeToolRequest("signoz_query_metrics", map[string]any{
				"metricName": "http.server.duration.bucket", "requestType": "scalar", "timeRange": "1h",
				"spaceAggregation": "p95", "reduceTo": "avg", "stepInterval": "300",
			}))
			if err != nil {
				t.Fatal(err)
			}
			notes := resultNotes(t, result)
			if want := "reduceTo avg combined the p95 of each step, so this is not the p95 over the whole window."; !strings.Contains(notes, want) {
				t.Errorf("notes lack %q:\n%s", want, notes)
			}
			if got := strings.Contains(logs.String(), "qb step intervals unparsed"); got != tc.wantWarn {
				t.Errorf("step metadata WARN = %t, want %t; logs:\n%s", got, tc.wantWarn, logs.String())
			}
		})
	}
}

func TestExecuteBuilderQuery_SaysWhatEachQueryMeasured(t *testing.T) {
	metricsQuery := func(name string, extra map[string]any) map[string]any {
		spec := map[string]any{
			"name": name, "signal": "metrics",
			"aggregations": []any{map[string]any{"metricName": "cpu", "timeAggregation": "avg", "spaceAggregation": "sum", "reduceTo": "avg"}},
			"groupBy":      []any{map[string]any{"name": "host"}},
		}
		for key, value := range extra {
			spec[key] = value
		}
		return map[string]any{"type": "builder_query", "spec": spec}
	}
	formula := map[string]any{"type": "builder_formula", "spec": map[string]any{"name": "F", "expression": "A + B"}}
	traces := func(extra map[string]any) map[string]any {
		spec := map[string]any{
			"name": "A", "signal": "traces",
			"filter":       map[string]any{"expression": "service.name = 'frontend'"},
			"aggregations": []any{map[string]any{"expression": "p99(duration_nano)"}},
		}
		for key, value := range extra {
			spec[key] = value
		}
		return map[string]any{"type": "builder_query", "spec": spec}
	}
	percentileOf := func(name, metric string, extra map[string]any) map[string]any {
		spec := map[string]any{
			"name": name, "signal": "metrics",
			"aggregations": []any{map[string]any{"metricName": metric, "spaceAggregation": "p95", "reduceTo": "avg"}},
		}
		for key, value := range extra {
			spec[key] = value
		}
		return map[string]any{"type": "builder_query", "spec": spec}
	}
	percentile := percentileOf("A", "latency.bucket", nil)
	// 2026-11-21T13:56:00Z to 14:16:00Z, mid-way through a 1200s step.
	const midStepStartMs, midStepEndMs = 1795269360000, 1795270560000
	tests := []struct {
		name        string
		requestType string
		start, end  float64 // Unix ms unless the case says otherwise
		queries     []any
		wantRule    string
		wantCount   int64 // notes of wantRule, when more than one
		alsoRule    string
		want        []string
		absent      string
	}{
		{name: "limited formula input", requestType: "scalar", queries: []any{metricsQuery("A", map[string]any{"limit": 1}), metricsQuery("B", nil), formula}, wantRule: "formula_input_limit", want: []string{`query "A" is a formula input with limit 1`}},
		{name: "unlimited formula inputs", requestType: "scalar", queries: []any{metricsQuery("A", nil), metricsQuery("B", nil), formula}},
		{name: "all spans of one service", requestType: "time_series", queries: []any{traces(nil)}, wantRule: "trace_all_spans", want: []string{`note: in query "A", this aggregates every span of service "frontend"`}},
		{name: "all spans of one service in a list of spans", requestType: "raw", queries: []any{traces(map[string]any{"aggregations": []any{map[string]any{"expression": "count()"}}})}},
		{name: "two queries that measure the same way", requestType: "scalar", queries: []any{traces(nil), traces(map[string]any{"name": "B"})}, wantRule: "trace_all_spans", wantCount: 2, want: []string{`note: in query "A", this aggregates every span`, `note: in query "B", this aggregates every span`}},
		{name: "a disabled trace query", requestType: "time_series", queries: []any{traces(map[string]any{"disabled": true})}},
		{name: "averaged percentile", requestType: "scalar", queries: []any{percentile}, wantRule: "percentile_of_steps", want: []string{`note: in query "A", reduceTo avg combined the p95 of each 60s step`, "query latency.bucket again with timeAggregation increase"}},
		{name: "averaged percentile of a Prometheus-named histogram", requestType: "scalar", queries: []any{percentileOf("A", "latency_bucket", nil)}, wantRule: "percentile_of_steps", want: []string{"query latency_bucket again with timeAggregation increase"}},
		{name: "averaged percentile of a query SigNoz skipped", requestType: "scalar", queries: []any{percentile, percentileOf("B", "missing.bucket", nil)}, wantRule: "percentile_of_steps", want: []string{`in query "A", reduceTo avg combined the p95`}, absent: `in query "B"`},
		{
			name: "a window in seconds, read the way SigNoz reads it", requestType: "scalar", start: midStepStartMs / 1000, end: midStepEndMs / 1000,
			queries:  []any{percentileOf("A", "latency.bucket", map[string]any{"stepInterval": 1200})},
			wantRule: "percentile_of_steps", alsoRule: "step_alignment",
			want: []string{"the first step starts at 2026-11-21T13:40:00Z, before the window", "the result starts at 2026-11-21T14:00:00Z"},
		},
		{
			name: "a window in nanoseconds, read the way SigNoz reads it", requestType: "scalar", start: midStepStartMs * 1e6, end: midStepEndMs * 1e6,
			queries:  []any{percentileOf("A", "latency.bucket", map[string]any{"stepInterval": 1200})},
			wantRule: "percentile_of_steps", alsoRule: "step_alignment",
			want: []string{"the first step starts at 2026-11-21T13:40:00Z, before the window"},
		},
		{name: "percentile over time", requestType: "time_series", queries: []any{percentile}},
		{name: "summed rate of histogram buckets", requestType: "scalar", queries: []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "metrics",
			"aggregations": []any{map[string]any{"metricName": "latency.bucket", "timeAggregation": "rate", "spaceAggregation": "sum", "reduceTo": "sum"}},
		}}}},
		{name: "summed rate of Prometheus-named histogram buckets", requestType: "scalar", queries: []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "metrics",
			"aggregations": []any{map[string]any{"metricName": "latency_bucket", "timeAggregation": "rate", "spaceAggregation": "sum", "reduceTo": "sum"}},
		}}}},
		{name: "summed rate of a counter", requestType: "scalar", queries: []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "metrics",
			"aggregations": []any{map[string]any{"metricName": "requests_total", "timeAggregation": "rate", "spaceAggregation": "sum", "reduceTo": "sum"}},
		}}}, wantRule: "rate_summed", want: []string{`note: in query "A", reduceTo sum added up the per-second rate of each 60s step`}},
		{name: "averaged percentile with having", requestType: "scalar", queries: []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "metrics", "having": map[string]any{"expression": "__result > 1"},
			"aggregations": []any{map[string]any{"metricName": "latency.bucket", "spaceAggregation": "p95", "reduceTo": "avg"}},
		}}}, wantRule: "percentile_of_steps", want: []string{"so this is not the p95 over the whole window."}, absent: "timeAggregation increase"},
		{name: "averaged percentile after a function", requestType: "scalar", queries: []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "metrics", "functions": []any{map[string]any{"name": "ewma3"}},
			"aggregations": []any{map[string]any{"metricName": "latency.bucket", "spaceAggregation": "p95", "reduceTo": "avg"}},
		}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, reader := notesHandler(t, `{}`)
			start, end := tc.start, tc.end
			if start == 0 {
				start, end = 1700000000000, 1700003600000
			}
			query := map[string]any{
				"schemaVersion": "v1", "start": start, "end": end,
				"requestType": tc.requestType, "compositeQuery": map[string]any{"queries": tc.queries},
			}
			body, _ := json.Marshal(query)
			var decoded map[string]any
			_ = json.Unmarshal(body, &decoded)
			result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": decoded}))
			if err != nil {
				t.Fatal(err)
			}
			notes := resultNotes(t, result)
			for _, text := range tc.want {
				if !strings.Contains(notes, text) {
					t.Errorf("notes lack %q:\n%s", text, notes)
				}
			}
			if tc.absent != "" && strings.Contains(notes, tc.absent) {
				t.Errorf("notes must not contain %q:\n%s", tc.absent, notes)
			}
			want := map[string]int64{}
			if tc.wantRule != "" {
				want["signoz_execute_builder_query/"+tc.wantRule] = max(tc.wantCount, 1)
			}
			if tc.alsoRule != "" {
				want["signoz_execute_builder_query/"+tc.alsoRule] = 1
			}
			if got := countedNotes(t, reader); !equalCounts(got, want) {
				t.Fatalf("counted notes = %v, want %v; notes:\n%s", got, want, notes)
			}
		})
	}
}

func equalCounts(got, want map[string]int64) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
