# Plan: SigNoz Backend User-Agent

## Status
In Progress

## Context
Outbound requests currently rely on Go's generic HTTP user agent, so the SigNoz backend cannot identify traffic originating from a particular MCP server release.

## Approach
- Build one RFC 9110 product identifier in `pkg/version`: `signoz-mcp-server/<version>`, with `dev` as the blank-version fallback.
- Set it on every request created by `doRequest` and `doValidationRequest`.
- Preserve a configured custom `User-Agent` and append the MCP product identifier; continue protecting content-type and authentication headers from override.
- Reuse the shared product identifier in the docs indexer and cache it for the backend client request path.
- Add request-capture tests for normal API calls, credential validation, and custom-header override protection.

## Files to Modify
- `internal/client/client.go` — define and attach the canonical user agent.
- `internal/client/client_test.go` — verify all outbound paths and override behavior.
- `internal/docs/fetcher.go` — reuse the shared product identifier before its docs-indexer comment.
- `pkg/version/version.go` — own the shared product identifier and blank-version fallback.
- `pkg/version/version_test.go` — cover release, branch, whitespace, and blank versions.
- `plans/signoz-backend-user-agent.context.md` — preserve decisions and discussion.
- `plans/signoz-backend-user-agent.plan.md` — track implementation and verification.

## Verification
- Run focused client tests normally and with a linker-injected version.
- Run the full Go test suite.
- Run `git diff --check`.
