# Plan: Official Go SDK Migration

## Status
In Progress

## Context
74 Go files import `mark3labs/mcp-go v0.56.0`. Upstream is behind on the protocol
(mark3labs/mcp-go#928); `modelcontextprotocol/go-sdk v1.7.0` is the reference
implementation and carries protocol 2026-07-28 while still serving legacy
`initialize`. The migration must preserve the client-visible catalog, the
fail-open validation policy, and the current telemetry.

The official SDK has **no equivalent** for four things we depend on today:

1. **Hooks** (`server.Hooks` with `BeforeAny`/`OnSuccess`/`OnError`) — rebuilt on `Server.AddReceivingMiddleware`, which sees the start and end of every method in one place.
2. **Tool-scoped middleware** (`server.ToolHandlerMiddleware`) — middleware is method-level, so the logging/validation/error-code decorators become per-tool handler wrappers or a `method == "tools/call"` branch in receiving middleware.
3. **Tracer hook** (`server.WithTracer`) — no tracing exists in the SDK. This server becomes the owner of method and tool spans; `pkg/otel/mcp.go` is replaced, not ported.
4. **Panic recovery** (`server.WithRecovery`) — no `recover()` anywhere in the SDK; a panicking handler kills the process without our own recovery middleware.

Tools are registered with the low-level `Server.AddTool`, not generic
`mcp.AddTool[In, Out]`: the typed helper validates input and shapes results
itself, which would override the fail-open, never-reject validation policy.

## Approach

### Phase 1 — Groundwork (this commit)
- `TestGuardrail_WireCatalogGoldens` pins `initialize`, `tools/list`, `resources/list`, `resources/templates/list`, and `prompts/list` at protocol 2025-11-25 by POSTing raw JSON-RPC at the production HTTP handler. No SDK type appears in the test, so it runs unchanged after the swap and any contract drift shows up as a golden diff.
- Extract `registerHandlers` so the goldens and `Run` share one registration path.
- Delete the obsolete `plans/mcp-go-v0.56-upgrade.*` pair.

### Phase 2 — SDK and transports
- `go.mod`: drop mcp-go, add `modelcontextprotocol/go-sdk v1.7.0`; consolidate on `google/jsonschema-go`.
- `mcp.NewServer` + `ServerOptions`; `NewStreamableHTTPHandler` with `Stateless: true`; `StdioTransport` for stdio. Drop the heartbeat option and `logging/setLevel` (GET/DELETE now 405), and update `scripts/test-mcp-protocol.sh`.
- Evaluate `MaxRequestBodyBytes` as the replacement for `maxBytesMiddleware`, and `PropagateRequestCancellation`.

### Phase 3 — Tools, resources, prompts
- Rewrite the ~362 option-builder call sites as explicit JSON Schemas; the four union-type `PropertyOption` hacks become plain schema literals.
- Port the registration funnel (`addTool`, duplicate guard, normalization) to `*jsonschema.Schema`, resource handlers to `*ReadResourceResult`, and swap the mcp-go types in the ~15 exported signatures directly.

### Phase 4 — Observability
- One receiving middleware for method observation, tool telemetry, and panic recovery; span ownership moves here.
- Re-derive client analytics from `req.ClientInfo()` / `req.ClientCapabilities()` / `req.ProtocolVersion()`; rewrite `NormalizeMCPMethod`'s allowlist as literal strings plus `server/discover` and `subscriptions/listen`.
- Re-map the mcp-go sentinel-error classification onto jsonrpc2 error codes.

### Phase 5 — Conformance and docs
- Rebuild the in-process test harness on `mcp.NewInMemoryTransports`.
- Regenerate the wire-catalog goldens only as a reviewed contract diff; confirm 2025-11-25 negotiation is byte-identical.
- Update `docs/architecture.md`, `CLAUDE.md`'s `mcp.WithInputSchema[T]` instruction, and state whether `SigNoz/agent-skills` needs a companion change.

## Files to Modify
- `internal/mcp-server/server.go` — server construction, transports, middleware, observability
- `internal/handler/tools/*.go` — schemas, registration funnel, decorators, resources
- `pkg/otel/mcp.go`, `pkg/prompts/prompts.go`, `pkg/toolerrors/errors.go`, `internal/docs/errors.go` — SDK types in signatures
- `go.mod`, `go.sum`, `scripts/test-mcp-protocol.sh`, `docs/architecture.md`

## Verification
`make fmt goimports`, `go build ./cmd/server`, `go test ./...`,
`go test -count=1 -run '^TestGuardrail_' ./...`,
`actionlint .github/workflows/guardrails.yaml`, and the protocol lane
(`scripts/test-mcp-protocol.sh`). The wire-catalog goldens must stay unchanged
across the swap; any diff is reviewed as a client-visible contract change.
