# Plan: OTel-native Log Attributes & Denoise

## Status
Done

## Context
Logs emitted inside a tool call are missing `gen_ai.tool.name` (only the 4 `loggingMiddleware` bookend lines carry it), because the tool name is passed as a per-call `slog.Attr` instead of being propagated through the request context the way session/search/tenant are. Separately, three per-request auth-breadcrumb DEBUG logs account for ~96% of all log volume, and a routine OAuth-expiry event is logged 252k/day at WARN. We want telemetry OTel-native (semconv-accurate) while keeping the durable stdout→filelog transport (logs survive OOM/SIGKILL where traces don't). DEBUG stays ON in prod, so denoising is done by removing the noisy statements at the source, not by level-gating.

## Approach

### 1. Propagate `gen_ai.tool.name` to all in-request logs (MUST)
- `pkg/util/context.go`: add `toolNameContextKey` + `SetToolName(ctx, name)` / `GetToolName(ctx)` (mirror the existing `SessionID` pair).
- `internal/mcp-server/server.go` `loggingMiddleware` (~line 940–948, before `tracer.Start`): `ctx = util.SetToolName(ctx, req.Params.Name)`.
- `pkg/log/handler.go` `ContextHandler.Handle`: after the `search_context` block, inject `gen_ai.tool.name` from `util.GetToolName(ctx)` when non-empty.
- Remove the now-redundant explicit `toolNameAttr` (var at ~line 975) from the 4 middleware log calls (`tool call started/finished/failed`, `tool call returned error result`) — the handler now adds it once. Leave the metric dimension `attribute.String("gen_ai.tool.name", …)` at ~line 1023 unchanged (separate signal).

### 2. Fix semconv key inconsistency (MUST)
- MCP method logs: use key `mcp.method.name` everywhere instead of `mcp.method` (matches the span attr and MCP semconv).

### 3. Denoise — delete high-volume breadcrumb logs (MUST; DEBUG stays globally enabled)
Delete these DEBUG statements (locate by message text; verify before removing):
- `OAuth access token extracted from Authorization header` (server.go ~1295)
- `Using URL from X-SigNoz-URL header` (server.go ~1373)
- `Using SIGNOZ-API-KEY header for auth` (server.go ~1266)
- `Using API KEY token authentication via SIGNOZ-API-KEY header`
- `Using JWT token authentication via Authorization header`
Rationale: pure header-parse narration; auth mode is already on the auth span (`mcp.auth.mode`) and tenant URL is `mcp.tenant_url` everywhere. KEEP `tool call *`, `mcp request`, `Tool called: X` (durable backbone).

### 4. OAuth-expiry: downgrade log + add metric (MUST)
- `internal/mcp-server/server.go` `logAuthFailure` (~1201): log at DEBUG for ROUTINE OAuth-lifecycle reasons — `expired_oauth_token` AND `missing_credentials` (the latter is the unauthenticated first-contact leg of OAuth discovery; prod data shows it is driven by legit MCP clients hitting `/mcp`). Log at WARN only for genuinely anomalous reasons: `invalid_oauth_token`, `invalid_signoz_url`, `missing_signoz_url`. (Add a level branch or level param.)
- `pkg/otel/metrics.go`: add `AuthFailures metric.Int64Counter` named `mcp.auth.failures` ("Count of request-time auth failures"). Wire into `Meters`.
- Record it in `logAuthFailure` with low-cardinality dims: `mcp.auth.failure_reason`, `mcp.auth.mode` (+ tenant URL via `otelpkg.AppendTenantURL`). Do NOT add per-request UUIDs.
- This is distinct from the existing `mcp.oauth.failures` (OAuth *endpoint* failures).

### 5. Truncate the outbound-request payload log (MUST)
- `internal/client/client.go:565` (`sending request`): stop logging the full request body. Log `url` + `slog.Int("request.body.size_bytes", len(body))` (or truncate body to ≤512 chars). Avoids dumping full `compositeQuery` JSON and leaking query content.

### 6. Spec-completeness adds (SHOULD — only if low-effort & non-breaking)
- `gen_ai.operation.name = "execute_tool"` on tool-call logs (propagate via the same ctx mechanism as #1, or inject in the handler when tool name is present).
- `mcp.protocol.version` on method/tool spans + logs IF the negotiated MCP protocol version is readily available from the mcp-go session; otherwise defer.

### Out of scope (do NOT do)
- Do NOT rename spans (`execute_tool`, `MCP <method>`) — breaks observability-plan filters.
- Do NOT migrate logs to the OTLP Logs SDK / otelslog (durability — keep stdout transport).
- Do NOT change the global/prod log level or level-gate (DEBUG stays on).
- Do NOT remove tool-call lifecycle logs, `mcp request`, or error/warn logs.

## Files to Modify
- `pkg/util/context.go` — add tool-name context key + Set/Get.
- `pkg/log/handler.go` — inject `gen_ai.tool.name` (and optionally `gen_ai.operation.name`) from ctx.
- `internal/mcp-server/server.go` — set tool name in ctx; remove redundant explicit attrs; fix `mcp.method.name`; delete breadcrumb logs; downgrade OAuth-expiry; record `mcp.auth.failures`.
- `pkg/otel/metrics.go` — add `mcp.auth.failures` counter.
- `internal/client/client.go` — truncate `sending request` payload.
- `internal/mcp-server/server_test.go` — add/extend tests (see Verification).
- `plans/otel-log-instrumentation.{context,plan}.md` — keep in sync.
- `README.md` / `manifest.json` — only if tool/metric metadata surfaced there changes (new `mcp.auth.failures` metric → note if metrics are documented).

## Verification
1. `go build ./...` and `go test ./...` pass.
2. New/extended unit tests:
   - A downstream log emitted within a tool call carries `gen_ai.tool.name` (assert on captured slog record via the existing handler test harness in `server_test.go`).
   - The 4 bookend log lines still carry `gen_ai.tool.name` exactly once (no duplicate key).
   - OAuth-expiry path logs at DEBUG (not WARN) and increments `mcp.auth.failures` with the expected `mcp.auth.failure_reason`/`mcp.auth.mode` dims.
   - `mcp request` log uses key `mcp.method.name`.
3. Manual/log-shape check: confirm deleted breadcrumb messages no longer appear; `sending request` no longer contains the full payload.
4. Dual review (Claude review agent + Codex gpt-5.5 xhigh) clean before requesting commit approval.
