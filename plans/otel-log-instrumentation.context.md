# Feature: OTel-native Log Attributes & Denoise — Context & Discussion

## Original Prompt
> Looks like logs has instrumentation issue. Some attributes are missing like gen_ai.tool.name are missing in some logs but present in some.
>
> (follow-ups) What else is missing? / Are OAuth expire events important, can we drop them? Which debug events can be dropped? / We want to keep all telemetry otel native according to latest spec. / I want to ensure logs are there in addition to traces even if duplicated as logs are more likely to be exported successfully (stdout) in case of pod kills/restarts. / We keep DEBUG on in prod as well. / Ask Codex to implement, add missing attributes, then dual-review.

## Reference Links
- GenAI semconv registry: https://github.com/open-telemetry/semantic-conventions-genai/blob/main/model/gen-ai/registry.yaml
- GenAI spans (`execute_tool {gen_ai.tool.name}`): https://github.com/open-telemetry/semantic-conventions-genai/blob/main/model/gen-ai/spans.yaml
- MCP semconv (4 keys only; tool name = `gen_ai.tool.name`): https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
- MCP registry: https://github.com/open-telemetry/semantic-conventions-genai/blob/main/model/mcp/registry.yaml
- OTLP Logs data model (trace ctx + severity are top-level LogRecord fields): https://opentelemetry.io/docs/specs/otel/logs/data-model/

## Key Decisions & Discussion Log

### 2026-06-06 — Root cause of the missing `gen_ai.tool.name`
- `gen_ai.tool.name` is attached ONLY to the 4 `loggingMiddleware` bookend log lines (`server.go` ~975–1018) as an explicit per-call `slog.Attr`. It is never put into the request context.
- `pkg/log/handler.go` `ContextHandler.Handle` is the single place that stamps context-scoped attrs onto every record (trace_id, span_id, mcp.tenant_url, mcp.session.id, mcp.search_context, mcp.client_source, mcp.assistant.*). `gen_ai.tool.name` is absent from that list, and there is no `util.SetToolName/GetToolName`.
- Result: every downstream log emitted inside a tool call (handlers, HTTP client, etc.) inherits session/search/trace context but NOT the tool name. Verified in prod: `Tool called: X`, `sending request`, etc. all lack it.

### 2026-06-06 — Durability is the architectural constraint
- Logs go slog→stdout→node file→filelog receiver: they hit disk at emit time and survive SIGKILL/OOM/restart. Traces/metrics buffer in-process (OTLP batch) and are lost on pod kill — exactly when forensics are needed. See memory `project_mcp_oom`, `project_log_durability`.
- DECISION: do NOT migrate logs to the in-process OTLP Logs SDK / otelslog exporter — that reintroduces the loss. Keep stdout as the durable transport. "OTel-native" applies at the *data-model / semconv-naming* level only.
- DECISION: intentional duplication (event in both a durable log and a rich span) is correct and desired.

### 2026-06-06 — Denoise mechanism: remove at source, NOT level-gating
- DECISION: DEBUG stays ON in production (the surviving DEBUG logs are the durable backbone). So volume reduction must come from deleting the specific noisy log statements, not from raising the log level.
- The 3 per-request auth breadcrumbs are ~96% of all DEBUG (`OAuth access token extracted…` 2.06M/day, `Using URL from X-SigNoz-URL header` 0.68M, `Using SIGNOZ-API-KEY header for auth` 0.67M) and carry no forensic value (auth mode + tenant URL already live on the auth span). Remove them.
- Tool-call lifecycle logs and `mcp request` are KEPT (durable backbone), now made self-describing by the `gen_ai.tool.name` fix.

### 2026-06-06 — OAuth "access token expired" is routine
- It's the designed 401→refresh handshake (252k/day WARN, ~95% of all WARN). Per-event it is not operator-actionable, but a *spike* matters (refresh loops / clock skew / TTL misconfig).
- DECISION: downgrade the per-event log from WARN→DEBUG (keep it durable, out of WARN noise) and add an `mcp.auth.failures` counter (dims: failure_reason, auth mode) so the rate is preserved cheaply. `logAuthFailure` is the single choke point.

