# Plan: Stateless MCP Server

## Status
In Progress

## Context
The Streamable HTTP transport currently runs with mcp-go's default
`StatelessGeneratingSessionIdManager`: it generates per-session UUIDs and registers a
per-session entry in `server.sessions` / `activeSessions` on every `initialize`. Because the
server is built without `WithSessionIdleTimeout`, the mcp-go session sweeper never runs, so
POST-only clients leak those entries until process restart.

The server has no functional need for sessions: auth + tenant URL are resolved per-request
from headers, there is no sampling / server→client traffic, and tools/resources are static.
The only session-keyed state is the `sessionClients` LRU used to attribute client name/version
on analytics events. Moving to full stateless (`WithStateLess(true)`) removes all per-session
retention, makes any instance able to serve any request (no sticky routing), and aligns with
the MCP 2026-07-28 spec direction (stateless by default, no session model).

## Approach

### 1. Flip the transport to stateless — `internal/mcp-server/server.go` (`buildHTTP`)
Add `server.WithStateLess(true)` to the `NewStreamableHTTPServer(...)` options, with a comment
explaining: no functional dependence on sessions, removes per-session retention, aligns with
MCP 2026-07-28. Keep `WithHeartbeatInterval` — GET listening streams still open in stateless
mode and the heartbeat keeps them alive through proxies.

### 2. Preserve client attribution on `session_registered`, drop the session→clientinfo LRU
- Replace the method `attachClientInfo(props, sessionID)` (which round-tripped through the LRU)
  with a free function `attachClientInfo(props map[string]any, info mcp.Implementation)` that
  sets `clientName`/`clientVersion` directly.
- In the `AfterInitialize` hook, call the new helper with `message.Params.ClientInfo` for both
  `traits` and `props` (client name/version still land on `session_registered`).
- Remove the now-dead machinery:
  - field `sessionClients` and its constructor init,
  - methods `rememberClientInfo`, `lookupClientInfo`, `forgetClientInfo`,
  - consts `sessionClientCacheSize`, `sessionClientCacheTTL` and the explanatory comment block,
  - the `expirable` import in `server.go` (still used in `internal/handler/tools/handler.go`,
    unaffected),
  - the `rememberClientInfo` call in `AfterInitialize`, the `attachClientInfo` calls in
    `OnRegisterSession` / `AfterGetPrompt` / `AfterReadResource` / tool-call middleware, and the
    `forgetClientInfo` call in `OnUnregisterSession` (hook keeps its log line).
- Where `AttrSessionID` is set inside `AfterGetPrompt` / `AfterReadResource`, guard on
  `session.SessionID() != ""` so stateless mode doesn't emit an empty-string `sessionId` prop
  (consistent with `AfterInitialize` and the tool-call middleware, which already guard).

### 3. Keep generic session-id telemetry plumbing
`util.SetSessionID` / `GetSessionID`, `MCPSessionIDKey` span attributes, and the `sessionId`
analytics prop stay (generic, guarded on non-empty, also used by `pkg/log/handler.go`). They go
unset at runtime under stateless — an accepted observability scope-down, not dead code.

### 4. Tests — `internal/mcp-server/server_test.go`
- Delete `TestUnregisterSessionHookClearsClientInfo` (tests the removed LRU).
- Rewrite `TestClientInfoAttachesToToolCallEvent` → `TestClientInfoAttachesToSessionRegisteredEvent`:
  drive `OnAfterInitialize` with a `ClientInfo`, assert `clientName`/`clientVersion` on the
  `session_registered` event, and assert they are ABSENT on the tool-call event (documents the
  intentional scope-down). Remove the `forgetClientInfo` / `lookupClientInfo` calls.
- Transport-level tests are unaffected: hook/middleware unit tests inject a `fakeSession` via
  `newAnalyticsTestContext` (bypass the transport), and no test asserts a returned
  `Mcp-Session-Id` header.

### 5. Docs — `docs/architecture.md`
Add a short "Stateless transport" note alongside the existing OAuth-stateless section: the MCP
endpoint retains no per-session state, any instance can serve any request, no sticky sessions.
No `manifest.json` / README tool-table changes (no tool/resource/config surface change).

## Files to Modify
- `internal/mcp-server/server.go` — add `WithStateLess(true)`; remove session→clientinfo LRU +
  helpers + consts + import; rework `AfterInitialize`; drop dead `attachClientInfo`/`forget`
  calls; guard `AttrSessionID` in prompt/resource hooks.
- `internal/mcp-server/server_test.go` — delete one test, rewrite one test.
- `docs/architecture.md` — stateless-transport note.
- `plans/stateless-server.context.md` / `.plan.md` — committed alongside the change.

## Verification
- `go build ./...` and `go vet ./...` clean.
- `go test ./internal/mcp-server/...` green, including the rewritten analytics test.
- Full `go test ./...` green.
- Automated transport regression: `TestStatelessTransportIssuesNoSessionID`
  (`internal/mcp-server/docs_http_test.go`) drives the real `buildHTTP` handler — POST
  `initialize` asserts no `Mcp-Session-Id` response header, then a session-less `tools/list`
  asserts HTTP 200 (not rejected as a missing/invalid session). (Replaces the originally
  planned manual check; added per Codex review.)
- Codex CLI review (gpt-5.5, high + fast mode — xhigh stalled on an expired-token MCP server):
  approve-with-nits; both nits addressed. See context log 2026-06-07.
