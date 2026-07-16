# Feature: Explicit QueryRange Limits and Ordering — Context & Discussion

## Original Prompt
> Let's plan to work on this: [https://github.com/SigNoz/nerve-pod/issues/132](https://github.com/SigNoz/nerve-pod/issues/132)

## Reference Links
- [SigNoz/nerve-pod issue #132 — Always specify limit and orderBy in queryRange payloads](https://github.com/SigNoz/nerve-pod/issues/132)
- [Slack discussion that established the proposed defaults and truncation caveat](https://signoz-team.slack.com/archives/C02B85R4R0A/p1782728421261829)
- [SigNoz Result Manipulation docs](https://signoz.io/docs/querying/result-manipulation/)
- [SigNoz Metrics Query Range API](https://signoz.io/docs/metrics-management/query-range-api/)
- [SigNoz Logs API pagination and ordering](https://signoz.io/docs/logs-management/logs-api/search-logs/)
- [SigNoz MCP Best practices](https://app.notion.com/p/signoz/MCP-Best-practices-38cfcc6bcd198071b904e3f2d911487d)
- `plans/response-request-body-limits.context.md` — prior decision not to clamp caller-authored builder-query limits
- `plans/param-consistency-cleanup.context.md` — prior inventory of limit, request-type, and ordering inconsistencies
- `plans/metrics-query-tool.context.md` — origin of the generated metrics payload path

## Key Decisions & Discussion Log

### 2026-07-15 — Issue, Slack, and contract audit
- The Slack thread confirms the intended omission defaults: raw queries use `limit: 100`; scalar and time-series queries use `limit: 1000`; every bounded query also carries explicit ordering.
- Limit semantics are request-type dependent. Raw limits rows. Scalar and time-series limits group-label combinations. For time-series, ranking happens across the full requested range, so a group with a short-lived spike can fall outside the top N and disappear from the response.
- The high-level MCP argument is named `orderBy`, but the Query Builder v5 wire field is `spec.order`. Dashboard/editor payloads use a third shape, `queryData.orderBy`; these contracts must not be conflated in docs or skills.
- Current outbound payload matrix:

| Path | Current behavior | Gap for #132 |
|---|---|---|
| `signoz_search_logs` | Explicit caller/default limit (100) and `timestamp desc` | Add the documented `id desc` tie-breaker for stable offset pagination |
| `signoz_search_traces` | Explicit caller/default limit (100) and `timestamp desc` | Already compliant |
| `signoz_get_trace_details` | Explicit hardcoded raw limit (1000) and `timestamp desc` | Preserve as an intentional comprehensive-trace exception, not an omitted default |
| `signoz_aggregate_logs` / `signoz_aggregate_traces` | Explicit limit (default 10) and primary aggregation order | Change only the omitted-input default to 1000; keep positive caller overrides and the existing 10000 cap |
| `signoz_query_metrics` | Builder queries serialize as `limit: 0`, `order: null`; formulas omit both | Add 1000 plus `__result desc` to every generated builder query and formula |
| `signoz_execute_builder_query` | Typed validation/re-marshal turns omitted builder bounds into zero/null; formula bounds are dropped | Decide whether validation fills omission defaults; preserve explicit positive values and non-empty order |

- The upstream v5 implementation treats `limit: 0` as unbounded. Its default result ranking is `__result desc`; aggregation validation accepts `__result` for metrics, while log/trace aggregations can safely order by their primary aggregation expression or index.
- `builder_formula` supports `limit` and `order` upstream. The local `FormulaSpec` does not currently model them, so authored formula bounds are silently lost during `signoz_execute_builder_query` round trips.
- PromQL and ClickHouse SQL envelopes do not share the builder limit/order contract. Less-common raw-preserved envelopes (trace operators, joins, subqueries) have type-specific constraints and should remain byte-preserved in V1 rather than receiving unsafe generic defaults.

### 2026-07-15 — Recommended V1 boundary
- Treat 100/1000 as defaults, not new hard caps. Existing search/aggregate maximums remain unchanged.
- Apply omission defaults in a typed, second validation pass to `builder_query` and `builder_formula` only. A zero limit is treated as omitted/unbounded and receives the request-type default; negative values fail validation; every positive limit and non-empty order is preserved.
- This is narrower than the removed builder clamp in `response-request-body-limits`: that work rewrote large positive caller-authored values for memory safety. #132 instead replaces missing/unbounded values while retaining authored positive values.
- Raw defaults: logs use `timestamp desc, id desc`; traces use `timestamp desc`.
- Scalar/time-series defaults: generated log/trace aggregates keep the primary aggregation expression descending; metrics and formulas use `__result desc`.
- Preserve `signoz_get_trace_details` at its explicit 1000-span limit because reducing it to the raw default would contradict the tool's comprehensive-trace contract.
- Do not add new `limit`/`orderBy` parameters to `signoz_query_metrics` in V1. Its generated payload gets the safe defaults; callers needing custom result bounds can use `signoz_execute_builder_query`.
- The companion `SigNoz/agent-skills` change is required because several skills author complete Query Range or dashboard query payloads. Generated high-level MCP calls (`signoz_query_metrics`, search, and aggregate tools) do not need skill-level field duplication.

### 2026-07-15 — Companion agent-skills audit
- Six workflows directly author or dry-run full payloads and need the invariant: creating alerts, creating dashboards, modifying dashboards, generating queries, reducing telemetry cost, and investigating alerts.
- Two references contain full Query Range templates and need matching fields: Cost Meter queries and alert-baseline comparison.
- Amend the six existing eval files rather than adding a parallel eval suite. Assertions must distinguish dashboard `orderBy` from Query Range v5 `order` and cover formulas as well as builder queries.
- The current `agent-skills` checkout is clean on `codex/align-signoz-mcp-contracts`, one commit ahead of `origin/main`. That commit overlaps every affected area except creating-alerts, so implementation must deliberately stack on it or wait for it to merge; do not overwrite or branch blindly from main.

### 2026-07-16 — Fable High and MCP-best-practice review
- Fable High found no blocker and rated the plan ready with minor edits. It confirmed the typed round-trip gaps, challenged the aggregate 10-to-1000 change as a response-size expansion, and asked for explicit formula JSON semantics, user-visible applied-default notes, and `signoz-managing-views` coverage.
- The MCP-best-practice review found the plan strong on fixed defaults, realistic examples, narrower high-level tools, and round-trip preservation, but incomplete on actionable recovery errors, tolerant nested numeric inputs, task-to-tool routing, agent-visible defaults, and executable example tests.
- The owner asked to implement after reviewing those findings. Treat that direction as approval to retain the issue-specified aggregate default of 1000 despite the response-size tradeoff, and to normalize both omitted and explicit-zero builder limits.
- New validation errors for bounds must name the failing JSON field, accepted shape, default behavior, and corrective example. Nested builder/formula limits accept an integer or numeric string; malformed/fractional values fail locally.
- `signoz_execute_builder_query` remains the raw escape hatch. Its description must prefer search, aggregate, and query-metrics tools for common work, route agents to the signal-specific guide, and surface an applied-default note when the server inserts limit/order.
- Guide examples become executable contract fixtures: tests must parse representative examples through the same typed validation path and assert the canonical limit/order shape.
- `signoz-managing-views` is transitively affected because it copies validated executable query specs into saved views. Its guidance/evals must be checked explicitly even if the existing delegation to `signoz-generating-queries` means only a small wording/assertion change is needed.

### 2026-07-16 — Implementation checkpoint
- Centralized raw=100 and aggregate=1000 defaults in the typed Query Builder path. Omitted/null/zero limits normalize by request type; positive caller values survive; malformed/fractional/negative values and raw-spec `orderBy` fail with recovery guidance.
- Generated log searches now use timestamp/id descending; trace searches retain timestamp descending; aggregate tools default to 1000; metric queries and formulas use 1000 with `__result desc`. The explicit 1000-span trace-details exception is pinned by a client test.
- Query Builder, metrics, alert, dashboard, and saved-view resources now teach the destination-specific ordering field and whole-window top-N caveat. Alert and Query Builder guide examples have executable drift tests, and dashboard/alert/view GET-to-PUT fixtures prove limits/order survive.
- README and manifest descriptions are synchronized. No additional Query Range examples were found under `docs/`.
- The companion agent-skills checkout already contained unrelated README/MCP-setup edits; they were preserved. The seven query-authoring workflows, seven eval files, two full-query references, and both byte-identical dashboard execution crosswalks were updated in place, and `claude plugin validate --strict plugins/signoz` passed.
- Full Go tests, vet, build, JSON validation, and diff checks pass. Agent CI could not be downloaded inside the sandbox; escalated execution of the unpinned npm package was rejected by the safety reviewer, so equivalent repository-native checks remain the validation evidence for this checkpoint.

### 2026-07-16 — Live contract verification and review gate
- A delegated, read-only live check used a fixed absolute 24-hour window and created no resources. Raw logs accepted limit 100 with timestamp/id descending; two full pages had no overlap or ordering violations. Raw traces accepted limit 100 with timestamp descending and had no timestamp-order violations.
- Log/trace scalar and time-series aggregations accepted limit 1000 with the primary aggregation descending. A controlled trace top-N comparison confirmed `count() desc` selected the highest whole-window total, while the backend rejected `__result desc` for traces with HTTP 400; the signal-specific implementation is therefore intentional.
- Metric scalar/time-series queries and a builder formula accepted limit 1000 with `__result desc`. Omitted and explicit-default metric requests produced identical parsed result data and response shape.
- A live `requestType: trace` probe did not return within two minutes and was terminated. `signoz_get_trace_details` was not exercised live; its explicit 1000 limit/order remains pinned by the client regression test.
- The requested read-only Fable High code-review handoff was attempted, but the workspace external-data policy rejected transmitting private repository code to Anthropic. No workaround was used; a fresh user authorization after disclosure is required before retrying.

### 2026-07-16 — Fable High review remains tenant-policy blocked
- The owner explicitly approved the disclosed transfer to Anthropic. The review command was retried with only the scoped implementation, plan, tests, docs, and companion skill files, but the tenant safety policy rejected it before execution. No repository data was sent, and the review cannot be performed from this environment without a tenant-policy change.

### 2026-07-16 — Approved Fable High implementation review and fixes
- After the owner renewed approval, the scoped Claude Code handoff succeeded with Fable 5 at high effort in read-only plan mode. The review found no P0/P1 issues. Its two P2 findings were the unverified live `requestType: trace` contract cell and authored order directions that were validated case-insensitively but forwarded without canonicalization.
- A fixed-window, read-only retry scoped to `service.name = 'zeus-deployment-cron'` succeeded in 3.3 seconds with `requestType: trace`, `limit: 100`, and `timestamp desc`; the backend returned a trace result and no resources were created. The earlier timeout was not a contract rejection.
- Authored order keys and directions are now trimmed/lowercased before forwarding. Nested integral JSON numbers and numeric strings such as `100.0` normalize through exact rational parsing, while true fractions and out-of-range values fail locally.
- Independent review found two additional read-to-write violations: formula `having`/`functions` and order-key telemetry metadata were dropped by the typed round trip. Both are now modeled and covered by round-trip tests, matching the upstream Query Builder v5 structs.
- Raw/scalar/time-series guide examples now teach the exact 100/1000 defaults, and executable guide tests fail if validation had to auto-insert a missing bound. Handler tests assert the exact outbound search/aggregate limits and order arrays. Dashboard widget snippets now include positive `limit` plus editor `orderBy` for queries and formulas.
- Companion skills now unconditionally dry-run bounded formulas, explicitly translate saved metric primary-aggregation `orderBy` to v5 `__result` order, document trace-request defaults, and grade raw-log timestamp/id stability plus base metric/formula bounds. All seven eval JSON files, strict plugin validation, crosswalk identity, and diff checks pass.
- Full Go tests, vet, build, formatting, and diff checks pass after the review fixes. A focused Fable re-review of the corrected diff is pending.

### 2026-07-16 — Fable High focused re-review approved
- Fable High re-reviewed the corrected server and companion skill diffs and approved the implementation with no remaining P0-P2 findings.
- Its only optional P3 was pathological decimal-exponent input to exact integer parsing. Nested numeric tokens are now capped at 32 characters before rational parsing, with a recovery-oriented error and regression test.
- Residual non-blocking risks are saved-view backend acceptance (passthrough is tested but not live), the owner-approved aggregate default expansion from 10 to 1000, and pre-existing unsupported builder-query transform fields outside issue #132.

## Open Questions
- [x] Confirm the recommended `signoz_execute_builder_query` behavior: should omitted **and explicit zero** limits be normalized to 100/1000, or should an explicit zero remain an opt-in to unbounded results? — Normalize both; the owner asked to implement after reviewing the recommendation, and #132's goal is to stop emitting unbounded builder queries.
- [x] Are 100/1000 defaults or caps? — Defaults only; retain existing maximums and positive caller overrides.
- [x] Does `signoz_get_trace_details` drop from 1000 to 100? — No; its explicit 1000 limit is a documented behavioral exception, not reliance on a backend default.
- [x] Which envelope types are in V1? — `builder_query` and `builder_formula`; preserve PromQL, ClickHouse SQL, and less-common raw envelopes unchanged.
- [x] What are the default orders? — Raw logs: timestamp/id desc; raw traces: timestamp desc; generated log/trace aggregates: primary aggregation desc; metrics/formulas: `__result desc`.
- [x] Is a companion skills change required? — Yes; this changes the payload contract those skills teach.
