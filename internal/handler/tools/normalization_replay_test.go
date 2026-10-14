package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// A schema-validating client rejects any form the advertised schema does not admit, so
// every form a handler reads, including the retired logs query that earns a coded error,
// must pass the registered schema without a validation notice.
func TestAdvertisedSchemasAdmitEveryArgumentFormHandlersRead(t *testing.T) {
	registered := registeredTestTools(t)
	tests := []struct {
		name      string
		tool      string
		arguments string
	}{
		{name: "integer limit", tool: "signoz_search_docs", arguments: `{"searchText":"cpu","limit":7}`},
		{name: "string limit", tool: "signoz_search_docs", arguments: `{"searchText":"cpu","limit":"7"}`},
		{name: "default limit and omitted searchContext", tool: "signoz_search_docs", arguments: `{"searchText":"cpu"}`},
		{name: "boolean", tool: "signoz_list_alerts", arguments: `{"active":true}`},
		{name: "case insensitive boolean string", tool: "signoz_list_alerts", arguments: `{"active":"TRUE"}`},
		{name: "docs query alias", tool: "signoz_search_docs", arguments: `{"query":"cpu"}`},
		{name: "retired logs query alias reaches its coded error", tool: "signoz_search_logs", arguments: `{"query":"service.name = 'api'"}`},
		{name: "numeric timestamps", tool: "signoz_search_logs", arguments: `{"start":1711130400000,"end":1711130460000}`},
		{name: "string timestamps", tool: "signoz_search_logs", arguments: `{"start":"1711130400000","end":"1711130460000"}`},
		{name: "groupBy string", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","groupBy":"service.name,k8s.pod.name"}`},
		{name: "groupBy array", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","groupBy":["service.name","k8s.pod.name"]}`},
		{name: "formulaQueries JSON string", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","formulaQueries":"[{\"name\":\"B\",\"metricName\":\"memory\"}]"}`},
		{name: "formulaQueries array", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","formulaQueries":[{"name":"B","metricName":"memory"}]}`},
		{name: "legacy resource id", tool: "signoz_get_alert", arguments: `{"ruleId":"rule-1"}`},
		{name: "logs limit integer", tool: "signoz_search_logs", arguments: `{"limit":50}`},
		{name: "logs limit string", tool: "signoz_search_logs", arguments: `{"limit":"50"}`},
		{name: "logs offset integer", tool: "signoz_search_logs", arguments: `{"offset":25}`},
		{name: "logs offset string", tool: "signoz_search_logs", arguments: `{"offset":"25"}`},
		{name: "metrics stepInterval integer", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","stepInterval":60}`},
		{name: "metrics stepInterval string", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","stepInterval":"60"}`},
		{name: "services limit integer", tool: "signoz_list_services", arguments: `{"limit":50}`},
		{name: "services limit string", tool: "signoz_list_services", arguments: `{"limit":"50"}`},
		{name: "views limit integer", tool: "signoz_list_views", arguments: `{"limit":50}`},
		{name: "views limit string", tool: "signoz_list_views", arguments: `{"limit":"50"}`},
		{name: "views offset integer", tool: "signoz_list_views", arguments: `{"offset":25}`},
		{name: "views offset string", tool: "signoz_list_views", arguments: `{"offset":"25"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := registered[tt.tool]
			if entry == nil {
				t.Fatalf("tool %s is not registered", tt.tool)
			}
			called := false
			h := &Handler{logger: logpkg.New("error")}
			s := newMCPTestServer()
			h.addTool(s, entry.Tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return mcp.NewToolResultText("ok"), nil
			})

			raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tt.tool, tt.arguments)
			response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(raw))
			encoded, _ := json.Marshal(response)
			if !called {
				t.Fatalf("raw-wire call did not reach the handler: %s", encoded)
			}
			if strings.Contains(string(encoded), inputValidationNoticePrefix) {
				t.Fatalf("accepted advertised form must not trigger a validation notice: %s", encoded)
			}
		})
	}
}

func TestStringFormIntegerAboveFloat53ReachesHandlerExactly(t *testing.T) {
	const exact = int64(1700000000000000129)

	tests := []struct {
		name      string
		argument  string
		wantStart int64
	}{
		{name: "string form preserves the exact integer", argument: `"1700000000000000129"`, wantStart: exact},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int64
			h := &Handler{logger: logpkg.New("error")}
			s := newMCPTestServer()
			tool := mcp.NewTool("precision_probe",
				mcp.WithString("metricName", mcp.Required()),
				mcp.WithString("start", intOrStringType()))
			h.addTool(s, tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				parsed, err := parseMetricsQueryArgs(req.GetArguments())
				if err != nil {
					return nil, err
				}
				got = parsed.Start
				return mcp.NewToolResultText(strconv.FormatInt(got, 10)), nil
			})

			raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"precision_probe","arguments":{"metricName":"cpu","start":%s}}}`, tt.argument)
			response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(raw))
			if got != tt.wantStart {
				encoded, _ := json.Marshal(response)
				t.Fatalf("production-normalized start = %d, want %d; response=%s", got, tt.wantStart, encoded)
			}
		})
	}

	// At nanosecond-epoch magnitudes (~1.7e18), adjacent float64 values are
	// roughly 256 ns apart. Numeric JSON can therefore round before handlers
	// call GetArguments; the accepted string form is the exact-value escape hatch.
}
