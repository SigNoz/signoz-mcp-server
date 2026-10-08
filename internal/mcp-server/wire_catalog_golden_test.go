package mcp_server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	wireCatalogGoldenDir       = "testdata/wire-catalog"
	wireCatalogProtocolVersion = "2025-11-25"
	wireSentinelVersion        = "<version>"
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

type wireOracle struct {
	t        *testing.T
	handler  http.Handler
	upstream *httptest.Server
	docs     *docsindex.IndexRegistry
}

func TestGuardrail_WireCatalogGoldens(t *testing.T) {
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
		sort.Slice(inventory, func(i, j int) bool { return inventory[i].Identity < inventory[j].Identity })
		assertWireGolden(t, "resources-content-inventory.json", inventory)
		sitemap := o.capture("resources/read", `{"uri":"signoz://docs/sitemap"}`)
		assertWireGolden(t, "resource-sitemap-literal.json", sitemap)
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
			assertWireGolden(t, tc.file, o.capture(tc.method, tc.params))
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
			s := official.NewServer(&official.Implementation{Name: "wire-oracle", Version: "0"}, &official.ServerOptions{Capabilities: &official.ServerCapabilities{Tools: &official.ToolCapabilities{}}})
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

			handler := official.NewStreamableHTTPHandler(func(*http.Request) *official.Server { return s }, &official.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + tt.params + `}`
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", wireCatalogProtocolVersion)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
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
	o.docs = newWireDocsRegistry(t)
	h.SetDocsIndex(o.docs)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	s := m.newSDKServer()
	m.registerHandlers(s)
	o.handler = m.buildHTTP(s).Handler
	return o
}

func newWireDocsRegistry(t *testing.T) *docsindex.IndexRegistry {
	t.Helper()
	registry, err := docsindex.NewIndexRegistry(context.Background(), wireDocsSnapshot())
	if err != nil {
		t.Fatalf("create deterministic docs index: %v", err)
	}
	return registry
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
	switch r.URL.Path {
	case "/api/v2/dashboards":
		_, _ = w.Write([]byte(`{"dashboards":[],"tags":[],"total":0}`))
	case "/api/v2/notification_channels":
		_, _ = w.Write([]byte(`{"status":"success","data":{"channels":[{"id":"019947a7-f200-7000-8000-000000000001","name":"on-call","displayName":"On call","kind":"email","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}],"total":1}}`))
	default:
		http.Error(w, `{"error":"unexpected wire-oracle upstream request"}`, http.StatusNotFound)
	}
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

// assertWireGolden compares a capture against its recorded fixture. Running
// with UPDATE_WIRE_GOLDENS=1 rewrites the fixture instead; the git diff of the
// fixture is the review artifact, and only entries an intended change touches
// may be committed.
func assertWireGolden(t *testing.T, name string, value any) {
	t.Helper()
	actual, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	path := filepath.Join(wireCatalogGoldenDir, name)
	if os.Getenv("UPDATE_WIRE_GOLDENS") == "1" {
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("update wire oracle %s: %v", path, err)
		}
		t.Logf("updated wire oracle %s; review its git diff before committing", path)
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wire oracle %s: %v (UPDATE_WIRE_GOLDENS=1 records it)", path, err)
	}
	if bytes.Equal(expected, actual) {
		return
	}
	gotPath := writeWireActual(t, name, actual)
	t.Fatalf("wire oracle %s changed: %s\nfull server output: %s\nreview with: diff %s %s\nif every difference is intended, rerun with UPDATE_WIRE_GOLDENS=1 and review the fixture's git diff",
		path, firstWireDifference(expected, actual), gotPath, path, gotPath)
}

// writeWireActual persists the server's full output so a mismatch is diffable
// even when the first differing lines are long or identical-looking.
func writeWireActual(t *testing.T, name string, actual []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", "wire-oracle-"+strings.ReplaceAll(name, string(os.PathSeparator), "_")+".*")
	if err != nil {
		t.Fatalf("write server output: %v", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close of a temp diff artifact
	if _, err := f.Write(actual); err != nil {
		t.Fatalf("write server output: %v", err)
	}
	return f.Name()
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
			return fmt.Sprintf("line %d:\n golden: %s\n server: %s", i+1, want, got)
		}
	}
	return fmt.Sprintf("golden=%d bytes server=%d bytes", len(expected), len(actual))
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
