package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/time/rate"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
)

const publicLimiterIdleTTL = 10 * time.Minute

type publicDocsRateLimiter struct {
	mu         sync.Mutex
	entries    map[string]*rateLimitEntry
	trusted    []*net.IPNet
	bypassIPs  map[string]struct{}
	now        func() time.Time
	sweepEvery time.Duration
}

type rateLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newPublicDocsRateLimiter(cfg *config.Config) *publicDocsRateLimiter {
	return &publicDocsRateLimiter{
		entries:    map[string]*rateLimitEntry{},
		trusted:    cfg.TrustedProxyCIDRs,
		bypassIPs:  cfg.PublicRateLimitBypassIPs,
		now:        time.Now,
		sweepEvery: time.Minute,
	}
}

func (l *publicDocsRateLimiter) start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(l.sweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.sweep()
			}
		}
	}()
}

func (m *MCPServer) publicDocsRateLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isPublicDocsRequest(r.Context()) || m.publicLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		method := publicMethod(r.Context())
		if method == "" {
			method = r.Method
		}
		key := m.publicLimiter.keyForRequest(r)
		if m.publicLimiter.bypassed(key) {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := m.publicLimiter.allow(key, method)
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		if m.meters != nil {
			m.meters.DocsRateLimited.Add(r.Context(), 1, metric.WithAttributes(attribute.String("tool", method)))
		}
		writePublicRateLimited(w, r, retryAfter)
	})
}

func (l *publicDocsRateLimiter) allow(key, method string) (bool, int) {
	limit := limitForPublicMethod(method)
	bucketKey := key + "|" + method
	l.mu.Lock()
	entry := l.entries[bucketKey]
	if entry == nil {
		entry = &rateLimitEntry{
			limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(limit)), limit),
		}
		l.entries[bucketKey] = entry
	}
	entry.lastSeen = l.now()
	reservation := entry.limiter.Reserve()
	l.mu.Unlock()
	if reservation.OK() && reservation.Delay() == 0 {
		return true, 0
	}
	if reservation.OK() {
		reservation.Cancel()
	}
	return false, max(1, int(time.Minute/time.Duration(limit)/time.Second))
}

func (l *publicDocsRateLimiter) sweep() {
	cutoff := l.now().Add(-publicLimiterIdleTTL)
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, entry := range l.entries {
		if entry.lastSeen.Before(cutoff) {
			delete(l.entries, key)
		}
	}
}

func (l *publicDocsRateLimiter) keyForRequest(r *http.Request) string {
	if sessionID := r.Header.Get(server.HeaderKeySessionID); sessionID != "" {
		return sessionID
	}
	return l.clientIP(r)
}

func (l *publicDocsRateLimiter) clientIP(r *http.Request) string {
	remote := docsindex.RemoteHost(r.RemoteAddr)
	remoteIP := net.ParseIP(remote)
	if remoteIP == nil {
		return remote
	}
	if l.trusts(remoteIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip.String()
			}
		}
	}
	return remoteIP.String()
}

func (l *publicDocsRateLimiter) trusts(ip net.IP) bool {
	for _, cidr := range l.trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (l *publicDocsRateLimiter) bypassed(key string) bool {
	_, ok := l.bypassIPs[key]
	return ok
}

func limitForPublicMethod(method string) int {
	switch method {
	case "signoz_fetch_doc":
		return 60
	case "signoz_search_docs", docsindex.DocsSitemapURI, "initialize", "tools/list", "resources/list", "prompts/list", "resources/templates/list":
		return 30
	default:
		return 30
	}
}

// Plan contract: for tool-call over-limit, return CallToolResult{isError:true}
// so the calling model can see the error via mcp-go's structured result. For
// discovery and resource reads (which don't have isError in mcp-go), return a
// JSON-RPC error with code -32005 — this is the "server-defined error" range
// defined by the JSON-RPC spec, matching the retry-after semantics we emit.
const publicDocsRateLimitedJSONRPCCode = -32005

func writePublicRateLimited(w http.ResponseWriter, r *http.Request, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", fmt.Sprint(retryAfter))
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"RATE_LIMITED","retry_after_seconds":` + fmt.Sprint(retryAfter) + `}`))
		return
	}
	body, _ := ioReadAndRestore(r)
	id := json.RawMessage("null")
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err == nil && len(msg.ID) > 0 {
		id = msg.ID
	}
	method := publicMethod(r.Context())
	if isPublicToolCallMethod(method) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result": map[string]any{
				"isError": true,
				"content": []map[string]any{{
					"type": "text",
					"text": "Public docs rate limit exceeded; retry later.",
				}},
				"structuredContent": map[string]any{
					"code":                docsindex.CodeRateLimited,
					"retry_after_seconds": retryAfter,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error": map[string]any{
			"code":    publicDocsRateLimitedJSONRPCCode,
			"message": "Public docs rate limit exceeded; retry later.",
			"data": map[string]any{
				"code":                docsindex.CodeRateLimited,
				"retry_after_seconds": retryAfter,
			},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func isPublicToolCallMethod(method string) bool {
	return method == "signoz_search_docs" || method == "signoz_fetch_doc"
}

func ioReadAndRestore(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
