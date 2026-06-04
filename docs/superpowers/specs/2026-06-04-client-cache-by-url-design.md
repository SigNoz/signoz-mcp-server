# Design: Cache SigNoz clients by URL (decouple credentials from the client)

Tracking issue: [#69](https://github.com/SigNoz/signoz-mcp-server/issues/69)
(follow-up to review comment on [#63](https://github.com/SigNoz/signoz-mcp-server/pull/63))

## Problem

`internal/handler/tools/handler.go` caches `*signozclient.SigNoz` instances keyed by
`util.HashTenantKey(apiKey, signozURL)`. The cache key includes the API key because the
client bakes the credential into itself: `SigNoz` stores `apiKey` / `authHeaderName` as
struct fields and stamps them onto every outbound request
(`req.Header.Set(s.authHeaderName, s.apiKey)` in `doRequest` and `doValidationRequest`).

Consequence: two users with **different API keys** but the **same `signozURL`** get two
separate `SigNoz` objects, each with its own `http.Client` and therefore its own TCP/TLS
connection pool. The whole point of caching the HTTP client — connection reuse — is
defeated for the multi-tenant-same-URL case.

The reviewer's framing (#69): the client should not store the API key at all. The cacheable
unit is the connection pool, which is a property of the **destination URL**, not of the
caller's credential. Credentials are inherently per-request and should travel per-request.

## Goal

Cache one `SigNoz` client (and thus one connection pool) **per `signozURL`**, shared across
all API keys hitting that URL, while keeping every request authenticated with the correct
caller-specific credential.

## Approach (chosen)

**Context-sourced credentials.** Credentials already enter the system through the request
`context` — both transports set `apiKey` / `authHeader` / `signozURL` on the context before
any tool handler runs, and that is exactly how `GetClient` reads them today. Every request
method on `SigNoz` already threads `ctx` through to `doRequest` / `doValidationRequest`. So
the request methods can read the credential from `ctx` at send time instead of from struct
fields. No new method-signature churn.

### Client changes (`internal/client/client.go`)

- Remove `apiKey` and `authHeaderName` from the `SigNoz` struct.
- Keep `baseURL` and `customHeaders` on the struct — both are properties of the *URL*, not
  the credential. (`customHeaders` is already config-derived and URL-gated by the caller.)
- `NewClient` signature drops `apiKey` and `authHeaderName`:
  `NewClient(log, baseURL, customHeaders)`.
- In `doRequest` / `doValidationRequest`, resolve credentials from `ctx`:
  - `apiKey, _ := util.GetAPIKey(ctx)`
  - `authHeader, _ := util.GetAuthHeader(ctx)`; default to `SIGNOZ-API-KEY` if empty
    (preserving the current fallback that lives in `GetClient`).
  - If `apiKey == ""`, return an error before sending — fail closed rather than send an
    unauthenticated request. This guards the implicit-context risk.
  - `req.Header.Set(authHeader, apiKey)` and use `authHeader` in the reserved-header skip
    checks for `customHeaders` (replacing the `s.authHeaderName` references at lines 249,
    329, 332).

### Identity cache (resolves the deferred open question)

`GetAnalyticsIdentity` caches an `AnalyticsIdentity` per client and selects the `/me`
endpoint from the auth header (`Authorization` → user endpoint, else service-account). Both
the cached value and the endpoint choice are **credential-specific**. With the client now
shared per-URL, a single `cachedIdentity` field would mix identities across API keys and
corrupt analytics attribution.

Fix: replace the single-value cache with a per-credential map guarded by the existing
`identityMu`:

```go
identityMu    sync.Mutex
identityCache map[string]identityEntry // key: hash of (apiKey, authHeader)
```

- Key by a hash of `(apiKey, authHeader)` — the auth header is part of the key because it
  changes which endpoint is queried and thus which identity shape comes back. `HashTenantKey`
  is currently used *only* at the cache-key site we're replacing, so after this change it has
  no callers. Repurpose it into `util.HashCredential(apiKey, authHeader)` (same SHA-256
  approach) and use it as the identity-cache map key, so raw keys are never held in the map and
  no dead code is left behind.
- `GetAnalyticsIdentity` reads `apiKey` / `authHeader` from `ctx`, computes the key, and
  does per-entry TTL expiry (same `analyticsIdentityCacheTTL`). Cache-hit / cache-miss meter
  emission is unchanged.
- `fetchAnalyticsIdentity` picks the endpoint from the ctx auth header instead of
  `s.authHeaderName`.

### Cache-key change (`internal/handler/tools/handler.go`)

- `GetClient`: `cacheKey := util.NormalizeSigNozURL(signozURL)` (already normalized
  upstream; use the normalized URL string as the key) instead of
  `util.HashTenantKey(apiKey, signozURL)`.
- Still read `apiKey` from ctx and keep the "missing tenant credentials" guard — a request
  with no API key is still an error; we just don't key the cache on it.
- `NewClient` call updated to the new signature (drop `apiKey`, `authHeader`).
- The custom-headers URL gate (`strings.EqualFold(signozURL, h.configURL)`) is unchanged.

### OAuth validation path (`internal/oauth/handlers.go`)

`validateSigNozCredentials` builds a client directly (not via the cache) and calls
`ValidateCredentials(ctx)`. It has `apiKey` / `signozURL` as function args but does **not**
put them on the context today — it relied on the struct fields. After the refactor it must
seed the context:

```go
ctx = util.SetAPIKey(ctx, apiKey)
ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
signozClient := client.NewClient(h.logger, signozURL, headers)
return signozClient.ValidateCredentials(ctx)
```

### Interface / mocks

- `internal/client/interface.go` — `GetAnalyticsIdentity` / `ValidateCredentials`
  signatures are unchanged (still ctx-only), so the interface is unaffected.
- `internal/client/mock.go` — unaffected (no creds in the mock surface).

## Data flow (after)

```
transport middleware ──sets ctx{apiKey, authHeader, signozURL}──► tool handler
        │
        ▼
GetClient(ctx): cacheKey = normalize(signozURL)         ◄── shared across API keys
        │  cache hit/miss → one *SigNoz per URL
        ▼
client.Method(ctx) → doRequest(ctx):
        apiKey/authHeader read from ctx → Header.Set     ◄── correct per-caller credential
        one http.Client / connection pool per URL        ◄── reused across callers
```

## Security considerations

- **No credential mixing.** The connection pool is shared, but each request stamps its own
  caller credential from its own context. HTTP keep-alive connections are not credential-
  bound at the TCP layer (auth is per-request header), so sharing the pool is safe.
- **Fail closed.** Missing `apiKey` in ctx → error before send, never an anonymous request.
- **Custom-header gate unchanged.** Proxy-auth headers are still only attached when the
  tenant URL equals the configured URL.
- **Identity attribution stays per-credential** via the keyed identity cache.

## Testing / verification

- Unit: `internal/client/client_test.go` — request methods stamp the ctx credential;
  missing-credential ctx errors; identity cache is keyed per credential (two ctxs with
  different keys against one client get distinct identities; same key hits cache).
- Unit: `internal/handler/tools/handler_test.go` (if present) / `GetClient` — two different
  API keys + same URL return the **same** cached client instance; different URLs return
  different instances.
- Unit: OAuth `validateSigNozCredentials` still authenticates after seeding ctx.
- `go test ./...`, `go vet ./...`, lint per repo CI.

## Docs / metadata sync

- `docs/architecture.md` — update the caching section (currently describes per-tenant
  apiKey+URL caching) to per-URL client caching with per-request credentials.
- No MCP tool schema, `README.md` tool table, or `manifest.json` changes — this is internal
  plumbing with no user-facing tool/config surface change.
- `plans/handle-client-cache.context.md` + `.plan.md` per repo convention.

## Out of scope / YAGNI

- No change to how transports populate the context (already correct).
- No new config knobs.
- `HashTenantKey` is repurposed (renamed to `HashCredential`), not deleted — same hashing,
  new call site.
```
