package mcp_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SigNoz/signoz-mcp-server/guardrails"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// sizedSpanRow builds one span row with the attribute volume of a service
// instrumented with the OpenTelemetry SDK, auto-instrumentation and the
// Kubernetes resource detector. 100 rows come back as about 147,000
// characters, against the 124,829 a real instance returned for search_traces at
// limit=100 (#452). A mock row with a handful of attributes stays under any cap
// and proves nothing.
func sizedSpanRow(i int) map[string]any {
	traceID := fmt.Sprintf("%032x", 0xa11ce000+i/40)
	return map[string]any{"data": map[string]any{
		"timestamp":            fmt.Sprintf("2026-11-19T08:%02d:%02d.%09dZ", i/60%60, i%60, i*1000),
		"trace_id":             traceID,
		"span_id":              fmt.Sprintf("%016x", 0xbeef0000+i),
		"parent_span_id":       fmt.Sprintf("%016x", 0xbeef0000+i-1),
		"name":                 "POST /api/v1/orders/{orderId}/payments",
		"kind_string":          "Server",
		"duration_nano":        120_000_000 + i*37,
		"has_error":            i%17 == 0,
		"response_status_code": "200",
		"attributes_string": map[string]string{
			"http.method":     "POST",
			"http.route":      "/api/v1/orders/{orderId}/payments",
			"http.url":        "https://payments.internal.example.com/api/v1/orders/8f3a2c1e/payments?currency=EUR&source=checkout",
			"db.system":       "postgresql",
			"db.name":         "payments",
			"db.statement":    "SELECT p.id, p.amount_cents, p.status FROM payments p JOIN orders o ON o.id = p.order_id WHERE o.customer_id = $1 ORDER BY p.created_at DESC LIMIT 50",
			"code.function":   "handlePayment",
			"thread.name":     "http-nio-8080-exec-7",
			"otel.scope.name": "io.opentelemetry.tomcat-10.0",
		},
		"attributes_number": map[string]float64{"http.status_code": 200, "http.request_content_length": 1843, "http.response_content_length": 612, "net.peer.port": 52118, "thread.id": 71},
		"resource": map[string]string{
			"service.name":           "payments-api",
			"service.version":        "2026.11.3-b7c4e19",
			"service.instance.id":    "payments-api-7d9f6c8b5-x2k4q",
			"deployment.environment": "production",
			"telemetry.sdk.name":     "opentelemetry",
			"cloud.region":           "europe-west4",
			"k8s.cluster.name":       "prod-eu-1",
			"k8s.namespace.name":     "checkout",
			"k8s.pod.name":           "payments-api-7d9f6c8b5-x2k4q",
			"k8s.node.name":          "gke-prod-pool-2-4f1e8a9c-vz7m",
		},
	}}
}

// sizedLogRow is a log line with the resource and attribute volume of the same
// service. 100 rows come back as about 69,000 characters, against the 61,132 a
// real instance returned for search_logs at limit=100 (#452).
func sizedLogRow(i int) map[string]any {
	return map[string]any{"data": map[string]any{
		"timestamp":     fmt.Sprintf("2026-11-19T08:%02d:%02d.%09dZ", i/60%60, i%60, i*1000),
		"id":            fmt.Sprintf("2Zq%024d", i),
		"severity_text": "INFO",
		"body":          "Authorized payment for order 8f3a2c1e after 2 retries; gateway=adyen latency_ms=412 currency=EUR amount_cents=12990",
		"trace_id":      fmt.Sprintf("%032x", 0xa11ce000+i/40),
		"span_id":       fmt.Sprintf("%016x", 0xbeef0000+i),
		"attributes_string": map[string]string{
			"logger.name": "com.example.payments.service.PaymentService",
			"thread.name": "http-nio-8080-exec-7",
			"order.id":    "8f3a2c1e",
		},
		"resources_string": map[string]string{
			"service.name":           "payments-api",
			"service.version":        "2026.11.3-b7c4e19",
			"deployment.environment": "production",
			"k8s.cluster.name":       "prod-eu-1",
			"k8s.namespace.name":     "checkout",
			"k8s.pod.name":           "payments-api-7d9f6c8b5-x2k4q",
		},
	}}
}

