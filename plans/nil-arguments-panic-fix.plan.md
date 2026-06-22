# Plan: Nil-Arguments Panic Fix

## Status
In Progress

## Context
Over the last 30 days, every production panic on the SigNoz MCP server was one class: an unchecked `req.Params.Arguments.(map[string]any)` type assertion in tool handlers. When a client invokes a tool with no `arguments` object, `req.Params.Arguments` is an untyped-nil interface and the bare assertion panics with `interface conversion: interface {} is nil, not map[string]interface {}`. The recover middleware caught it (the server stayed up), but the client got a generic error and a crash was logged.

## Approach
Replace the unguarded assertion with mcp-go's nil-safe `req.GetArguments()` at all 12 panic-prone sites. Comma-ok sites already handle the failure and are left as-is. Downstream of every changed site is read-only (no map writes), so `GetArguments()` returning `nil` is safe.

## Files to Modify
- `internal/handler/tools/alerts.go` — handleListAlerts, handleGetAlert, handleGetAlertHistory
- `internal/handler/tools/dashboards.go` — handleGetDashboard, handleDeleteDashboard
- `internal/handler/tools/services.go` — handleListServices, handleGetServiceTopOperations
- `internal/handler/tools/metrics.go` — handleListMetrics
- `internal/handler/tools/metrics_query.go` — handleQueryMetrics
- `internal/handler/tools/traces.go` — handleGetTraceDetails
- `internal/handler/tools/notification_channels.go` — handleCreateNotificationChannel, handleUpdateNotificationChannel
- `internal/handler/tools/nil_arguments_test.go` — unit regression test (untyped-nil `Arguments`), covering all 12 touched handlers: error-path (9 required-field incl. notification create/update) + success-path defaults (3 list tools)
- `internal/mcp-server/nil_arguments_e2e_test.go` — end-to-end test through the real in-process MCP pipeline (JSON marshal → server deserialize → recovery middleware → dispatch), proving a no-arguments `signoz_get_alert` returns a clean validation result instead of a recovered panic

## Verification
- `go build ./...`, `gofmt -l`, and `go vet ./internal/handler/tools/` clean.
- `go test ./internal/handler/tools/` passes.
- Unit regression test reproduces the exact production panic when the fix is reverted (verified by temporarily restoring the bare assertion → `interface conversion: interface {} is nil` panic), passes with the fix.
- End-to-end test through the real MCP transport reproduces the **byte-for-byte** production error when reverted (`internal error: panic recovered in signoz_get_alert tool handler: interface conversion: interface {} is nil, not map[string]interface {}`) and passes with the fix — confirming the fix addresses the real client-facing path, not just an isolated unit assumption.
- Codex review (gpt-5.5/xhigh): no runtime defect; complete and safe.
- No tool schema/name/description changes → README.md and manifest.json unchanged.
