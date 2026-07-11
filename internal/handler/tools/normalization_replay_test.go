package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestAcceptedNormalizationFormsReplayThroughAdvertisedSchemas(t *testing.T) {
	registered := registeredTestTools(t)
	tests := []struct {
		name      string
		tool      string
		arguments string
		want      string
		normalize func(mcp.CallToolRequest) (string, error)
	}{
		{
			name: "integer limit", tool: "signoz_search_docs", arguments: `{"searchText":"cpu","limit":7}`, want: "7",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				return strconv.Itoa(parseLimit(req.GetArguments()["limit"], 10)), nil
			},
		},
		{
			name: "string limit", tool: "signoz_search_docs", arguments: `{"searchText":"cpu","limit":"7"}`, want: "7",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				return strconv.Itoa(parseLimit(req.GetArguments()["limit"], 10)), nil
			},
		},
		{
			name: "default limit and omitted searchContext", tool: "signoz_search_docs", arguments: `{"searchText":"cpu"}`, want: "10",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				return strconv.Itoa(parseLimit(req.GetArguments()["limit"], 10)), nil
			},
		},
		{
			name: "boolean", tool: "signoz_list_alerts", arguments: `{"active":true}`, want: "true",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				value, _, err := parseBoolArg(req.GetArguments(), "active")
				return strconv.FormatBool(value), err
			},
		},
		{
			name: "case insensitive boolean string", tool: "signoz_list_alerts", arguments: `{"active":"TRUE"}`, want: "true",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				value, _, err := parseBoolArg(req.GetArguments(), "active")
				return strconv.FormatBool(value), err
			},
		},
		{
			name: "docs query alias", tool: "signoz_search_docs", arguments: `{"query":"cpu"}`, want: "cpu",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				args := req.GetArguments()
				value, _ := args["searchText"].(string)
				if value == "" {
					value, _ = args["query"].(string)
				}
				return value, nil
			},
		},
		{
			name: "filter query alias", tool: "signoz_search_logs", arguments: `{"query":"service.name = 'api'"}`, want: "service.name = 'api'",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				return readFilterExpr(req.GetArguments())
			},
		},
		{
			name: "numeric timestamps", tool: "signoz_search_logs", arguments: `{"start":1711130400000,"end":1711130460000}`, want: "1711130400000/1711130460000",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				args := req.GetArguments()
				if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
					return "", err
				}
				start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitMillis)
				return start + "/" + end, nil
			},
		},
		{
			name: "string timestamps", tool: "signoz_search_logs", arguments: `{"start":"1711130400000","end":"1711130460000"}`, want: "1711130400000/1711130460000",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				args := req.GetArguments()
				if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
					return "", err
				}
				start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitMillis)
				return start + "/" + end, nil
			},
		},
		{
			name: "groupBy string", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","groupBy":"service.name,k8s.pod.name"}`, want: "2",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				parsed, err := parseMetricsQueryArgs(req.GetArguments())
				if err != nil {
					return "", err
				}
				return strconv.Itoa(len(parsed.GroupBy)), nil
			},
		},
		{
			name: "groupBy array", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","groupBy":["service.name","k8s.pod.name"]}`, want: "2",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				parsed, err := parseMetricsQueryArgs(req.GetArguments())
				if err != nil {
					return "", err
				}
				return strconv.Itoa(len(parsed.GroupBy)), nil
			},
		},
		{
			name: "formulaQueries JSON string", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","formulaQueries":"[{\"name\":\"B\",\"metricName\":\"memory\"}]"}`, want: "1",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				parsed, err := parseMetricsQueryArgs(req.GetArguments())
				if err != nil {
					return "", err
				}
				return strconv.Itoa(len(parsed.FormulaQueries)), nil
			},
		},
		{
			name: "formulaQueries array", tool: "signoz_query_metrics", arguments: `{"metricName":"cpu","formulaQueries":[{"name":"B","metricName":"memory"}]}`, want: "1",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				parsed, err := parseMetricsQueryArgs(req.GetArguments())
				if err != nil {
					return "", err
				}
				return strconv.Itoa(len(parsed.FormulaQueries)), nil
			},
		},
		{
			name: "legacy resource id", tool: "signoz_get_alert", arguments: `{"ruleId":"rule-1"}`, want: "rule-1",
			normalize: func(req mcp.CallToolRequest) (string, error) {
				return readResourceID(req.GetArguments(), "ruleId"), nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := registered[tt.tool]
			if entry == nil {
				t.Fatalf("tool %s is not registered", tt.tool)
			}
			called := false
			got := ""
			h := &Handler{logger: logpkg.New("error")}
			s := server.NewMCPServer("test", "0.0.0")
			h.addTool(s, entry.Tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				var err error
				got, err = tt.normalize(req)
				if err != nil {
					return nil, err
				}
				return mcp.NewToolResultText(got), nil
			})

			raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tt.tool, tt.arguments)
			response := s.HandleMessage(context.Background(), json.RawMessage(raw))
			encoded, _ := json.Marshal(response)
			if !called {
				t.Fatalf("raw-wire call did not reach the handler: %s", encoded)
			}
			if got != tt.want {
				t.Fatalf("normalized value = %q, want %q", got, tt.want)
			}
			if strings.Contains(string(encoded), inputValidationNoticePrefix) {
				t.Fatalf("accepted advertised form must not trigger a validation notice: %s", encoded)
			}
		})
	}
}

func TestRawWireIntegerAboveFloat53ProductionDecodeCharacterization(t *testing.T) {
	const exact = int64(1700000000000000129)
	rounded := int64(float64(exact))
	if rounded == exact {
		t.Fatal("fixture must expose float64 precision loss")
	}

	tests := []struct {
		name      string
		argument  string
		wantStart int64
	}{
		{name: "numeric form rounds through GetArguments", argument: `1700000000000000129`, wantStart: rounded},
		{name: "string form preserves the exact integer", argument: `"1700000000000000129"`, wantStart: exact},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int64
			h := &Handler{logger: logpkg.New("error")}
			s := server.NewMCPServer("test", "0.0.0")
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
			response := s.HandleMessage(context.Background(), json.RawMessage(raw))
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
