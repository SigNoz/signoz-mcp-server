# Plan: OpenAPI-Compatible Tool Schemas

## Status
Done

## Context
hiAgent cannot synchronize SigNoz MCP tools because it parses advertised `inputSchema` data through an OpenAPI 3 parser. The current generated schemas include JSON Schema boolean subschemas (`true`) for open-ended `interface{}` fields, which are legal JSON Schema but invalid as OpenAPI 3 `properties` entries.

## Approach
Normalize advertised MCP tool schemas after each tool is built and before registration. Walk only schema-bearing positions and replace `true` subschemas with empty schema objects `{}`. Leave normal boolean fields and annotations unchanged.

## Files to Modify
- `internal/handler/tools/schema_compat.go` — helper to normalize MCP tool schemas and register tools.
- `internal/handler/tools/*` — use the helper for tool registration.
- `internal/mcp-server/integration_test.go` — regression test over `tools/list` advertised schemas.

## Verification
- Run targeted tests for tool discovery and schema normalization.
- Confirm no `inputSchema.properties.*` boolean subschemas remain in `tools/list`.
- Run `go test ./...`.
