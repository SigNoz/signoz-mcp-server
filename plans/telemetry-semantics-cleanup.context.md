# Feature: Telemetry Semantics Cleanup — Context & Discussion

## Original Prompt
> Review the telemetry audit decisions and implement the selected changes:
> - Drop `MCP Session: Registered` instead of renaming it.
> - Drop incoming `Mcp-Session-Id` from logs and spans.
> - Fix MCP `_meta.traceparent` propagation.
> - Explain and implement the correct OpenTelemetry semantics for tool spans.
> - Fix tool error classification to use structured error codes and expose useful operational attributes.
> - Drop `signoz_docs_fetcher_retries_total`.
> - Drop `signoz_docs_index_age_seconds`.
> - Classify MCP method cancellations and deadlines correctly.

## Reference Links
- [OpenTelemetry MCP semantic conventions](https://raw.githubusercontent.com/open-telemetry/semantic-conventions-genai/main/docs/gen-ai/mcp.md)
- [SEP-414 request metadata trace propagation](https://modelcontextprotocol.io/seps/414-request-meta)
- [mcp-go v0.56.0 server tracing source](https://github.com/mark3labs/mcp-go/blob/v0.56.0/server/tracing.go)

## Key Decisions & Discussion Log

### 2026-07-15 — Owner-selected telemetry cleanup
- Drop the initialization-derived analytics event and counter; do not replace them with renamed client-initialization signals.
- Drop session lifecycle hook logs and their analytics Identify side effect along with the event. Product events continue resolving identity independently before Track calls.
- Stop treating the unvalidated incoming `Mcp-Session-Id` HTTP header as an observability correlation value. Actual SDK session context remains available where the transport genuinely has one.
- Adopt MCP `_meta` W3C trace context as the remote parent for MCP server spans and link the ambient HTTP transport span when both exist.
- Use one MCP `SERVER` span per request. Tool calls use span name `tools/call {gen_ai.tool.name}`, required `mcp.method.name=tools/call`, `gen_ai.operation.name=execute_tool`, and `gen_ai.tool.name`. Do not synthesize `gen_ai.tool.call.id`; the JSON-RPC request ID is a separate semantic field and is not exposed to the tool middleware.
- For `CallToolResult{isError:true}`, keep semconv `error.type=tool_error` and add the bounded structured application code as custom `mcp.tool.error.code`. Product analytics uses the structured code instead of display-text parsing, with the legacy classifier only as fallback.
- Remove the docs retry counter and index-age gauge entirely.
- Preserve `context.Canceled` and `context.DeadlineExceeded` through method observation cleanup so they become `cancelled` and `timeout`, not `internal`.

## Open Questions
- [x] Rename the dropped session event/metric? → No; remove both.
- [x] Keep the raw incoming session header under a diagnostic name? → No; drop it.
- [x] What is the correct tool span? → One MCP `SERVER` span named `tools/call {tool}` with MCP and GenAI attributes; no fabricated tool-call ID.
- [x] Keep the docs age/retry instruments? → No; remove them.

### 2026-07-15 — Trace propagation excluded
- The owner explicitly excluded extracting `traceparent` / `tracestate` from MCP `params._meta` from this change.
- Keep the corrected MCP request/tool span naming and attributes, but preserve the existing transport-owned parent context and do not register an MCP metadata propagator.

### 2026-07-15 — Structured tool errors are authoritative
- Product analytics no longer infers an error category from user-facing text. Known structured result codes are authoritative; an error result without one is classified only as `tool_error`.

### 2026-07-15 — Reason for excluding MCP metadata parenting
- The owner noted that an instrumented agent may not export its telemetry to the same SigNoz Cloud tenant. Parenting MCP server spans to the agent's `_meta.traceparent` would then leave the ingested MCP trace without its remote root.
- Preserve locally complete MCP traces. If cross-agent correlation is added later, evaluate an opt-in span link instead of unconditional remote parenting.

### 2026-07-15 — Preserve initialization client metadata
- The dropped registration event was the only Mixpanel event carrying MCP `ClientInfo` name/version.
- Restore the successful-initialize Track event as `MCP Client: Initialized`. It reports client metadata without claiming that a durable session was registered.
- Do not restore `mcp.session.registered`, session IDs, session lifecycle logs, or the profile-level Identify side effect.

### 2026-07-15 — Multi-agent review remediation
- Bound MCP method span names and `mcp.method.name` to the protocol's known method set; record unrecognized methods as `unknown` so client-controlled JSON-RPC methods cannot create unbounded telemetry cardinality.
- Observe `tools/call` failures that occur before registered tool middleware runs, using the bounded tool name `unknown` and the same metrics, analytics, and span error semantics as handler-observed calls.
- Remove manual `End` calls for spans owned by the mcp-go tracing lifecycle.
- Move the known structured tool-error taxonomy and extraction into a shared package used by producers and telemetry consumers.
- Add explicit second-based histogram buckets for docs search and refresh durations.
