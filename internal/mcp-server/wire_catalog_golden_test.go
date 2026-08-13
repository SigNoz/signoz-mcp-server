package mcp_server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var updateWireCatalogGoldens = flag.Bool(
	"signoz-wire-oracle-update",
	false,
	"rewrite the pre-migration wire oracle (available only while mark3labs/mcp-go is the production SDK)",
)

const (
	wireCatalogGoldenDir       = "testdata/wire-catalog"
	wireCatalogProtocolVersion = "2025-11-25"
	wireSentinelVersion        = "<version>"
	wireSentinelAsOf           = "<asOf>"
	wireSentinelHistoryStart   = "<historyStart>"
	wireSentinelHistoryEnd     = "<historyEnd>"
)

type wireCapture struct {
	Method          string `json:"method"`
	ProtocolVersion string `json:"protocolVersion"`
	Request         any    `json:"request"`
	HTTPStatus      int    `json:"httpStatus"`
	ContentType     string `json:"contentType"`
	Framing         string `json:"framing"`
	Response        any    `json:"response"`
}

type wireContentDigest struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Meta     any    `json:"_meta,omitempty"`
	Length   int    `json:"serializedLength"`
	SHA256   string `json:"sha256"`
}

type wireInventoryEntry struct {
	Identity    string              `json:"identity"`
	Description string              `json:"description,omitempty"`
	Contents    []wireContentDigest `json:"contents"`
}

// acceptedMigrationDifferences is deliberately path-specific. Phase 0 asserts
// the old side; post-swap tests must assert the named new side rather than add a
// generic ignore rule.
var acceptedMigrationDifferences = []struct {
	Method         string
	PathOrBehavior string
	Old            string
	New            string
}{
	{"initialize", "result.capabilities.logging", "{}", "absent"},
	{"*/list", "result top-level discovery collection ordering", "mark3 order", "order-insensitive"},
	{"cacheable methods", "result.ttlMs", "absent", "0"},
	{"cacheable methods", "result.cacheScope", "absent", `"public"`},
	{"resources/read", "unknown resource error", `-32002 "Resource not found"`, `-32602 "Invalid params" with official data`},
	{"tools/call", "successful input mismatch notice detail", "validator-library detail", "repository-owned deterministic sentence"},
	{"2026-07-28", "result.resultType and server metadata", "absent", "present"},
	{"HTTP GET/DELETE", "stateless transport", "listening stream / accepted DELETE", "405"},
	{"stdio", "malformed frame", "JSON-RPC error may continue", "connection termination"},
}

type wireOracle struct {
	t                    *testing.T
	handler              http.Handler
	upstream             *httptest.Server
	docs                 *docsindex.IndexRegistry
	mu                   sync.Mutex
	historyRequest       types.AlertHistoryRequest
	dashboardRequestBody []byte
}

