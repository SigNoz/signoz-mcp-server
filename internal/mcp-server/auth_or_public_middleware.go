package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/mark3labs/mcp-go/server"
)

const publicPeekLimitBytes = 64 * 1024

type publicDocsContextKey struct{}
type publicMethodContextKey struct{}

type jsonRPCProbe struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type toolsCallParams struct {
	Name string `json:"name"`
}

type resourceReadParams struct {
	URI string `json:"uri"`
}

type peekedResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *peekedResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (m *MCPServer) authOrPublicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			public, method, err := publicJSONRPCRequest(r)
			if err != nil {
				http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
				return
			}
			if !public {
				next.ServeHTTP(w, r)
				return
			}
			// Tenant clients that send credentials on a public-eligible method
			// (typically `initialize`) must be routed to the authenticated
			// path — otherwise their session ID ends up in publicSessions and
			// later GET/DELETE on that session would bypass authMiddleware.
			// That would effectively downgrade a tenant SSE stream into a
			// public one. Detect tenant intent via the standard auth headers
			// and fall through to authMiddleware.
			if hasTenantAuthHeaders(r) {
				next.ServeHTTP(w, r)
				return
			}
			ctx := markPublicDocs(r.Context(), method)
			wrapped := &peekedResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(wrapped, r.WithContext(ctx))
			if method == "initialize" && wrapped.status < http.StatusBadRequest {
				if sessionID := w.Header().Get(server.HeaderKeySessionID); sessionID != "" {
					m.publicSessions.Store(sessionID, struct{}{})
				}
			}
		case http.MethodGet:
			sessionID := r.Header.Get(server.HeaderKeySessionID)
			if _, ok := m.publicSessions.Load(sessionID); ok && sessionID != "" {
				next.ServeHTTP(w, r.WithContext(markPublicDocs(r.Context(), "GET")))
				return
			}
			next.ServeHTTP(w, r)
		case http.MethodDelete:
			sessionID := r.Header.Get(server.HeaderKeySessionID)
			if _, ok := m.publicSessions.Load(sessionID); ok && sessionID != "" {
				next.ServeHTTP(w, r.WithContext(markPublicDocs(r.Context(), "DELETE")))
				m.forgetPublicSession(sessionID)
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func markPublicDocs(ctx context.Context, method string) context.Context {
	ctx = context.WithValue(ctx, publicDocsContextKey{}, true)
	return context.WithValue(ctx, publicMethodContextKey{}, method)
}

func isPublicDocsRequest(ctx context.Context) bool {
	v, _ := ctx.Value(publicDocsContextKey{}).(bool)
	return v
}

func publicMethod(ctx context.Context) string {
	v, _ := ctx.Value(publicMethodContextKey{}).(string)
	return v
}

// publicJSONRPCRequest peeks at the JSON-RPC body and decides whether it is
// eligible for the public (unauthenticated) docs path. On success it restores
// r.Body byte-for-byte so downstream handlers see the original stream.
//
// Batches are NEVER eligible for the public path, even when every item is an
// otherwise-public method. Per-item billing against the public rate limiter
// would require threading each item through the limiter and handling partial
// failures; it is safer to require that unauthenticated clients use
// single-request JSON-RPC. Authenticated clients can still send batches —
// they fall through to authMiddleware normally.
func publicJSONRPCRequest(r *http.Request) (bool, string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, publicPeekLimitBytes+1))
	if err != nil {
		return false, "", err
	}
	if len(body) > publicPeekLimitBytes {
		// Oversize bodies are handed to authMiddleware without being drained
		// further — we reconstruct the stream so the downstream parser sees
		// exactly what the client sent.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return false, "", nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return false, "", err
	}
	if _, isArray := raw.([]any); isArray {
		// JSON-RPC batches are never public; defer to authMiddleware.
		return false, "", nil
	}
	return publicJSONRPCPayload(body)
}

func publicJSONRPCPayload(body []byte) (bool, string, error) {
	var msg jsonRPCProbe
	if err := json.Unmarshal(body, &msg); err != nil {
		return false, "", err
	}
	switch msg.Method {
	case "initialize", "notifications/initialized", "ping", "tools/list", "resources/list", "prompts/list", "resources/templates/list":
		return true, msg.Method, nil
	case "tools/call":
		var params toolsCallParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return false, msg.Method, err
		}
		return params.Name == "signoz_search_docs" || params.Name == "signoz_fetch_doc", params.Name, nil
	case "resources/read":
		var params resourceReadParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return false, msg.Method, err
		}
		return params.URI == docsindex.DocsSitemapURI, docsindex.DocsSitemapURI, nil
	default:
		return false, msg.Method, nil
	}
}

// hasTenantAuthHeaders reports whether the request is carrying credentials
// that authMiddleware would consume. We treat presence of SIGNOZ-API-KEY or
// Authorization as "tenant intent" and defer to authMiddleware even on
// otherwise-public methods so the session doesn't get stored as public.
// X-SigNoz-URL alone is not sufficient — a public client might still supply
// a tenant URL for routing but without creds; that stays public.
func hasTenantAuthHeaders(r *http.Request) bool {
	if r.Header.Get("SIGNOZ-API-KEY") != "" {
		return true
	}
	if r.Header.Get("Authorization") != "" {
		return true
	}
	return false
}

func (m *MCPServer) forgetPublicSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.publicSessions.Delete(sessionID)
	if m.publicLimiter != nil {
		m.publicLimiter.unregister(sessionID)
	}
}
