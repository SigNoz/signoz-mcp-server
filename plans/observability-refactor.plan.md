# Plan: Observability Refactor

## Status
Done

## Context
The MCP server's observability has three gaps relative to Zeus's pattern:

1. Logs are zap JSON to stdout with manual trace/session/tenant injection at ~188 call sites (no ContextHandler).
2. No custom metrics — only the auto `otelhttp` server metrics. Tool calls and OAuth events are not measured.
3. No graceful shutdown, no runtime metrics, no `service.version` on the resource. OTLP batches can be dropped on pod eviction.

Goal: match Zeus's shape — slog + ContextHandler + `pkg/otel` — so the MCP server gets the same log/trace correlation, custom metrics, and lifecycle behavior as every other SigNoz Go service.

## Cross-Cutting Decisions (apply to all commits)

- **Logs ship as JSON on stderr**. No direct OTLP log exporter. This keeps stdout reserved for MCP stdio protocol frames while remaining compatible with container log collection.
- **OTLP always on** for traces + metrics — no `deployment.environment` noop gating. Exporters fail gracefully at connect time with a single warning log. (Simpler than Zeus; acceptable because a missing OTLP endpoint just means no data, not crashes.)
- **Metrics container**: a typed `*otel.Meters` struct instantiated in `main` and injected into `MCPServer`. No package-level globals — simpler to test, explicit dependencies.
- **Redaction / size caps**: any log field that carries a request body, response body, or error payload is capped at 4 KiB and truncated with a `...(truncated)` suffix. Existing "log detailed errors for LLM" behavior stays (per `feedback_security_scope`), but unbounded bodies don't.
- **Stacktraces on Error**: `ContextHandler` captures stacktraces, but depth is capped at 64 frames (matches Zeus). Configurable via `LOG_STACKTRACE=false` env to disable entirely for hot error paths.

## Approach

Single PR, three commits. Each commit compiles and tests green independently so the PR is bisectable.

### Commit 1 — OTel scaffolding + graceful lifecycle (no logger migration)

Introduce `pkg/otel`, `pkg/version`, and refactor the server for graceful shutdown. **Zap stays.** No `pkg/log` yet.

**New packages:**
- `pkg/otel/resource.go` — `NewResource(ctx, serviceVersion)` with `WithFromEnv/Host/Process/Container/TelemetrySDK` + `semconv.ServiceVersion(version.Version)`.
- `pkg/otel/tracer.go` — `InitTracerProvider(ctx, res)` — OTLP gRPC batched.
- `pkg/otel/meter.go` — `InitMeterProvider(ctx, res)` — OTLP periodic reader. Runtime metrics polling (`go.opentelemetry.io/contrib/instrumentation/runtime`) is started from `cmd/server/main.go` after the meter provider is installed, so it records against the global provider.
- `pkg/otel/attr.go` — moved from `internal/telemetry/genai.go`. Same constants (`GenAISystemKey`, `MCPMethodKey`, …).
- `pkg/version/version.go` — `var Version = "dev"`. Overridable via `-ldflags "-X github.com/SigNoz/signoz-mcp-server/pkg/version.Version=$VERSION"`.

**Keep (temporary, removed in commit 2):**
- `internal/telemetry/telemetry.go` — `NewLogger`, `LogLevel`, `LoggerWithURL` remain so commit 1 doesn't touch `*zap.Logger` call sites.
- `internal/telemetry/telemetry.go` re-exports the new OTel init funcs: `InitTracer` and `InitMeterProvider` become thin wrappers over `pkg/otel.*` so main.go changes minimally.

**Delete:**
- `internal/telemetry/genai.go` (moved to `pkg/otel/attr.go`).
- Update all call sites that used `telemetry.GenAI*` / `telemetry.MCP*` to use `otel.GenAI*` / `otel.MCP*`.