func queryRangeBody(t *testing.T, rows []map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"status": "success",
		"data": map[string]any{"type": "raw", "data": map[string]any{
			"results": []any{map[string]any{"queryName": "A", "rows": rows}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func rowsOf(n int, build func(int) map[string]any) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = build(i)
	}
	return rows
}

// callToolOverProductionHTTP serves upstreamBody for every query_range call and
// returns the decoded tools/call result for toolName.
func callToolOverProductionHTTP(t *testing.T, upstreamBody []byte, toolName string, arguments map[string]any, env map[string]string) map[string]any {
	t.Helper()
	return callToolOverProductionHTTPAt(t, "/api/v5/query_range", upstreamBody, toolName, arguments, env)
}

// callToolOverProductionHTTPAt serves upstreamBody for every POST to
// upstreamPath and returns the decoded tools/call result for toolName.
func callToolOverProductionHTTPAt(t *testing.T, upstreamPath string, upstreamBody []byte, toolName string, arguments map[string]any, env map[string]string) map[string]any {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != upstreamPath {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(upstreamBody)
	}))
	t.Cleanup(upstream.Close)

	t.Setenv(config.SignozURL, upstream.URL)
	t.Setenv(config.SignozApiKey, "size-test-key")
	t.Setenv(config.TransportMode, "http")
	t.Setenv(config.MaxResultCharsEnv, "")
	for key, value := range env {
		t.Setenv(key, value)
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	logger := logpkg.New("error")
	handler := tools.NewHandler(logger, cfg)
	server := NewMCPServer(logger, handler, cfg, nil, nil)
	sdkServer := server.newSDKServer()
	handler.RegisterTracesHandlers(sdkServer)
	handler.RegisterLogsHandlers(sdkServer)
	handler.RegisterServiceHandlers(sdkServer)
	httpHandler := server.buildHTTP(sdkServer).Handler

	response := protocolPOST(t, httpHandler, protocolRequestJSON(t, 900, "tools/call", map[string]any{
		"name": toolName, "arguments": arguments,
	}), http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%.300s", response.status, response.body)
	}
	result, ok := response.wire["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want an object", response.wire["result"])
	}
	if result["isError"] == true {
		t.Fatalf("tool returned an error result: %.300s", response.body)
	}
	return result
}

// resultTextBlocks returns each content text block of a tools/call result.
func resultTextBlocks(t *testing.T, result map[string]any) []string {
	t.Helper()
	content, _ := result["content"].([]any)
	blocks := make([]string, 0, len(content))
	for _, item := range content {
		block, _ := item.(map[string]any)
		text, _ := block["text"].(string)
		blocks = append(blocks, text)
	}
	return blocks
}

func totalChars(blocks []string) int {
	total := 0
	for _, block := range blocks {
		total += utf8.RuneCountInString(block)
	}
	return total
}

// TestGuardrail_ToolResultsStayWithinSizeBudget pins that the default server
// returns at most guardrails.MaxToolResultChars characters of text for the
// tools whose real results crossed the MCP client limit (#452): span search,
// log search and single-trace details, each over the production HTTP handler
// with realistically sized span and log rows.
func TestGuardrail_ToolResultsStayWithinSizeBudget(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
		rows      []map[string]any
		minUpChar int
	}{
		{
			name:      "search_traces limit 100",
			tool:      "signoz_search_traces",
			arguments: map[string]any{"limit": "100", "timeRange": "1h"},
			rows:      rowsOf(100, sizedSpanRow),
			minUpChar: 100_000,
		},
		{
			name:      "search_traces limit 1000",
			tool:      "signoz_search_traces",
			arguments: map[string]any{"limit": "1000", "timeRange": "1h"},
			rows:      rowsOf(1000, sizedSpanRow),
			minUpChar: 1_000_000,
		},
		{
			name:      "search_logs limit 100",
			tool:      "signoz_search_logs",
			arguments: map[string]any{"limit": "100", "timeRange": "1h"},
			rows:      rowsOf(100, sizedLogRow),
			minUpChar: 50_000,
		},
		{
			name:      "get_trace_details one trace",
			tool:      "signoz_get_trace_details",
			arguments: map[string]any{"traceId": "00000000000000000000000000a11ce0"},
			rows:      rowsOf(1000, sizedSpanRow),
			minUpChar: 1_000_000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := queryRangeBody(t, tt.rows)
			if got := utf8.RuneCount(upstream); got < tt.minUpChar {
				t.Fatalf("fixture is %d characters, want at least %d so it is as large as a real instance's result", got, tt.minUpChar)
			}

			result := callToolOverProductionHTTP(t, upstream, tt.tool, tt.arguments, nil)
			blocks := resultTextBlocks(t, result)
			if got := totalChars(blocks); got > guardrails.MaxToolResultChars {
				t.Fatalf("result text is %d characters, want at most %d (guardrails.MaxToolResultChars)", got, guardrails.MaxToolResultChars)
			}
			note := blocks[len(blocks)-1]
			for _, want := range []string{"result truncated", config.MaxResultCharsEnv, `smaller "limit"`} {
				if !strings.Contains(note, want) {
					t.Fatalf("last block = %q, want it to contain %q", note, want)
				}
			}
			if _, present := result["structuredContent"]; present {
				t.Fatalf("truncated result still carries structuredContent, which repeats the full payload")
			}
		})
	}
}

