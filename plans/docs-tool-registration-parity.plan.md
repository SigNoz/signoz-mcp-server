# Plan: Docs Tool Registration Parity

## Status
Done

## Context
Production MCP server registration includes docs handlers, but the in-process integration test server omitted them and asserted only a hard-coded tool count. The docs tools also bypassed the shared `addTool` helper, so their schemas skipped the same normalization path used by other tools.

## Approach
- Register docs handlers in `buildTestServer` in the same position as production registration.
- Replace the hard-coded tools-list count with exact name parity against `manifest.json`.
- Register `signoz_search_docs` and `signoz_fetch_doc` via `addTool`.
- Leave README.md and manifest.json unchanged because this preserves the existing MCP surface.

## Files to Modify
- `internal/mcp-server/integration_test.go` - add docs handler registration and manifest parity assertion.
- `internal/handler/tools/docs.go` - use shared tool registration helper for docs tools.
- `plans/docs-tool-registration-parity.context.md` - append-only context log.
- `plans/docs-tool-registration-parity.plan.md` - current plan/status.

## Verification
- `gofmt` touched Go files.
- `go test ./internal/mcp-server ./internal/handler/tools`.
