# Feature: Docs Search Fetch - Context & Discussion

## Original Prompt
> Implement the Codex-approved plan at `/tmp/signoz-docs-impl-plan.md` in the SigNoz MCP server repo at `/Users/makeavish/signoz/signoz-mcp-server`. Follow it literally. Add `signoz_search_docs`, `signoz_fetch_doc`, and `signoz://docs/sitemap`, with public-auth gate, rate limiter, corpus build command, scheduled GitHub Action, golden-set CI gate, blob-integrity CI gate, and OTel telemetry. Stop after local verification passes. Do not commit or push.

## Reference Links
- `/tmp/signoz-docs-impl-plan.md`

## Key Decisions & Discussion Log
### 2026-04-24 — dependency fetch blocked in sandbox
- Attempted `go get github.com/blevesearch/bleve/v2 golang.org/x/time/rate && go get -t go.uber.org/goleak && go mod tidy`.
- The command failed because this sandbox cannot resolve `proxy.golang.org`; implementation continues against the approved Bleve-based design, but final verification will require running the dependency command in a network-enabled environment.
- `go run ./cmd/build-docs-index`, `go build ./...`, `go vet ./...`, and docs-package tests are also blocked until the Bleve module and `go.sum` entries are available. The checked-in `internal/docs/assets/corpus.gob.gz` remains a placeholder and must be regenerated once dependencies/network are available.

### 2026-04-24 - Implementation start
- Read `/tmp/signoz-docs-impl-plan.md` end to end twice before source edits.
- Confirmed current `/mcp` HTTP route is wrapped by `authMiddleware` before mcp-go sees JSON-RPC methods, matching the plan's Auth posture section.
- Attempted to create branch `feat/docs-search-fetch`, but the sandbox could not create `.git/refs/heads/feat/docs-search-fetch.lock` due to filesystem permissions. Continuing without commit/push and documenting this blocker.

### 2026-04-28 - Docs tools moved under normal auth
- Decision: `signoz_search_docs`, `signoz_fetch_doc`, and `signoz://docs/sitemap` should require the same MCP auth as other tools/resources.
- Remove the public docs auth bypass, public rate limiter, public-session token signer, and docs-only unauthenticated stdio mode from this feature.
- Keep the embedded docs index, refresh workflow, docs tools/resource, and `/readyz` readiness behavior.

## Open Questions
- [x] Branch creation failed in the sandbox. Chosen interpretation: continue implementation on the current checkout without commit/push, since the user explicitly said not to commit/push and source edits remain reviewable.
