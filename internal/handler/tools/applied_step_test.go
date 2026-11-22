package tools

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// callWithQueryRangeMeta calls a registered tool against a fake SigNoz whose
// query_range response carries meta (a JSON object, or "" for no meta) and,
// when warned, one backend warning. It returns the result and the server's
// WARN logs.
func callWithQueryRangeMeta(t *testing.T, tool, arguments, meta string, warned bool) (*mcp.CallToolResult, string) {
	t.Helper()
	body := `{"status":"success","data":{"type":"time_series","data":{"results":[]}`
	if meta != "" {
		body += `,"meta":` + meta
	}
	if warned {
		body += `,"warning":{"code":"warning","message":"1 warning","warnings":[{"message":"no data found for the metric m in the query time range"}]}`
	}
	body += `}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	var logs bytes.Buffer
	h := &Handler{
		logger:         slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	result := callRegisteredTool(t, server, tool, arguments)
	if result.IsError {
		t.Fatalf("%s returned an error: %s", tool, allTextBlocks(result))
	}
	return result, logs.String()
}

// SigNoz raises a stepInterval below the window's minimum. A time_series
// aggregate then says so with the step it applied and a window that allows
// the requested one; otherwise it adds no note.
func TestAggregateTools_SayWhenSigNozRaisedTheStep(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		meta      string
		wantNote  string // "" means no note mentions stepInterval
		wantWarn  bool   // the missing-stepIntervals drift WARN
	}{
		{
			name:      "raised",
			arguments: `{"aggregations":"count()","timeRange":"7d","requestType":"time_series","stepInterval":60}`,
			meta:      `{"stepIntervals":{"A":400}}`,
			wantNote:  "note: SigNoz raised stepInterval from 60 s to 400 s, the minimum it allows for this window. For 60 s buckets, query a window of 25 hours or less.",
		},
		{
			name:      "raised to the 5 s floor",
			arguments: `{"aggregations":"count()","timeRange":"1h","requestType":"time_series","stepInterval":"1"}`,
			meta:      `{"stepIntervals":{"A":5}}`,
			wantNote:  "note: SigNoz raised stepInterval from 1 s to 5 s, the minimum it allows for this window. Buckets are never smaller than 5 s.",
		},
		{
			name:      "below the floor and raised past it",
			arguments: `{"aggregations":"count()","timeRange":"7d","requestType":"time_series","stepInterval":2}`,
			meta:      `{"stepIntervals":{"A":400}}`,
			wantNote:  "note: SigNoz raised stepInterval from 2 s to 400 s, the minimum it allows for this window. Buckets are never smaller than 5 s. For 5 s buckets, query a window of 2 hours or less.",
		},
		{
			name:      "unchanged",
			arguments: `{"aggregations":"count()","timeRange":"25h","requestType":"time_series","stepInterval":60}`,
			meta:      `{"stepIntervals":{"A":60}}`,
		},
		{
			name:      "scalar, where the step shapes no buckets",
			arguments: `{"aggregations":"count()","timeRange":"7d","stepInterval":60}`,
			meta:      `{"stepIntervals":{"A":400}}`,
		},
		{
			name:      "no stepIntervals fails open with a WARN",
			arguments: `{"aggregations":"count()","timeRange":"7d","requestType":"time_series","stepInterval":60}`,
			wantWarn:  true,
		},
	}
	for _, tool := range []string{"signoz_aggregate_logs", "signoz_aggregate_traces"} {
		for _, tc := range tests {
			t.Run(tool+" "+tc.name, func(t *testing.T) {
				result, logs := callWithQueryRangeMeta(t, tool, tc.arguments, tc.meta, false)
				var stepNotes []string
				for _, block := range result.Content[1:] {
					if text, ok := mcp.AsTextContent(block); ok && strings.Contains(text.Text, "stepInterval") {
						stepNotes = append(stepNotes, text.Text)
					}
				}
				if tc.wantNote == "" && len(stepNotes) != 0 {
					t.Fatalf("SigNoz applied the requested step or the request is scalar, so no note may mention stepInterval; got %q", stepNotes)
				}
				if tc.wantNote != "" && (len(stepNotes) != 1 || stepNotes[0] != tc.wantNote) {
					t.Fatalf("SigNoz raised the step, so the result needs exactly one step note\nwant: %q\ngot:  %q", tc.wantNote, stepNotes)
				}
				if got := strings.Contains(logs, "qb response has no stepIntervals"); got != tc.wantWarn {
					t.Fatalf("missing-stepIntervals WARN logged = %v, want %v; logs:\n%s", got, tc.wantWarn, logs)
				}
			})
		}
	}
}

// The stepInterval line of the query_metrics decisions note reports the step
// SigNoz applied, read from query A whatever order the entries arrive in, and
// names a next step that is true for metrics.
func TestQueryMetrics_StepDecisionReportsTheStepSigNozApplied(t *testing.T) {
	const formula = `,"formula":"A / B","formulaQueries":[{"name":"B","metricName":"m2","metricType":"gauge"}]`
	tests := []struct {
		name      string
		arguments string
		meta      string
		warned    bool   // SigNoz attached a backend warning
		want      string // "" means no stepInterval line
		wantWarn  bool   // the missing-stepIntervals drift WARN
	}{
		{
			name:      "raised to the 60 s floor",
			arguments: `{"stepInterval":10,"timeRange":"1h"}`,
			meta:      `{"stepIntervals":{"A":60}}`,
			want:      "stepInterval: 60s (raised from the requested 10s, the minimum SigNoz allows for this window; metric steps are never smaller than 60s)",
		},
		{
			name:      "below the floor and raised past it",
			arguments: `{"stepInterval":10,"timeRange":"7d"}`,
			meta:      `{"stepIntervals":{"A":1800}}`,
			want:      "stepInterval: 1800s (raised from the requested 10s, the minimum SigNoz allows for this window; metric steps are never smaller than 60s, so query a shorter window for 60s steps)",
		},
		{
			name:      "raised above the floor",
			arguments: `{"stepInterval":"120","timeRange":"7d"}`,
			meta:      `{"stepIntervals":{"A":1800}}`,
			want:      "stepInterval: 1800s (raised from the requested 120s, the minimum SigNoz allows for this window; query a shorter window for 120s steps)",
		},
		{
			name:      "Cost Meter states only the applied minimum",
			arguments: `{"stepInterval":60,"timeRange":"1h","source":"meter"}`,
			meta:      `{"stepIntervals":{"A":3600}}`,
			want:      "stepInterval: 3600s (raised from the requested 60s, the minimum SigNoz allows for this window; pass 3600 or more)",
		},
		{
			name:      "formula queries at one step report it once",
			arguments: `{"stepInterval":10,"timeRange":"1h"` + formula + `}`,
			meta:      `{"stepIntervals":{"C":60,"B":60,"A":60}}`,
			want:      "stepInterval: 60s (raised from the requested 10s, the minimum SigNoz allows for this window; metric steps are never smaller than 60s)",
		},
		{
			name:      "several queries report query A's step",
			arguments: `{"stepInterval":10,"timeRange":"1h"` + formula + `}`,
			meta:      `{"stepIntervals":{"C":120,"B":120,"A":60}}`,
			want:      "stepInterval: 60s (raised from the requested 10s, the minimum SigNoz allows for this window; metric steps are never smaller than 60s)",
		},
		{
			name:      "unchanged",
			arguments: `{"stepInterval":60,"timeRange":"1h"}`,
			meta:      `{"stepIntervals":{"A":60}}`,
			want:      "stepInterval: 60s (caller-provided)",
		},
		{
			name:      "no stepIntervals fails open with a WARN",
			arguments: `{"stepInterval":10,"timeRange":"1h"}`,
			want:      "stepInterval: 10s (caller-provided)",
			wantWarn:  true,
		},
		{
			name:      "no stepIntervals beside a backend warning is marked on that log, not reported as drift",
			arguments: `{"stepInterval":10,"timeRange":"1h"}`,
			warned:    true,
			want:      "stepInterval: 10s (caller-provided)",
		},
		{
			name:      "no requested step",
			arguments: `{"timeRange":"1h"` + formula + `}`,
			meta:      `{"stepIntervals":{"C":120,"B":120,"A":60}}`,
			want:      "stepInterval: 60s (backend-determined)",
		},
		{
			name:      "no requested step and no stepIntervals says nothing, with a WARN",
			arguments: `{"timeRange":"1h"}`,
			wantWarn:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arguments := `{"metricName":"m","metricType":"gauge",` + strings.TrimPrefix(tc.arguments, "{")
			result, logs := callWithQueryRangeMeta(t, "signoz_query_metrics", arguments, tc.meta, tc.warned)
			var stepLines []string
			for _, line := range strings.Split(allTextBlocks(result), "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, "stepInterval:") {
					stepLines = append(stepLines, line)
				}
			}
			if tc.want == "" && len(stepLines) != 0 {
				t.Fatalf("SigNoz reported no step, so the decisions note may not state one; got %q", stepLines)
			}
			if tc.want != "" && (len(stepLines) != 1 || stepLines[0] != tc.want) {
				t.Fatalf("the decisions note needs one stepInterval line\nwant: %q\ngot:  %q", tc.want, stepLines)
			}
			if got := strings.Contains(logs, "qb response has no stepIntervals"); got != tc.wantWarn {
				t.Fatalf("missing-stepIntervals WARN logged = %v, want %v; logs:\n%s", got, tc.wantWarn, logs)
			}
			if tc.warned && !strings.Contains(logs, "missingStepIntervals=true") {
				t.Fatalf("the backend-warning log must mark the missing steps; logs:\n%s", logs)
			}
		})
	}
}
