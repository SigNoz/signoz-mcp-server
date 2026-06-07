# Plan: Cache SigNoz clients by URL (#69)

## Status
In Progress

## Context
`Handler.GetClient` cached `SigNoz` clients by `HashTenantKey(apiKey, signozURL)`, because the
client baked the API key into itself and stamped it on every request. So two API keys on the
same SigNoz URL got separate clients and separate HTTP connection pools — the "incomplete
attempt to cache the http client" called out in #69. This is independent of, and complementary
to, the stateless transport (#192): with stateless requests there is no session affinity, so a
shared per-URL connection pool is what keeps per-request client resolution efficient.

## Approach
Reuse PR #187's implementation (cherry-picked, akshaysw authorship preserved), stacked on
`feat/stateless-transport`:
- **Credential-free client** — drop `apiKey`/`authHeaderName` from `SigNoz`/`NewClient`; read
  credentials from request context and stamp per outbound request (`credentialsFromContext`).
  Missing API key → fail closed before any HTTP call.
- **Cache by URL** — `cacheKey = strings.ToLower(signozURL)`; one client + pool per backend URL.
- **Per-credential identity cache** — `/me` cache becomes a map keyed by
  `HashCredential(apiKey, authHeader)` (renamed from `HashTenantKey`), serialized by mutex.
- **OAuth** — seed credentials onto ctx before `ValidateCredentials`.
- **Docs** — `docs/architecture.md` cache labels + new "Client Caching" section (done by hand to
  coexist with the stateless section, since #187's doc commit was not cherry-picked).

## Files Modified
- `internal/client/client.go`, `internal/client/client_test.go`
- `internal/handler/tools/handler.go`, `internal/handler/tools/handler_test.go`
- `internal/oauth/handlers.go`
- `pkg/util/context.go` (`HashTenantKey` → `HashCredential`)
- `docs/architecture.md` (cache description + Client Caching section)
- `plans/client-cache-by-url.{context,plan}.md`

## Verification
- `go build ./...`, `go vet ./...`, `go test ./... -count=1`, `go test -race ./internal/client/... ./internal/handler/tools/...` — all green (done).
- #187's own tests carried over: `TestGetClient_SharedAcrossAPIKeysPerURL` (different keys/same
  URL reuse one client; different URLs differ), `TestGetClient_MissingCredentialsError` /
  `TestDoRequest_MissingCredentialFailsClosed` (fail closed), `TestGetAnalyticsIdentity_PerCredentialCache`.
- Stacked PR base = `feat/stateless-transport`; retarget to `main` after #192 merges.
- Optional follow-up: a Codex review + live E2E pass mirroring the stateless PR, if desired.
