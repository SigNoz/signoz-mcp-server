package mcp_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestGuardrail_RowSearchResultsFitTheirSizeBudget catches a raw row tool
// whose delivered text outgrows its result size budget: what MCP clients
// truncate, drop, or move to a file. Both tools go through the production HTTP
// handler against a SigNoz stub that returns about 400 KB of rows; the text
// blocks on the wire, notes and decorator notices included, must fit.
func TestGuardrail_RowSearchResultsFitTheirSizeBudget(t *testing.T) {
	rows := make([]string, 200)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"timestamp":"2026-11-21T10:00:00Z","data":{"id":"row-%03d","trace_id":"trace-%03d","duration_nano":9007199254740993,"body":%q}}`,
			i, i, strings.Repeat("payload ", 250))
	}
	body := `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","nextCursor":"c","rows":[` + strings.Join(rows, ",") + `]}]}}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{
		URL:             upstream.URL,
		APIKey:          "budget-guardrail-key",
		TransportMode:   "http",
		Host:            "127.0.0.1",
		Port:            "0",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
		MaxRequestBytes: 1 << 20,
	}
	handler := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, handler, cfg, nil, nil)
	sdkServer := server.newSDKServer()
	handler.RegisterLogsHandlers(sdkServer)
	handler.RegisterTracesHandlers(sdkServer)
	httpHandler := server.buildHTTP(sdkServer).Handler

	for index, tc := range []struct {
		name      string
		arguments map[string]any
		budget    int
	}{
		{name: "server default", arguments: map[string]any{"limit": 200}, budget: config.DefaultMaxResultBytes},
		{name: "per-call budget", arguments: map[string]any{"limit": 200, "maxResultBytes": "5000"}, budget: 5000},
		{name: "with decorator notices", arguments: map[string]any{"limit": 200, "maxResultBytes": 5000, "stray_argument_one": 1, "stray_argument_two": 2}, budget: 5000},
	} {
		for _, tool := range []string{"signoz_search_logs", "signoz_search_traces"} {
			t.Run(tool+" "+tc.name, func(t *testing.T) {
				response := protocolPOST(t, httpHandler, protocolRequestJSON(t, 900+index, "tools/call", map[string]any{
					"name": tool, "arguments": tc.arguments,
				}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
				if response.status != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%.400s", response.status, response.body)
				}
				result, _ := response.wire["result"].(map[string]any)
				if result == nil || result["isError"] == true {
					t.Fatalf("want a successful tool result, got %.400s", response.body)
				}
				content, _ := result["content"].([]any)
				textBytes := 0
				for _, block := range content {
					text, _ := block.(map[string]any)["text"].(string)
					textBytes += len(text)
				}
				if textBytes > tc.budget {
					t.Fatalf("%s delivered %d bytes of text, over its %d-byte result size budget. Fit whole rows into the budget in rawSearchResult and count every note the decorators append.", tool, textBytes, tc.budget)
				}

				block0, _ := content[0].(map[string]any)["text"].(string)
				var page struct {
					Pagination struct {
						Returned   int  `json:"returned"`
						HasMore    bool `json:"hasMore"`
						NextOffset int  `json:"nextOffset"`
						Truncated  bool `json:"truncated"`
					} `json:"pagination"`
				}
				if err := json.Unmarshal([]byte(block0), &page); err != nil {
					t.Fatalf("block 0 is not valid JSON: %v", err)
				}
				if got := page.Pagination; got.Returned < 1 || !got.HasMore || !got.Truncated || got.NextOffset != got.Returned {
					t.Fatalf("pagination = %+v, want at least one row, hasMore and truncated true, and nextOffset = returned", got)
				}
			})
		}
	}
}

