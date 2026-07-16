# Plan: Error Log Noise Reduction

## Status
Done

## Context
Two expected client behaviors were logged at ERROR, together ~28% of production ERROR volume:
1. `resources/subscribe` rejections (454/7d) — the server does not implement resource subscription (and correctly does not advertise it), but the `buildHooks` OnError hook logged every rejection at ERROR.
2. Client cancellations (410/7d) — MCP client disconnects surface as `context canceled` from upstream SigNoz calls, and every tool handler logged them at ERROR.

## Approach
- `pkg/log.LevelForError(err)`: shared classifier — `context.Canceled` → DEBUG, everything else (including `context.DeadlineExceeded`) → ERROR.
- Hook path (`internal/mcp-server/server.go`): `methodErrorLogLevel(err)` maps mcp-go's typed `server.ErrUnsupported` (wrapped by the subscribe/unsubscribe rejections) to DEBUG, otherwise defers to `LevelForError`; the OnError hook logs "mcp error" via `logger.Log(ctx, level, ...)`. The middleware "tool call failed" branch (Go-error path) also uses `LevelForError`.
- Tool handlers (`internal/handler/tools`): new `(*Handler).logUpstreamFailure(ctx, msg, err, attrs...)` in `errs.go` picks the level via `LevelForError`, appends "(request cancelled by client)" on the DEBUG path, and always emits. Substituted at all 39 upstream-call ERROR sites; local marshal/parse failures unchanged.
- Capability advertisement: already correct (no `WithResourceCapabilities`; implicit `subscribe: false`). Pinned with a test on the initialize result.

## Files to Modify
- `pkg/log/log.go` — add `LevelForError`.
- `internal/mcp-server/server.go` — `methodErrorLogLevel`; OnError hook and "tool call failed" log level selection.
- `internal/handler/tools/errs.go` — `logUpstreamFailure` helper.
- `internal/handler/tools/{alerts,dashboards,fields,logs,metric_usage,metrics_query,metrics_top,metric_cardinality,notification_channels,query_builder,metrics,services,traces,views}.go` — route upstream-call failure logs through the helper.
- `pkg/log/level_test.go`, `internal/handler/tools/log_level_test.go`, `internal/mcp-server/log_noise_test.go` — tests.

## Verification
- `go build ./...`, `go vet ./...`, `go test ./...` all pass.
- `TestInitializeDoesNotAdvertiseResourceSubscribe` asserts the initialize result advertises resources without `subscribe: true`.
- `TestBuildHooks_ErrorLogSeverityClassification` drives the OnError hook: subscribe-unsupported and canceled → DEBUG (and still emitted), deadline-exceeded and generic → ERROR.
- `TestLogUpstreamFailureLevels` / `TestLevelForError` pin the helper contract.
