# Feature: Official Go SDK Migration — Context & Discussion

## Original Prompt
> Migrate off `mark3labs/mcp-go v0.56.0` to `modelcontextprotocol/go-sdk v1.7.0`.
> Land the safety net first: a commit that proves the swap preserved the
> client-visible contract, without swapping the SDK.

## Reference Links
- [SigNoz/nerve-pod#194](https://github.com/SigNoz/nerve-pod/issues/194) — migration issue
- [modelcontextprotocol/go-sdk v1.7.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0) — target SDK, protocol 2026-07-28
- [mark3labs/mcp-go#928](https://github.com/mark3labs/mcp-go/issues/928) — upstream gap that motivates leaving mcp-go
- [`guardrails/README.md`](../guardrails/README.md) — wire-catalog goldens and CI mechanics
- [`plans/failure-request-observability.plan.md`](failure-request-observability.plan.md) — the observability surface this migration must preserve

## Key Decisions & Discussion Log

### 2026-08-12 — Migration decisions (Vishal Sharma)
- **Stateless HTTP GET/DELETE rejection is acceptable.** The official SDK's stateless mode answers GET and DELETE with 405. Nothing in this server relies on server-initiated notifications, so the listening stream and its 20 s heartbeat can go away with it.
- **`logging/setLevel` is dropped.** SEP-2577 deprecates the logging feature; the capability is not advertised by the official SDK. `scripts/test-mcp-protocol.sh` loses that assertion — in the migration commit, not before.
- **One JSON Schema library.** Consolidate on the SDK's `google/jsonschema-go` and retire `santhosh-tekuri/jsonschema/v6` from the validation decorator. Migration commit.
- **`EventClientInitialized` semantics may change.** No downstream consumer depends on the once-per-session `AfterInitialize` shape, so it can be re-derived per request from `req.ClientInfo()` / `req.ClientCapabilities()` / `req.ProtocolVersion()`.
- **`plans/mcp-go-v0.56-upgrade.*` is obsolete** and is deleted in the groundwork commit. Its verified mcp-go behaviours only describe the SDK we are leaving; the policies it established (fail-open validation, decorator-owned validation, no strict `additionalProperties`) are restated here.
- **2025-11-25 must keep working alongside 2026-07-28.** Clients pinned to 2025-11-25 are in the field; the wire-catalog goldens are captured at that revision so a negotiation regression fails CI.

### 2026-08-12 — Groundwork commit scope
- The existing budget test builds its catalog through `mcpclient.NewInProcessClient`, an mcp-go type that will not exist after the swap. The new `TestGuardrail_WireCatalogGoldens` instead POSTs hand-written JSON-RPC bodies at the production HTTP handler and pins the raw responses, so the same file runs unchanged against both SDKs.
- Registration moved from inline in `Run` to `registerHandlers`, giving the goldens the production catalog through a seam whose signature carries no SDK type.
- No alias/adapter package for the ~15 exported signatures that leak mcp-go types: the type swap happens directly in the migration commit to keep the diff lean.

## Open Questions
- [x] Does the official SDK still serve legacy `initialize`? Yes — `methodInitialize` stays in `serverMethodInfos` alongside `server/discover`, so 2025-11-25 clients keep negotiating.
- [x] Typed `mcp.AddTool[In, Out]` or low-level `Server.AddTool`? Low-level: the typed helper auto-validates input and shapes results, which contradicts the fail-open, never-reject policy the decorators implement.
- [x] Is panic recovery available? No. There is no `recover()` anywhere in the SDK, so `server.WithRecovery()` must be replaced by our own middleware.
- [x] Where does method observability go? A single `AddReceivingMiddleware` sees the start and end of every method, replacing the hooks plus the tombstone/exactly-once machinery.
