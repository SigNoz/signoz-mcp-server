package mcp_server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProductionHTTPProtocolLifecycleMatrix(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	t.Run("legacy initialize and initialized notification", func(t *testing.T) {
		initialize := protocolRequestJSON(t, 101, "initialize", map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy-test", "version": "1"},
		})
		response := protocolPOST(t, oracle.handler, initialize, nil)
		if response.status != http.StatusOK {
			t.Fatalf("initialize status = %d, want 200; body=%s", response.status, response.body)
		}
		result := response.wire["result"].(map[string]any)
		if result["protocolVersion"] != "2025-11-25" {
			t.Fatalf("negotiated protocol = %#v, want 2025-11-25", result["protocolVersion"])
		}
		capabilities := result["capabilities"].(map[string]any)
		if _, ok := capabilities["logging"]; ok {
			t.Fatal("legacy initialize advertised capabilities.logging")
		}
		if got := response.header.Get("Mcp-Session-Id"); got != "" {
			t.Fatalf("Mcp-Session-Id = %q, want absent", got)
		}

		notification := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
		initialized := protocolPOST(t, oracle.handler, notification, http.Header{"Mcp-Protocol-Version": {"2025-11-25"}})
		if initialized.status != http.StatusAccepted || len(initialized.body) != 0 {
			t.Fatalf("initialized notification = status %d body %q, want 202 with empty body", initialized.status, initialized.body)
		}
	})

	t.Run("modern discover", func(t *testing.T) {
		response := modernProductionCall(t, oracle.handler, 102, "server/discover", "", nil)
		if response.status != http.StatusOK {
			t.Fatalf("discover status = %d, want 200; body=%s", response.status, response.body)
		}
		result := response.wire["result"].(map[string]any)
		gotVersions := stringsFromAny(t, result["supportedVersions"])
		wantVersions := []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
		if !reflect.DeepEqual(gotVersions, wantVersions) {
			t.Fatalf("supportedVersions = %v, want %v", gotVersions, wantVersions)
		}
		assertModernResultEnvelope(t, response, true)
		if capabilities, ok := result["capabilities"].(map[string]any); !ok || capabilities["tools"] == nil || capabilities["resources"] == nil || capabilities["prompts"] == nil {
			t.Fatalf("discover capabilities = %#v, want tools/resources/prompts", result["capabilities"])
		} else if _, ok := capabilities["logging"]; ok {
			t.Fatal("modern discover advertised capabilities.logging")
		}
	})

	t.Run("modern direct calls require no initialize", func(t *testing.T) {
		tests := []struct {
			method    string
			name      string
			fields    map[string]any
			resultKey string
			wantCount int
		}{
			{method: "tools/list", resultKey: "tools", wantCount: 43},
			{method: "resources/list", resultKey: "resources", wantCount: 22},
			{method: "resources/templates/list", resultKey: "resourceTemplates", wantCount: 2},
			{method: "prompts/list", resultKey: "prompts", wantCount: 4},
			{method: "tools/call", name: "signoz_search_docs", fields: map[string]any{"name": "signoz_search_docs", "arguments": map[string]any{"searchText": "docker"}}, resultKey: "content", wantCount: 1},
			{method: "resources/read", name: "signoz://docs/sitemap", fields: map[string]any{"uri": "signoz://docs/sitemap"}, resultKey: "contents", wantCount: 1},
			{method: "prompts/get", name: "debug_service_errors", fields: map[string]any{"name": "debug_service_errors", "arguments": map[string]any{"service": "checkout", "timeRange": "2h"}}, resultKey: "messages", wantCount: 1},
		}
		for index, tt := range tests {
			t.Run(tt.method, func(t *testing.T) {
				response := modernProductionCall(t, oracle.handler, 110+index, tt.method, tt.name, tt.fields)
				if response.status != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%s", response.status, response.body)
				}
				assertModernResultEnvelope(t, response, false)
				result := response.wire["result"].(map[string]any)
				items, ok := result[tt.resultKey].([]any)
				if !ok || len(items) != tt.wantCount {
					t.Fatalf("result.%s = %#v, want %d items", tt.resultKey, result[tt.resultKey], tt.wantCount)
				}
			})
		}
	})

	t.Run("modern removed logging method", func(t *testing.T) {
		response := modernProductionCall(t, oracle.handler, 120, "logging/setLevel", "", map[string]any{"level": "debug"})
		requireProtocolError(t, response, http.StatusNotFound, 120, -32601, "method not found")
	})
}