**Graceful shutdown — server API change:**
- `internal/mcp-server/server.go`:
  - Replace `Start() error` with `Run(ctx context.Context) error` + `Shutdown(ctx context.Context) error`.
  - HTTP: `startHTTP` becomes `buildHTTP` returning an `*http.Server` stored on `MCPServer.httpSrv`. `Run` calls `httpSrv.ListenAndServe`; `Shutdown` calls `httpSrv.Shutdown`.
  - Stdio: **do not call `server.ServeStdio`** — it creates its own `context.Background()` and installs its own SIGINT/SIGTERM handler ([mcp-go stdio.go:572-589](/Users/makeavish/go/pkg/mod/github.com/mark3labs/mcp-go@v0.38.0/server/stdio.go)), so the parent ctx cannot cancel it. Use `server.NewStdioServer(s)` + `stdio.Listen(ctx, os.Stdin, os.Stdout)` with the `WithStdioContextFunc` option set via the constructor. `Listen` exits when our ctx cancels; `Shutdown` is a no-op for stdio.
  - **Panic path (single owner)**: keep `server.WithRecovery()` as the *inner* middleware (library-provided). Register middleware so `loggingMiddleware` wraps recovery (outermost). Recovery converts a panic into an error `result` that bubbles out of the inner chain; `loggingMiddleware` sees the error via the normal return path and records `mcp.tool.calls{is_error=true}` + span `codes.Error`. No second `recover()` in logging middleware. mcp-go applies tool-handler middlewares in **reverse-slice order** (first-appended wraps outermost), so the implementation appends `WithToolHandlerMiddleware(loggingMiddleware)` **before** `WithRecovery()` in `NewMCPServer` options — this yields `logging(recovery(tool))` as intended. (Shipped in `internal/mcp-server/server.go`.)
- `cmd/server/main.go`:
  - `ctx, stop := signal.NotifyContext(context.Background(), SIGINT, SIGTERM)`.
  - Start `srv.Run(ctx)` in a goroutine via `errgroup.Group`.
  - On signal / error, run shutdown with a **fresh `context.WithTimeout(context.Background(), 15*time.Second)`** — never reuse the cancelled signal ctx.
  - **Shutdown order** (strict):
    1. `srv.Shutdown(shutCtx)` — stop accepting new requests, drain in-flight.
    2. Wait briefly (`analyticsAsyncTimeout` = 5s) for detached analytics goroutines. A `sync.WaitGroup` on `dispatchAnalytics` makes this exact rather than a sleep.
    3. `analytics.Stop(shutCtx)` — drain buffered Segment events.
    4. `meterProvider.Shutdown(shutCtx)` — flush last metric batch.
    5. `tracerProvider.Shutdown(shutCtx)` — flush last span batch.
- `analytics.Analytics` gets no API change; we track in-flight dispatches via a new `MCPServer.analyticsWG sync.WaitGroup` that `dispatchAnalytics` Add/Done's.

