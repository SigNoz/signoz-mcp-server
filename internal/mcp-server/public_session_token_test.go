package mcp_server

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/pkg/session"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// These tests cover the stateless-session behavior in isolation from
// the mcp-go request pipeline. They exercise just the
// authOrPublicMiddleware with a sentinel `next` handler so we can
// assert the middleware's authorization decision directly — did it
// accept the token? Did it mark the context public? Did it rewrite the
// Mcp-Session-Id header to the underlying UUID?
//
// For end-to-end "does a real MCP call succeed" coverage, see
// TestAuthOrPublicLifecycle in public_docs_test.go.

func newSessionTokenTestServer(signer *session.Signer) *MCPServer {
	cfg := &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}}
	return &MCPServer{
		config:        cfg,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		publicLimiter: newPublicDocsRateLimiter(cfg),
		sessionSigner: signer,
	}
}

func TestBuildPublicSessionSigner_HTTPEphemeralWhenKeysAreUnset(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ephemeralSigner := buildPublicSessionSigner(logger, &config.Config{
		TransportMode: "http",
	})
	require.NotNil(t, ephemeralSigner)

	sharedSigner := buildPublicSessionSigner(logger, &config.Config{
		TransportMode:     "http",
		PublicSessionKeys: [][]byte{bytes.Repeat([]byte{'s'}, 32)},
	})
	require.NotNil(t, sharedSigner)
}

// sentinelNext records what the downstream handler saw so we can
// assert on per-request state (public flag, rewritten session ID).
type sentinelNext struct {
	called      bool
	wasPublic   bool
	methodLabel string
	seenSID     string
}

func (s *sentinelNext) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.wasPublic = isPublicDocsRequest(r.Context())
		s.methodLabel = publicMethod(r.Context())
		s.seenSID = r.Header.Get(server.HeaderKeySessionID)
		w.WriteHeader(http.StatusOK)
	}
}

// TestPublicSession_MultiPod simulates the multi-replica case: Pod A
// mints a token for an underlying session ID, then Pod B (with the
// SAME signing key but its own server instance) receives a GET with
// that token and must accept it.
//
// This is the property that sticky sessions are normally required to
// preserve — here we prove it works without them.
func TestPublicSession_MultiPod(t *testing.T) {
	sharedKey := bytes.Repeat([]byte{'s'}, 32)
	signer, err := session.NewSigner(session.SignerConfig{Keys: [][]byte{sharedKey}})
	require.NoError(t, err)

	podA := newSessionTokenTestServer(signer)
	podB := newSessionTokenTestServer(signer) // same signer

	// Pod A mints a token as it would on a public `initialize` response.
	const underlyingSID = "uuid-minted-on-pod-A"
	token, err := podA.sessionSigner.Sign(underlyingSID)
	require.NoError(t, err)

	// The client now goes to Pod B with that token.
	sentinel := &sentinelNext{}
	handler := podB.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "multi-pod token must verify; body=%s", rr.Body.String())
	require.True(t, sentinel.called)
	require.True(t, sentinel.wasPublic, "pod B must route cross-pod token as public")
	require.Equal(t, "GET", sentinel.methodLabel)
	require.Equal(t, underlyingSID, sentinel.seenSID,
		"pod B must unwrap the token into the raw mcp-go session ID before forwarding")
}

// TestPublicSession_ExpiredTokenIs401 fast-forwards the signer clock
// past the token's exp and asserts the middleware returns 401 with an
// actionable WWW-Authenticate hint.
func TestPublicSession_ExpiredTokenIs401(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	signer, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'x'}, 32)},
		TTL:  time.Minute,
		Now:  func() time.Time { return now },
	})
	require.NoError(t, err)
	m := newSessionTokenTestServer(signer)

	token, err := signer.Sign("uuid")
	require.NoError(t, err)

	// Fast-forward 2 minutes — past the 1-minute TTL.
	now = now.Add(2 * time.Minute)

	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Contains(t, rr.Body.String(), "expired")
	require.Contains(t, rr.Header().Get("WWW-Authenticate"), "Session")
	require.False(t, sentinel.called, "expired token must NOT reach downstream handler")
}

