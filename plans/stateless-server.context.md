# Feature: Stateless MCP Server — Context & Discussion

## Original Prompt
> Working on https://github.com/SigNoz/signoz-mcp-server/issues/70#issuecomment-4642683172
> Should make it stateless?

Issue #70 ("timeout and body size limit is not defined") tracks three items. Items 1
(read/write timeouts) and 3 (request/response body size limits, closed by #191) are done.
This work is item 2: the stateful/stateless nature of the server, deferred as a separate
investigation.

## Reference Links
- [Issue #70 comment](https://github.com/SigNoz/signoz-mcp-server/issues/70#issuecomment-4642683172)
- [MCP Streamable HTTP transport spec (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
- [MCP 2026-07-28 release candidate announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- mcp-go v0.49.0 `WithStateLess` / session managers: `server/streamable_http.go`

## Key Decisions & Discussion Log

### 2026-06-07 — Investigation of current state model
- The issue comment called the server "stateful". That is imprecise. `NewStreamableHTTPServer`
  in mcp-go v0.49.0 defaults to `StatelessGeneratingSessionIdManager` (`streamable_http.go:219`):
  it generates a UUID session ID on `initialize` but only validates format, never existence —
  it never rejects unknown/expired IDs. It is NOT the `InsecureStatefulSessionIdManager`.
- The real choice is "stateless-generating (retains per-session transport state) → fully
  stateless (`Generate()` returns `""`, retains nothing)", not "stateful → stateless".
- The server has no functional dependence on session state:
  - Per-request auth + tenant URL are resolved fresh from headers in `authMiddleware`
    (`server.go:1400-1425`). No tool reads session-scoped state.
  - No sampling / server→client requests anywhere in the app (grep: zero usage).
  - Tools/resources are static (`WithToolCapabilities(false)`); no `listChanged` notifications.
  - The only thing keyed on session ID is analytics attribution: the `sessionClients` LRU
    (`server.go:72,82-119`) correlates client name/version from `initialize` to later
    tool-call/prompt/resource events.

### 2026-06-07 — Latent leak found (independent of the stateless decision)
- The server is built with only `WithHeartbeatInterval(...)` — no `WithSessionIdleTimeout`.
  So `sessionIdleTTL == 0` and the mcp-go session sweeper never starts (`streamable_http.go:237`).
- A POST `initialize` registers a session into `server.sessions` + `activeSessions` with no
  deferred cleanup (`streamable_http.go:560-571`); only a GET-stream close (`:614-616`) or the
  sweeper cleans it up. So POST-only clients leak session entries until process restart.
- The maintainer already half-knew this — the `sessionClients` LRU exists precisely because
  "POST-only clients never trigger explicit cleanup" (`server.go:41-43`), but mcp-go's internal
  maps have no equivalent bound. Going fully stateless removes these maps entirely.

### 2026-06-07 — Spec + library support confirmed
- MCP spec 2025-06-18: sessions are explicitly optional ("A server ... **MAY** assign a
  session ID"). A server that returns no `Mcp-Session-Id` is fully compliant. The resumability
  section even names the "session management is not in use" case.
- mcp-go v0.49.0: `WithStateLess(true)` → `StatelessSessionIdManager`, whose `Generate()`
  returns `""` and `Validate()` always returns `(false, nil)`. With an empty session ID the
  `isInitializeRequest && sessionID != ""` guards (`:547`, `:560`) are skipped — no
  `Mcp-Session-Id` header, no `RegisterSession`, no `activeSessions` entry.
- Caveat: in stateless mode the GET listening stream still opens but collapses all streams
  onto the empty-key session via `LoadOrStore("")` (`:604`). Benign here because the server
  sends zero server→client messages; the heartbeat ping still keeps each GET connection alive.

### 2026-06-07 — MCP 2026-07-28 RC reframes this as forward-alignment
- The RC for spec revision 2026-07-28 (locked 2026-05-21, final 2026-07-28) eliminates the
  protocol-level session model: "any MCP request can land on any server instance", removing
  session stores and sticky routing. Protocol version + client info move into request metadata.
  Roots, Sampling, and Logging are deprecated (12-month window).
- Going stateless now moves *with* the spec direction, not against it. It also confirms the
  session-keyed `sessionClients` LRU is dead either way — the protocol is deleting the session
  abstraction it relies on.

### 2026-06-07 — Decision: full stateless (option a)
- Chosen: `WithStateLess(true)`, accept the analytics scope-down.
  - Per-tool-call / prompt / resource events lose the `client.name` / `client.version`
    dimension (they were looked up from the session-keyed LRU; no session ⇒ no correlation).
  - `client.name` / `client.version` are PRESERVED on the `session_registered` event because
    `message.Params.ClientInfo` is available directly in the `AfterInitialize` hook — no LRU
    needed.
  - `client.source` + assistant correlation survive (they come from headers, not the session).
  - The `mcp.session.id` span attribute and `sessionId` analytics prop are guarded on a
    non-empty session ID, so they simply go unset at runtime (accepted observability scope-down).
- Rejected option (b) (preserve client attribution via request metadata) for now — more work;
  the per-tool-call client breakdown is not a relied-upon dashboard dimension. Can revisit if
  the 2026-07-28 metadata-based client info lands.

## Open Questions
- [x] Does the MCP spec and mcp-go support full stateless? → Yes, both (see 2026-06-07 log).
- [x] Config flag vs hardcoded? → Hardcode `WithStateLess(true)`. No functional need for
  sessions, aligns with the 2026-07-28 stateless-by-default direction, minimal config surface.
  Easy to make configurable later if a use case appears.
- [x] How to handle analytics client attribution? → Option (a): keep it on `session_registered`
  (direct from `ClientInfo`), drop it on per-call events, remove the session→clientinfo LRU.
- [ ] Should mcp-go be bumped for 2026-07-28 support? → Out of scope here; this change is
  forward-compatible and the SDK bump is a separate follow-up once mcp-go ships RC support.

### 2026-06-07 — Codex review (gpt-5.5, high + fast mode) — approve-with-nits
- xhigh hung 3× (no output, 0 CPU). Root cause: codex is configured with the SigNoz staging
  MCP server whose OAuth token is expired; codex stalls on MCP init at startup. Fix for the
  review run: `--config 'mcp_servers={}'` to disable MCP servers. Endpoint itself was fine.
- Verdict: approve-with-nits. No Critical/Major. Codex independently confirmed no missed
  session dependency (static tools/resources, no sampling/roots/elicitation/server→client),
  and that the client-info LRU removal is complete with `clientName`/`clientVersion` still on
  `session_registered` from `InitializeRequest.ClientInfo`.
- Minor (addressed): hook/middleware tests use an injected fake session and don't prove the
  real HTTP transport emits no `Mcp-Session-Id`. → Added `TestStatelessTransportIssuesNoSessionID`
  in `docs_http_test.go`: POST initialize asserts no `Mcp-Session-Id` header, then a session-less
  `tools/list` asserts HTTP 200 (not rejected as missing/invalid session).
- Nit (addressed): "retains no per-session state" overstated — a GET listening stream still
  holds transient SDK stream state via `LoadOrStore("")`. → Qualified the wording in the
  `buildHTTP` comment and `docs/architecture.md` to "no issued session ID / no POST session
  state; an open GET listener may hold transient SDK stream state (harmless — no server→client
  messaging)."
