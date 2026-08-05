# Feature: Failure Request Observability — Context & Discussion

## Original Prompt
> We want to log full request in case of validation or any tool failures so that we can replicate and imporve MCP server
> Let's support for it
>
> Also we want to ensure mcp.client_source is added everywhere
> *> Data note: advisory schema-validation mismatch events were 73 vs 50 (+46.0%) and are not failed calls. The metric lacks mcp.client\_source and populated bounded tool/path/constraint labels, so cohort and tool attribution is unavailable.*

## Reference Links
- [`docs/mcp-best-practices.md`](../docs/mcp-best-practices.md)
- [`plans/observability-refactor.plan.md`](observability-refactor.plan.md)
- [`plans/mcp-go-v0.56-upgrade.plan.md`](mcp-go-v0.56-upgrade.plan.md)

## Key Decisions & Discussion Log

### 2026-08-05 — Central failure capture and request-scoped attribution
- Capture the complete handler-visible `mcp.CallToolRequest` on every schema mismatch, missing structured output, returned tool error, and Go tool error. HTTP headers remain excluded by `mcp-go`'s request JSON contract, so ingress credentials are never copied into the payload field.
- Preserve the existing observability safety boundary: recursively redact credential-shaped argument keys and cap the serialized request at 4 KiB. The remaining tool name, argument structure, non-secret values, MCP metadata, and task parameters are sufficient for a local replay in ordinary requests; truncation is explicit in the field value.
- Stop deduplicating schema-mismatch WARN records. Two requests can hit the same bounded `(tool, direction, path, constraint)` tuple with different payloads, so suppressing later records defeats reproduction. Metrics remain exact as before.
- Treat `mcp.client_source` as a required dimension on request-scoped telemetry. Add it to validation/missing-output, auth-failure, docs-tool, and identity-cache metrics that currently omit it. Registration-time schema compilation and background docs/OAuth metrics remain excluded because they do not represent an MCP client request.
- Keep validation metric dimensions bounded: registered tool name, normalized direction, normalized schema path, and normalized constraint. Add regressions that assert these labels are populated together with `mcp.client_source`.
- This is internal observability only: no MCP tool/resource/prompt/configuration contract changes, so README/manifest and the companion `SigNoz/agent-skills` repo do not require updates.

### 2026-08-05 — Implementation and verification
- Added redacted, 4 KiB-bounded `mcp.request` capture to every schema mismatch, missing structured output, returned tool error, Go tool error, and pre-handler/unknown-tool error log path.
- Added `mcp.client_source` to validation, missing-output, auth-failure, docs-tool, and analytics-identity-cache metrics, with regressions covering each family and the bounded validation dimensions.
- Focused tests, `go test ./...`, `go vet ./...`, `go build ./cmd/server`, formatting/imports, and `git diff --check` pass.
- Agent CI could not start because `/var/run/docker.sock` is unavailable on this machine; repository-native checks were run as the fallback. No live tenant verification was needed because the change is local logging/OTel instrumentation with manual-reader tests.

### 2026-08-05 — Multi-agent review and hardening
- Three independent reviews covered reuse, code quality/security, and efficiency/overengineering. Confirmed findings: request redaction was eagerly evaluated on successful calls; registered Go errors could serialize the request twice; notification-channel webhook URLs/routing keys escaped generic redaction; repeated advisory mismatches could amplify WARN logs; and raw error strings were not bounded with the request payload.
- Fixed the confirmed findings: failure payloads are now built only when the relevant log level is enabled, hook capture is skipped after registered middleware observes the request, known notification credentials are redacted everywhere they recur (including `searchContext`), error text is 4 KiB-bounded, and validation WARNs are rate-limited per bounded tool/direction/path/constraint tuple while metrics remain exact.
- Reused `TruncAny` after redaction and hoisted key-normalization tables per the reuse/efficiency reviews.
- Declined a streaming one-pass redactor: after lazy evaluation it runs only on emitted failure/WARN paths, inbound requests are already capped at 4 MiB, `mcp-go`'s custom `CallToolParams.MarshalJSON` preserves `RawArguments`, and a multi-pass representation is needed to discover credential values before removing them from repeated free text.
- Retained caller-attribute stamping in `completeUnobservedToolCall`: `BeforeAny` covers unknown/filtered tools, but malformed `tools/call` requests can invoke `OnError` without `BeforeAny`, so the fallback remains necessary for that path.