// TestPublicSession_TamperedTokenIs401 flips a byte in the payload
// segment and asserts that:
//  1. the middleware rejects with 401,
//  2. the error message is generic (doesn't leak whether it was MAC
//     failure vs unknown-key vs corrupted JSON),
//  3. the downstream handler was NOT reached.
func TestPublicSession_TamperedTokenIs401(t *testing.T) {
	signer, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'t'}, 32)},
	})
	require.NoError(t, err)
	m := newSessionTokenTestServer(signer)

	token, err := signer.Sign("uuid")
	require.NoError(t, err)

	// Tamper: flip a byte in the payload part.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	// parts[0] = "v1" (schema tag), parts[1] = payload b64, parts[2] = mac b64
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	payloadBytes[0] ^= 0xff
	parts[1] = base64.RawURLEncoding.EncodeToString(payloadBytes)
	tampered := strings.Join(parts, ".")

	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, tampered)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid session token")
	require.False(t, sentinel.called, "tampered token must NOT reach downstream handler")
}

// TestPublicSession_CrossDeploymentTokenIs401 models the case where a
// token minted by deployment-A somehow reaches deployment-B (different
// signing keys entirely). Must be rejected with 401 and never reach
// the downstream handler.
func TestPublicSession_CrossDeploymentTokenIs401(t *testing.T) {
	deployA, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'A'}, 32)},
	})
	require.NoError(t, err)
	deployB, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'B'}, 32)},
	})
	require.NoError(t, err)

	foreign, err := deployA.Sign("uuid-from-other-deployment")
	require.NoError(t, err)

	m := newSessionTokenTestServer(deployB)
	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, foreign)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.False(t, sentinel.called, "foreign-deployment token must NOT reach downstream handler")
}

// TestPublicSession_KeyRotationAcceptsOldToken is the rolling-deploy
// guarantee: during rotation, pod B is on the new key ring [new, old]
// and must still accept tokens minted by a peer (pod A) that's still
// on [old] alone.
func TestPublicSession_KeyRotationAcceptsOldToken(t *testing.T) {
	oldKey := bytes.Repeat([]byte{'o'}, 32)
	newKey := bytes.Repeat([]byte{'n'}, 32)

	podA, err := session.NewSigner(session.SignerConfig{Keys: [][]byte{oldKey}})
	require.NoError(t, err)
	podB, err := session.NewSigner(session.SignerConfig{Keys: [][]byte{newKey, oldKey}})
	require.NoError(t, err)

	token, err := podA.Sign("rolling-sid")
	require.NoError(t, err)

	m := newSessionTokenTestServer(podB)
	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, sentinel.called)
	require.True(t, sentinel.wasPublic)
	require.Equal(t, "rolling-sid", sentinel.seenSID)
}

// TestPublicSession_POSTWithTokenUnwrapsBeforeForward confirms that
// POST requests carrying a token (e.g. POST notifications/initialized
// on an already-established public session) also see the header
// unwrapped. This is what keeps mcp-go's session registry working —
// the UUID it handed out is what it expects back.
func TestPublicSession_POSTWithTokenUnwrapsBeforeForward(t *testing.T) {
	signer, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'p'}, 32)},
	})
	require.NoError(t, err)
	m := newSessionTokenTestServer(signer)

	const rawSID = "mcp-go-uuid-42"
	token, err := signer.Sign(rawSID)
	require.NoError(t, err)

	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	// A public POST that's NOT initialize, e.g. a follow-up ping.
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(server.HeaderKeySessionID, token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, sentinel.called)
	require.Equal(t, rawSID, sentinel.seenSID,
		"POST requests with signed tokens must have the header unwrapped before downstream sees them")
}

// TestPublicSession_TenantUUIDPassesThroughUnchanged asserts we do NOT
// touch non-token session IDs. Tenant clients use raw mcp-go UUIDs;
// burning HMAC cycles on them or mistakenly 401ing them would be a
// regression.
func TestPublicSession_TenantUUIDPassesThroughUnchanged(t *testing.T) {
	signer, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'u'}, 32)},
	})
	require.NoError(t, err)
	m := newSessionTokenTestServer(signer)

	const tenantUUID = "abc12345-6789-4def-a123-456789abcdef"

	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, tenantUUID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Tenant UUIDs don't start with "v1." so the middleware skips the
	// token path entirely and falls through to the next handler without
	// marking the request public. Our sentinel handler is the only
	// thing downstream here, and it records the unchanged header.
	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, sentinel.called)
	require.False(t, sentinel.wasPublic, "tenant UUID must not be marked public")
	require.Equal(t, tenantUUID, sentinel.seenSID, "tenant UUID must pass through unchanged")
	require.Empty(t, rr.Header().Get("WWW-Authenticate"),
		"tenant UUIDs must not trigger the public-session WWW-Authenticate hint")
}

