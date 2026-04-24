package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/session"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// testSessionSigner constructs a deterministic signer so middleware
// tests can mint and verify tokens without relying on the real env var
// parsing. 32 bytes of 'k' is sufficient for HMAC-SHA256 and avoids
// accidental collisions with the per-pod ephemeral key that
// NewMCPServer would generate.
func testSessionSigner(t *testing.T) *session.Signer {
	t.Helper()
	key := bytes.Repeat([]byte{'k'}, 32)
	s, err := session.NewSigner(session.SignerConfig{Keys: [][]byte{key}})
	require.NoError(t, err)
	return s
}

func TestAuthOrPublicLifecycle(t *testing.T) {
	handler, m := newPublicDocsHTTPHandler(t)

	initResp := serveJSONRPC(t, handler, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+mcp.LATEST_PROTOCOL_VERSION+`","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	require.Equal(t, http.StatusOK, initResp.Code)
	sessionID := initResp.Header().Get(server.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)
	// The middleware rewrites the Mcp-Session-Id response header to a
	// signed token for public initialize — the v1. prefix lets later
	// GET/DELETE on any pod verify statelessly without a shared map.
	require.True(t, strings.HasPrefix(sessionID, session.TokenPrefix),
		"public initialize must return a signed session token; got %q", sessionID)
	_, verifyErr := m.sessionSigner.Verify(sessionID)
	require.NoError(t, verifyErr, "returned token must be verifiable by the same signer")

	initialized := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	require.Less(t, initialized.Code, http.StatusBadRequest)

	sseCtx, cancel := context.WithCancel(context.Background())
	getReq := httptest.NewRequestWithContext(sseCtx, http.MethodGet, "/mcp", nil)
	getReq.Header.Set(server.HeaderKeySessionID, sessionID)
	getResp := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(getResp, getReq)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	require.Less(t, getResp.Code, http.StatusBadRequest)

	callResp := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"signoz_search_docs","arguments":{"query":"docker","limit":1}}}`)
	require.Equal(t, http.StatusOK, callResp.Code)
	require.Contains(t, callResp.Body.String(), "Docker")

	t.Run("public list methods do not need credentials", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
		require.Equal(t, http.StatusOK, resp.Code)
	})
	t.Run("non public tools require credentials", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"signoz_list_dashboards","arguments":{}}}`)
		require.Equal(t, http.StatusUnauthorized, resp.Code)
	})
	t.Run("docs sitemap resource is public", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"`+docsindex.DocsSitemapURI+`"}}`)
		require.Equal(t, http.StatusOK, resp.Code)
		require.Contains(t, resp.Body.String(), "Install SigNoz Using Docker")
	})
	t.Run("non docs resource requires credentials", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"signoz://promql/instructions"}}`)
		require.Equal(t, http.StatusUnauthorized, resp.Code)
	})
	t.Run("mixed batch is not public", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `[{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}},{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"signoz_list_dashboards","arguments":{}}}]`)
		require.Equal(t, http.StatusUnauthorized, resp.Code)
	})
	t.Run("invalid json is rejected before auth", func(t *testing.T) {
		resp := serveJSONRPC(t, handler, sessionID, `{`)
		require.Equal(t, http.StatusBadRequest, resp.Code)
	})

	delReq := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	delReq.Header.Set(server.HeaderKeySessionID, sessionID)
	delResp := httptest.NewRecorder()
	handler.ServeHTTP(delResp, delReq)
	require.Less(t, delResp.Code, http.StatusBadRequest)
	// Stateless tokens: there's nothing on the server side to scrub on
	// DELETE beyond the rate-limit bucket (handled by mcp-go's
	// OnUnregisterSession hook). The token itself remains syntactically
	// valid until its embedded exp; replay protection is the client's
	// responsibility. Not asserting a "session gone" property here
	// because there's no longer one to check.
	_ = m
}

func TestReadinessEndpointTracksDocsIndex(t *testing.T) {
	t.Run("ready when docs index is ready", func(t *testing.T) {
		handler, _ := newPublicDocsHTTPHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "ok", rr.Body.String())
	})

	t.Run("not ready while docs index is placeholder", func(t *testing.T) {
		logger := logpkg.New("error")
		cfg := &config.Config{
			TransportMode:            "http",
			Port:                     "0",
			ClientCacheSize:          8,
			ClientCacheTTL:           time.Minute,
			PublicRateLimitBypassIPs: map[string]struct{}{},
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

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rr := httptest.NewRecorder()
		m.buildHTTP(s).Handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusServiceUnavailable, rr.Code)
		require.Contains(t, rr.Body.String(), "docs index not ready")
	})
}

func TestAuthOrPublicBodyRestoration(t *testing.T) {
	m := &MCPServer{
		config:        &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		publicLimiter: newPublicDocsRateLimiter(&config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}}),
		sessionSigner: testSessionSigner(t),
	}
	var received []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = body
		w.WriteHeader(http.StatusOK)
	})
	wrapped := m.authOrPublicMiddleware(next)

	cases := []struct {
		name string
		body string
	}{
		{"public init", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`},
		{"non-public tool falls through untouched", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"signoz_list_dashboards","arguments":{}}}`},
		{"batch falls through untouched", `[{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}},{"jsonrpc":"2.0","id":4,"method":"ping"}]`},
		{"public docs resource read", `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"` + docsindex.DocsSitemapURI + `"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			received = nil
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)
			require.Equal(t, tc.body, string(received), "downstream handler must see the original body bytes byte-for-byte")
		})
	}
}

