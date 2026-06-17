# Feature: Docs Tool Registration Parity - Context & Discussion

## Reference Links
- None.

## Key Decisions & Discussion Log
### 2026-06-17 - Initial plan
- No existing plan covered this parity fix.
- Keep the change scoped to test-server registration, tools-list parity assertion, and docs handler registration helper usage.
- README.md and manifest.json require no content changes because no tools/resources/configuration are added, renamed, or removed.

### 2026-06-17 - Implementation complete
- `buildTestServer` now registers docs handlers, matching production registration.
- `tools/list` integration coverage now compares registered tool names against `manifest.json` instead of a hard-coded count.
- Docs tools now use the shared `addTool` registration helper.
- Verification passed with `go test ./internal/mcp-server ./internal/handler/tools`.

## Open Questions
- [x] Should docs/metadata files change? Resolved: no; this PR preserves the existing MCP surface and only makes registration/tests match the already-advertised manifest.
