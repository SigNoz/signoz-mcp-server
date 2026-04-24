package mcp_server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"

	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/session"
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

// peekedResponseWriter wraps the real ResponseWriter so we can
//
//   - observe the final status code (for "was initialize successful?")
//   - transform response headers just-in-time, before they're flushed,
//     so we can rewrite the mcp-go-generated Mcp-Session-Id into a
//     signed token on the public initialize path.
//
// The transform MUST run inside WriteHeader (not after ServeHTTP
// returns) because by the time net/http flushes a response, the
// headers are on the wire and w.Header().Set is a no-op.
//
// Flusher and Hijacker pass-through is preserved so downstream SSE and
// connection-upgrade semantics keep working transparently.
type peekedResponseWriter struct {
	http.ResponseWriter
	status          int
	transformHeader func(http.Header)
	wroteHeader     bool
}

func (w *peekedResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	if w.transformHeader != nil {
		w.transformHeader(w.ResponseWriter.Header())
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *peekedResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *peekedResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *peekedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not support hijacking")
	}
	return h.Hijack()
}

func (m *MCPServer) authOrPublicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Uniformly unwrap any signed public-session token in the
		// incoming Mcp-Session-Id header. Doing this BEFORE the method
		// switch means POST /tools-call, POST /notifications-initialized,
		// GET /listen, and DELETE /session all see the raw mcp-go UUID —
		// which is what mcp-go's session registry expects.
		//
		// Tenant clients send raw UUIDs (never v1.-prefixed), so they
		// skip this step entirely. A v1.-prefixed value that fails
		// verification is unambiguously a public token problem; we 401
		// rather than fall through to authMiddleware (which would emit
		// a confusing "needs credentials" message for an expired
		// stateless token).
		tokenVerified := false
		if m.sessionSigner != nil {
			if raw := r.Header.Get(server.HeaderKeySessionID); looksLikeSessionToken(raw) {
				sid, err := m.sessionSigner.Verify(raw)
				if err != nil {
					rejectPublicToken(w, r.Method, err, m.logger)
					return
				}
				r.Header.Set(server.HeaderKeySessionID, sid)
				tokenVerified = true
			}
		}

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
			// Fail-closed when the signer didn't initialize: public
			// initialize without a signer would leak the raw mcp-go
			// UUID as Mcp-Session-Id (no transform runs below),
			// creating a confusing client UX where the returned
			// session ID looks usable but 401s on the very next
			// request. Send these to authMiddleware instead — tenant
			// credentials work, public path is off.
			if m.sessionSigner == nil {
				next.ServeHTTP(w, r)
				return
			}
			// Tenant clients that send credentials on a public-eligible
			// method (typically `initialize`) must be routed to the
			// authenticated path — otherwise their session ID gets
			// wrapped in a public signed token and later GET/DELETE
			// would bypass authMiddleware. Detect tenant intent via the
			// standard auth headers and fall through to authMiddleware.
			if hasTenantAuthHeaders(r) {
				next.ServeHTTP(w, r)
				return
			}
			ctx := markPublicDocs(r.Context(), method)
			wrapped := &peekedResponseWriter{ResponseWriter: w, status: http.StatusOK}
			// On a successful public `initialize`, mcp-go writes its
			// internally-generated session ID into the Mcp-Session-Id
			// response header. We swap that for a signed token before
			// the client ever sees it, so every subsequent request can
			// be validated statelessly — no per-pod map, no shared
			// store, no unbounded memory growth. The nil-signer guard
			// above ensures we never reach this transform without a
			// valid signer.
			if method == "initialize" {
				wrapped.transformHeader = func(h http.Header) {
					sid := h.Get(server.HeaderKeySessionID)
					if sid == "" {
						return
					}
					token, err := m.sessionSigner.Sign(sid)
					if err != nil {
						// Signing failed — fail closed by stripping
						// the session header. The client sees "no
						// session" and re-initializes, rather than
						// getting a raw UUID we couldn't later
						// authorize.
						if m.logger != nil {
							m.logger.Warn("failed to sign public session token", logpkg.ErrAttr(err))
						}
						h.Del(server.HeaderKeySessionID)
						return
					}
					h.Set(server.HeaderKeySessionID, token)
				}
			}
			next.ServeHTTP(wrapped, r.WithContext(ctx))
		case http.MethodGet:
			if tokenVerified {
				next.ServeHTTP(w, r.WithContext(markPublicDocs(r.Context(), "GET")))
				return
			}
			next.ServeHTTP(w, r)
		case http.MethodDelete:
			if tokenVerified {
				// NOTE: we deliberately do NOT clear the rate-limit
				// bucket on DELETE. Stateless tokens outlive the
				// server's notion of a session — the token remains
				// valid until its intrinsic exp regardless of DELETE
				// — so scrubbing buckets here would hand the client a
				// "reset-my-quota" button: exhaust budget, DELETE,
				// resume with fresh buckets on the same still-valid
				// token. The idle sweeper in publicDocsRateLimiter
				// reclaims buckets after publicLimiterIdleTTL, which
				// is the correct bound for stateless tokens.
				next.ServeHTTP(w, r.WithContext(markPublicDocs(r.Context(), "DELETE")))
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// rejectPublicToken returns 401 with a hint when a v1.-prefixed token
// arrives but verifies as invalid or expired. We deliberately do NOT
// fall through to authMiddleware here — a failed session token is its
// own well-defined failure mode and deserves an actionable client
// message, not a generic "credentials required" 401.
func rejectPublicToken(w http.ResponseWriter, method string, err error, logger *slog.Logger) {
	w.Header().Set("WWW-Authenticate", `Session error="invalid_token"`)
	http.Error(w, publicSessionErrorMessage(err), http.StatusUnauthorized)
	if logger != nil {
		logger.Debug("rejected public session token",
			logpkg.ErrAttr(err),
			slog.String("http.method", method))
	}
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
// otherwise-public methods so the session doesn't get wrapped as public.
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

// looksLikeSessionToken is a cheap prefix check so tenant UUIDs don't
// burn HMAC cycles or generate log spam on every request. The v1. tag
// is reserved by pkg/session and mcp-go's UUID-format session IDs will
// never start with it.
func looksLikeSessionToken(s string) bool {
	return len(s) > len(session.TokenPrefix) && s[:len(session.TokenPrefix)] == session.TokenPrefix
}

// publicSessionErrorMessage maps signer errors to client-safe text.
// We deliberately avoid leaking which key rotated out or whether the
// MAC was correct-but-expired — all three look the same on the wire.
func publicSessionErrorMessage(err error) string {
	switch {
	case errors.Is(err, session.ErrExpired):
		return "session expired; please re-initialize"
	case errors.Is(err, session.ErrBadSignature),
		errors.Is(err, session.ErrUnknownKey),
		errors.Is(err, session.ErrInvalidToken):
		return "invalid session token; please re-initialize"
	default:
		return "session token rejected; please re-initialize"
	}
}
