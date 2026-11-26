package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

// source="audit" sends the logs query to SigNoz audit events; any other value
// falls back to application logs, and the result says so.
func TestLogTools_SourceSelectsAuditEvents(t *testing.T) {
	tools := []struct {
		name   string
		args   map[string]any
		handle func(*Handler) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"signoz_search_logs", map[string]any{},
			func(h *Handler) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleSearchLogs
			}},
		{"signoz_aggregate_logs", map[string]any{"aggregations": "count()"},
			func(h *Handler) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleAggregateLogs
			}},
	}
	// A SigNoz that predates audit routing ignores source and reads application
	// logs; the event_name condition keeps those out of an audit result.
	const auditOnly = "event_name LIKE 'signoz.audit.%'"
	cases := []struct {
		name       string
		source     any // nil leaves the argument out
		filter     string
		wantSource string
		wantFilter string
		wantNote   bool
	}{
		{name: "omitted reads application logs", filter: "x = 1", wantFilter: "x = 1"},
		{name: "empty reads application logs", source: ""},
		{name: "audit reads only audit events", source: "audit", wantSource: "audit", wantFilter: auditOnly},
		{name: "audit keeps the caller's filter", source: "audit", filter: "a = 1 OR b = 2", wantSource: "audit",
			wantFilter: "(a = 1 OR b = 2) AND " + auditOnly},
		{name: "an unsupported value reads application logs with a note", source: "audits", filter: "x = 1", wantFilter: "x = 1", wantNote: true},
		{name: "a non-string value reads application logs with a note", source: true, wantNote: true},
	}
	for _, tool := range tools {
		for _, tc := range cases {
			t.Run(tool.name+"/"+tc.name, func(t *testing.T) {
				h, bodies := queryRangeRecorder(t, emptyQueryRangeResponse)
				args := map[string]any{"timeRange": "1h"}
				for k, v := range tool.args {
					args[k] = v
				}
				if tc.source != nil {
					args["source"] = tc.source
				}
				if tc.filter != "" {
					args["filter"] = tc.filter
				}
				result, err := tool.handle(h)(testCtx(), makeToolRequest(tool.name, args))
				require.NoError(t, err)
				require.False(t, result.IsError, textContent(t, result))
				require.Len(t, *bodies, 1)

				_, envelope := sentEnvelope(t, (*bodies)[0])
				spec := envelope["spec"].(map[string]any)
				require.Equal(t, "logs", spec["signal"])
				if tc.wantSource == "" {
					require.NotContains(t, spec, "source")
				} else {
					require.Equal(t, tc.wantSource, spec["source"])
				}
				var gotFilter any
				if filter, ok := spec["filter"].(map[string]any); ok {
					gotFilter = filter["expression"]
				}
				if tc.wantFilter == "" {
					require.Contains(t, []any{nil, ""}, gotFilter)
				} else {
					require.Equal(t, tc.wantFilter, gotFilter)
				}

				require.Equal(t, tc.wantNote, strings.Contains(allTextBlocks(result),
					`note: source was ignored because only "audit" is supported, so this result covers application logs; set source="audit" for SigNoz audit events.`))
			})
		}
	}
}

// SigNoz does not run search() on audit events, so the scopes that build it are
// rejected before any request; body still searches the event body.
func TestSearchLogs_AuditAcceptsOnlyTheBodyScope(t *testing.T) {
	t.Run("body scope searches the event body", func(t *testing.T) {
		h, bodies := queryRangeRecorder(t, emptyQueryRangeResponse)
		result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs",
			map[string]any{"timeRange": "1h", "source": "audit", "searchText": "alice", "searchScope": "body"}))
		require.NoError(t, err)
		require.False(t, result.IsError, textContent(t, result))
		require.Len(t, *bodies, 1)
		_, envelope := sentEnvelope(t, (*bodies)[0])
		spec := envelope["spec"].(map[string]any)
		require.Equal(t, "(body CONTAINS 'alice') AND event_name LIKE 'signoz.audit.%'", spec["filter"].(map[string]any)["expression"])
	})
	for _, scope := range []string{"attribute", "resource", "all", "everywhere"} {
		t.Run(scope+" scope is rejected", func(t *testing.T) {
			h, bodies := queryRangeRecorder(t, emptyQueryRangeResponse)
			result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs",
				map[string]any{"timeRange": "1h", "source": "audit", "searchText": "alice", "searchScope": scope}))
			require.NoError(t, err)
			require.Equal(t, CodeValidationFailed, resultCode(t, result))
			require.Equal(t, `Parameter validation failed: "searchScope" "`+scope+`" is not supported with source="audit" because SigNoz does not run search() on audit events. Omit searchScope to search the event body, or filter on a named audit field instead, such as signoz.principal.email = 'alice@acme.com'`,
				resultText(t, result))
			require.Empty(t, *bodies, "a rejected scope must not reach SigNoz")
		})
	}
}

