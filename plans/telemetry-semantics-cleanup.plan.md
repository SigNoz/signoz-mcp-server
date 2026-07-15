# Plan: Telemetry Semantics Cleanup

## Status
Done

## Context
Several analytics, log, metric, and trace signals either describe lifecycle state the stateless server does not own, expose unvalidated correlation values, or diverge from current MCP OpenTelemetry conventions. The owner selected the signals to remove and the correctness fixes to implement.

## Approach
1. Replace the misleading session-registration analytics event with a successful client-initialization event carrying `ClientInfo`; remove its session counter, lifecycle logs, Identify side effect, and session-specific fields.
2. Remove incoming `Mcp-Session-Id` from HTTP log and span attributes.
3. Replace the custom HTTP-only method-span ownership with mcp-go request tracing backed by a repository adapter that:
   - preserves the transport-provided parent context and does not extract trace context from MCP `_meta`;
   - emits semconv MCP server span names/attributes;
   - suppresses mcp-go's redundant internal tool span.
4. Decorate tool request spans as `tools/call {tool}`, omit synthetic tool-call IDs, and emit semconv `error.type` plus bounded structured `mcp.tool.error.code` on spans and metrics.
5. Classify method deadlines/cancellations before generic internal failures and preserve the actual context error through observation expiry.
6. Remove the docs index-age and fetcher-retry instruments and their tests.
7. Apply multi-agent review findings: bound unknown method telemetry, observe pre-handler tool failures, remove stale span ownership, centralize structured error codes, and configure docs duration buckets in seconds.

## Files to Modify
- `internal/mcp-server/server.go` — server tracing ownership, event removals, session-header removal, and error classification.
- `pkg/otel/mcp.go` — MCP tracer adapter.
- `pkg/otel/attr.go` — corrected MCP/tool attributes.
- `pkg/otel/metrics.go` — remove selected metrics.
- `pkg/log/http.go`, `pkg/util/http.go` — remove incoming session-header telemetry.
- `pkg/analytics/events.go` — define the client-initialized event and its client/protocol fields without session fields.
- `internal/docs/metrics.go` — stop recording index age.
- `pkg/toolerrors/errors.go` — shared structured tool-error taxonomy and extraction.
- Focused tests under `internal/mcp-server`, `internal/docs`, `pkg/otel`, `pkg/log`, and `pkg/util`.

## Verification
- `gofmt` on changed Go files — passed.
- Focused tests for client-initialization analytics, bounded span names/attributes, pre-handler tool failures, structured tool errors, method cancellation/timeout classification, docs histogram buckets, and removed metrics — passed.
- `go test ./...` — passed.
- `go vet ./...` — passed.
- `go test -race ./internal/mcp-server ./pkg/otel ./pkg/toolerrors` — passed.
- `git diff --check` — passed.
