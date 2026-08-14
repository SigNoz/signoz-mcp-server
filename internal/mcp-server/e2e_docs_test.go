package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestE2EDocsAgentFlow drives the SigNoz MCP docs feature with a real
// official-SDK Streamable HTTP client against an in-process httptest.Server.
// Unlike the ServeHTTP-level auth/readiness tests, this exercises the full
// client round-trip: Initialize handshake →
// ListTools discovery → CallTool for both docs tools → ReadResource for
// the sitemap → structured error code on an out-of-scope URL. It is the
// closest thing to a "remote agent calling our server" without actually
// spinning up a second process.
func TestE2EDocsAgentFlow(t *testing.T) {
	handler, _ := newDocsHTTPHandler(t)
	testSrv := httptest.NewServer(handler)
	t.Cleanup(testSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("SIGNOZ-API-KEY", "test-key")
		req.Header.Set("X-SigNoz-URL", "https://example.signoz.cloud")
		return http.DefaultTransport.RoundTrip(req)
	})}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: testSrv.URL + "/mcp", HTTPClient: httpClient}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	initResult := session.InitializeResult()
	require.NoError(t, err)
	require.NotEmpty(t, initResult.ServerInfo.Name)

	// 1. Discovery via tools/list — both docs tools must appear to the
	//    authenticated client (plan §Tools).
	toolsResp, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	toolNames := make(map[string]struct{}, len(toolsResp.Tools))
	for _, tool := range toolsResp.Tools {
		toolNames[tool.Name] = struct{}{}
	}
	require.Contains(t, toolNames, "signoz_search_docs")
	require.Contains(t, toolNames, "signoz_fetch_doc")

	// 2. signoz_search_docs happy path — the snapshot seeded by
	//    docsSnapshot() contains a "docker" page, so a body-term
	//    query must return a non-empty result set.
	searchArgs, _ := json.Marshal(map[string]any{
		"searchText": "docker",
		"limit":      5,
	})
	var searchArguments map[string]any
	require.NoError(t, json.Unmarshal(searchArgs, &searchArguments))
	searchRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "signoz_search_docs", Arguments: searchArguments})
	require.NoError(t, err)
	require.False(t, searchRes.IsError, "search should succeed for a seeded term: %s", firstTextContent(t, searchRes.Content))
	searchJSON := firstTextContent(t, searchRes.Content)
	require.Contains(t, strings.ToLower(searchJSON), "docker")

	// 3. signoz_fetch_doc happy path — plain URL with no heading returns
	//    the full markdown + populated available_headings list and
	//    truncation_reason "none".
	fetchArgs, _ := json.Marshal(map[string]any{
		"url": "https://signoz.io/docs/install/docker/",
	})
	var fetchArguments map[string]any
	require.NoError(t, json.Unmarshal(fetchArgs, &fetchArguments))
	fetchRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "signoz_fetch_doc", Arguments: fetchArguments})
	require.NoError(t, err)
	require.False(t, fetchRes.IsError)
	fetchJSON := firstTextContent(t, fetchRes.Content)
	var fetchPayload struct {
		URL               string           `json:"url"`
		Content           string           `json:"content"`
		TruncationReason  string           `json:"truncation_reason"`
		AvailableHeadings []map[string]any `json:"available_headings"`
	}
	require.NoError(t, json.Unmarshal([]byte(fetchJSON), &fetchPayload))
	require.Equal(t, "https://signoz.io/docs/install/docker/", fetchPayload.URL)
	require.Contains(t, fetchPayload.Content, "Docker")
	require.Equal(t, "none", fetchPayload.TruncationReason)

	// 4. signoz_fetch_doc out-of-scope URL — plan error contract requires
	//    CallToolResult{isError:true, structuredContent.code=OUT_OF_SCOPE_URL},
	//    NOT a JSON-RPC protocol error.
	outOfScopeArgs, _ := json.Marshal(map[string]any{"url": "https://evil.example.com/docs/x/"})
	var outOfScopeArguments map[string]any
	require.NoError(t, json.Unmarshal(outOfScopeArgs, &outOfScopeArguments))
	outOfScopeRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "signoz_fetch_doc", Arguments: outOfScopeArguments})
	require.NoError(t, err, "out-of-scope URL must surface as a tool-result error, not a JSON-RPC protocol error")
	require.True(t, outOfScopeRes.IsError)
	// StructuredContent is a generic `any`; go through a JSON
	// round-trip so we compare against the wire shape the agent would see.
	structuredJSON, err := json.Marshal(outOfScopeRes.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(structuredJSON), docsindex.CodeOutOfScopeURL)

	// 5. sitemap MCP resource — plan §MCP resource: the resource is a
	//    pass-through of the indexed sitemap (not a live fetch), so the
	//    body must contain the seed pages from docsSnapshot().
	sitemapRes, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: docsindex.DocsSitemapURI})
	require.NoError(t, err)
	require.NotEmpty(t, sitemapRes.Contents)
	textContent := sitemapRes.Contents[0]
	require.Contains(t, textContent.Text, "Install SigNoz Using Docker")
	require.Contains(t, textContent.Text, "Send logs to SigNoz")
}

