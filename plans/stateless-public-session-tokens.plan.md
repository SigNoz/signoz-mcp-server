# Plan: Stateless Public-Session Tokens

## Status
Done

## Context
This plan is superseded. The docs tools now use the same authentication path as every other MCP tool, so the server no longer needs a public docs session model.

The earlier stateless-token work solved a real multi-pod problem for unauthenticated public docs sessions. Once docs moved under auth, that problem disappeared because public docs sessions no longer exist.

## Approach
- Remove `authOrPublicMiddleware`, the public docs token bucket limiter, and the public-session HMAC signer.
- Remove `SIGNOZ_MCP_PUBLIC_SESSION_KEYS`, `SIGNOZ_MCP_PUBLIC_SESSION_TTL`, trusted-proxy, public-rate-limit-bypass, and docs-only unauthenticated config.
- Keep `signoz_search_docs`, `signoz_fetch_doc`, and `signoz://docs/sitemap` registered as normal authenticated handlers.
- Keep `/readyz` tied to docs index readiness so Kubernetes still avoids sending traffic to pods that would return `INDEX_NOT_READY`.
- Update tests so unauthenticated docs calls fail with the normal auth error and authenticated docs E2E still succeeds.
- Update README, architecture docs, and the docs-search plan to remove public-session deployment guidance.

## Files to Modify
- `internal/mcp-server/server.go` - route `/mcp` through normal auth only.
- `internal/mcp-server/auth_or_public_middleware.go` - remove.
- `internal/mcp-server/public_rate_limiter.go` - remove.
- `pkg/session/token.go` - remove.
- `internal/config/config.go` - remove public session and docs-only config.
- `internal/mcp-server/*docs*_test.go` - replace public-path tests with auth-required coverage.
- `README.md`, `docs/architecture.md`, `plans/docs-search-fetch.plan.md` - remove public-session docs.

## Verification
- `go test ./internal/mcp-server ./internal/config ./internal/handler/tools`
- `go test ./...`
- `go vet ./...`