### 2026-06-06 — Spec findings that shape "add missing attributes"
- MCP semconv defines exactly 4 keys: `mcp.method.name`, `mcp.session.id`, `mcp.resource.uri`, `mcp.protocol.version`. There is NO `mcp.tool.name` / `mcp.request.id` / `mcp.transport`. MCP tool calls use `gen_ai.tool.name` + `gen_ai.operation.name="execute_tool"`; request id = `jsonrpc.request.id`; transport = `network.transport`.
- The server's custom `mcp.*` keys (search_context, tenant_url, tool.is_error, tool.result.size_bytes, client_source, assistant.*, auth.*) have NO spec equivalent → keep as custom extensions.
- BUG: `mcp request` log uses key `mcp.method` (server.go:816) but the span uses `mcp.method.name`. Fix the log to `mcp.method.name`.
- Span renaming to spec form (`execute_tool {tool}`, `{mcp.method.name}`) is OUT OF SCOPE — would break `name='execute_tool'` filters in `plans/mcp-server-observability.plan.md`.

### 2026-06-06 — Execution plan
- Implement via Codex CLI (gpt-5.5, high effort, fast mode). Then dual review: Claude review agent + Codex review (gpt-5.5, xhigh, fast). No git commit/push until user approves the reviewed diff (per `feedback_commit_workflow`).

### 2026-06-06 — Implementation completed
- Added context propagation for `gen_ai.tool.name` and `gen_ai.operation.name="execute_tool"` so downstream tool-call logs inherit tool metadata from `ContextHandler`.
- Removed the specified auth breadcrumb DEBUG logs, changed `mcp request` to emit `mcp.method.name`, downgraded expired OAuth token logs to DEBUG, and added the `mcp.auth.failures` request-time counter.
- Updated the outbound query log to emit request body size instead of the full/truncated query payload.
- README.md and manifest.json were left unchanged because they document MCP tools/environment configuration, not the internal MCP server metric catalog.

### 2026-06-06 — "Dropped" requests are the OAuth lifecycle, not failures
- 24h: 261,966 of ~261,990 dropped requests are `401`s at the auth layer (`400`:21, `502`:3). By reason: `expired_oauth_token` 244,832 (93.5%), `missing_credentials` 17,098 (6.5%), `invalid_oauth_token` 38, missing/invalid SigNoz URL 16.
- `missing_credentials` is dominated by legitimate MCP clients (claude-code across many versions ~16k, Claude-User, Cursor, VS Code) hitting `/mcp` — i.e. the unauthenticated first-contact leg of the MCP OAuth discovery handshake, NOT scanners/attackers.
- REFINEMENT to decision #4: treat BOTH `expired_oauth_token` AND `missing_credentials` as routine → DEBUG; keep WARN only for `invalid_oauth_token`, `invalid_signoz_url`, `missing_signoz_url`. The `mcp.auth.failures` metric (dim: failure_reason) preserves spike-detection for all reasons. Plan #4 updated; fold into Codex implementation on resume (first pass downgraded expired only).

### 2026-06-06 — Reviewer follow-up applied
- `missing_credentials` now follows `expired_oauth_token` to DEBUG while still incrementing `mcp.auth.failures`.
- The remaining method backbone logs now use `mcp.method.name`; tests no longer reference the old `mcp.method` log key.

## Open Questions
- [ ] Add `mcp.protocol.version` and/or `jsonrpc.request.id` now, or defer? (Spec attrs; only worth it if the negotiated version / JSON-RPC id is readily available from the mcp-go session. Marked SHOULD/optional.)
- [x] Move logs to OTLP Logs SDK? → No (durability). Resolved above.
- [x] Prod log level INFO? → No, keep DEBUG on; remove noisy statements at source. Resolved above.