func modernProductionCall(t *testing.T, handler http.Handler, id int, method, name string, fields map[string]any) protocolHTTPResponse {
	t.Helper()
	body := protocolRequestJSON(t, id, method, modernParams(nil, fields))
	return protocolPOST(t, handler, body, modernHeaders(method, name))
}

func assertModernResultEnvelope(t *testing.T, response protocolHTTPResponse, discover bool) {
	t.Helper()
	if got := response.header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("Mcp-Session-Id = %q, want absent", got)
	}
	result, ok := response.wire["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want object; body=%s", response.wire["result"], response.body)
	}
	if result["resultType"] != "complete" {
		t.Fatalf("resultType = %#v, want complete", result["resultType"])
	}
	meta, ok := result["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("result._meta = %#v, want object", result["_meta"])
	}
	serverInfo, ok := meta["io.modelcontextprotocol/serverInfo"].(map[string]any)
	if !ok || serverInfo["name"] != "SigNozMCP" {
		t.Fatalf("serverInfo = %#v, want SigNozMCP", meta["io.modelcontextprotocol/serverInfo"])
	}
	if discover {
		if result["ttlMs"] != float64(0) || result["cacheScope"] != "public" {
			t.Fatalf("discover cache envelope = ttlMs:%#v cacheScope:%#v", result["ttlMs"], result["cacheScope"])
		}
	}
}

func stringsFromAny(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want array", value)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		var ok bool
		out[i], ok = item.(string)
		if !ok {
			t.Fatalf("value[%d] = %#v, want string", i, item)
		}
	}
	return out
}

