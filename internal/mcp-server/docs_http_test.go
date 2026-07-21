package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestDocsToolsRequireAuth(t *testing.T) {
	handler, _ := newDocsHTTPHandler(t)

	resp := serveJSONRPC(t, handler, "", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"signoz_search_docs","arguments":{"query":"docker","limit":1}}}`)
	require.Equal(t, http.StatusUnauthorized, resp.Code)
	require.Contains(t, resp.Body.String(), "Authorization or SIGNOZ-API-KEY header required")
}

func TestReadinessAndHealthEndpointsTrackDocsIndex(t *testing.T) {
	t.Run("ready when docs index is ready", func(t *testing.T) {
		handler, _ := newDocsHTTPHandler(t)
		for _, path := range []string{"/readyz", "/healthz"} {
			t.Run(path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				require.Equal(t, http.StatusOK, rr.Code)
				require.Equal(t, "ok", rr.Body.String())
			})
		}
	})

	t.Run("not ready while docs index is placeholder", func(t *testing.T) {
		logger := logpkg.New("error")
		cfg := &config.Config{
			TransportMode:   "http",
			Port:            "0",
			ClientCacheSize: 8,
			ClientCacheTTL:  time.Minute,
		}
		h := tools.NewHandler(logger, cfg)
		ctx, cancel := context.WithCancel(context.Background())
		reg, err := docsindex.NewPlaceholderRegistry(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			cancel()
			reg.Close(context.Background())
		})
		h.SetDocsIndex(reg)
		m := NewMCPServer(logger, h, cfg, nil, nil)
		s := server.NewMCPServer("SigNozMCP", version.Version, server.WithToolCapabilities(false), server.WithRecovery())
		h.RegisterDocsHandlers(s)

		handler := m.buildHTTP(s).Handler
		for _, path := range []string{"/readyz", "/healthz"} {
			t.Run(path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				require.Equal(t, http.StatusServiceUnavailable, rr.Code)
				require.Contains(t, rr.Body.String(), "docs index not ready")
			})
		}
	})
}

func TestLivenessEndpointDoesNotRequireReadiness(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
		ClientCacheSize: 8,
		ClientCacheTTL:  time.Minute,
	}
	h := tools.NewHandler(logger, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := docsindex.NewPlaceholderRegistry(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		reg.Close(context.Background())
	})
	h.SetDocsIndex(reg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	s := server.NewMCPServer("SigNozMCP", version.Version, server.WithToolCapabilities(false), server.WithRecovery())
	h.RegisterDocsHandlers(s)
	handler := m.buildHTTP(s).Handler

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok", rr.Body.String())
}

func TestBuildHTTPListenAddress(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "all interfaces by default", want: ":18080"},
		{name: "configured loopback host", host: "127.0.0.1", want: "127.0.0.1:18080"},
		{name: "configured IPv6 host", host: "::1", want: "[::1]:18080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &MCPServer{
				logger: logpkg.New("error"),
				config: &config.Config{Host: tt.host, Port: "18080"},
			}
			s := server.NewMCPServer("SigNozMCP", version.Version)

			require.Equal(t, tt.want, m.buildHTTP(s).Addr)
		})
	}
}

func newDocsHTTPHandler(t *testing.T) (http.Handler, *MCPServer) {
	t.Helper()
	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
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
	s := server.NewMCPServer("SigNozMCP", version.Version, server.WithToolCapabilities(false), server.WithRecovery())
	h.RegisterDocsHandlers(s)
	return m.buildHTTP(s).Handler, m
}

func serveJSONRPC(t *testing.T, handler http.Handler, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set(server.HeaderKeySessionID, sessionID)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func docsSnapshot() docsindex.CorpusSnapshot {
	now := time.Now().UTC()
	docker := "# Install SigNoz Using Docker\n\nDocker Compose installation.\n"
	logs := "# Send logs to SigNoz\n\nDocker logs and OpenTelemetry logs.\n"
	pages := []docsindex.PageRecord{
		docsPage("https://signoz.io/docs/install/docker/", "Install SigNoz Using Docker", "install", "Install > Docker", docker, now),
		docsPage("https://signoz.io/docs/logs-management/send-logs-to-signoz/", "Send logs to SigNoz", "logs-management", "Logs Management > Send Logs", logs, now),
	}
	sitemap := "- [Install SigNoz Using Docker](https://signoz.io/docs/install/docker/)\n- [Send logs to SigNoz](https://signoz.io/docs/logs-management/send-logs-to-signoz/)\n"
	return docsindex.CorpusSnapshot{
		SchemaVersion: docsindex.CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    sitemap,
		SitemapHash:   docsindex.SitemapHash(sitemap),
		Pages:         pages,
	}
}

// TestStatelessTransportIssuesNoSessionID is the transport-level regression for
// WithStateLess(true): a POST initialize must not return an Mcp-Session-Id, and a
// follow-up request carrying no session id must still be served (not rejected as a
// missing/invalid session). Auth is satisfied via env credentials on cfg so the
// requests reach the MCP layer.
func TestStatelessTransportIssuesNoSessionID(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode:   "http",
		Port:            "0",
		URL:             "https://example.signoz.cloud",
		APIKey:          "test-key",
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
	s := server.NewMCPServer("SigNozMCP", version.Version, server.WithToolCapabilities(false), server.WithRecovery())
	h.RegisterDocsHandlers(s)
	handler := m.buildHTTP(s).Handler

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// POST initialize — stateless server must NOT issue a session id.
	initRR := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"1"}}}`)
	require.Equal(t, http.StatusOK, initRR.Code, "initialize body: %s", initRR.Body.String())
	require.Empty(t, initRR.Header().Get(server.HeaderKeySessionID), "stateless server must not issue an Mcp-Session-Id")

	// POST a follow-up request with NO session id — must be served, not rejected
	// as a missing/invalid/terminated session (HTTP 400/404).
	listRR := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	require.Equal(t, http.StatusOK, listRR.Code, "tools/list without a session id should succeed; body: %s", listRR.Body.String())
}

func docsPage(url, title, section, breadcrumb, body string, fetchedAt time.Time) docsindex.PageRecord {
	rawHeadings, _ := json.Marshal(docsindex.ExtractHeadings(body))
	return docsindex.PageRecord{
		URL:               url,
		Title:             title,
		SectionSlug:       section,
		SectionBreadcrumb: breadcrumb,
		HeadingsJSON:      string(rawHeadings),
		BodyMarkdown:      body,
		FetchedAt:         fetchedAt,
	}
}