// An empty audit search can also mean SigNoz records no audit events, and an
// empty audit searchText page points at named audit fields, not at the wider
// scopes SigNoz rejects on audit events.
func TestSearchLogs_EmptyAuditPageNotes(t *testing.T) {
	const onePage = `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"data":{"body":{"message":"alice deleted dashboard"}}}]}]}}}`
	// What SigNoz returns for an audit search with no rows: rows is null, not [].
	const nullRows = `{"status":"success","data":{"type":"raw","meta":{"rowsScanned":0,"bytesScanned":0,"durationMs":13,"stepIntervals":{"A":10}},"data":{"results":[{"queryName":"A","nextCursor":"","rows":null}]}}}`
	tests := []struct {
		name      string
		args      map[string]any
		response  string
		wantNotes []string
		notWant   []string
	}{
		{"audit, empty first page", map[string]any{"source": "audit"}, emptyQueryRangeResponse,
			[]string{auditEmptyResultNote}, []string{auditSearchTextNote, bodyOnlySearchTextNote}},
		{"audit, null rows on the first page", map[string]any{"source": "audit"}, nullRows,
			[]string{auditEmptyResultNote}, []string{auditSearchTextNote, bodyOnlySearchTextNote}},
		{"audit searchText, empty first page", map[string]any{"source": "audit", "searchText": "alice"}, emptyQueryRangeResponse,
			[]string{auditEmptyResultNote, auditSearchTextNote}, []string{bodyOnlySearchTextNote}},
		{"audit, rows returned", map[string]any{"source": "audit", "searchText": "alice"}, onePage,
			nil, []string{auditEmptyResultNote, auditSearchTextNote, bodyOnlySearchTextNote}},
		{"audit, empty later page", map[string]any{"source": "audit", "offset": "20"}, emptyQueryRangeResponse,
			nil, []string{auditEmptyResultNote}},
		{"application logs, empty searchText page", map[string]any{"searchText": "alice"}, emptyQueryRangeResponse,
			[]string{bodyOnlySearchTextNote}, []string{auditEmptyResultNote, auditSearchTextNote}},
		{"ignored source, empty first page", map[string]any{"source": "audits"}, emptyQueryRangeResponse,
			nil, []string{auditEmptyResultNote}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := queryRangeRecorder(t, tc.response)
			args := map[string]any{"timeRange": "1h"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", args))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			text := allTextBlocks(result)
			for _, note := range tc.wantNotes {
				require.Contains(t, text, note)
			}
			for _, note := range tc.notWant {
				require.NotContains(t, text, note)
			}
		})
	}
}

// An audit aggregate with nothing in it can also mean SigNoz records no audit
// events. SigNoz answers an ungrouped count() over nothing with one zero row,
// so that counts as empty too; a zero from countIf, any grouped row, and any
// time-series point do not.
func TestAggregateLogs_EmptyAuditResultNote(t *testing.T) {
	const countColumn = `{"name":"__result_0","queryName":"A","aggregationIndex":0,"columnType":"aggregation"}`
	const secondColumn = `{"name":"__result_1","queryName":"A","aggregationIndex":1,"columnType":"aggregation"}`
	tests := []struct {
		name     string
		args     map[string]any
		response string
		want     bool
	}{
		{"scalar with no rows", map[string]any{"source": "audit"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":[]}]}}}`, true},
		{"scalar with null rows", map[string]any{"source": "audit"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":null}]}}}`, true},
		{"ungrouped scalar of zero", map[string]any{"source": "audit"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":[[0]]}]}}}`, true},
		{"ungrouped conditional count of zero", map[string]any{"source": "audit", "aggregations": "countIf(severity_text = 'ERROR')"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":[[0]]}]}}}`, false},
		{"ungrouped count beside another aggregation, both zero", map[string]any{"source": "audit", "aggregations": []any{"count()", "count_distinct(severity_text)"}},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `,` + secondColumn + `],"data":[[0,0]]}]}}}`, true},
		{"ungrouped count above a conditional zero", map[string]any{"source": "audit", "aggregations": []any{"count()", "countIf(severity_text = 'ERROR')"}},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `,` + secondColumn + `],"data":[[5,0]]}]}}}`, false},
		{"ungrouped scalar with a count", map[string]any{"source": "audit"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":[[3]]}]}}}`, false},
		{"grouped row of zero", map[string]any{"source": "audit", "groupBy": "signoz.audit.verb"},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[],"data":[["delete",0]]}]}}}`, false},
		{"time series with no results", map[string]any{"source": "audit", "requestType": "time_series"},
			`{"status":"success","data":{"type":"time_series","data":{"results":[]}}}`, true},
		{"time series with null aggregations", map[string]any{"source": "audit", "requestType": "time_series"},
			`{"status":"success","data":{"type":"time_series","data":{"results":[{"queryName":"A","aggregations":null}]}}}`, true},
		{"time series with a point", map[string]any{"source": "audit", "requestType": "time_series"},
			`{"status":"success","data":{"type":"time_series","data":{"results":[{"queryName":"A","aggregations":[{"index":0,"alias":"__result_0","series":[{"values":[{"timestamp":1,"value":2}]}]}]}]}}}`, false},
		{"application logs with no rows", map[string]any{},
			`{"status":"success","data":{"type":"scalar","data":{"results":[{"queryName":"A","columns":[` + countColumn + `],"data":[]}]}}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := queryRangeRecorder(t, tc.response)
			args := map[string]any{"timeRange": "1h", "aggregations": "count()"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := h.handleAggregateLogs(testCtx(), makeToolRequest("signoz_aggregate_logs", args))
			require.NoError(t, err)
			require.False(t, result.IsError, textContent(t, result))
			require.Equal(t, tc.want, strings.Contains(allTextBlocks(result),
				"note: an empty or all-zero audit result can also mean this SigNoz does not record audit events (it is too old, or audit logging is not enabled), so do not read it as proof that nothing happened."),
				allTextBlocks(result))
		})
	}
}