### 2026-08-05 — Claude Opus 5 high-effort overengineering review
- Ran an uncapped, read-only `claude-opus-5` review in high-effort mode after two capped attempts were stopped before verdict. The completed run explicitly reviewed correctness, security, performance, and removable complexity.
- Confirmed and fixed two P1s: cross-field substring replacement could become quadratic on a 4 MiB failure request, and the context logger could emit raw `mcp.search_context` beside a redacted `mcp.request` on the same record.
- Simplified redaction to deterministic key-based replacement. Advisory free-text `searchContext` is always redacted from captured failure requests; `ContextHandler` omits the duplicate raw context whenever a captured request is present. Replay-critical tool arguments remain available except credential-shaped fields.
- Collapsed validation rate limiting from per-tuple mutex/counter state to one atomic deadline, removed unreachable value/nil request-hook branches, restored metric discovery hints in rate-limited WARN messages, and preserved the bounded error type with a span-status regression.
- Bounded the newly added pre-auth auth-failure metric dimension: known `user-client`/`ai-assistant` values remain, while arbitrary unauthenticated header values collapse to `other`. Authenticated tool/method telemetry retains normalized custom source taxonomy.
- Opus's one-pass/request-precision concern was not applicable after source verification: `mcp-go` v0.56 `CallToolParams.MarshalJSON` explicitly re-emits `RawArguments`, and the helper now runs only on enabled failure/WARN logs. Docs-duration attribution was retained because adding `mcp.client_source` to request-scoped metrics is the requested contract.

### 2026-08-05 — Final Opus 5 approval and superseding decisions
- Final uncapped `claude-opus-5` high-effort verification approved the production code with no P0/P1/P2 findings and no overengineering after the following two simplifications.
- `searchContext` remains verbatim on both existing observability surfaces (`mcp.search_context` and the captured `mcp.request`). It is established observability content rather than a credential field; credential-shaped tool arguments and ingress headers remain redacted/excluded. This supersedes the earlier intermediate decision to redact it only from failure records.
- `util.NormalizeClientSource` is now the single ingress policy for HTTP requests: `user-client` and `ai-assistant` are retained, every other value collapses to `other`, and that same bounded value flows to logs, spans, metrics, and analytics. Stdio seeds `user-client`. This supersedes the intermediate pre-auth-only clamp and permissive authenticated taxonomy.
- Opus's remaining findings were planning-record consistency only; the plan file and the earlier assistant-correlation open question were updated accordingly.

### 2026-08-05 — Live staging MCP E2E
- Delegated a credential-hygienic live run of the modified HTTP server against `https://app.us.staging.signoz.cloud`. The supplied bearer token stayed process-ephemeral and was not printed, persisted, or committed.
- Initialization negotiated protocol `2025-11-25`; `tools/list` returned 43 tools; the read-only `signoz_list_alerts` call returned structured `data` and `pagination`. No tenant resource was created or modified.
- An intentionally wrong-type `limit` on `signoz_search_docs` remained advisory (`isError=false`) and appended the input-validation notice. Its WARN carried `direction=input`, `path=/limit`, `constraint=type`, and `mcp.client_source=ai-assistant`; the captured request redacted a nested dummy `apiToken` and truncated at 4,096 characters.
- A bad alert UUID returned coded `VALIDATION_FAILED` and logged the handler-visible method, tool name, `searchContext`, ID, and a redacted dummy `privateKey`. A successful tool call emitted no `mcp.request`, confirming failure-only serialization.
- An unknown tool with an 8 KiB name bounded both `error` and `mcp.request` at 4,096 characters and normalized an unsupported client source to `other`.
- The server was stopped, port 18091 was confirmed closed, the temporary directory was removed, and the credential was unset. Live metric export was deliberately disabled; metric dimensions remain covered by deterministic manual-reader tests.

## Open Questions
- [x] Should raw auth headers be logged? — No. `mcp.CallToolRequest.Header` is excluded from JSON serialization by `mcp-go`, and the failure payload helper accepts only the handler-visible request.
- [x] Should repeated schema mismatches remain deduplicated? — Rate-limit WARN request capture per bounded mismatch tuple. The first representative request is enough to reproduce the contract mismatch; counters remain exact and the WARN points operators to the counter for total volume.
- [x] Does `mcp.client_source` apply to background or browser-only telemetry? — No. It is required on request-scoped MCP telemetry only; background schema/docs work and OAuth browser flows have no MCP client source.
