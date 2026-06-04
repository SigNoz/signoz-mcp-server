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
- In **both** `doRequest` *and* `doValidationRequest` (the two functions that actually stamp
  the auth header — lines 246 and 329), resolve credentials from `ctx`:
  - `apiKey, _ := util.GetAPIKey(ctx)`
  - `authHeader, _ := util.GetAuthHeader(ctx)`; default to `SIGNOZ-API-KEY` if empty
    (preserving the current fallback that lives in `GetClient`).
  - If `apiKey == ""`, return an error before sending — fail closed rather than send an
    unauthenticated request. This guards the implicit-context risk. Applies to both functions.
  - `req.Header.Set(authHeader, apiKey)` and use `authHeader` in the reserved-header skip
    checks for `customHeaders` (replacing the `s.authHeaderName` references at lines 249–250,
    329, 332).
  - Note: `doValidationRequest` is the shared sender for `ValidateCredentials` *and*
    `fetchAnalyticsIdentity`, so fixing it covers all three public entry points. The URL
    strings those callers build still come from `s.baseURL` (unchanged — `baseURL` stays on
    the struct); only the auth header now comes from ctx.
- `fetchAnalyticsIdentity` (line ~177) currently selects its endpoint via
  `strings.EqualFold(s.authHeaderName, "Authorization")`. Change this to read the auth header
  from ctx (`authHeader, _ := util.GetAuthHeader(ctx)`) so endpoint selection follows the
  per-request credential, not a struct field.

### Identity cache (resolves the deferred open question)

`GetAnalyticsIdentity` caches an `AnalyticsIdentity` per client and selects the `/me`
endpoint from the auth header (`Authorization` → user endpoint, else service-account). Both
the cached value and the endpoint choice are **credential-specific**. With the client now
shared per-URL, a single `cachedIdentity` field would mix identities across API keys and
corrupt analytics attribution.

Fix: replace the single-value cache with a per-credential map guarded by the existing
`identityMu`:

```go
// remove: cachedIdentity *AnalyticsIdentity / identityCachedAt time.Time
identityMu    sync.Mutex
identityCache map[string]identityEntry // key: HashCredential(apiKey, authHeader)

type identityEntry struct {
    identity *AnalyticsIdentity
    cachedAt time.Time
}
```

(The map is lazily initialized — `if s.identityCache == nil { s.identityCache = map[...]{} }`
under the mutex, or initialized in `NewClient`.)

- Key by a hash of `(apiKey, authHeader)` — the auth header is part of the key because it
  changes which endpoint is queried and thus which identity shape comes back. `HashTenantKey`
  is currently used *only* at the cache-key site we're replacing, so after this change it has
  no callers. Repurpose it into `util.HashCredential(apiKey, authHeader)` (same SHA-256
  approach) and use it as the identity-cache map key, so raw keys are never held in the map and
  no dead code is left behind.
- **Defaulting consistency (avoid a subtle cache-mismatch bug):** apply the
  `authHeader == "" → "SIGNOZ-API-KEY"` default *once*, before both the key computation and
  the endpoint selection in `fetchAnalyticsIdentity`. If the map key used the raw (empty)
  header while the endpoint logic used the defaulted header, two equivalent requests could
  map to different keys or pick mismatched endpoints. Resolve+default the header at the top of
  `GetAnalyticsIdentity`, pass the normalized header down (or re-default identically).
- `GetAnalyticsIdentity` reads `apiKey` / `authHeader` from `ctx`, computes the key, and
  does per-entry TTL expiry (same `analyticsIdentityCacheTTL`). Cache-hit / cache-miss meter
  emission is unchanged.
- `fetchAnalyticsIdentity` picks the endpoint from the ctx auth header instead of
  `s.authHeaderName`.

### Cache-key change (`internal/handler/tools/handler.go`)

- `GetClient`: replace `cacheKey := util.HashTenantKey(apiKey, signozURL)` with a key derived
  from the URL alone: `cacheKey := strings.ToLower(signozURL)`.
  - **Do not** call `util.NormalizeSigNozURL` here: it returns `(string, error)` (can't be
    assigned inline) and is not applied to the ctx URL anywhere today, so introducing it would
    be a behavior change beyond this issue's scope. The ctx `signozURL` is used raw today (the
    old key hashed it raw); we keep that semantics and only drop the apiKey from the key.
  - `ToLower` is used (rather than the raw string) only to match the case-insensitive
    `strings.EqualFold(signozURL, h.configURL)` semantics already used for the custom-header
    gate — so a single tenant URL maps to one cache entry regardless of case. This is a
    deliberate, conservative choice; if exact-match keying is preferred we can use `signozURL`
    verbatim, which is also correct (just slightly less sharing). Either way it is not a
    correctness/security issue because credentials are no longer part of the key.
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

- **Existing-test migration (large, mechanical, mandatory):** `internal/client/client_test.go`
  has ~40 call sites using the old 5-arg `NewClient(logger, url, apiKey, authHeader, headers)`.
  All must move to the 3-arg signature, and every test that then calls a request method must
  seed the credential into the test's ctx (`ctx = util.SetAPIKey(ctx, "test-api-key"); ctx =
  util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")`). Tests that today vary `authHeaderName` per case
  (e.g. lines 365, 392, 414 — the `Authorization` cases) must set the header on ctx instead of
  passing it to the constructor. Without this, the new fail-closed guard makes every request
  test error. A shared test helper that returns a credentialed ctx is the cleanest path.
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
