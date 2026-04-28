# Feature: Stateless Public-Session Tokens — Context & Discussion

## Original Prompt

> Public MCP sessions are per-pod state
>
> initialize stores public session IDs in this process-local sync.Map,
> and later GET/DELETE requests only bypass auth if they land on the
> same process. In a multi-replica Kubernetes Service, a client can
> initialize on pod A and open the stream on pod B, causing public
> docs sessions to 401 unless ingress uses sticky sessions. Also bound
> this map with TTL/LRU, since unauthenticated initializes can
> otherwise accumulate session IDs indefinitely.
>
> What's solution for this? Suggest multiple

Then: `Let's implement 1` (the HMAC-signed stateless-token proposal).

## Reference Links

- MCP Streamable HTTP transport:
  https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http
- JSON-RPC server-error code range (we use `-32005` for rate-limit):
  https://www.jsonrpc.org/specification#error_object
- Token format inspiration (JWT-ish but deliberately NOT a JWT to
  avoid alg-confusion footguns and the header bloat):
  https://datatracker.ietf.org/doc/html/rfc7519

## Key Decisions & Discussion Log

### 2026-04-24 — five options surveyed, #1 selected

Five options were on the table:

1. **Signed stateless tokens** — HMAC-SHA256 over payload `{sid, iat,
   exp, kid}`. Zero infra, zero cross-pod coordination.
2. **Shared Redis / KV store** — adds Redis dependency on the
   critical path for every public request (~1–5 ms + failure mode).
3. **Sticky sessions at ingress** — ops-only, does NOT solve
   unbounded accumulation, fragile under pod restarts.
4. **Drop session binding for public path** — simplest code but may
   break mcp-go's Streamable HTTP session-continuity assumptions.
5. **Consistent hashing / session-pinning proxy** — overkill;
   significant infra work.

User chose #1.

### 2026-04-24 — fingerprint vs index for `kid`

Originally considered using an array index for `kid` (smaller payload,
~5 bytes). Rejected: index-based addressing silently remaps tokens to
the WRONG key if an operator reorders the env-var list. Fingerprint
(first 4 bytes of SHA-256(key), hex-encoded → 8 chars) is still
compact and makes key removal = deterministic revocation.

### 2026-04-24 — prefix sniff before HMAC verify

Every GET/DELETE request from a TENANT client also carries a session
ID in the header (a raw mcp-go UUID). We do NOT want to burn HMAC
cycles verifying those or flood logs with false rejections.

The `v1.` tag is our reserved prefix — no mcp-go UUID will ever start
with `v1.`. Cheap string check before any crypto.

### 2026-04-24 — verify failure is explicit 401, not fall-through

When a `v1.`-prefixed token arrives but fails verification
(expired/tampered/unknown-key), we 401 with `WWW-Authenticate:
Session error="invalid_token"` rather than falling through to
`authMiddleware`.

Rationale: falling through would produce a misleading "credentials
required" message for what's actually a session-continuity problem.
Well-behaved MCP clients can see the Session error hint and know to
re-`initialize` instead of retry with credentials they don't have.

The tradeoff: an attacker could forge `v1.xxx` garbage to probe our
rejection path. That probe is ~50ns of HMAC + log write per attempt;
not a meaningful DoS vector, and rate-limiter gates it anyway.

### 2026-04-24 — ephemeral key fallback

If `SIGNOZ_MCP_PUBLIC_SESSION_KEYS` is unset, we generate a 32-byte
key per pod boot and emit a WARN. This preserves the pre-existing
single-pod developer UX (nothing to configure) while giving multi-pod
operators a loud hint they need to set the env var.

Rejected alternative: refuse to start without the env var. Would have
broken every local-dev workflow, `make test`, and the stdio path.

### 2026-04-24 — POST requests ALSO need token unwrap

Initial implementation only unwrapped tokens on GET/DELETE. Discovered
while running `TestAuthOrPublicLifecycle`: POST
`notifications/initialized` with the token failed because mcp-go's
session registry doesn't know the token string — it expects the UUID
it originally minted.

Fix: moved the unwrap to the top of the middleware, before the method
switch. Now every request class (POST/GET/DELETE) sees the header
rewritten to the underlying UUID before downstream handlers run.

### 2026-04-24 — key-length floor at 16 bytes

HMAC-SHA256 has no formal minimum key length, but accepting anything
short-circuits operator typos into weak signers. 16 bytes = 128 bits
of security, enforced at BOTH `ParseKeysFromEnv` and
`session.NewSigner` so config-time failures surface before runtime.

### 2026-04-24 — DELETE also releases rate-limit bucket early

Stateless tokens don't need a DELETE-time cleanup hook (no map to
scrub), but we keep `publicLimiter.unregister(sid)` on DELETE so a
well-behaved client that explicitly terminates its session can
reclaim its rate-limit bucket immediately instead of waiting for TTL
expiry.