func TestProductionHTTPModernCallerMetadataIsRequestScoped(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 8, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20}
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	server := m.newSDKServer()
	m.registerHandlers(server)

	type caller struct {
		name     string
		sampling bool
	}
	var mu sync.Mutex
	var callers []caller
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if request, ok := request.(*mcp.ListToolsRequest); ok {
				entry := caller{}
				if info := request.ClientInfo(); info != nil {
					entry.name = info.Name
				}
				if capabilities := request.ClientCapabilities(); capabilities != nil {
					entry.sampling = capabilities.Sampling != nil
				}
				mu.Lock()
				callers = append(callers, entry)
				mu.Unlock()
			}
			return next(ctx, method, request)
		}
	})
	handler := m.buildHTTP(server).Handler

	metas := []map[string]any{
		{
			"io.modelcontextprotocol/protocolVersion":    modernProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{"sampling": map[string]any{}},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "caller-a", "version": "1"},
		},
		{
			"io.modelcontextprotocol/protocolVersion":    modernProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "caller-b", "version": "2"},
		},
	}
	for index, meta := range metas {
		body := protocolRequestJSON(t, 130+index, "tools/list", modernParams(meta, nil))
		response := protocolPOST(t, handler, body, modernHeaders("tools/list", ""))
		if response.status != http.StatusOK {
			t.Fatalf("caller %d status = %d; body=%s", index, response.status, response.body)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	want := []caller{{name: "caller-a", sampling: true}, {name: "caller-b", sampling: false}}
	if !reflect.DeepEqual(callers, want) {
		t.Fatalf("request-scoped callers = %#v, want %#v", callers, want)
	}
}

type rawIOProtocolSession struct {
	t       *testing.T
	input   *io.PipeWriter
	scanner *bufio.Scanner
}

func newRawIOProtocolSession(t *testing.T, server *mcp.Server) *rawIOProtocolSession {
	t.Helper()
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	serverSession, err := server.Connect(context.Background(), &mcp.IOTransport{
		Reader: clientToServerReader,
		Writer: serverToClientWriter,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientToServerWriter.Close()
		_ = serverToClientReader.Close()
		_ = serverSession.Close()
	})
	scanner := bufio.NewScanner(serverToClientReader)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	return &rawIOProtocolSession{t: t, input: clientToServerWriter, scanner: scanner}
}

func (session *rawIOProtocolSession) send(message string, expectResponse bool) map[string]any {
	session.t.Helper()
	if _, err := io.WriteString(session.input, message+"\n"); err != nil {
		session.t.Fatalf("write IOTransport request: %v", err)
	}
	if !expectResponse {
		return nil
	}
	if !session.scanner.Scan() {
		session.t.Fatalf("read IOTransport response: %v", session.scanner.Err())
	}
	var response map[string]any
	if err := json.Unmarshal(session.scanner.Bytes(), &response); err != nil {
		session.t.Fatalf("decode IOTransport response: %v\nbody: %s", err, session.scanner.Bytes())
	}
	return response
}

func TestProductionIOTransportProtocolLifecycleMatrix(t *testing.T) {
	assertCatalogsAndRepresentativeCalls := func(t *testing.T, session *rawIOProtocolSession, firstID int, params func(map[string]any) map[string]any) {
		t.Helper()
		tests := []struct {
			method    string
			fields    map[string]any
			resultKey string
			wantCount int
		}{
			{method: "tools/list", resultKey: "tools", wantCount: 43},
			{method: "resources/list", resultKey: "resources", wantCount: 22},
			{method: "resources/templates/list", resultKey: "resourceTemplates", wantCount: 2},
			{method: "prompts/list", resultKey: "prompts", wantCount: 4},
			{method: "tools/call", fields: map[string]any{"name": "signoz_search_docs", "arguments": map[string]any{"searchText": "docker"}}, resultKey: "content", wantCount: 1},
			{method: "resources/read", fields: map[string]any{"uri": "signoz://docs/sitemap"}, resultKey: "contents", wantCount: 1},
			{method: "prompts/get", fields: map[string]any{"name": "debug_service_errors", "arguments": map[string]any{"service": "checkout", "timeRange": "2h"}}, resultKey: "messages", wantCount: 1},
		}
		for index, tt := range tests {
			response := session.send(protocolRequestJSON(t, firstID+index, tt.method, params(tt.fields)), true)
			result, ok := response["result"].(map[string]any)
			items, itemsOK := result[tt.resultKey].([]any)
			if !ok || !itemsOK || len(items) != tt.wantCount {
				t.Fatalf("%s response = %#v, want result.%s with %d items", tt.method, response, tt.resultKey, tt.wantCount)
			}
		}
	}

	t.Run("legacy initialized lifecycle", func(t *testing.T) {
		docs := newWireDocsRegistry(t)
		t.Cleanup(func() { docs.Close(context.Background()) })
		session := newRawIOProtocolSession(t, buildTestServerWithDocs(t, docs))
		initialize := protocolRequestJSON(t, 201, "initialize", map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "legacy-io", "version": "1"},
		})
		initResponse := session.send(initialize, true)
		result, ok := initResponse["result"].(map[string]any)
		if !ok || result["protocolVersion"] != "2025-11-25" {
			t.Fatalf("initialize response = %#v, want negotiated legacy protocol", initResponse)
		}
		session.send(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, false)
		assertCatalogsAndRepresentativeCalls(t, session, 202, func(fields map[string]any) map[string]any { return fields })
	})

	t.Run("modern discover and direct calls", func(t *testing.T) {
		docs := newWireDocsRegistry(t)
		t.Cleanup(func() { docs.Close(context.Background()) })
		session := newRawIOProtocolSession(t, buildTestServerWithDocs(t, docs))
		discover := session.send(protocolRequestJSON(t, 211, "server/discover", modernParams(nil, nil)), true)
		discoverResult, ok := discover["result"].(map[string]any)
		if !ok {
			t.Fatalf("discover response = %#v, want result", discover)
		}
		wantVersions := []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
		if got := stringsFromAny(t, discoverResult["supportedVersions"]); !reflect.DeepEqual(got, wantVersions) {
			t.Fatalf("supportedVersions = %v, want %v", got, wantVersions)
		}

		assertCatalogsAndRepresentativeCalls(t, session, 212, func(fields map[string]any) map[string]any { return modernParams(nil, fields) })
	})
}

func TestProductionIOTransportMalformedFrameTerminatesConnection(t *testing.T) {
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	serverSession, err := buildTestServer(t).Connect(context.Background(), &mcp.IOTransport{
		Reader: clientToServerReader,
		Writer: serverToClientWriter,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientToServerWriter.Close()
		_ = serverToClientReader.Close()
		_ = serverSession.Close()
	})

	if _, err := io.WriteString(clientToServerWriter, "not-json\n"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serverSession.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("malformed IOTransport frame ended the connection without an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("malformed IOTransport frame did not terminate the connection")
	}
}