func TestAuthOrPublicOversizeBodyFallsToAuth(t *testing.T) {
	m := &MCPServer{
		config:        &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		publicLimiter: newPublicDocsRateLimiter(&config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}}),
		sessionSigner: testSessionSigner(t),
	}
	var receivedLen int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedLen = len(body)
		// Mimic authMiddleware: reject without creds.
		w.WriteHeader(http.StatusUnauthorized)
	})
	wrapped := m.authOrPublicMiddleware(next)

	// Construct a body larger than the 64 KB peek limit. Even though its JSON
	// shape is a "public" method, it must fall through to the auth path and
	// the downstream handler must see the full original bytes.
	padding := strings.Repeat("A", 70*1024)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` + padding + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Equal(t, len(body), receivedLen, "oversize bodies must still be byte-for-byte delivered to the next handler")
}

func TestAuthOrPublicNestedParamsNameIsRejected(t *testing.T) {
	handler, _ := newPublicDocsHTTPHandler(t)
	// params.name is a nested object instead of a string — our probe can't
	// decide tools/call public-ness, so the request must be rejected with
	// HTTP 400 before it ever reaches any tool handler.
	resp := serveJSONRPC(t, handler, "", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":{"nested":"signoz_search_docs"}}}`)
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestAuthOrPublicPublicBatchRejected(t *testing.T) {
	handler, _ := newPublicDocsHTTPHandler(t)
	// All items look public, but JSON-RPC batches are never eligible for the
	// public path — per-item rate-limit accounting would be footgun-heavy and
	// uncommon enough to not warrant the complexity. The batch must defer to
	// authMiddleware, which rejects for lack of credentials.
	resp := serveJSONRPC(t, handler, "", `[{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}},{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}]`)
	require.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestAuthOrPublicTenantInitializedThenUnauthGETRejected(t *testing.T) {
	handler, m := newPublicDocsHTTPHandler(t)
	// POST initialize WITH tenant creds: authOrPublicMiddleware must see the
	// Authorization/SIGNOZ-API-KEY header and defer to authMiddleware instead
	// of wrapping the session in a public signed token. Otherwise a later
	// unauthenticated GET with that token would bypass authMiddleware.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.LATEST_PROTOCOL_VERSION + `","capabilities":{},"clientInfo":{"name":"tenant","version":"1"}}}`
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("SIGNOZ-API-KEY", "tenant-key")
	initReq.Header.Set("X-SigNoz-URL", "https://tenant.signoz.io")
	initRR := httptest.NewRecorder()
	handler.ServeHTTP(initRR, initReq)
	require.Equal(t, http.StatusOK, initRR.Code)
	sessionID := initRR.Header().Get(server.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)
	// The emitted session ID must be the raw mcp-go UUID — NOT a signed
	// token. If it were a token, the middleware's GET path would later
	// let an unauthenticated request through on the public branch.
	require.False(t, strings.HasPrefix(sessionID, session.TokenPrefix),
		"tenant-authed session must NOT be wrapped as a public token; got %q", sessionID)
	// And our signer must refuse to verify it — otherwise an attacker
	// could cross-contaminate the two paths.
	_, verifyErr := m.sessionSigner.Verify(sessionID)
	require.Error(t, verifyErr, "tenant session ID must not be a valid public token")

	// GET on that session without creds → authMiddleware path → 401.
	getReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	getReq.Header.Set(server.HeaderKeySessionID, sessionID)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)
	require.Equal(t, http.StatusUnauthorized, getRR.Code)
}