func TestE2EAuthFailureTelemetry(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	prevTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracerProvider)
		require.NoError(t, traceProvider.Shutdown(context.Background()))
	})

	var logBuf lockedBuffer
	handler := newDocsHTTPHandlerWithLogger(t, newBufferedLogger(&logBuf, slog.LevelDebug))
	testSrv := httptest.NewServer(handler)
	t.Cleanup(testSrv.Close)

	req, err := http.NewRequest(http.MethodPost, testSrv.URL+"/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-code/2.1.133 (cli)")
	req.Header.Set("X-Forwarded-For", "198.51.100.9, 10.0.0.2")
	req.Header.Set("Mcp-Session-Id", "mcp-session-e2e")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	records := parseJSONLogLines(t, &logBuf)
	require.NotEmpty(t, records)
	rec := records[len(records)-1]
	require.Equal(t, "No API key found in headers or environment", rec["msg"])
	require.Equal(t, authFailureMissingCredential, rec["mcp.auth.failure_reason"])
	require.Equal(t, authModeNone, rec["mcp.auth.mode"])
	require.Equal(t, "198.51.100.9", rec["client.address"])
	require.Equal(t, "claude-code/2.1.133 (cli)", rec["user_agent.original"])
	_, hasSessionLogAttr := rec["mcp.session.id"]
	require.False(t, hasSessionLogAttr, "incoming session header must not be logged")

	var httpAttrs []attribute.KeyValue
	for _, span := range traceExporter.GetSpans() {
		if span.Name == "HTTP POST /mcp" {
			httpAttrs = span.Attributes
			break
		}
	}
	require.NotNil(t, httpAttrs, "expected otelhttp root span")
	for key, want := range map[attribute.Key]string{
		"mcp.auth.failure_reason": authFailureMissingCredential,
		"mcp.auth.mode":           authModeNone,
		"client.address":          "198.51.100.9",
		"user_agent.original":     "claude-code/2.1.133 (cli)",
	} {
		got, ok := spanAttrValue(httpAttrs, key)
		require.True(t, ok, "missing span attr %s", key)
		require.Equal(t, want, got.AsString(), "span attr %s", key)
	}
	_, hasSessionSpanAttr := spanAttrValue(httpAttrs, attribute.Key("mcp.session.id"))
	require.False(t, hasSessionSpanAttr, "incoming session header must not be attached to spans")
}

func firstTextContent(t *testing.T, content []mcp.Content) string {
	t.Helper()
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("no TextContent in tool result")
	return ""
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newDocsHTTPHandlerWithLogger(t *testing.T, logger *slog.Logger) http.Handler {
	t.Helper()
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
		OAuthEnabled:    true,
		OAuthIssuerURL:  "https://mcp.example.com",
		ClientCacheSize: 8,
		ClientCacheTTL:  time.Minute,
	}
	h := tools.NewHandler(logger, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := docsindex.NewIndexRegistry(ctx, docsSnapshot())
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		reg.Close(context.Background())
	})
	h.SetDocsIndex(reg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	s := m.newSDKServer()
	h.RegisterDocsHandlers(s)
	return m.buildHTTP(s).Handler
}
