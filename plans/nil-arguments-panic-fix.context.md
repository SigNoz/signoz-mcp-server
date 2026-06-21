# Feature: Nil-Arguments Panic Fix — Context & Discussion

## Original Prompt
> Fix the bug, get review from codex

Follow-up to a panic investigation: "Explore more MCP server panics and crashes ... categorize/list issues", which found that 30 days of production panics were all one class — nil-map interface-conversion panics in the `signoz_get_alert` and `signoz_get_dashboard` tool handlers.

## Reference Links
- Dashboard "SigNoz MCP Server" (panic panel): https://nightswatch.signoz.cloud/dashboard/019e44b4-0cee-7e38-8290-5b438baf3f34
- Production panic signature: `interface conversion: interface {} is nil, not map[string]interface {}`

## Key Decisions & Discussion Log
### 2026-06-21 — root cause + scope
- Root cause: an unchecked `req.Params.Arguments.(map[string]any)` assertion. In the reported handlers the `, ok` guarded the *outer* `.(string)`, leaving the inner map assertion bare — so when a client calls a tool with no `arguments` object, `req.Params.Arguments` is an untyped-nil interface and the bare assertion panics.
- Scope: 12 panic-prone sites total, not just the 2 that fired in the 30d window — 9 bare `args := req.Params.Arguments.(map[string]any)` and 3 compound `x, ok := …(map[string]any)[key]`. Comma-ok sites (e.g. `handleAggregateLogs`, `handleCreateDashboard`, the views handlers) already guard `if !ok { return }` and were left untouched.
- Fix: replace the unguarded assertion with the library's nil-safe `req.GetArguments()` (mcp-go v0.49.0).
- Subtlety verified: `GetArguments()` returns `nil` (not an empty map, despite its doc comment) on non-map input. Reads on a nil map are safe in Go; only writes panic. Confirmed all 12 changed sites are read-only downstream (the only map writers — `views.go`, `aggregate_helper.go:resolveTimestamps` — are reached only via comma-ok-guarded handlers).
- Behavior: list-style tools (optional args) now fall through to their defaults path; required-field tools return their existing validation error instead of panicking. No tool schema/name/description change → no README/manifest doc-sync needed.
- Test: added `requestWithNilArguments` (untyped-nil `Arguments`) regression test. Proved it reproduces the exact production panic when the fix is reverted, and passes with the fix.

### 2026-06-21 — Codex review (gpt-5.5 / xhigh)
- Verdict: **no runtime defect; fix complete and safe.** Codex independently confirmed: no remaining panic-prone sites in `internal/`; nil-map reads are safe at every changed site; the only map-writer (`resolveTimestamps`, aggregate_helper.go) is reached only after `metricName` validation on the `query_metrics` path; list tools still flow to defaults with no args.
- One LOW finding: the regression test covered only the 7 required-field handlers, not the 5 list/create/update sites. Resolved: extended the test to all 12 touched handlers — added create/update notification channel to the error-path table, and a new `TestListHandlers_NilArguments_UseDefaults` that locks in list-tool defaults (list_alerts / list_services / list_metrics succeed with nil args rather than erroring).
- `go build ./...`, `go vet`, and `go test ./internal/handler/tools/` all green.

### 2026-06-21 — end-to-end verification (does this fix the real user-facing bug?)
- Question raised: the unit test constructs the request struct directly, so it only proves our handler survives a nil `Arguments` — it assumes the real transport produces nil `Arguments` for a no-args call. Closed that gap with an e2e test through the real in-process MCP pipeline (client JSON-marshal → server `HandleMessage` deserialize → recovery middleware → dispatch), recovery enabled exactly as in prod.
- Result: with the bug reverted, the e2e test reproduces the **byte-for-byte production error** — `internal error: panic recovered in signoz_get_alert tool handler: interface conversion: interface {} is nil, not map[string]interface {}` — and with the fix it returns a clean `ruleId` validation result. This confirms the fix resolves the actual client-facing path, not just a unit-level assumption.
- Caveat: we still cannot replay tenant `momentic.us`'s exact request, and the fix is verified against the real transport but not yet on the live deployment (can only confirm post-deploy).

## Open Questions
- [x] Codex review feedback — **resolved**: verdict was "sound and complete"; the one LOW test-coverage gap was closed by extending the regression test to all 12 handlers.
