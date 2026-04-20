# Feature: Observability Refactor — Context & Discussion

## Original Prompt
> Explain current observability setup and suggest improvements for it. We want to keep it otel native.
>
> https://signoz.io/docs/instrumentation/opentelemetry-golang/
> https://signoz.io/blog/mcp-observability-with-otel/
>
> Also explore zeus for log and observability setup: /Users/makeavish/signoz/zeus/pkg/log/log.go
> I'm fine with updating the large files, but I want a observability setup similar to Zeus and uses best practices.

## Reference Links
- [SigNoz Go instrumentation](https://signoz.io/docs/instrumentation/opentelemetry-golang/)
- [MCP observability with OTel](https://signoz.io/blog/mcp-observability-with-otel/)
- Zeus log pkg: `/Users/makeavish/signoz/zeus/pkg/log/{log,handler}.go`
- Zeus otel pkg: `/Users/makeavish/signoz/zeus/pkg/otel/{tracer,meter,resource}.go`
- Zeus run orchestration: `/Users/makeavish/signoz/zeus/cmd/common/run.go`

## Key Decisions & Discussion Log

### 2026-04-19 — Initial audit of current state
Current setup uses zap (JSON to stdout), OTLP gRPC for traces and metrics, otelhttp middleware for HTTP spans, GenAI semconv on a single `execute_tool` span. Gaps:
- No OTel logs signal; zap child loggers built at 188 call sites manually injecting trace_id/span_id/session/tenant.
- No graceful shutdown on SIGINT/SIGTERM — OTLP batches can be lost.
- No custom metrics (tool calls, active sessions, oauth events).
- No runtime metrics.
- No `service.version` on resource (only what `OTEL_RESOURCE_ATTRIBUTES` provides).
- Errors do not auto-mark spans `codes.Error` unless done by hand in middleware.

### 2026-04-19 — Alignment with Zeus
Zeus's `pkg/log.ContextHandler` is the key pattern: slog handler that auto-injects trace_id/span_id, marks spans `codes.Error` on `>=Error` records, and captures stacktrace on error. This eliminates the boilerplate `requestLogger`/`tenantLogger` we have today. Decision: migrate zap → slog with a Zeus-style ContextHandler.

### 2026-04-19 — Log shipping mechanism
Confirmed Zeus ships logs as **JSON to stdout**, not OTLP. No `otlplog`/`otelslog`/`NewLoggerProvider` in Zeus. The pattern assumes an OTel Collector scrapes stdout, stitches logs to traces via the trace_id/span_id fields. Decision: mirror Zeus — no direct OTel log exporter, just stdout JSON + ContextHandler. Simpler binary, no blocking dep on OTLP endpoint, composable with k8s log pipelines.

### 2026-04-19 — Rollout shape
Originally considered 3 PRs (restructure / zap→slog / custom meters). User requested all three as **three commits in a single PR** to keep the migration atomic. Each commit must build and pass tests independently so the PR is still bisectable.

### 2026-04-19 — Codex round 1 review (gpt-5.4, xhigh, fast)
Codex flagged 8 issues. Resolutions folded into the plan:
1. Commit 1 was not atomic (created `pkg/log` + deleted telemetry while zap stayed). → Commit 1 now keeps `internal/telemetry` as a thin shim; `pkg/log` moves to commit 2.
2. `Start()` has no shutdown handle. → Added: refactor `MCPServer` to `Run(ctx)`+`Shutdown(ctx)`; keep `*http.Server` on the struct.
3. `mcp.sessions.active` is broken because streamable-HTTP POST-only clients don't fire unregister. → Dropped from commit 3; replaced with a `mcp.session.registered` counter.
4. Deleting `requestLogger`/`tenantLogger` drops tenant/session/search-context from logs. → `ContextHandler` now explicitly reads `util.GetSigNozURL/SessionID/SearchContext` from ctx in commit 2.
5. Shutdown order was reversed and used cancelled ctx. → Strict order: HTTP drain → analytics WaitGroup → analytics.Stop → meter → tracer; fresh `context.Background()+15s` timeout.
6. `service.version` needs goreleaser ldflags + hard-coded `"0.0.1"` in `NewMCPServer` still wrong. → Goreleaser gets `-X .../pkg/version.Version={{.Version}}`; `NewMCPServer` uses `version.Version`; integration test reads same.
7. `WithRecovery()` wraps logging middleware, so panicking tools bypass metrics. → `loggingMiddleware` becomes outermost with its own `defer recover()` that records metrics/span status before re-raising.
8. Log bodies unbounded; stacktraces could blow up hot paths. → Added 4 KiB body cap helper; stacktrace depth 32 (half Zeus), `LOG_STACKTRACE=false` to disable.

Also resolved from plan/context mismatch:
- Noop providers for local/unknown env → **skip** (OTLP fails gracefully).
- Metrics container → typed `*otel.Meters` struct injected, not package globals.
- Zeus `pkg/http/client/plugin/log.go` + separate status server → **out of scope** (would require redaction; `/healthz` already covers health).

### 2026-04-19 — Codex round 2 review (gpt-5.4, xhigh, fast)
After round-1 revisions, Codex flagged 3 more blockers:
1. `ServeStdio` has its own `context.Background()` and signal handler (verified in mcp-go stdio.go:572-589), so `Run(ctx)` cannot stop it. → Switched stdio to `NewStdioServer(s).Listen(ctx, os.Stdin, os.Stdout)` with `WithStdioContextFunc` set via the constructor.
2. Panic-handling story was inconsistent (three mutually incompatible suggestions). → Committed to a single design: keep `server.WithRecovery()` as INNER middleware; `loggingMiddleware` is OUTERMOST; recovery converts panic to error that bubbles out; logging sees err via normal return and records metrics + span error. No second `recover()` in logging.
3. `mcp.session.registered` was referenced but not defined in the `Meters` struct. → Added `SessionRegistered metric.Int64Counter` to the struct; wired in `AddAfterInitialize` hook (only fires on successful initialize).

### 2026-04-19 — Codex round 3 review (gpt-5.4, xhigh, fast)
APPROVED. Residual watch items during implementation:
- Make stdio context hook application match actual mcp-go API shape.
- Add a real test for panic path — validate middleware order behaves correctly under mcp-go's reverse-wrapping semantics.

### 2026-04-20 — Claude agent review of shipped code
Three blockers found and fixed before opening PR:
1. Middleware order was inverted (`WithRecovery` first → outermost). mcp-go applies tool-handler middlewares in reverse-slice order, so first-appended is outermost. Fix: swap options so `WithToolHandlerMiddleware(loggingMiddleware)` comes first. Production chain is now `logging(recovery(tool))`; panic surfaces as error to logging and records metrics + span error.
2. Panic test was self-recovering (wrapped only loggingMiddleware around a handler that recovered its own panic). Rebuilt to mirror the real composition: `loggingMiddleware(recovery(panicTool))` where `recovery` is a local function matching `server.WithRecovery()`'s body exactly. Asserts via in-memory tracer + manual metric reader.
3. `runStdio` used `WithStdioContextFunc(fn)(stdio)` — valid but brittle (reads as a no-op; a future cleanup could accidentally drop env-credential injection). Swapped to `stdio.SetContextFunc(fn)`, mcp-go's idiomatic setter.

Additional hygiene: added `pkg/log/handler_test.go` with 5 tests pinning ContextHandler behavior — ctx-reading of tenant/session/search, trace/span ID injection, stacktrace on Error by default, `LOG_STACKTRACE=false` toggle, and `TruncBody` bounds.

### 2026-04-20 — Naming sanity pass
- `MCPServer.httpSrv` → `httpServer` (clearer and matches local `httpServer` var convention elsewhere).
- Local `httpServer := server.NewStreamableHTTPServer(s)` in `buildHTTP` → `mcpHandler`, since it's not a `*http.Server` — disambiguates from the struct field.
- `otel.GenAISystemMCP` → `otel.GenAISystemValueMCP`: it's a value paired with `GenAISystemKey`, so the `Value` suffix keeps key/value naming consistent.

### 2026-04-20 — Codex xhigh final review
Two real findings beyond the agent pass:

### 2026-04-20 — Remaining observability cleanup pass
- Hardened `methodSpanMiddleware` against oversized JSON-RPC bodies by capping reads at 1 MiB and returning HTTP 413 instead of buffering unbounded request bodies.
- Moved method-observation timeout tuning from a package-global test override to per-server fields so future `t.Parallel()` usage cannot race on shared state.
- Added a true concurrent finish-vs-expire race test plus stronger assertions for timeout warning logs and span exception event names/messages.
- Deduplicated OTel metric test helpers into `internal/testutil/oteltest` and replaced brittle hook-slice indexing with `singleHook(...)` helpers.
- Expanded client retry tests to cover eventual success after retry and to assert non-retryable 4xx responses never emit retry-only logs or `retries_exhausted`.

### 2026-04-20 — Cardinality and timeout alignment follow-up
- Tightened `methodSpanMiddleware` to whitelist only known MCP request methods before starting a span, preventing attacker-controlled span-name cardinality on unknown JSON-RPC methods.
- Interactive OAuth authorize-submit failures now feed the same `mcp.oauth.failures` counter and structured failure log path as JSON OAuth errors, so credential-entry mistakes show up in dashboards and alerts.
- Non-tool method spans are no longer ended at raw HTTP-handler return. They now stay open until hook success/error completion or timeout cleanup, which keeps timeout traces aligned with `mcp.method.calls{error.type=internal}` metrics in production.
1. 5 payload-logging sites in `internal/handler/tools/*.go` bypass the 4 KiB cap (`metrics_query.go`, `alerts.go`, `logs.go`, `dashboards.go`, `notification_channels.go`). Added `pkg/log.TruncAny` helper that marshals an `any` to JSON and applies `TruncBody`; wrapped all five sites.
2. Plan text no longer matched shipped design after the middleware-order fix and the decision to start `runtime.Start()` from main.go rather than meter.go. Plan updated in-place to reflect what shipped.

### 2026-04-20 — Review-finding follow-up
Follow-up fixes for the PR review findings:
1. Handled MCP tool failures were invisible at the default `info` log level because `loggingMiddleware` only logged completion at `Debug`. Updated it to emit `Warn` for `result.IsError` and `Error` for returned Go errors, while keeping successful completions at `Debug`.
2. OAuth failures had no structured telemetry. `writeOAuthError` now logs every OAuth failure with status/code/method/path and increments `mcp.oauth.failures{oauth.error_code,http.response.status_code}`.
3. Retry attempts in the SigNoz client were over-leveled as warnings. Intermediate retry attempts are now `Debug`; only the terminal exhausted-retry outcome is `Warn`.
4. Non-tool MCP methods still lacked first-class spans and metrics. Added hook-layer spans plus `mcp.method.calls` / `mcp.method.duration` for non-tool methods.

### 2026-04-20 — Cleanup pass after agent review
Claude's follow-up comments uncovered two real cleanup items and several design nits:
1. The non-tool method observation map could leak on panics because mcp-go only recovers tool handlers. Since hooks cannot replace the request context, the fix is a bounded timeout cleanup for hook-started method spans rather than a ctx-attached defer.
2. `writeOAuthError(nil, ...)` dropped request context on a few server-error paths. The handler signatures were adjusted so OAuth failures always log and emit metrics with the real request context.
3. The custom `mcp.method.is_error` attribute was removed in favor of semconv-aligned `error.type` on method spans and metrics.
4. Zero-duration session register/unregister spans were removed; the remaining signal is the session counter plus structured logs.
5. Retry terminal logging in the SigNoz client was collapsed into a single warn branch with retry metadata, and tests were added for the new warn/error log branches across MCP, OAuth, and client retries.

### 2026-04-20 — Method span propagation fix
Claude found one real follow-up regression in the non-tool method tracing path: the hook-created `MCP <method>` span was being started inside `BeforeAny` but could not be propagated into downstream handler context because mcp-go hooks do not return a modified ctx. The fix moved non-tool method span creation to an HTTP middleware that parses the JSON-RPC method before `HandleMessage` runs and reattaches the request context with the method span already active. The hooks now decorate and finalize the active method span instead of creating a detached one.

### 2026-04-20 — Fresh-eyes cleanup pass
The next review pass surfaced a few smaller but real issues:
1. Non-tool errors were being recorded twice on the same method span: once in `completeMethodObservation` and again in `AddOnError`. The hook now only records span errors for methods that do not use method-observation finishing.
2. `retries_exhausted` in the SigNoz client logs was misleading on non-retryable 4xx responses. The field is now only emitted for retryable terminal failures.
3. Method-span attributes were being redundantly restamped. The method span now gets `mcp.method.name` and tenant URL at start, session ID in `BeforeAny`, and `error.type` at completion.
4. Added deterministic regression tests for late-expire-after-finish and late-finish-after-expire so the timer cleanup path stays idempotent.

## Open Questions
_(none — plan approved by Codex after 3 rounds; post-ship review findings addressed)_