**Version plumbing:**
- `server.NewMCPServer("SigNozMCP", version.Version, ...)` — replaces hard-coded `"0.0.1"` at [server.go:263](internal/mcp-server/server.go#L263).
- Integration test still pins an expected version by reading `version.Version` at test time.
- `Makefile` `build` target adds `-ldflags "-X github.com/SigNoz/signoz-mcp-server/pkg/version.Version=$(VERSION)"` (VERSION defaults to `git describe --tags --always --dirty 2>/dev/null || echo dev`).
- `.goreleaser.yaml` — add `ldflags: -s -w -X github.com/SigNoz/signoz-mcp-server/pkg/version.Version={{.Version}}` under `builds[0]` (same convention Goreleaser uses; `{{.Version}}` is the tag without leading `v`).

**Commit 1 compiles because:**
- Zap is untouched.
- `internal/telemetry` still exists; it just delegates into `pkg/otel`.
- All GenAI/MCP attribute constants preserved under a new import path (aliased or updated via sed).

### Commit 2 — zap → slog with ContextHandler

Replace zap everywhere. Delete hand-rolled correlators. Delete `internal/telemetry` entirely.

**New packages:**
- `pkg/log/log.go` — `New(level string) *slog.Logger`. JSON handler to stdout, `code`/`timestamp` rename (Zeus-style).
- `pkg/log/handler.go` — `ContextHandler` wraps any base slog.Handler and auto-attaches, from ctx:
  - `trace_id`, `span_id` (from OTel SpanContext)
  - `mcp.tenant_url` (from `util.GetSigNozURL`)
  - `mcp.session.id` (from `util.GetSessionID`)
  - `mcp.search_context` (from `util.GetSearchContext`)
  - On `>=Error` records: sets `codes.Error` status on the active span, captures stacktrace (depth 64, env-toggleable) under `exception.stacktrace` group.
  - `ErrAttr(err) slog.Attr` helper.
- This handler reads tenant/session/search-context **from ctx**, so deleting `requestLogger`/`tenantLogger` does not drop those fields from logs.

**Migration scope (14 files):**
- `cmd/server/main.go`, `internal/mcp-server/{server,server_test,integration_test}.go`, `internal/client/{client,client_test}.go`, `internal/handler/tools/*.go` (metrics, logs, traces, alerts, dashboards, services, fields, query_builder, notification_channels, resource_templates, metrics_query, handler, testutil_test), `internal/oauth/{handlers,handlers_test}.go`, `pkg/analytics/segmentanalytics/{provider,logger}.go`.
- Mechanical swaps: `*zap.Logger` → `*slog.Logger`, `zap.String(k,v)` → `slog.String(k,v)`, `zap.Error(err)` → `log.ErrAttr(err)`, `logger.Info(msg, fields...)` → `logger.InfoContext(ctx, msg, fields...)` wherever ctx is in scope.
- Delete `(s *SigNoz).requestLogger` — replaced by `logger.InfoContext(ctx, ...)`.
- Delete `(h *Handler).tenantLogger` — same.
- Delete `internal/telemetry/telemetry.go` and the whole directory.
- `go.mod`: remove `go.uber.org/zap`, `go.uber.org/multierr`.

**Body-size caps:**
- `pkg/log` exposes a helper `log.TruncBody(b []byte) string` that caps at 4 KiB with a truncation suffix. Apply to places that currently log full payloads: `client.go` `doRequest` error path, `QueryBuilderV5` debug body log, `evaluateValidationResponse` response body.

### Commit 3 — Custom meters

Add tool + oauth metrics as a typed struct.

**New:**
- `pkg/otel/metrics.go` — `type Meters struct { ToolCalls metric.Int64Counter; ToolCallDuration metric.Float64Histogram; MethodCalls metric.Int64Counter; MethodDuration metric.Float64Histogram; SessionRegistered metric.Int64Counter; OAuthEvents metric.Int64Counter; OAuthFailures metric.Int64Counter; IdentityCacheHits metric.Int64Counter; IdentityCacheMisses metric.Int64Counter }` and `NewMeters(mp metric.MeterProvider) (*Meters, error)`. Instantiated once in `main`, injected via `NewMCPServer(..., meters)`.

**Metrics chosen (after deferring broken ones):**
- `mcp.tool.calls` — counter, attrs: `gen_ai.tool.name`, `mcp.tool.is_error`, `mcp.tenant_url`.
- `mcp.tool.call.duration` — histogram (ms), same attrs.
- `mcp.method.calls` — counter, attrs: `mcp.method.name`, optional `error.type`, `mcp.tenant_url`. Recorded for non-tool MCP methods from the hook layer.
- `mcp.method.duration` — histogram (ms), same attrs.
- `mcp.session.registered` — counter, attr: `mcp.tenant_url`. Incremented from `AddAfterInitialize` hook, which only fires after a successful initialize, so counts are accurate for session rate even though unregister is unreliable for streamable HTTP.
- `mcp.oauth.events` — counter, attrs: `event` (one of authorize/register/token/refresh), `mcp.tenant_url`.
- `mcp.oauth.failures` — counter, attrs: `oauth.error_code`, `http.response.status_code`, `mcp.tenant_url` (present on post-decrypt failure modes; absent on pre-decrypt malformed-request failures).
- `mcp.identity_cache.hit` / `mcp.identity_cache.miss` — counters, attr: `mcp.tenant_url`. Low cost, immediately actionable for the /me hot path.
- **Deferred**: `mcp.sessions.active` up/down gauge. `OnUnregisterSession` is documented-unreliable for streamable HTTP POST-only clients, so the counter would leak upward. Revisit when mcp-go fires unregister reliably.

**Multi-tenant attribution:** `pkg/otel/attr.go` exposes `TenantURLAttr(ctx)` and `AppendTenantURL(ctx, attrs)` helpers that read `util.GetSigNozURL` and conditionally emit `mcp.tenant_url`. Every metric above uses these; every span that the MCP server creates or decorates (`otelhttp` root span inside `authMiddleware`, method span at start, `AddBeforeAny` parent-span decoration, tool span in `loggingMiddleware`, method-observation completion, `AddOnError` non-observed path) carries the attr. Post-decrypt OAuth handlers (`handleAuthorizationCodeGrant`, `handleRefreshTokenGrant`, `issueTokenPair`) seed `signozURL` on the request context before invoking `writeOAuthError`, so per-tenant failure dashboards are populated for actionable failure modes. Cardinality is bounded by customer count (hundreds to low thousands), safe for Prometheus/OTLP.

**Wiring:**
- `loggingMiddleware` records `ToolCalls` + `ToolCallDuration` after the handler returns (error surfaces through inner recovery middleware as a normal return value) and logs handled `result.IsError` outcomes at `Warn`, not only `Debug`.
- Non-tool MCP method spans are started at the HTTP transport boundary before `mcp-go` calls `HandleMessage`, but only for known MCP request methods to avoid attacker-controlled span-name cardinality. The request context seen by hooks and handlers already carries the method span. `AddBeforeAny` + `OnSuccess` / `OnError` then decorate that active span and record `MethodCalls` + `MethodDuration`, and the span is ended from the observation lifecycle itself so timeout cleanup can still set error state before export. A bounded in-memory map still tracks in-flight observations for duration/error accounting and panic-timeout cleanup.
- `methodSpanMiddleware` caps request-body inspection at 1 MiB while extracting JSON-RPC method names, so observability parsing cannot buffer arbitrarily large POST bodies.
- `AddAfterInitialize` hook increments `SessionRegistered`.
- `trackOAuthEvent` records `OAuthEvents` alongside the analytics dispatch.
- `writeOAuthError` records `OAuthFailures` and emits structured warn/error logs for OAuth failures, and interactive authorize-form failures reuse the same telemetry path when the form is re-rendered with an error.
- `GetAnalyticsIdentity` records hit/miss after the TTL check in `client.go`.

## Commit Atomicity

Each commit independently:
- Builds: `go build ./...`
- Passes vet: `go vet ./...`
- Passes tests: `go test ./...`
- Produces a working binary (not bisect-broken).

Specifically:
- Commit 1 keeps zap, so no logger-typed signatures change. `internal/telemetry` is still importable.
- Commit 2 removes `internal/telemetry` and adds `pkg/log` in the **same commit** as the zap→slog call-site migration — they must ship together to stay green.
- Commit 3 is purely additive (new package + two wiring points).

## Files to Modify

### New (commit 1)
- `pkg/otel/{resource,tracer,meter,attr}.go`
- `pkg/version/version.go`

### New (commit 2)
- `pkg/log/{log,handler}.go`

### New (commit 3)
- `pkg/otel/metrics.go`

### Modified (commit 1)
- `cmd/server/main.go` — signal.NotifyContext, errgroup, strict shutdown order, version.Version to resource + MCPServer name.
- `internal/mcp-server/server.go` — `Run/Shutdown`, panic-safe middleware, use `version.Version`, swap attr imports.
- `internal/mcp-server/integration_test.go` — use `version.Version`.
- `internal/client/client.go` — swap attr imports.
- `internal/telemetry/telemetry.go` — thin shim delegating to `pkg/otel`; remove `genai.go` (moved).
- `Makefile` — `-ldflags` on build target.
- `.goreleaser.yaml` — `ldflags: -s -w -X .../pkg/version.Version={{.Version}}`.

### Modified (commit 2)
- All 14 files listed in commit 2 "Migration scope" — mechanical zap→slog.
- `go.mod` — drop zap, multierr; add `log/slog` imports (stdlib, no go.mod change needed).
- Delete `internal/telemetry/` directory entirely.

### Modified (commit 3)
- `internal/mcp-server/server.go` — accept `*otel.Meters`, record in middleware + oauth handler + hooks.
- `internal/client/client.go` — record identity cache hit/miss.
- `cmd/server/main.go` — `NewMeters(mp)` and pass into `NewMCPServer`.

### Deleted
- `internal/telemetry/genai.go` (commit 1 — moved).
- `internal/telemetry/telemetry.go` and directory (commit 2).

## Explicitly Out of Scope
- Zeus-style HTTP request/response body logger plugin (adds redaction burden, we already have `otelhttp` traces).
- Zeus-style separate status server on a second port — `/healthz` already exists.
- Direct OTLP log exporter (`otelslog`) — stdout JSON is the deliberate Zeus-aligned choice.
- Noop providers gated on `deployment.environment` — OTLP fails gracefully anyway.
- `mcp.sessions.active` gauge — see commit 3.

## Verification
- After each commit: `go build ./...`, `go vet ./...`, `go test ./...`.
- Manual: run HTTP mode against staging SigNoz — confirm traces + metrics arrive, logs on stdout show trace_id/session/tenant.
- Manual: send `SIGTERM` during a burst of tool calls — no OTLP "shutdown timed out" warnings; last batch reaches collector.
- Manual: trigger a tool panic — verify `mcp.tool.calls{is_error=true}` increments and the span is marked `codes.Error`.

## Resolved Questions
- [x] Stdout JSON vs OTLP log exporter → **stdout JSON**.
- [x] Three commits vs three PRs → **three commits, one PR**.
- [x] Noop providers for local env → **skip; OTLP fails gracefully**.
- [x] `mcp.sessions.active` feasibility → **defer, use counter instead**.
- [x] Shutdown ctx source → **fresh `context.Background()+timeout`, not cancelled signal ctx**.
- [x] Middleware order for panic coverage → **`loggingMiddleware` appended before `WithRecovery()` in NewMCPServer options; mcp-go's reverse-slice wrap yields `logging(recovery(tool))` so recovery catches the panic and logging records metrics via the normal return path. No second `recover()` in logging middleware.**
- [x] Version plumbing → **goreleaser ldflags + version.Version in `NewMCPServer`**.
