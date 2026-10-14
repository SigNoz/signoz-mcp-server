package mcp_server

import (
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
	"github.com/stretchr/testify/require"
)

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
		s := m.newSDKServer()
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
	s := m.newSDKServer()
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
			s := m.newSDKServer()

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
	s := m.newSDKServer()
	h.RegisterDocsHandlers(s)
	return m.buildHTTP(s).Handler, m
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
