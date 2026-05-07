# Feature: Assistant Correlation Headers — Context & Discussion

## Original Prompt
> SigNoz AI assistant calls SigNoz cloud MCP server and even MCP server produces it's own events. Now how to differentiate if MCP server is being used by SigNoz AI Assistant or user's client?
> We added Custom HTTP headers set by the AI Assistant backend on every MCP call. This is the cleanest mechanism and the one to build around. When the assistant backend (or the Claude Agent SDK on its behalf) calls the MCP server, inject:
>
> X-SigNoz-Client-Source: ai-assistant
> X-SigNoz-Assistant-Thread-Id: <thread_id>
> X-SigNoz-Assistant-Execution-Id: <execution_id>
>
> In this PR: https://github.com/SigNoz/signoz-ai-assistant/pull/136, consume them in your events or logs

## Reference Links
- AI Assistant PR (sender side, merged): https://github.com/SigNoz/signoz-ai-assistant/pull/136
- Existing analytics design: `plans/analytics.md`
- Existing observability refactor: `plans/observability-refactor.plan.md`

## Key Decisions & Discussion Log

### 2026-05-06 — Mechanism choice
- Three correlation headers carried on every MCP HTTP request from the AI Assistant backend:
  - `X-SigNoz-Client-Source` — categorical, low-cardinality (`ai-assistant` today; default `user-client` when absent).
  - `X-SigNoz-Assistant-Thread-Id` — UUID, high cardinality.
  - `X-SigNoz-Assistant-Execution-Id` — UUID, high cardinality.
- Followed the existing `searchContext` pipeline as the template: `authMiddleware` reads → ctx via `pkg/util/context.go` setters → log handler / span / metrics / analytics auto-enrich.

### 2026-05-06 — Default for missing `X-SigNoz-Client-Source`
- Default to literal string `"user-client"` (not empty) when header is absent or blank, so the dimension is always populated and downstream group-bys never have to handle null.
- Kept as a raw string with no enum validation. The assistant PR explicitly leaves taxonomy headroom (`ai-assistant-agent`, `ai-assistant-undo`, etc.) — strict validation here would force a coordinated deploy on every taxonomy change.

### 2026-05-06 — Analytics emission policy
- Decision: keep emit-with-tag, do **not** suppress `MCP Tool: Called` (and similar) when `client_source = ai-assistant`.
- Reason: suppression at the source loses data we cannot reconstruct. Filtering downstream (`client_source != 'ai-assistant'`) is cheap; un-emitting is irreversible. The assistant team can still split user-facing counts in dashboards.

### 2026-05-06 — Cardinality split between metrics and other sinks
- `client_source` is added as an attribute to the OTel counters (`ToolCalls`, `MethodCalls`, `SessionRegistered`, etc.). Bounded set, useful for split-by.
- `assistant.thread_id` and `assistant.execution_id` are **not** added to metrics. Per-execution UUIDs would explode Prometheus / metrics backend cardinality. They go on logs, spans, and analytics events only.

### 2026-05-06 — Drop `searchContext` from analytics events
- Decision: stop including `searchContext` as an analytics attribute. Keep it on logs and spans where it is still useful for trace-level debugging.
- Reason: `searchContext` is free-form LLM-authored prose and unbounded as an analytics dimension; it inflates Segment payload size with no aggregation value. Logs/spans retain it for individual-request investigation, which is where it actually pays off.
- Touchpoint: remove the `props[analytics.AttrSearchContext] = sc` block in `internal/mcp-server/server.go` (around the tool-called event); the `AttrSearchContext` constant in `pkg/analytics/events.go` is removed if it has no remaining users.

### 2026-05-07 — Multi-AI review pass (Codex gpt-5.4/xhigh + Gemini gemini-3-flash-preview)
- Both reviewers concurred on two real risks; Codex flagged three additional issues. Findings and resolution:
  - **Header length bloat (Codex M / Gemini M)** — all three correlation headers were forwarded raw into ctx and from there into every log/span/Segment payload. A single oversized header would multiply downstream. **Resolved**: introduced `util.CallerCorrelationHeaderMaxLen = 256` and `util.TruncateCallerCorrelationValue` in `pkg/util/context.go`; `authMiddleware` truncates each of the three header values before stashing on ctx. Truncation is silent — preserves the "advisory, never gates" rule.
  - **Cardinality on `client_source` (Codex H / Gemini L)** — Codex argued the deliberate "no allowlist" design lets a hostile client spray unique values into `mcp.tool.calls` / `mcp.method.calls` / `mcp.session.registered` counters. **Decision**: stay with length-cap-only (option 2 in the synthesis). The 256-char truncation bounds per-value size; we accept residual unique-value spam risk in exchange for taxonomy headroom on the assistant side. Revisit if the metric backend ever shows actual cardinality pressure here.
  - **stdio path missing default (Codex L)** — `runStdio.SetContextFunc` seeded API key and URL but never `client_source`, so stdio sessions emitted the dimension absent. **Resolved**: stdio context func now sets `util.SetClientSource(ctx, util.ClientSourceUserClient)`, matching `authMiddleware`'s default.
  - **Test gap on metric invariant (Codex L)** — no test asserted the central cardinality claim (`client_source` present on counters, `assistant_*` absent). **Resolved**: added `TestLoggingMiddleware_MetricCardinalityInvariants` that runs the middleware against a real `sdkmetric.ManualReader` and asserts the attribute presence/absence on both `mcp.tool.calls` and `mcp.tool.call.duration`.
  - **`AttrSearchContext` removal as Go API break (Codex L)** — `pkg/analytics` is not `internal/`, so external Go importers could in theory exist. **Decision**: accept the break. There are no known external consumers; a deprecated-but-kept constant is a maintenance smell that would silently rot. Mentioned in plan/context for future readers.
  - Gemini also flagged "manual ctx-copy in `detachedAnalyticsContext` is brittle as fields grow" (Nit) and "redundant root-span attrs vs child spans" (Nit, acceptable). Both deferred.
- HTTP-path propagation, `r.WithContext` correctness, and `detachedAnalyticsContext` copy were validated by both reviewers as correct.

### 2026-05-06 — Implementation: OAuth counters out of scope
- During implementation, confirmed the assistant only injects the three correlation headers on `/mcp` calls. The OAuth handler endpoints (`/oauth/authorize`, `/oauth/token`, etc.) are browser-driven and never carry these headers, so `OAuthEvents` / `OAuthFailures` counters cannot meaningfully be tagged with `client_source`.
- Removed OAuth counters from the metrics-tagging scope. The `/mcp` POST that *uses* an OAuth bearer token still flows through `authMiddleware` and is correctly tagged because header reading happens there, before any auth-method branching.

## Open Questions
- [x] Default value for missing `X-SigNoz-Client-Source`? Answer: `"user-client"`.
- [x] Suppress analytics events when `client_source = ai-assistant`? Answer: no — emit with tag, filter downstream.
- [x] Add `thread_id` / `execution_id` to metrics? Answer: no, cardinality risk; logs + spans + analytics only.
- [x] Keep `searchContext` on analytics events? Answer: no — drop from analytics, retain on logs and spans.
- [ ] Future: when `X-SigNoz-Client-Source` taxonomy expands beyond `ai-assistant`, do we want a server-side allowlist or stay permissive? Out of scope for this change.
