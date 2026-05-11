# Feature: MCP Auth Failure Telemetry - Context & Discussion

## Original Prompt
> An user reported
>
> lichu@frontec.ai https://massive-leech.us.signoz.cloud
> May 8, 9:54:29 PM> when i connect the signoz MCP to claude code, it connects correctly but IMMEDIATELY disconnects. I don't understand what's happening and it's very annoying
>
> Investigate this, all telemetry data is available on nightswatch
>
> Ok let's improve telemetry

## Reference Links
- None

## Key Decisions & Discussion Log
### 2026-05-11 - Initial investigation
- Live telemetry showed healthy long-lived MCP streams for the tenant and repeated unauthenticated `/mcp` requests from the same client shape.
- The code should make unauthenticated `/mcp` rejects easier to diagnose by logging auth mode, failure reason, request path, server host, client address, user agent, and MCP session ID when present.

### 2026-05-11 - Multi-agent review follow-up
- Review flagged that unauthenticated request metadata must be bounded and sanitized because it is sourced from request headers.
- Review also flagged that OAuth request logs should not copy MCP session headers because OAuth endpoints are not MCP session scoped.
- Review asked for E2E coverage through the real HTTP handler and `otelhttp` root span, not only direct middleware unit tests.

### 2026-05-11 - Verification
- Added unit and E2E-style coverage for `/mcp` auth failure logs and spans, request metadata normalization, and OAuth request metadata boundaries.
- Verified with targeted E2E, touched-package tests, and the full `go test ./...` suite.

### 2026-05-12 - PR review: quoted forwarded host normalization
- Codex review flagged that quoted RFC `Forwarded` host parameters with ports were split before quote removal.
- Fixed `server.address` normalization to remove surrounding quotes before stripping ports, and added coverage for quoted hostname and bracketed IPv6 host values with ports.

## Open Questions
- [x] Should OAuth failure logs include `mcp.session.id` if a caller sends that header? Answer: no; only `/mcp` request logs should include MCP session IDs.
