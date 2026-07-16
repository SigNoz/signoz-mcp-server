# Feature: Error Log Noise Reduction — Context & Discussion

## Original Prompt
> Production error-log analysis found two log-noise problems accounting for ~28% of all ERROR logs:
> 1. `request error: resources subscribe not supported` — 454 ERROR logs in 7 days. Clients call `resources/subscribe`; the server rejects it and the `buildHooks` error hook logs each rejection at ERROR. Ensure the capability advertisement does not declare subscribe support, and classify these rejections as expected protocol negotiation logged at DEBUG.
> 2. Client cancellations logged at ERROR — 410 logs in 7 days. When an MCP client disconnects mid-request, upstream calls fail with `context canceled` and tool handlers log "Failed to search logs" etc. at ERROR. Fix centrally: `errors.Is(err, context.Canceled)` → DEBUG; `context.DeadlineExceeded` stays ERROR.

## Reference Links
- mcp-go v0.56.0 `server/server.go` — `handleSubscribe` rejects with `fmt.Errorf("resources subscribe %w", ErrUnsupported)`; initialize advertises `resources.subscribe` only via `WithResourceCapabilities`.

## Key Decisions & Discussion Log
### 2026-07-17 — capability advertisement finding
- The server never calls `server.WithResourceCapabilities`. Registering resources via `AddResource` implicitly enables the resources capability with `subscribe: false` (omitted via `omitempty`). Advertisement was already correct; only the logging needed fixing. Pinned with a new initialize-result test so a future option change can't silently start advertising subscribe.

### 2026-07-17 — classification signals
- Subscribe rejection: matched on the typed sentinel `server.ErrUnsupported` (wrapped by mcp-go), not on message strings. This also covers `resources/unsubscribe` and any other unadvertised-capability rejection.
- Cancellation: `errors.Is(err, context.Canceled)` in a single shared helper `logpkg.LevelForError`. `context.DeadlineExceeded` deliberately stays ERROR — timeouts are operational signals.

### 2026-07-17 — where the classification lives
- `pkg/log.LevelForError(err)` is the single severity classifier.
- Hook path: `methodErrorLogLevel` (server.go) = `ErrUnsupported` → DEBUG, else `LevelForError`.
- Tool handlers: per-callsite logging was scattered across ~39 sites, so a shared `(*Handler).logUpstreamFailure(ctx, msg, err, attrs...)` was added in `errs.go` and substituted at every site that logs a failed ctx-bearing outbound call (SigNoz client calls + dashboard template fetch). Local marshal/parse/pagination failures keep plain `ErrorContext` — they cannot be context-canceled.
- The `loggingMiddleware` "tool call failed" branch also routes through `LevelForError` for handlers that return a Go error directly.
- Fail open, never fail silent: downgraded records are still emitted (at DEBUG, with a "(request cancelled by client)" note on the tool path), never dropped; tests assert emission.

## Open Questions
- [x] Was subscribe support being advertised? — No. `WithResourceCapabilities` is never called; implicit resource registration advertises `subscribe: false`. Only the logging needed fixing.
