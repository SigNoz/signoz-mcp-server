package mcp_server

import (
	"bytes"
	"encoding/json"
	"flag"
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
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// updateWireCatalogGoldens regenerates testdata/wire-catalog from the live
// server: go test ./internal/mcp-server -run TestGuardrail_WireCatalogGoldens -update
var updateWireCatalogGoldens = flag.Bool("update", false, "rewrite the testdata/wire-catalog goldens from the current server responses")

const wireCatalogGoldenDir = "testdata/wire-catalog"

// wireCatalogProtocolVersion is the protocol revision the goldens are captured
// at. It must stay supported across SDK changes: clients pinned to it are in
// the field, so a golden diff here is a client-visible break.
const wireCatalogProtocolVersion = "2025-11-25"

// wireCatalogCapture is one golden file: the exact JSON-RPC request sent over
// the production HTTP transport plus the normalized response it produced.
type wireCatalogCapture struct {
	Method          string          `json:"method"`
	ProtocolVersion string          `json:"protocolVersion"`
	Request         json.RawMessage `json:"request"`
	HTTPStatus      int             `json:"httpStatus"`
	Framing         string          `json:"framing"`
	Response        any             `json:"response"`
}

// TestGuardrail_WireCatalogGoldens pins the transport-level JSON-RPC responses
// for the discovery surface every MCP client sees before it can call anything.
//
// It deliberately owns no SDK types: the catalog is driven by POSTing
// hand-written JSON-RPC bodies at the production HTTP handler and capturing the
// raw response bytes. That keeps the file valid across an MCP SDK replacement,
// which is the only way it can prove the replacement preserved the contract.
func TestGuardrail_WireCatalogGoldens(t *testing.T) {
	handler := newWireCatalogHandler(t)

	cases := []struct {
		golden  string
		method  string
		request string
	}{
		{
			golden:  "initialize.json",
			method:  "initialize",
			request: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + wireCatalogProtocolVersion + `","capabilities":{},"clientInfo":{"name":"wire-catalog-golden","version":"1"}}}`,
		},
		{
			golden:  "tools-list.json",
			method:  "tools/list",
			request: `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		},
		{
			golden:  "resources-list.json",
			method:  "resources/list",
			request: `{"jsonrpc":"2.0","id":3,"method":"resources/list","params":{}}`,
		},
		{
			golden:  "resources-templates-list.json",
			method:  "resources/templates/list",
			request: `{"jsonrpc":"2.0","id":4,"method":"resources/templates/list","params":{}}`,
		},
		{
			golden:  "prompts-list.json",
			method:  "prompts/list",
			request: `{"jsonrpc":"2.0","id":5,"method":"prompts/list","params":{}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			status, framing, payload := postWireCatalogRequest(t, handler, tc.request)
			if status != http.StatusOK {
				t.Fatalf("%s returned HTTP %d: %s", tc.method, status, payload)
			}

			response := decodeWireCatalogResponse(t, tc.method, payload)
			capture := wireCatalogCapture{
				Method:          tc.method,
				ProtocolVersion: wireCatalogProtocolVersion,
				Request:         json.RawMessage(tc.request),
				HTTPStatus:      status,
				Framing:         framing,
				Response:        normalizeWireCatalogNode(response, ""),
			}
			encoded, err := json.MarshalIndent(capture, "", "  ")
			if err != nil {
				t.Fatalf("encode %s capture: %v", tc.method, err)
			}
			encoded = append(encoded, '\n')

			assertWireCatalogGolden(t, filepath.Join(wireCatalogGoldenDir, tc.golden), encoded)
		})
	}
}

// newWireCatalogHandler builds the production HTTP handler with the full
// advertised catalog registered. Static config credentials satisfy
// authMiddleware so requests reach the MCP layer without per-request headers.
//
// Nothing here names an SDK type: the SDK server is created and registered
// through the package's own seams (newSDKServer, registerHandlers, buildHTTP),
// so replacing the SDK cannot invalidate this test.
func newWireCatalogHandler(t *testing.T) http.Handler {
	t.Helper()

	// Resource sizes advertised in resources/list are derived from the exported
	// ClickHouse schemas, so initialize them before registration to keep the
	// goldens independent of test execution order.
	dashboard.InitClickhouseSchema()

	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
		URL:             "https://example.signoz.cloud",
		APIKey:          "test-key",
		ClientCacheSize: 8,
		ClientCacheTTL:  time.Minute,
	}
	m := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, nil, nil)
	s := m.newSDKServer()
	m.registerHandlers(s)
	return m.buildHTTP(s).Handler
}

