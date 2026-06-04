# Plan: Cache SigNoz Clients by URL

## Status
Done

## Context
`Handler.GetClient` cached clients by `HashTenantKey(apiKey, signozURL)` because the `SigNoz`
client baked the API key into itself and stamped it on every request. That defeated HTTP
connection reuse for the common multi-tenant-same-URL case (different API keys → different
clients → different connection pools). Issue #69 asks to cache by `signozURL` alone, which
requires removing the credential from the client.

## Approach
- Make the `SigNoz` client credential-free: drop `apiKey` / `authHeaderName` from the struct
  and `NewClient`. Read `apiKey` / `authHeader` from the request context inside `doRequest`
  and `doValidationRequest` (via `credentialsFromContext`), defaulting the header to
  `SIGNOZ-API-KEY`. Fail closed when no API key is present.
- Cache the client per URL: `cacheKey = strings.ToLower(signozURL)`.
- Make the analytics identity cache per-credential: a map keyed by
  `util.HashCredential(apiKey, authHeader)` (renamed from `HashTenantKey`), guarded by the
  existing mutex; `fetchAnalyticsIdentity` takes the resolved auth header as a parameter.
- Seed credentials onto ctx in the OAuth `validateSigNozCredentials` path (it builds a client
  directly and previously relied on the struct fields).

## Files to Modify
- `pkg/util/context.go` — `HashTenantKey` → `HashCredential(apiKey, authHeader)`.
- `internal/client/client.go` — credential-free struct/`NewClient`; ctx-sourced creds in both
  senders; per-credential identity cache.
- `internal/handler/tools/handler.go` — URL-only cache key; 3-arg `NewClient`.
- `internal/oauth/handlers.go` — seed ctx creds before `ValidateCredentials`.
- `internal/client/client_test.go` — migrate to ctx-supplied creds; add fail-closed and
  per-credential-cache tests.
- `internal/handler/tools/handler_test.go` (new) — per-URL cache sharing tests.
- `docs/architecture.md` — per-URL caching description.

## Verification
- `go build ./... && go vet ./... && go test ./... -count=1` all green.
- Two different API keys + same URL return the same cached client; different URLs differ.
- A request with no API key in context fails closed (no HTTP sent).
- See the full TDD breakdown in `docs/superpowers/plans/2026-06-04-client-cache-by-url.md`.
