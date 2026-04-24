# Plan: Stateless Public-Session Tokens

## Status
Done

## Context
Public docs MCP sessions were tracked in a process-local `sync.Map`
(`MCPServer.publicSessions`). Two correctness problems flowed from that:

1. **Multi-pod breakage.** A client could call `initialize` on Pod A
   and then hit Pod B for the subsequent GET (SSE listen) or DELETE.
   Pod B didn't have the session in its map, so `authOrPublicMiddleware`
   fell through to `authMiddleware`, which 401'd the unauthenticated
   request. Sticky sessions at ingress were the only workaround.

2. **Unbounded memory.** Unauthenticated `initialize` calls accumulated
   entries forever. A hostile or buggy client could exhaust pod memory
   with zero credentials. There was no TTL or LRU bound.

Signed stateless tokens fix both: the token carries its own expiry and
HMAC signature, any pod with the same key ring can verify it, and zero
memory is consumed on the server.

## Approach

### Token wire format
```
v1.<base64url(payload)>.<base64url(hmac-sha256(payload))>
```

Payload is JSON: `{sid, iat, exp, kid}`.

- `sid` — the underlying mcp-go session ID (UUID string).
- `iat` — issued-at (unix seconds, informational).
- `exp` — expiry (unix seconds, verified).
- `kid` — 8-hex-char key fingerprint (first 4 bytes of SHA-256(key)).

The `kid` is a fingerprint, not an array index, so that removing a key
from the ring deterministically revokes every token minted with it.
Index-based addressing would silently remap revoked tokens to the wrong
key if an operator reordered the list.

### Key ring & rotation

`SIGNOZ_MCP_PUBLIC_SESSION_KEYS=<base64-key-1>,<base64-key-2>,...`

- `Keys[0]` is the active signer; all entries accepted on verify.
- Rotation is two-deploy: prepend new key, redeploy; then drop the
  retired key from the tail. (Full runbook in README.md under
  "Public-session key rotation runbook".)
- Identical duplicate keys are tolerated silently (first occurrence
  wins), so a paste-error during rotation can't break the signer.
- Distinct keys whose 32-bit kid happens to collide (~2^-32 per pair
  — astronomically unlikely) are rejected with an error rather than
  silently dropped, so the "all entries accepted for verify" contract
  is never violated.
- Each key must decode to ≥16 bytes.

If the env var is unset, the pod mints a 32-byte ephemeral key at boot
and emits a WARN. Single-replica deployments still Just Work; multi-replica
deployments that forget to set the env var will see 401s in prod — the
WARN gives them a hint *before* they hit that.

### Middleware wiring

- **POST `initialize` (public)**: intercept the response via
  `peekedResponseWriter.transformHeader`. Before mcp-go flushes
  headers, replace `Mcp-Session-Id` with the signed token.
- **POST (any) with token in request**: unwrap the header at the top
  of the middleware so mcp-go's registry lookup uses the raw UUID.
- **GET with token**: same unwrap; mark context as public; forward
  downstream.
- **DELETE with token**: same unwrap; mark context as public; forward
  downstream. NOTE: we deliberately do NOT clear the rate-limit
  bucket on DELETE. Stateless tokens outlive the server's notion of
  a session — the token remains valid until its intrinsic exp
  regardless of DELETE — so scrubbing buckets here would hand the
  client a "reset-my-quota" button. The idle sweeper in
  `publicDocsRateLimiter` reclaims buckets after
  `publicLimiterIdleTTL`, which is the correct bound.
- **Invalid/expired token**: 401 with `WWW-Authenticate: Session
  error="invalid_token"`. Do NOT fall through to `authMiddleware` —
  that would emit a misleading "needs credentials" message for what
  is actually a session-continuity problem.
- **Missing/raw UUID (tenant)**: unchanged — flows to `authMiddleware`
  for tenant auth.
- **Nil signer (public POST)**: fail-closed — we route the request
  to authMiddleware rather than letting public initialize succeed
  with a raw Mcp-Session-Id that would 401 on the next request.
  Tenants can still authenticate; the public path is effectively
  disabled.

### Package layout

- `pkg/session/token.go` — Signer, payload, sign/verify, key parsing,
  `GenerateKey`.
- `pkg/session/token_test.go` — round-trip, rotation, tamper,
  cross-deployment, parsing, expiry.
- `internal/config/config.go` — parse `SIGNOZ_MCP_PUBLIC_SESSION_KEYS`
  (fails loudly on bad base64 / short key) and
  `SIGNOZ_MCP_PUBLIC_SESSION_TTL`.
- `internal/mcp-server/server.go` — build signer in `NewMCPServer`;
  ephemeral-key fallback with WARN; drop `publicSessions sync.Map`.
- `internal/mcp-server/auth_or_public_middleware.go` — uniform token
  unwrap at top of middleware; `peekedResponseWriter` extended with
  `transformHeader` + Flusher/Hijacker pass-through.
- `internal/mcp-server/public_docs_test.go` — updated assertions to
  match signed-token response shape.
- `internal/mcp-server/public_session_token_test.go` — new
  integration tests: multi-pod, expiry, tamper, key rotation,
  cross-deployment, POST-with-token unwrap, tenant UUID
  pass-through, nil-signer fallthrough.

## Files to Modify

- `pkg/session/token.go` *(new)* — Signer with sign/verify/rotation.
- `pkg/session/token_test.go` *(new)* — unit tests.
- `internal/config/config.go` — env var parsing, new fields.
- `internal/mcp-server/server.go` — build signer; drop `publicSessions`;
  update `OnUnregisterSession` hook to release rate-limit bucket
  directly.
- `internal/mcp-server/auth_or_public_middleware.go` — rewrite to use
  signer.
- `internal/mcp-server/public_docs_test.go` — remove `publicSessions`
  refs; assert signed-token response shape.
- `internal/mcp-server/public_session_token_test.go` *(new)* —
  integration tests described above.
- `README.md` — document `SIGNOZ_MCP_PUBLIC_SESSION_KEYS` and
  `SIGNOZ_MCP_PUBLIC_SESSION_TTL`.

## Verification

Locally passing at time of writing:

- `go test ./pkg/session/ -race -count=1` — 20 unit tests cover
  sign/verify, rotation (both directions), all error classes,
  cross-deployment, and env-var parsing.
- `go test ./internal/mcp-server/ -race -count=1` — full middleware
  suite including new multi-pod / expiry / tamper integration tests
  and the end-to-end lifecycle test that now asserts a signed token in
  the response.
- `go test ./... -race -count=1 -timeout 300s` — whole project green.
- `go vet ./...` — clean.

### Manual check for multi-pod behavior

Two pods sharing `SIGNOZ_MCP_PUBLIC_SESSION_KEYS`:

```bash
# Pod A:
curl -sD- -H 'Content-Type: application/json' \
  http://pod-a:8000/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}' \
  | grep -i mcp-session-id
# → Mcp-Session-Id: v1.eyJz...

# Pod B (different replica, same key):
curl -sD- -H "Mcp-Session-Id: v1.eyJz..." http://pod-b:8000/mcp
# → 200 OK (SSE stream or 404 from mcp-go session registry —
#   never 401, which is what we're guarding)
```

### Rolling-rotation check

Deploy N+1 with `SIGNOZ_MCP_PUBLIC_SESSION_KEYS=new,old` while older
pods still have `old`. During the rolling window, both pods accept
both tokens. After the final deploy drops `old`, any outstanding
tokens minted under `old` receive 401 with the "please re-initialize"
hint — expected revocation behavior.
