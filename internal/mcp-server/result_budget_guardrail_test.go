package mcp_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/guardrails"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestGuardrail_OffsetToolResultsFitTheirSizeBudget catches a tool that pages
// with offset and that the production server, as configured by default, lets
// outgrow the result size budget. The handler tests pin how a page is cut.
func TestGuardrail_OffsetToolResultsFitTheirSizeBudget(t *testing.T) {
	padding := strings.Repeat("payload ", 20)
	items := func(format string) string {
		list := make([]string, 200)
		for i := range list {
			list[i] = fmt.Sprintf(format, i, i, padding)
		}
		return strings.Join(list, ",")
	}
	// Each route answers with 200 items, about 45 KB: more than the default budget.
	routes := map[string]string{
		"/api/v5/query_range":           `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","nextCursor":"c","rows":[` + items(`{"timestamp":"2026-11-21T10:00:00Z","data":{"id":"row-%03d","trace_id":"trace-%d","body":%q}}`) + `]}]}}}`,
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
	headers := http.Header{"Mcp-Protocol-Version": {"2025-11-25"}}

	// The tools come from the registry, so a new one that pages with offset is
	// held to the budget without being added to a list here.
	listed, _ := protocolPOST(t, httpHandler, protocolRequestJSON(t, 900, "tools/list", map[string]any{}), headers).wire["result"].(map[string]any)
	registered, _ := listed["tools"].([]any)
	if cursor, _ := listed["nextCursor"].(string); cursor != "" {
		t.Fatal("tools/list returned a second page; follow nextCursor here so that no tool that pages with offset escapes this test")
	}
	pagesWithOffset := map[string]bool{}
	for _, entry := range registered {
		tool, _ := entry.(map[string]any)
		schema, _ := tool["inputSchema"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		if _, ok := properties["offset"]; ok {
			pagesWithOffset[tool["name"].(string)] = true
		}
	}
	if len(pagesWithOffset) == 0 {
		t.Fatal("tools/list returned no tool with an offset parameter; this test read the registry wrong")
	}
	for name := range guardrails.UnbudgetedOffsetTools {
		if !pagesWithOffset[name] {
			t.Errorf("guardrails.UnbudgetedOffsetTools names %s, which is not a registered tool that pages with offset. Remove the entry.", name)
		}
	}

	id := 900
	for name := range pagesWithOffset {
		if _, unbudgeted := guardrails.UnbudgetedOffsetTools[name]; unbudgeted {
			continue
		}
		id++
		t.Run(name, func(t *testing.T) {
			response := protocolPOST(t, httpHandler, protocolRequestJSON(t, id, "tools/call", map[string]any{
				"name": name, "arguments": map[string]any{"limit": 200},
			}), headers)
			result, _ := response.wire["result"].(map[string]any)
			if response.status != http.StatusOK || result == nil || result["isError"] == true {
				t.Fatalf("%s returned no page (status %d): %.400s\nA tool that pages with offset needs a route in this test that answers with a page over the budget (OUT-8).", name, response.status, response.body)
			}
			content, _ := result["content"].([]any)
			textBytes := 0
			for _, block := range content {
				text, _ := block.(map[string]any)["text"].(string)
				textBytes += len(text)
			}
			if textBytes > config.DefaultMaxResultBytes {
				t.Fatalf("%s delivered %d bytes of text, over the default %d-byte result size budget. Return its page through listResult or rawSearchResult, which keep the leading whole items that fit (OUT-8). Only when SigNoz returns its items in no fixed order, name it in guardrails.UnbudgetedOffsetTools with the reason.", name, textBytes, config.DefaultMaxResultBytes)
			}
			block0, _ := content[0].(map[string]any)["text"].(string)
			var page struct {
				Pagination struct {
					Truncated bool `json:"truncated"`
				} `json:"pagination"`
			}
			if err := json.Unmarshal([]byte(block0), &page); err != nil || !page.Pagination.Truncated {
				t.Fatalf("%s returned its page without a budget cut, so this test proved nothing. Make its route here answer with a page over %d bytes: %.300s", name, config.DefaultMaxResultBytes, block0)
			}
		})
	}
}