// TestPublicSession_NilSignerFallsThrough: if the signer failed to
// initialize (e.g. crypto/rand exhaustion at boot), we treat the
// public path as disabled — no crashes, no accidental "accept any
// session ID". Tokens go straight to authMiddleware like any other
// unknown value.
func TestPublicSession_NilSignerFallsThrough(t *testing.T) {
	m := &MCPServer{
		config: &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		publicLimiter: newPublicDocsRateLimiter(
			&config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}}),
		sessionSigner: nil,
	}
	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(server.HeaderKeySessionID, "v1.pretend.token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// With no signer, the v1. token isn't verified here; it flows
	// through to the downstream handler untouched. The sentinel doesn't
	// enforce auth on its own, but that matches how authMiddleware
	// would reject the unknown value in the real chain.
	require.True(t, sentinel.called)
	require.False(t, sentinel.wasPublic)
	require.Equal(t, "v1.pretend.token", sentinel.seenSID)
}

// TestPublicSession_NilSignerPublicInitializeFailsClosed is the
// critical guard: if the signer didn't build, public `initialize` must
// NOT be served. Otherwise mcp-go would emit its raw UUID as
// Mcp-Session-Id (no transform), and the client would see a
// "successful" handshake whose session ID 401s on the very next
// request. Fail-closed by routing the request to authMiddleware
// instead — no public path without a signer.
func TestPublicSession_NilSignerPublicInitializeFailsClosed(t *testing.T) {
	m := &MCPServer{
		config: &config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		publicLimiter: newPublicDocsRateLimiter(
			&config.Config{PublicRateLimitBypassIPs: map[string]struct{}{}}),
		sessionSigner: nil,
	}
	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())

	// The classic public initialize — without a signer this must route
	// as non-public and reach the downstream handler with wasPublic
	// false (matching the authMiddleware-then-tenant-check path in the
	// real server chain).
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.True(t, sentinel.called)
	require.False(t, sentinel.wasPublic,
		"nil signer must never mark a request public; otherwise raw UUIDs would leak as session IDs")
}

// TestPublicSession_DeleteDoesNotResetRateLimit is the regression
// guard for Codex-H1: stateless tokens remain valid after DELETE, so
// the middleware must NOT scrub rate-limit buckets on DELETE —
// otherwise a client that exhausts its quota can just send DELETE and
// resume with fresh buckets on the same still-valid token.
//
// Strategy: drive the rate limiter to exhaustion on a specific
// (sessionID, method) pair, fire a DELETE with the matching signed
// token, then prove the next request with the same token is still
// rate-limited.
func TestPublicSession_DeleteDoesNotResetRateLimit(t *testing.T) {
	const sessionID = "sid-rl-replay"
	const method = "signoz_search_docs"
	limit := limitForPublicMethod(method)

	signer, err := session.NewSigner(session.SignerConfig{
		Keys: [][]byte{bytes.Repeat([]byte{'r'}, 32)},
	})
	require.NoError(t, err)
	m := newSessionTokenTestServer(signer)

	token, err := signer.Sign(sessionID)
	require.NoError(t, err)

	// Exhaust the bucket for (sessionID, method). We drive the
	// publicLimiter directly — same path the middleware would hit on
	// each real POST — so we don't have to round-trip bleve just to
	// spend 30 calls.
	for i := 0; i < limit; i++ {
		ok, _ := m.publicLimiter.allow(sessionID, method)
		require.True(t, ok, "prelude must fit within the quota")
	}
	ok, _ := m.publicLimiter.allow(sessionID, method)
	require.False(t, ok, "prelude must exhaust the quota")

	// Fire DELETE with the matching signed token. Middleware verifies,
	// rewrites the header, marks public, and calls next. Crucially it
	// must NOT call publicLimiter.unregister(sessionID).
	sentinel := &sentinelNext{}
	handler := m.authOrPublicMiddleware(sentinel.handler())
	del := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	del.Header.Set(server.HeaderKeySessionID, token)
	delRR := httptest.NewRecorder()
	handler.ServeHTTP(delRR, del)
	require.Equal(t, http.StatusOK, delRR.Code)
	require.True(t, sentinel.called)
	require.Equal(t, sessionID, sentinel.seenSID)
	require.Equal(t, "DELETE", sentinel.methodLabel)

	// The same sessionID must STILL be rate-limited. If the DELETE
	// scrubbed the bucket, this call would succeed — which would be
	// the Codex-H1 bug: a free "reset-my-quota" button for anyone
	// holding a still-valid token.
	ok, _ = m.publicLimiter.allow(sessionID, method)
	require.False(t, ok,
		"DELETE must NOT reset rate-limit buckets — stateless tokens outlive mcp-go's session lifecycle")
}
