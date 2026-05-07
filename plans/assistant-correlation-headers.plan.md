# Plan: Assistant Correlation Headers

## Status
In Progress

## Context
The SigNoz AI Assistant backend now sends three correlation headers on every outbound MCP call (see PR https://github.com/SigNoz/signoz-ai-assistant/pull/136):

- `X-SigNoz-Client-Source` — categorical (`ai-assistant`); default `user-client` when absent.
- `X-SigNoz-Assistant-Thread-Id` — UUID per assistant thread.
- `X-SigNoz-Assistant-Execution-Id` — UUID per assistant execution.

The MCP server emits its own logs, spans, metrics, and Segment analytics events (`MCP Tool: Called`, `MCP Session: Registered`, etc.). Without these correlation tags, assistant-driven traffic is indistinguishable from direct user-client traffic, which makes it impossible to (a) split usage analytics, (b) join MCP-side traces back to a specific assistant execution during incident triage, or (c) avoid double-counting when the assistant team builds user-facing usage dashboards.

This change consumes the headers and propagates them through the same observability pipeline that already handles `searchContext` and `sessionID`.

## Approach
Mirror the existing `searchContext` flow exactly. No new machinery — just three more context-borne fields plumbed through the same five surfaces. Also drop `searchContext` from analytics events as a side-quest cleanup (it stays on logs and spans).

1. **Context keys.** Add `clientSourceContextKey`, `assistantThreadIDContextKey`, `assistantExecutionIDContextKey` in `pkg/util/context.go` next to the existing keys. Each gets a `Get*` / `Set*` helper pair following the file's established pattern.

2. **Header read in `authMiddleware`.** In `internal/mcp-server/server.go` (around the existing `X-SigNoz-URL` / auth-header extraction in `authMiddleware`), pull the three headers off the request, normalize `X-SigNoz-Client-Source` to `"user-client"` when blank, and stash all three on the request context via the new setters before calling `r.WithContext(ctx)`.

3. **Auto-enrich slog records.** Extend the context-aware handler in `pkg/log/handler.go` so every emitted log line picks up `mcp.client_source`, `mcp.assistant.thread_id`, and `mcp.assistant.execution_id` when they are present in context. Free coverage for every existing slog call site — no per-call-site edits needed.

4. **Span attributes.** Define new attribute keys in `pkg/otel/attr.go` (dotted semconv form: `mcp.client_source`, `mcp.assistant.thread_id`, `mcp.assistant.execution_id`). Apply them at the existing `span.SetAttributes(...)` sites in `internal/mcp-server/server.go` — the tool-call span in `loggingMiddleware`, the hooks span, and the auth-middleware span.

5. **Metrics — `client_source` only.** Add `client_source` as an attribute to the OTel counters whose call sites can see the headers: `ToolCalls`, `ToolCallDuration`, `MethodCalls`, `MethodDuration`, `SessionRegistered`. A small helper in `pkg/otel/attr.go` (`AppendClientSource(ctx, attrs)`) keeps call sites tidy. Do **not** add the two UUID fields to any metric — per-execution cardinality is unbounded. **OAuth counters are excluded** — `/oauth/*` endpoints are browser-driven and never carry the assistant headers.

6. **Analytics properties.** Add `AttrClientSource`, `AttrAssistantThreadID`, `AttrAssistantExecutionID` constants in `pkg/analytics/events.go` (camelCase per the file's stated convention). Merge the three values into the property maps at the `trackEventAsync` sites for `EventToolCalled`, `EventSessionRegistered`, `EventPromptFetched`, `EventResourceFetched`. Skip the OAuth analytics events — same reason as the metrics exclusion. Same code shape as how `sessionID` is already merged.

7. **Drop `searchContext` from analytics.** Remove the `props[analytics.AttrSearchContext] = sc` injection in the tool-called analytics path in `internal/mcp-server/server.go`. Remove the `AttrSearchContext` constant in `pkg/analytics/events.go` if grep confirms it has no remaining consumers. `searchContext` continues to flow into logs (via the slog handler) and spans (via `MCPSearchContextKey`) as before.

8. **Async context preservation.** `detachedAnalyticsContext()` in `internal/mcp-server/server.go` already copies `searchContext` and `sessionID` forward into background goroutines. Add the three new keys to that copy so async analytics emissions keep the tags.

## Files to Modify
- `pkg/util/context.go` — three new context keys + `Get*`/`Set*` pairs.
- `internal/mcp-server/server.go`:
  - `authMiddleware` — read the three headers, default `client_source`, attach to ctx.
  - `loggingMiddleware` and hooks span sites — add the new span attributes.
  - Tool-called / session-registered / prompt / resource analytics property maps — merge the three new attrs.
  - Tool-called analytics property map — remove `props[analytics.AttrSearchContext] = sc`.
  - `detachedAnalyticsContext()` — extend ctx copy with the three new keys.
  - Metric attribute lists for `ToolCalls`, `ToolCallDuration`, `MethodCalls`, `MethodDuration`, `SessionRegistered` — add `client_source`.
- `pkg/log/handler.go` — emit the three new fields on every record when present in ctx.
- `pkg/otel/attr.go` — three new attribute key constants and an `AppendClientSource` helper for metrics.
- `pkg/analytics/events.go` — three new attribute constants; remove `AttrSearchContext` if unused after step 7.
- `internal/mcp-server/server_test.go` (or whichever existing test file covers `authMiddleware` and analytics emission) — header propagation, default-when-absent, metric attribute presence, analytics map shape (including absence of `searchContext`).
- `pkg/log/handler_test.go` — log enrichment when ctx carries the three keys.
- `README.md` — document the three honored request headers (under whatever section already covers `X-SigNoz-URL`).
- `manifest.json` / `docs/` — review per CLAUDE.md doc-sync checklist; likely no change since these are transport-level headers, not tool inputs.

## Verification
- `go test ./pkg/util ./pkg/log ./pkg/otel ./pkg/analytics ./internal/mcp-server`
- `go test ./...`
- Local end-to-end: run the MCP server, hit a tool with all three headers via `curl` (and with none), confirm:
  - JSON log lines for the request carry `mcp.client_source`, `mcp.assistant.thread_id`, `mcp.assistant.execution_id` when supplied; carry `mcp.client_source=user-client` when not.
  - The OTel span for the tool call carries the three attributes.
  - The `mcp_server_tool_calls_total` counter increments with a `client_source` label.
  - The Segment debug sink (or capture) shows `clientSource`, `assistantThreadId`, `assistantExecutionId` on `MCP Tool: Called`, and shows that `searchContext` is **no longer** an attribute.
- Coordinate one E2E sweep against the AI Assistant local instance (the sender PR documented all 3 paths — agent-driven, approval-snapshot, undo/replay) to confirm headers are received and tagged.
