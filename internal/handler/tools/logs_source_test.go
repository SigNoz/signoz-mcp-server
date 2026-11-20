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