// TestGuardrail_ListResultsFitTheirSizeBudget catches a list tool whose
// delivered text outgrows the server's result size budget. Each budgeted list
// tool goes through the production HTTP handler against a
// SigNoz stub whose 200 items come to about 45 KB; the text blocks on the
// wire, notes and decorator notices included, must fit, and the page must say
// where the next one starts. Items are smaller than the notices, so a notice
// appended after the cut cannot hide in the space a cut leaves.
func TestGuardrail_ListResultsFitTheirSizeBudget(t *testing.T) {
	padding := strings.Repeat("payload ", 20)
	items := func(format string) string {
		list := make([]string, 200)
		for i := range list {
			list[i] = fmt.Sprintf(format, i, i, padding)
		}
		return strings.Join(list, ",")
	}
	routes := map[string]string{
		"/api/v2/services":              `{"status":"success","data":[` + items(`{"serviceName":"svc-%03d","p99":1,"tag":"%d %s"}`) + `]}`,
		"/api/v1/dependency_graph":      `[` + items(`{"parent":"svc-%03d","child":"redis","callCount":3,"tag":"%d %s"}`) + `]`,
		"/api/v1/alerts":                `{"status":"success","data":[` + items(`{"labels":{"ruleId":"rule-%03d","alertname":"%d %s"},"status":{"state":"active"}}`) + `]}`,
		"/api/v3/rules":                 `{"status":"success","data":{"rules":[` + items(`{"id":"rule-%03d","alert":"rule %d","description":%q}`) + `],"total":200}}`,
		"/api/v2/notification_channels": `{"status":"success","data":{"channels":[` + items(`{"id":"019947a7-f200-7000-8000-000000000%03d","name":"channel-%d","displayName":%q,"kind":"email","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`) + `],"total":200}}`,
		"/api/v2/dashboards":            `{"status":"success","data":{"dashboards":[` + items(`{"id":"dash-%03d","name":"dashboard %d","description":%q}`) + `],"tags":[],"total":200}}`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	logger := logpkg.New("error")
	cfg := &config.Config{
		URL:             upstream.URL,
		APIKey:          "budget-guardrail-key",
		TransportMode:   "http",
		Host:            "127.0.0.1",
		Port:            "0",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
		MaxRequestBytes: 1 << 20,
	}
	handler := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, handler, cfg, nil, nil)
	sdkServer := server.newSDKServer()
	handler.RegisterAllToolHandlers(sdkServer)
	httpHandler := server.buildHTTP(sdkServer).Handler

	stray := map[string]any{}
	for i := range 10 {
		stray[fmt.Sprintf("unknown_argument_with_a_long_name_%02d", i)] = "x"
	}
	id := 950
	for _, tc := range []struct {
		tool      string
		arguments map[string]any
		itemsAt   []string
		// strict tools reject undeclared arguments instead of serving them with a notice.
		strict bool
	}{
		{tool: "signoz_list_services", itemsAt: []string{"data"}},
		{tool: "signoz_get_service_map", itemsAt: []string{"data"}},
		{tool: "signoz_list_alerts", itemsAt: []string{"data"}},
		{tool: "signoz_list_alert_rules", itemsAt: []string{"data"}},
		{tool: "signoz_list_notification_channels", itemsAt: []string{"channels"}, strict: true},
		{tool: "signoz_list_dashboards", itemsAt: []string{"data", "dashboards"}},
	} {
		for _, withNotices := range []bool{false, true} {
			if withNotices && tc.strict {
				continue
			}
			name := tc.tool + " server default"
			arguments := map[string]any{"limit": 200}
			for key, value := range tc.arguments {
				arguments[key] = value
			}
			if withNotices {
				name = tc.tool + " with decorator notices"
				for key, value := range stray {
					arguments[key] = value
				}
			}
			id++
			t.Run(name, func(t *testing.T) {
				response := protocolPOST(t, httpHandler, protocolRequestJSON(t, id, "tools/call", map[string]any{
					"name": tc.tool, "arguments": arguments,
				}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
				if response.status != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%.400s", response.status, response.body)
				}
				result, _ := response.wire["result"].(map[string]any)
				if result == nil || result["isError"] == true {
					t.Fatalf("want a successful tool result, got %.400s", response.body)
				}
				content, _ := result["content"].([]any)
				textBytes := 0
				for _, block := range content {
					text, _ := block.(map[string]any)["text"].(string)
					textBytes += len(text)
				}
				if textBytes > config.DefaultMaxResultBytes {
					t.Fatalf("%s delivered %d bytes of text, over its %d-byte result size budget. Route the page through listResult or budgetedListResult, which keep the leading whole items that fit with every note the decorators append.", tc.tool, textBytes, config.DefaultMaxResultBytes)
				}
				if withNotices {
					last, _ := content[len(content)-1].(map[string]any)["text"].(string)
					if !strings.Contains(last, "this tool ignored the arguments") {
						t.Fatalf("the ignored-arguments notice must be the last block, got %.200q", last)
					}
				}

				block0, _ := content[0].(map[string]any)["text"].(string)
				var page map[string]json.RawMessage
				if err := json.Unmarshal([]byte(block0), &page); err != nil {
					t.Fatalf("block 0 is not a JSON object: %v", err)
				}
				var pagination struct {
					Offset     int  `json:"offset"`
					HasMore    bool `json:"hasMore"`
					NextOffset int  `json:"nextOffset"`
					Truncated  bool `json:"truncated"`
				}
				if err := json.Unmarshal(page["pagination"], &pagination); err != nil {
					t.Fatalf("block 0 has no pagination object: %v", err)
				}
				holder := page
				for _, key := range tc.itemsAt[:len(tc.itemsAt)-1] {
					_ = json.Unmarshal(holder[key], &holder)
				}
				var listed []json.RawMessage
				_ = json.Unmarshal(holder[tc.itemsAt[len(tc.itemsAt)-1]], &listed)
				if len(listed) < 1 || !pagination.HasMore || !pagination.Truncated || pagination.NextOffset != pagination.Offset+len(listed) {
					t.Fatalf("%s returned %d items with pagination %+v; want at least one item, hasMore and truncated true, and nextOffset = offset + items returned", tc.tool, len(listed), pagination)
				}
			})
		}
	}
}