### 2026-04-24 — chose NOT to reuse mcp-go's session ID directly

One design alternative was to have mcp-go emit the signed token as
its session ID (via a custom SessionIDGenerator hook). Rejected
because:

1. mcp-go validates its session IDs against its own registry on
   subsequent requests — it would re-issue tokens as new "sessions"
   instead of treating the token as a lookup key.
2. Coupling our token format to mcp-go's internals would make library
   upgrades risky.
3. The middleware-translation approach keeps the two layers cleanly
   decoupled: mcp-go uses whatever session ID it likes; we wrap/unwrap
   at the network edge.

## Open Questions

- [x] Should we expose token metrics (minted/verified/rejected
  counters) via OTel? — Resolved: deferred. The existing
  `DocsRateLimited` metric covers the rejection-under-load case; a
  dedicated session-token counter is only useful after we have rotation
  SLOs to alarm on, which we don't yet. Revisit if/when we do.
- [x] Do we need `kid` rotation telemetry in startup logs? — Resolved:
  yes, added `log.Info("public-session signer initialized",
  slog.String("active_kid", signer.ActiveKID()))` on boot so
  operators can correlate pods on the same key ring in log search.
- [x] Should DELETE also clear the rate-limit bucket for the underlying
  UUID? — Resolved NO after Codex round-1 review: stateless tokens
  outlive the DELETE, so scrubbing buckets would be a quota-reset
  oracle. The idle sweeper handles reclamation. See the 2026-04-24
  Codex review entry below.
- [ ] Long term: should we offer a Redis-backed mode for deployments
  that want per-session revocation (not just TTL-bound)? Deferred
  — no current requirement, and stateless tokens handle the
  multi-pod correctness case without it.

### 2026-04-24 — Codex review round 1 (gpt-5.5 xhigh, fast mode)

Codex raised four concerns; three were actionable fixes, one was a
docs gap.

- **H1 (fixed): DELETE rate-limit reset oracle.** Original middleware
  called `publicLimiter.unregister(sid)` on DELETE, and the
  `OnUnregisterSession` mcp-go hook did the same on stream close.
  With stateless tokens the DELETE doesn't invalidate the token, so
  clients could exhaust quota, DELETE, and resume with fresh
  buckets. Removed BOTH unregister calls; added
  `TestPublicSession_DeleteDoesNotResetRateLimit` regression.
- **M1 (fixed): Nil-signer public initialize leaked raw UUID.** The
  `transformHeader` guard `if method == "initialize" && signer != nil`
  meant a nil signer would let mcp-go's raw UUID go out as the
  session ID. Clients would see a "successful" handshake whose
  session ID 401s on the next request. Added a top-of-POST
  fail-closed: when signer is nil, public POSTs fall through to
  authMiddleware. The later `if method == "initialize"` guard
  simplified accordingly. Added
  `TestPublicSession_NilSignerPublicInitializeFailsClosed`.
- **M2 (fixed): 32-bit kid distinct-key collision silently dropped a
  key.** `NewSigner` treated any `kid` repeat as a paste-dup. Now
  compares full key bytes when kid repeats; errors on distinct-key
  collision. Probability is astronomically low (~2^-32 per pair) but
  silent violation of the "all keys accepted" contract is worth
  guarding against. Test uses a test-overridable fingerprint hook
  to force a collision deterministically.
- **Low (fixed): README rotation docs too thin.** Expanded the env
  var row to point at a new "Public-session key rotation runbook"
  section with the full four-step sequence, wait time, expected
  401 behavior, and compromised-key collapse guidance.
- **Out of scope (noted):** Codex flagged `/readyz` and
  `DocsIndexReady()` as drift. Those landed under the prior
  `docs-search-fetch` feature — not this plan's scope.

### 2026-04-24 — why remove unregister on DELETE (details)

Codex-H1 is subtle: the pre-existing unregister-on-DELETE was
idiomatic when session IDs were in a per-pod map, because DELETE
really did destroy the session. Under stateless tokens, DELETE is
just a polite hint to the server — the token itself remains valid
until `exp`. If we continued to scrub buckets on DELETE, the client
would have a free "reset-my-quota" mechanism. The idle sweeper
(`publicLimiterIdleTTL=10min`) reclaims buckets when activity
actually stops, which is the correct bound: the bucket lives as
long as the session is actively making requests, regardless of
DELETE/reconnect cycles.

### 2026-04-28 - Superseded by auth-only docs decision

Decision changed: docs tools now sit behind the normal MCP auth path,
same as other tools. That removes the need for public docs sessions
entirely, so the stateless public-session token implementation is
removed rather than carried forward.

The multi-pod problem this plan solved was real only for unauthenticated
public docs traffic. With auth required, `/mcp` can use the existing
auth middleware and mcp-go session behavior without public token wrapping,
public rate-limit buckets, or public-session key rotation.
