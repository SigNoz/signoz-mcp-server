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
1. 5 payload-logging sites in `internal/handler/tools/*.go` bypass the 4 KiB cap (`metrics_query.go`, `alerts.go`, `logs.go`, `dashboards.go`, `notification_channels.go`). Added `pkg/log.TruncAny` helper that marshals an `any` to JSON and applies `TruncBody`; wrapped all five sites.
2. Plan text no longer matched shipped design after the middleware-order fix and the decision to start `runtime.Start()` from main.go rather than meter.go. Plan updated in-place to reflect what shipped.

## Open Questions
_(none — plan approved by Codex after 3 rounds; post-ship review findings addressed)_