func TestToolResultUnderCapIsReturnedWhole(t *testing.T) {
	upstream := queryRangeBody(t, rowsOf(5, sizedSpanRow))
	result := callToolOverProductionHTTP(t, upstream, "signoz_search_traces",
		map[string]any{"limit": "5", "timeRange": "1h"}, nil)

	blocks := resultTextBlocks(t, result)
	if !json.Valid([]byte(blocks[0])) {
		t.Fatalf("first block is not complete JSON: %.200s", blocks[0])
	}
	for _, block := range blocks {
		if strings.Contains(block, "result truncated") {
			t.Fatalf("a result under the cap got a truncation note: %q", block)
		}
	}
}

func TestToolResultCapFollowsEnvironment(t *testing.T) {
	upstream := queryRangeBody(t, rowsOf(100, sizedSpanRow))
	arguments := map[string]any{"limit": "100", "timeRange": "1h"}

	small := resultTextBlocks(t, callToolOverProductionHTTP(t, upstream, "signoz_search_traces", arguments,
		map[string]string{config.MaxResultCharsEnv: "8000"}))
	if got := totalChars(small); got > 8000 {
		t.Fatalf("with %s=8000 the result is %d characters", config.MaxResultCharsEnv, got)
	}

	large := resultTextBlocks(t, callToolOverProductionHTTP(t, upstream, "signoz_search_traces", arguments,
		map[string]string{config.MaxResultCharsEnv: "400000"}))
	if !json.Valid([]byte(large[0])) {
		t.Fatalf("with %s=400000 the 100-row result was still cut: %.200s", config.MaxResultCharsEnv, large[0])
	}
}

// topOperationsBody builds the /api/v2/service/top_operations response for n
// operations. A real gateway with 980 operations returned 115,526 characters
// (about 118 per row, #452), so the row uses route-shaped names and the
// nanosecond latencies SigNoz reports.
func topOperationsBody(t *testing.T, n int) []byte {
	t.Helper()
	operations := make([]map[string]any, n)
	for i := range operations {
		operations[i] = map[string]any{
			"name":       fmt.Sprintf("GET /api/v1/orders/{id}/items/%04d", i),
			"p50":        float64(11_400_000 + i*1013),
			"p95":        float64(48_700_000 + i*2711),
			"p99":        float64(91_300_000 + i*4973),
			"numCalls":   120_000 - i*97,
			"errorCount": i % 13,
		}
	}
	body, err := json.Marshal(map[string]any{"status": "success", "data": operations})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestToolResultCapAppliesToServiceTopOperations pins the fourth tool the
// same instance showed over the line besides the span and log tools:
// signoz_get_service_top_operations returned 115,751 characters for 980
// operations at limit=5000.
func TestToolResultCapAppliesToServiceTopOperations(t *testing.T) {
	upstream := topOperationsBody(t, 980)
	if got := utf8.RuneCount(upstream); got < 100_000 {
		t.Fatalf("fixture is %d characters, want at least 100000 so it is as large as the real 980-operation table", got)
	}

	result := callToolOverProductionHTTPAt(t, "/api/v2/service/top_operations", upstream,
		"signoz_get_service_top_operations",
		map[string]any{"service": "gateway", "timeRange": "1h", "limit": "5000"}, nil)
	blocks := resultTextBlocks(t, result)
	if got := totalChars(blocks); got > guardrails.MaxToolResultChars {
		t.Fatalf("result text is %d characters, want at most %d", got, guardrails.MaxToolResultChars)
	}
	if note := blocks[len(blocks)-1]; !strings.Contains(note, "result truncated") {
		t.Fatalf("last block = %q, want the truncation note", note)
	}
}

// TestToolResultBetweenHalfAndFullCapIsReturnedWhole pins that the default cap
// does not cut a result that fits. The same instance had query_metrics at
// 42,397 characters, list_dashboard_templates at 34,087 and
// check_metric_cardinality at 32,661: under 50,000, so they must reach the
// agent complete rather than with a note and a missing tail.
func TestToolResultBetweenHalfAndFullCapIsReturnedWhole(t *testing.T) {
	// 360 operations come to about 42,000 characters.
	upstream := topOperationsBody(t, 360)
	result := callToolOverProductionHTTPAt(t, "/api/v2/service/top_operations", upstream,
		"signoz_get_service_top_operations",
		map[string]any{"service": "gateway", "timeRange": "1h", "limit": "5000"}, nil)

	blocks := resultTextBlocks(t, result)
	got := totalChars(blocks)
	if got < 40_000 || got > guardrails.MaxToolResultChars {
		t.Fatalf("result text is %d characters, want between 40000 and %d", got, guardrails.MaxToolResultChars)
	}
	var operations []map[string]any
	if err := json.Unmarshal([]byte(blocks[0]), &operations); err != nil {
		t.Fatalf("first block is not complete JSON: %v", err)
	}
	if len(operations) != 360 {
		t.Fatalf("got %d operations, want all 360", len(operations))
	}
	for _, block := range blocks {
		if strings.Contains(block, "result truncated") {
			t.Fatalf("a result under the cap got a truncation note: %q", block)
		}
	}
}
