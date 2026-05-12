# Plan: MCP Auth Failure Telemetry

## Status
Done

## Context
Some Claude Code connection failures currently appear to users as immediate disconnects, while the server only reports a generic missing credential response. We need enough structured log and span metadata on `/mcp` auth failures to identify whether a client is missing credentials, presenting a stale OAuth token, sending an invalid SigNoz URL, or falling into another auth branch.

## Approach
- Add bounded HTTP request metadata helpers for `server.address`, `client.address`, `user_agent.original`, and MCP session ID.
- Decorate `/mcp` auth failure logs and the `otelhttp` root span with `mcp.auth.mode`, `mcp.auth.failure_reason`, status, and request metadata.
- Keep OAuth failure logs on generic request metadata only, without MCP session IDs.
- Validate forwarded client addresses before recording them and bound user-agent/header-derived values.
- Add unit coverage for the helpers and auth failure log/span metadata.
- Add an E2E-style test that exercises the real HTTP mux and `otelhttp` stack for an unauthenticated `/mcp` request.

## Files to Modify
- `internal/mcp-server/server.go` - decorate auth failures with structured log and span attributes.
- `internal/mcp-server/server_test.go` - cover middleware auth-failure telemetry.
- `internal/mcp-server/e2e_docs_test.go` - cover the real HTTP stack and root span.
- `internal/oauth/handlers.go` - reuse generic request metadata in OAuth failure logs.
- `internal/oauth/handlers_test.go` - cover OAuth request metadata without MCP session leakage.
- `pkg/util/http.go` - centralize bounded HTTP metadata extraction.
- `pkg/util/http_test.go` - cover forwarded address, server host, and user-agent normalization.
- `pkg/log/http.go` - centralize structured request log attributes.

## Verification
- `go test ./internal/mcp-server -run 'TestE2E(AuthFailureTelemetry|DocsAgentFlow)' -count=1`
- `go test ./internal/mcp-server ./internal/oauth ./pkg/util ./pkg/log`
- `go test ./...`
