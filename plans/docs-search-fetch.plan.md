# Plan: Docs Search Fetch

## Status
In Progress

## Context
Add public SigNoz documentation search and fetch capabilities to the MCP server so agents can answer product, setup, deployment, instrumentation, API, and troubleshooting questions from the indexed `signoz.io/docs` markdown corpus.

## Approach
Implement the approved `/tmp/signoz-docs-impl-plan.md` literally:
- Build a gzipped gob corpus snapshot from `signoz.io/docs` markdown and reconstruct a bleve in-memory index at boot.
- Register `signoz_search_docs`, `signoz_fetch_doc`, and the `signoz://docs/sitemap` MCP resource.
- Use an `IndexRegistry` with RWMutex, atomic refcounts, generation tracking, and deferred close for atomic swaps.
- Add refresh logic with singleflight, success thresholds, transient-404 grace, and context-bound shutdown.
- Add JSON-RPC-aware public auth gating and public docs rate limiting for the documented methods.
- Add docs-only stdio mode via `SIGNOZ_MCP_MODE=docs-only`.
- Add tests and documentation/manifest updates required by the Verification and Documentation & Metadata Sync sections.

## Files to Modify
- `cmd/build-docs-index/main.go` - corpus builder.
- `internal/docs/*` - corpus schema, fetcher, parser, index, refresh, normalization, error helpers, tests, and assets.
- `internal/handler/tools/docs.go` - MCP tool and resource handlers.
- `internal/mcp-server/server.go` - docs index boot, handler registration, refresh, auth/rate-limit middleware wiring.
- `internal/mcp-server/auth_or_public_middleware.go` - public docs pre-auth gate.
- `internal/mcp-server/public_rate_limiter.go` - public token-bucket limiter.
- `internal/config/config.go` - docs-only stdio mode and docs refresh configuration.
- `pkg/otel/metrics.go` - docs telemetry meters.
- `manifest.json`, `README.md`, `.github/workflows/docs-index-refresh.yml`, `go.mod`, `go.sum` - docs and metadata sync.

## Verification
Run the plan's 25 verification items, including:
- `go run ./cmd/build-docs-index`
- `go test ./internal/docs/...`
- `go test ./... -race`
- `go vet ./...`
- blob size and manifest integrity checks
- golden-set recall@3 and precision@1 thresholds