func TestGuardrail_WireCatalogGoldens(t *testing.T) {
	if *updateWireCatalogGoldens {
		assertLegacySDKCanGenerateWireGoldens(t)
	}
	o := newWireOracle(t)
	t.Cleanup(o.close)

	for _, tc := range []struct{ file, method, params string }{
		{"initialize.json", "initialize", `{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"wire-oracle","version":"1"}}`},
		{"tools-list.json", "tools/list", `{}`},
		{"resources-list.json", "resources/list", `{}`},
		{"resource-templates-list.json", "resources/templates/list", `{}`},
		{"prompts-list.json", "prompts/list", `{}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			capture := o.capture(tc.method, tc.params)
			if _, ok := capture.Response.(map[string]any)["error"]; ok {
				t.Fatalf("%s returned JSON-RPC error: %#v", tc.method, capture.Response)
			}
			assertWireGolden(t, tc.file, capture)
		})
	}

	t.Run("static resource inventory", func(t *testing.T) {
		list := o.capture("resources/list", `{}`)
		resources := resultArray(t, list.Response, "resources")
		inventory := make([]wireInventoryEntry, 0, len(resources))
		for _, item := range resources {
			uri := item.(map[string]any)["uri"].(string)
			read := o.capture("resources/read", mustJSON(map[string]any{"uri": uri}))
			inventory = append(inventory, digestResultContents(t, uri, read.Response))
		}
		if len(inventory) != 22 {
			t.Fatalf("static resource count = %d, want 22", len(inventory))
		}
		sort.Slice(inventory, func(i, j int) bool { return inventory[i].Identity < inventory[j].Identity })
		assertWireGolden(t, "resources-content-inventory.json", inventory)
		sitemap := o.capture("resources/read", `{"uri":"signoz://docs/sitemap"}`)
		assertWireGolden(t, "resource-sitemap-literal.json", sitemap)
	})

	t.Run("resource template literals", func(t *testing.T) {
		dashboardCapture := o.capture("resources/read", `{"uri":"signoz://dashboard/dashboard-wire/summary"}`)
		assertWireGolden(t, "resource-template-dashboard-literal.json", dashboardCapture)
		if got, want := string(o.dashboardRequestBody), `{"data":{"title":"Checkout RED","widgets":[{"title":"Request rate"}],"variables":{"service":{"type":"DYNAMIC"}}}}`; got != want {
			t.Fatalf("dashboard bytes changed across the production client: got %q, want %q", got, want)
		}

		alertCapture := o.capture("resources/read", `{"uri":"signoz://alert/rule-wire/summary"}`)
		assertAlertWindow(t, alertCapture.Response, o.historyRequest)
		normalizeAlertTemplateTimestamps(t, alertCapture.Response)
		assertWireGolden(t, "resource-template-alert-literal.json", alertCapture)
	})

	t.Run("prompt inventories and literals", func(t *testing.T) {
		promptCases := []struct{ name, args string }{
			{"compare_metrics", `{"metricName":"http.server.duration","period1":"previous day","period2":"today"}`},
			{"debug_service_errors", `{"service":"checkout","timeRange":"2h"}`},
			{"incident_triage", `{"alertId":"rule-wire"}`},
			{"latency_analysis", `{"service":"checkout","timeRange":"2h"}`},
		}
		inventory := make([]wireInventoryEntry, 0, len(promptCases))
		for _, prompt := range promptCases {
			capture := o.capture("prompts/get", mustJSON(map[string]any{"name": prompt.name, "arguments": decodeJSON(t, []byte(prompt.args))}))
			inventory = append(inventory, digestPromptMessages(t, prompt.name, capture.Response))
			if prompt.name == "debug_service_errors" || prompt.name == "incident_triage" {
				assertWireGolden(t, "prompt-"+prompt.name+"-literal.json", capture)
			}
		}
		assertWireGolden(t, "prompts-content-inventory.json", inventory)
	})

	t.Run("representative tool and error literals", func(t *testing.T) {
		cases := []struct{ file, method, params string }{
			{"tool-success-structured.json", "tools/call", `{"name":"signoz_list_notification_channels","arguments":{}}`},
			{"tool-success-fail-open-input.json", "tools/call", `{"name":"signoz_list_dashboards","arguments":{"limit":{}}}`},
			{"tool-error-coded-omitted-arguments.json", "tools/call", `{"name":"signoz_get_alert"}`},
			{"tool-error-coded-null-arguments.json", "tools/call", `{"name":"signoz_get_alert","arguments":null}`},
			{"error-unknown-tool.json", "tools/call", `{"name":"signoz_unknown","arguments":{}}`},
			{"error-unknown-resource.json", "resources/read", `{"uri":"signoz://unknown"}`},
			{"error-unknown-prompt.json", "prompts/get", `{"name":"signoz_unknown","arguments":{}}`},
		}
		for _, tc := range cases {
			capture := o.capture(tc.method, tc.params)
			assertWireGolden(t, tc.file, capture)
		}
	})

	t.Run("accepted migration differences have focused legacy assertions", func(t *testing.T) {
		if len(acceptedMigrationDifferences) != 9 {
			t.Fatalf("accepted migration difference count = %d, want 9", len(acceptedMigrationDifferences))
		}
		initialize := o.capture("initialize", `{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"wire-oracle","version":"1"}}`)
		result := initialize.Response.(map[string]any)["result"].(map[string]any)
		capabilities := result["capabilities"].(map[string]any)
		if _, ok := capabilities["logging"]; !ok {
			t.Fatal("legacy initialize no longer advertises capabilities.logging")
		}
		if _, ok := result["ttlMs"]; ok {
			t.Fatal("legacy initialize unexpectedly has ttlMs")
		}
		if _, ok := result["cacheScope"]; ok {
			t.Fatal("legacy initialize unexpectedly has cacheScope")
		}
		unknown := o.capture("resources/read", `{"uri":"signoz://unknown"}`)
		errObject := unknown.Response.(map[string]any)["error"].(map[string]any)
		if errObject["code"] != json.Number("-32002") {
			t.Fatalf("legacy unknown-resource code = %v", errObject["code"])
		}
	})
}

func TestWireOracleRawArgumentsCharacterization(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		wantRaw     string
		wantDecoded any
	}{
		{name: "omitted", params: `{"name":"raw_arguments_probe"}`},
		{name: "null", params: `{"name":"raw_arguments_probe","arguments":null}`, wantRaw: "null"},
		{name: "object whitespace", params: `{"name":"raw_arguments_probe","arguments":{ "value" : "wire" }}`, wantRaw: `{ "value" : "wire" }`, wantDecoded: map[string]any{"value": "wire"}},
		{name: "integer above float53", params: `{"name":"raw_arguments_probe","arguments":{"value":9007199254740993}}`, wantRaw: `{"value":9007199254740993}`, wantDecoded: map[string]any{"value": float64(9007199254740992)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tools.Handler{}
			s := server.NewMCPServer("wire-oracle", "0")
			called := false
			h.AddTool(s, mcp.NewTool("raw_arguments_probe"), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				if got := string(req.Params.RawArguments); got != tt.wantRaw {
					t.Errorf("RawArguments = %q, want %q", got, tt.wantRaw)
				}
				if got := req.Params.Arguments; !equalJSONValue(got, tt.wantDecoded) {
					t.Errorf("Arguments = %#v (%T), want %#v (%T)", got, got, tt.wantDecoded, tt.wantDecoded)
				}
				return mcp.NewToolResultText("ok"), nil
			})

			raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + tt.params + `}`
			s.HandleMessage(context.Background(), json.RawMessage(raw))
			if !called {
				t.Fatal("raw-wire request did not reach the registered handler")
			}
		})
	}
}

func newWireOracle(t *testing.T) *wireOracle {
	t.Helper()
	dashboard.InitClickhouseSchema()
	o := &wireOracle{t: t}
	o.upstream = httptest.NewServer(http.HandlerFunc(o.serveUpstream))

	logger := logpkg.New("error")
	cfg := &config.Config{TransportMode: "http", Host: "127.0.0.1", Port: "0", URL: o.upstream.URL, APIKey: "dummy-wire-key", ClientCacheSize: 8, ClientCacheTTL: time.Minute, MaxRequestBytes: 4 << 20}
	h := tools.NewHandler(logger, cfg)
	reg, err := docsindex.NewIndexRegistry(context.Background(), wireDocsSnapshot())
	if err != nil {
		t.Fatalf("create deterministic docs index: %v", err)
	}
	o.docs = reg
	h.SetDocsIndex(reg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	s := m.newSDKServer()
	m.registerHandlers(s)
	o.handler = m.buildHTTP(s).Handler
	return o
}

func (o *wireOracle) close() {
	if o.docs != nil {
		o.docs.Close(context.Background())
	}
	if o.upstream != nil {
		o.upstream.Close()
	}
}

func (o *wireOracle) serveUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/v2/dashboards/dashboard-wire":
		body := `{"data":{"title":"Checkout RED","widgets":[{"title":"Request rate"}],"variables":{"service":{"type":"DYNAMIC"}}}}`
		o.mu.Lock()
		o.dashboardRequestBody = []byte(body)
		o.mu.Unlock()
		_, _ = w.Write([]byte(body))
	case r.URL.Path == "/api/v2/dashboards":
		_, _ = w.Write([]byte(`{"dashboards":[],"tags":[],"total":0}`))
	case r.URL.Path == "/api/v1/channels":
		_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"channel-wire","name":"on-call","type":"email","data":"{}"}]}`))
	case r.URL.Path == "/api/v2/rules/rule-wire":
		_, _ = w.Write([]byte(`{"data":{"id":"rule-wire","alert":"Checkout errors"}}`))
	case r.URL.Path == "/api/v2/rules/rule-wire/history/timeline":
		start, _ := parseInt64(r.URL.Query(), "start")
		end, _ := parseInt64(r.URL.Query(), "end")
		limit, _ := parseInt(r.URL.Query(), "limit")
		o.mu.Lock()
		o.historyRequest = types.AlertHistoryRequest{Start: start, End: end, Order: r.URL.Query().Get("order"), Limit: limit}
		o.mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{"items":[{"status":"firing"}]}}`))
	default:
		http.Error(w, `{"error":"unexpected wire-oracle upstream request"}`, http.StatusNotFound)
	}
}

func parseInt64(values url.Values, key string) (int64, error) {
	var value int64
	_, err := fmt.Sscan(values.Get(key), &value)
	return value, err
}

func parseInt(values url.Values, key string) (int, error) {
	var value int
	_, err := fmt.Sscan(values.Get(key), &value)
	return value, err
}

func wireDocsSnapshot() docsindex.CorpusSnapshot {
	builtAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sitemap := "- [Send logs](https://signoz.io/docs/logs-management/send-logs/)\n- [Install Docker](https://signoz.io/docs/install/docker/)\n"
	return docsindex.CorpusSnapshot{
		SchemaVersion: docsindex.CorpusSchemaVersion,
		BuiltAt:       builtAt,
		SitemapRaw:    sitemap,
		SitemapHash:   docsindex.SitemapHash(sitemap),
		Pages: []docsindex.PageRecord{
			{URL: "https://signoz.io/docs/logs-management/send-logs/", Title: "Send logs", SectionSlug: "logs-management", SectionBreadcrumb: "Logs > Send logs", HeadingsJSON: `[]`, BodyMarkdown: "# Send logs\n\nUse OpenTelemetry.\n", FetchedAt: builtAt},
			{URL: "https://signoz.io/docs/install/docker/", Title: "Install Docker", SectionSlug: "install", SectionBreadcrumb: "Install > Docker", HeadingsJSON: `[]`, BodyMarkdown: "# Install Docker\n\nRun Docker Compose.\n", FetchedAt: builtAt},
		},
	}
}

func (o *wireOracle) capture(method, params string) wireCapture {
	o.t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": json.Number("0"), "method": method}
	if params != "" {
		request["params"] = decodeJSON(o.t, []byte(params))
	}
	body := mustJSON(request)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", wireCatalogProtocolVersion)
	rr := httptest.NewRecorder()
	o.handler.ServeHTTP(rr, req)
	framing, payload := decodeFraming(o.t, rr)
	return wireCapture{Method: method, ProtocolVersion: wireCatalogProtocolVersion, Request: request, HTTPStatus: rr.Code, ContentType: rr.Header().Get("Content-Type"), Framing: framing, Response: normalizeWireNode(decodeJSON(o.t, payload), "")}
}

func decodeFraming(t *testing.T, rr *httptest.ResponseRecorder) (string, []byte) {
	t.Helper()
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") {
		return "json", rr.Body.Bytes()
	}
	var payload []byte
	for _, line := range strings.Split(rr.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "data:"); ok {
			payload = append(payload, strings.TrimSpace(data)...)
		}
	}
	if len(payload) == 0 {
		t.Fatalf("SSE response has no data frame: %s", rr.Body.String())
	}
	return "sse", payload
}

func normalizeWireNode(node any, path string) any {
	switch value := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if childPath == "result.serverInfo.version" {
				out[key] = wireSentinelVersion
			} else {
				out[key] = normalizeWireNode(child, childPath)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = normalizeWireNode(value[i], path)
		}
		if path == "result.tools" || path == "result.resources" || path == "result.resourceTemplates" || path == "result.prompts" {
			sortCatalog(out)
		}
		return out
	default:
		return node
	}
}

func sortCatalog(entries []any) {
	sort.SliceStable(entries, func(i, j int) bool { return catalogIdentity(entries[i]) < catalogIdentity(entries[j]) })
}

func catalogIdentity(entry any) string {
	object, _ := entry.(map[string]any)
	for _, key := range []string{"name", "uri", "uriTemplate"} {
		if identity, ok := object[key].(string); ok {
			return key + "\x00" + identity
		}
	}
	return ""
}

func resultArray(t *testing.T, response any, key string) []any {
	t.Helper()
	root, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response is %T", response)
	}
	result, ok := root["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %#v", response)
	}
	items, ok := result[key].([]any)
	if !ok {
		t.Fatalf("result.%s is %T", key, result[key])
	}
	return items
}

func digestResultContents(t *testing.T, identity string, response any) wireInventoryEntry {
	return digestContents(t, identity, resultArray(t, response, "contents"))
}

func digestPromptMessages(t *testing.T, identity string, response any) wireInventoryEntry {
	t.Helper()
	root := response.(map[string]any)
	result := root["result"].(map[string]any)
	messages := resultArray(t, response, "messages")
	contents := make([]any, 0, len(messages))
	for _, raw := range messages {
		message := raw.(map[string]any)
		content := message["content"].(map[string]any)
		copy := make(map[string]any, len(content)+1)
		for key, value := range content {
			copy[key] = value
		}
		copy["role"] = message["role"]
		contents = append(contents, copy)
	}
	entry := digestContents(t, identity, contents)
	entry.Description = stringValue(result["description"])
	return entry
}

func equalJSONValue(got, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && bytes.Equal(gotJSON, wantJSON)
}

func digestContents(t *testing.T, identity string, contents []any) wireInventoryEntry {
	t.Helper()
	entry := wireInventoryEntry{Identity: identity, Contents: make([]wireContentDigest, 0, len(contents))}
	for i, content := range contents {
		object := content.(map[string]any)
		encoded := []byte(mustJSON(object))
		sum := sha256.Sum256(encoded)
		kind, _ := object["type"].(string)
		if kind == "" {
			if _, ok := object["text"]; ok {
				kind = "text"
			} else if _, ok := object["blob"]; ok {
				kind = "blob"
			}
		}
		entry.Contents = append(entry.Contents, wireContentDigest{Index: i, Kind: kind, URI: stringValue(object["uri"]), MIMEType: stringValue(object["mimeType"]), Meta: object["_meta"], Length: len(encoded), SHA256: hex.EncodeToString(sum[:])})
	}
	return entry
}

func assertAlertWindow(t *testing.T, response any, request types.AlertHistoryRequest) {
	t.Helper()
	if request.Limit != 10 || request.Order != "desc" {
		t.Fatalf("alert history request = %+v", request)
	}
	if request.End-request.Start != int64(6*time.Hour/time.Millisecond) {
		t.Fatalf("alert history span = %d ms", request.End-request.Start)
	}
	contents := resultArray(t, response, "contents")
	var payload map[string]any
	if err := json.Unmarshal([]byte(contents[0].(map[string]any)["text"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	window := payload["historyWindow"].(map[string]any)
	if int64(payload["asOf"].(float64)) != request.End || int64(window["start"].(float64)) != request.Start || int64(window["end"].(float64)) != request.End {
		t.Fatalf("emitted timestamps do not match request: payload=%#v request=%+v", payload, request)
	}
}

func normalizeAlertTemplateTimestamps(t *testing.T, response any) {
	t.Helper()
	contents := resultArray(t, response, "contents")
	content := contents[0].(map[string]any)
	payload := decodeJSON(t, []byte(content["text"].(string))).(map[string]any)
	payload["asOf"] = wireSentinelAsOf
	window := payload["historyWindow"].(map[string]any)
	window["start"] = wireSentinelHistoryStart
	window["end"] = wireSentinelHistoryEnd
	content["text"] = mustJSON(payload)
}

func assertWireGolden(t *testing.T, name string, value any) {
	t.Helper()
	actual, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	path := filepath.Join(wireCatalogGoldenDir, name)
	if *updateWireCatalogGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v; regenerate on the pre-migration SDK with -signoz-wire-oracle-update", path, err)
	}
	if bytes.Equal(expected, actual) {
		return
	}
	t.Fatalf("wire oracle %s changed: %s", path, firstWireDifference(expected, actual))
}

func firstWireDifference(expected, actual []byte) string {
	wantLines, gotLines := strings.Split(string(expected), "\n"), strings.Split(string(actual), "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var want, got string
		if i < len(wantLines) {
			want = wantLines[i]
		}
		if i < len(gotLines) {
			got = gotLines[i]
		}
		if want != got {
			return fmt.Sprintf("line %d: golden %q, server %q", i+1, truncateWireLine(want), truncateWireLine(got))
		}
	}
	return fmt.Sprintf("golden=%d bytes server=%d bytes", len(expected), len(actual))
}

func truncateWireLine(value string) string {
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
}

func assertLegacySDKCanGenerateWireGoldens(t *testing.T) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate wire oracle source")
	}
	moduleFile := filepath.Join(filepath.Dir(filename), "..", "..", "go.mod")
	goMod, err := os.ReadFile(moduleFile)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	hasLegacy, hasOfficial := directMCPRequirements(goMod)
	if !hasLegacy || hasOfficial {
		t.Fatal("golden regeneration is disabled after the production SDK migration; fixtures are an immutable mark3 baseline")
	}
}

func TestWireOracleRegenerationSDKGate(t *testing.T) {
	tests := []struct {
		name         string
		goMod        string
		wantLegacy   bool
		wantOfficial bool
	}{
		{
			name: "legacy only",
			goMod: `module example.com/oracle