func TestAuthOrPublicPublicGETWithoutInitRejected(t *testing.T) {
	handler, _ := newPublicDocsHTTPHandler(t)
	// GET with a fabricated session ID that is NOT in publicSessions must
	// fall through to authMiddleware, which rejects without creds.
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, "never-initialized-session")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestPublicDocsRateLimitListReturnsJSONRPCError(t *testing.T) {
	cfg := &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}, ClientCacheSize: 8, ClientCacheTTL: time.Minute}
	m := &MCPServer{config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), publicLimiter: newPublicDocsRateLimiter(cfg)}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := m.publicDocsRateLimiter(next)

	var limited *httptest.ResponseRecorder
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
		req.Header.Set(server.HeaderKeySessionID, "list-session")
		req = req.WithContext(markPublicDocs(req.Context(), "tools/list"))
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Body.Len() > 0 && strings.Contains(rr.Body.String(), docsindex.CodeRateLimited) {
			limited = rr
			break
		}
		require.Equal(t, http.StatusOK, rr.Code)
	}
	require.NotNil(t, limited, "tools/list must eventually hit the rate limit")

	// Plan: non-tool-call over-limit responses are JSON-RPC errors with code
	// -32005, NOT CallToolResult{isError:true}. The envelope must have "error"
	// (not a nested "result.isError"), and the error data must carry the
	// structured retry-after hint.
	var payload struct {
		Error *struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"error"`
		Result map[string]any `json:"result"`
	}
	require.NoError(t, json.Unmarshal(limited.Body.Bytes(), &payload))
	require.Nil(t, payload.Result, "list rate-limit must NOT return a tool-call result envelope")
	require.NotNil(t, payload.Error)
	require.Equal(t, publicDocsRateLimitedJSONRPCCode, payload.Error.Code)
	require.Equal(t, docsindex.CodeRateLimited, payload.Error.Data["code"])
}

func TestPublicDocsRateLimitToolCallReturnsCallToolResult(t *testing.T) {
	cfg := &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}, ClientCacheSize: 8, ClientCacheTTL: time.Minute}
	m := &MCPServer{config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), publicLimiter: newPublicDocsRateLimiter(cfg)}
	wrapped := m.publicDocsRateLimiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	var limited *httptest.ResponseRecorder
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
		req.Header.Set(server.HeaderKeySessionID, "tool-session")
		req = req.WithContext(markPublicDocs(req.Context(), "signoz_search_docs"))
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if strings.Contains(rr.Body.String(), docsindex.CodeRateLimited) {
			limited = rr
			break
		}
	}
	require.NotNil(t, limited)

	// Plan: tool-call over-limit responses are CallToolResult{isError:true}
	// with structuredContent.code=RATE_LIMITED so the calling model sees the
	// error via its normal tool-result parsing, not as a JSON-RPC error.
	var payload struct {
		Error  any `json:"error"`
		Result *struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(limited.Body.Bytes(), &payload))
	require.Nil(t, payload.Error, "tool-call rate-limit must NOT return a JSON-RPC error envelope")
	require.NotNil(t, payload.Result)
	require.True(t, payload.Result.IsError)
	require.Equal(t, docsindex.CodeRateLimited, payload.Result.StructuredContent["code"])
}

func TestPublicDocsRateLimit(t *testing.T) {
	cfg := &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}, ClientCacheSize: 8, ClientCacheTTL: time.Minute}
	m := &MCPServer{config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), publicLimiter: newPublicDocsRateLimiter(cfg)}
	nextCalls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusOK)
	})
	wrapped := m.publicDocsRateLimiter(next)

	var limited *httptest.ResponseRecorder
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
		req.Header.Set(server.HeaderKeySessionID, "public-session")
		req = req.WithContext(markPublicDocs(req.Context(), "signoz_search_docs"))
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if strings.Contains(rr.Body.String(), docsindex.CodeRateLimited) {
			limited = rr
			break
		}
		require.Equal(t, http.StatusOK, rr.Code)
	}
	require.Equal(t, 30, nextCalls)
	require.NotNil(t, limited)
	require.Contains(t, limited.Body.String(), docsindex.CodeRateLimited)

	_, bypassNet, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)
	cfg = &config.Config{
		TrustedProxyCIDRs:        []*net.IPNet{bypassNet},
		PublicRateLimitBypassIPs: map[string]struct{}{"203.0.113.9": {}},
		ClientCacheSize:          8,
		ClientCacheTTL:           time.Minute,
	}
	m = &MCPServer{config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), publicLimiter: newPublicDocsRateLimiter(cfg)}
	calls := 0
	wrapped = m.publicDocsRateLimiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 35; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
		req.RemoteAddr = "10.1.2.3:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req = req.WithContext(markPublicDocs(req.Context(), "signoz_search_docs"))
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
	require.Equal(t, 35, calls)

	remoteOnly := m.publicLimiter.clientIP(&http.Request{
		RemoteAddr: "198.51.100.7:4321",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.9"}},
	})
	require.Equal(t, "198.51.100.7", remoteOnly)
}

func newPublicDocsHTTPHandler(t *testing.T) (http.Handler, *MCPServer) {
	t.Helper()
	logger := logpkg.New("error")
	cfg := &config.Config{
		TransportMode:            "http",
		Port:                     "0",
		ClientCacheSize:          8,
		ClientCacheTTL:           time.Minute,
		PublicRateLimitBypassIPs: map[string]struct{}{},
	}
	h := tools.NewHandler(logger, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := docsindex.NewIndexRegistry(ctx, publicDocsSnapshot())
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

func publicDocsSnapshot() docsindex.CorpusSnapshot {
	now := time.Now().UTC()
	docker := "# Install SigNoz Using Docker\n\nDocker Compose installation.\n"
	logs := "# Send logs to SigNoz\n\nDocker logs and OpenTelemetry logs.\n"
	pages := []docsindex.PageRecord{
		publicDocsPage("https://signoz.io/docs/install/docker/", "Install SigNoz Using Docker", "install", "Install > Docker", docker, now),
		publicDocsPage("https://signoz.io/docs/logs-management/send-logs-to-signoz/", "Send logs to SigNoz", "logs-management", "Logs Management > Send Logs", logs, now),
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

func publicDocsPage(url, title, section, breadcrumb, body string, fetchedAt time.Time) docsindex.PageRecord {
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