// postWireCatalogRequest POSTs a JSON-RPC body at /mcp and returns the status,
// the response framing ("json" or "sse"), and the JSON-RPC payload bytes.
func postWireCatalogRequest(t *testing.T, handler http.Handler, body string) (int, string, []byte) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", wireCatalogProtocolVersion)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	raw := rr.Body.Bytes()
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") {
		return rr.Code, "json", raw
	}

	var payload []byte
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = append(payload, strings.TrimSpace(data)...)
	}
	if len(payload) == 0 {
		t.Fatalf("event-stream response carried no data frame: %s", raw)
	}
	return rr.Code, "sse", payload
}

// decodeWireCatalogResponse decodes a JSON-RPC response and rejects anything
// that is not a successful result, so -update can never freeze a broken server.
func decodeWireCatalogResponse(t *testing.T, method string, payload []byte) map[string]any {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode %s response: %v (payload: %s)", method, err, payload)
	}
	if rpcError, ok := response["error"]; ok {
		t.Fatalf("%s returned a JSON-RPC error: %v", method, rpcError)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || len(result) == 0 {
		t.Fatalf("%s returned no result object: %s", method, payload)
	}
	return response
}

// normalizeWireCatalogNode removes the only nondeterminism in the discovery
// surface: the build-stamped server version, and catalog ordering that the SDK
// is free to change without changing the contract.
func normalizeWireCatalogNode(node any, path string) any {
	switch value := node.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(value))
		for key, child := range value {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if childPath == "result.serverInfo.version" {
				normalized[key] = "<version>"
				continue
			}
			normalized[key] = normalizeWireCatalogNode(child, childPath)
		}
		return normalized
	case []any:
		normalized := make([]any, 0, len(value))
		for _, child := range value {
			normalized = append(normalized, normalizeWireCatalogNode(child, path))
		}
		sortWireCatalogEntries(normalized)
		return normalized
	default:
		return node
	}
}

// sortWireCatalogEntries sorts an array of catalog entries by their identity
// key. Arrays whose elements carry no identity key (schema composition arrays,
// enum values) keep their declared order.
func sortWireCatalogEntries(entries []any) {
	keys := make([]string, len(entries))
	for i, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			return
		}
		for _, candidate := range []string{"name", "uri", "uriTemplate"} {
			identity, ok := object[candidate].(string)
			if !ok {
				continue
			}
			keys[i] = candidate + "\x00" + identity
			break
		}
		if keys[i] == "" {
			return
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return keys[i] < keys[j] })
}

func assertWireCatalogGolden(t *testing.T, path string, actual []byte) {
	t.Helper()

	if *updateWireCatalogGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden directory: %v", err)
		}
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden %s (%d bytes)", path, len(actual))
		return
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v; regenerate with -update", path, err)
	}
	if bytes.Equal(expected, actual) {
		return
	}
	t.Errorf("wire catalog golden %s is stale.\n%s\nIf the change is intended, review it and regenerate with:\n  go test ./internal/mcp-server -run TestGuardrail_WireCatalogGoldens -update",
		path, firstGoldenDifference(expected, actual))
}

// firstGoldenDifference reports the first diverging line so a stale multi-line
// golden fails with a reviewable message instead of a full dump.
func firstGoldenDifference(expected, actual []byte) string {
	expectedLines := strings.Split(string(expected), "\n")
	actualLines := strings.Split(string(actual), "\n")
	for i := 0; i < len(expectedLines) || i < len(actualLines); i++ {
		var expectedLine, actualLine string
		if i < len(expectedLines) {
			expectedLine = expectedLines[i]
		}
		if i < len(actualLines) {
			actualLine = actualLines[i]
		}
		if expectedLine == actualLine {
			continue
		}
		return fmt.Sprintf("first difference at line %d:\n golden: %s\nserver: %s", i+1, truncateGoldenLine(expectedLine), truncateGoldenLine(actualLine))
	}
	return fmt.Sprintf("golden is %d bytes, server response is %d bytes", len(expected), len(actual))
}

func truncateGoldenLine(line string) string {
	const limit = 240
	if len(line) <= limit {
		return line
	}
	return line[:limit] + "… (truncated)"
}