require github.com/mark3labs/mcp-go v0.56.0
`,
			wantLegacy: true,
		},
		{
			name: "dual SDK direct requirements",
			goMod: `module example.com/oracle

require (
	github.com/mark3labs/mcp-go v0.56.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)
`,
			wantLegacy:   true,
			wantOfficial: true,
		},
		{
			name: "official only indirect does not close generation gate",
			goMod: `module example.com/oracle

require github.com/mark3labs/mcp-go v0.56.0

require github.com/modelcontextprotocol/go-sdk v1.7.0 // indirect
`,
			wantLegacy: true,
		},
		{
			name: "module graph comments are ignored",
			goMod: `module example.com/oracle

require github.com/mark3labs/mcp-go v0.56.0

// github.com/modelcontextprotocol/go-sdk is present only in the transitive graph.
`,
			wantLegacy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLegacy, gotOfficial := directMCPRequirements([]byte(tt.goMod))
			if gotLegacy != tt.wantLegacy || gotOfficial != tt.wantOfficial {
				t.Fatalf("directMCPRequirements() = (%t, %t), want (%t, %t)", gotLegacy, gotOfficial, tt.wantLegacy, tt.wantOfficial)
			}
		})
	}
}

func directMCPRequirements(goMod []byte) (hasLegacy, hasOfficial bool) {
	const (
		legacyModule   = "github.com/mark3labs/mcp-go"
		officialModule = "github.com/modelcontextprotocol/go-sdk"
	)

	inRequireBlock := false
	scanner := bufio.NewScanner(bytes.NewReader(goMod))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !inRequireBlock {
			if line == "require (" {
				inRequireBlock = true
				continue
			}
			if !strings.HasPrefix(line, "require ") {
				continue
			}
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		} else if line == ")" {
			inRequireBlock = false
			continue
		}

		declaration, comment, _ := strings.Cut(line, "//")
		if strings.Contains(comment, "indirect") {
			continue
		}
		fields := strings.Fields(declaration)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case legacyModule:
			hasLegacy = true
		case officialModule:
			hasOfficial = true
		}
	}
	return hasLegacy, hasOfficial
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON %q: %v", data, err)
	}
	return value
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
func stringValue(value any) string { text, _ := value.(string); return text }
