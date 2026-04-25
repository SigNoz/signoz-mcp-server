# Feature: OpenAPI-Compatible Tool Schemas — Context & Discussion

## Original Prompt
> Help me triage this issue: https://github.com/SigNoz/signoz-mcp-server/issues/118
>
> Is it really a bug on signoz mcp server or client specific issue?
>
> If we fix it Will it have any other consequences as well? Is it safe to fix?
>
> Let's implement it and create PR

## Reference Links
- [GitHub Issue #118](https://github.com/SigNoz/signoz-mcp-server/issues/118)
- [MCP schema reference](https://modelcontextprotocol.io/specification/draft/schema)
- [JSON Schema boolean schemas](https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-00#section-4.3.2)
- [OpenAPI 3.0 Schema Object](https://spec.openapis.org/oas/v3.0.3.html#schema-object)

## Key Decisions & Discussion Log
### 2026-04-24 — triage
- hiAgent fails tool synchronization because some advertised MCP input schemas contain JSON Schema boolean subschemas (`true`) under schema-bearing fields.
- JSON Schema allows `true` as an accept-anything schema, but OpenAPI 3.0 `properties` entries must be Schema Objects.
- Treat this as a SigNoz MCP server compatibility bug exposed by an OpenAPI-shaped client, not a SigNoz backend runtime bug.
- Local `tools/list` inspection found affected schemas on `signoz_create_alert`, `signoz_update_alert`, `signoz_create_dashboard`, and `signoz_update_dashboard`.

### 2026-04-24 — implementation choice
- Normalize generated tool schemas at registration time instead of changing runtime argument parsing or tightening SigNoz payload structs.
- Replace only schema-position `true` subschemas with `{}` so semantics remain accept-anything.
- Do not rewrite real boolean-valued metadata such as tool annotations or schema fields like `{ "type": "boolean" }`.

### 2026-04-24 — implementation complete
- Added shared tool registration helper that normalizes raw and structured MCP input/output schemas before they are advertised.
- Added unit coverage for schema-position `true` conversion and integration coverage over `tools/list`.
- Verified with focused package tests and full `go test ./...`.

## Open Questions
- [x] Is this server-side or client-specific? Answer: server-side compatibility bug; permissive JSON Schema clients may work, but MCP clients reasonably expect object-valued tool property schemas.
- [x] Is the fix safe? Answer: yes if limited to schema-position `true` to `{}` normalization, because those are semantically equivalent.
