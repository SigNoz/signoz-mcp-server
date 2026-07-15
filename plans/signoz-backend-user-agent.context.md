# Feature: SigNoz Backend User-Agent — Context & Discussion

## Original Prompt
> Can we also send user-agent in requests to SigNoz backend from mcp?
> so when the mcp go-client makes an http call to signoz
> it should send in the User Agent HTTP header to something like signoz/mcp-server? or whatever is the way to set right semantic user agents?

## Reference Links
- [RFC 9110, User-Agent](https://www.rfc-editor.org/rfc/rfc9110.html#name-user-agent)

## Key Decisions & Discussion Log

### 2026-07-15 — Product identifier and coverage
- Use `signoz-mcp-server/<version>`: RFC 9110 defines each product identifier as `name[/version]`, so `signoz/mcp-server` would incorrectly identify `signoz` as the product and `mcp-server` as its version.
- Use `pkg/version.Version` without stripping the leading `v`, keeping the outbound client identity aligned with `service.version` and the Docker image tag.
- Set the header on both client request paths: normal SigNoz API calls and validation/identity requests.
- Treat `User-Agent` as a reserved header so `SIGNOZ_CUSTOM_HEADERS` cannot replace the server's product identity.
- Keep the identifier minimal; do not append Go runtime, OS, architecture, or other fingerprinting detail.

### 2026-07-15 — Independent review corrections
- Reuse review found that the docs indexer already built the same `signoz-mcp-server/<version>` product identifier with a blank-version fallback. Move that shared behavior into `pkg/version.UserAgent`; the docs indexer appends only its specific comment.
- Efficiency review found that reserving and dropping an operator-configured `User-Agent` would break the documented `SIGNOZ_CUSTOM_HEADERS` behavior and could affect reverse-proxy routing. Preserve the configured value as the leading product/comment sequence and append the MCP product identifier.
- Cache the process-static MCP product identifier once instead of rebuilding it for every request and retry attempt.

## Open Questions
- [x] Header form → `signoz-mcp-server/<version>`.
- [x] Custom value → preserved first, with the MCP server product identifier appended.
