# Feature: AI Aggregation Query Guidance — Context & Discussion

## Original Prompt
> can you update the mcp server to reflect the changes we have done here so that it can answers
> question related to ai aggregation queries in list and aggregated views

Follow-up to a review + live edge-case testing session against the SigNoz backend branch that adds
scalar / time-series support to `builder_ai_query`.

## Reference Links
- [SigNoz PR #12121 — ts/scalar for llm spans](https://github.com/SigNoz/signoz/pull/12121)
- [SigNoz PR #12027 — QB support for llm trace list and span list](https://github.com/SigNoz/signoz/pull/12027)

## Key Decisions & Discussion Log

### 2026-08-07 — scope
- Backend PR #12121 adds `scalar` and `time_series` request types to `builder_ai_query`; the guide
  documented only `raw` and `trace`, so the server could not answer aggregated AI questions.
- Guide content (`signoz://traces/query-builder-guide`) is the primary lever; tool descriptions are
  the secondary lever because they decide whether the model reaches for the AI envelope at all.

### 2026-08-07 — no validator changes needed
- `pkg/types/querybuilder.go` already routes `builder_ai_query` through builder validation and pins
  `signal: traces` on unmarshal, so `defaultOrderForQuery` returns order-by-aggregation-expression
  for scalar/time_series.
- Verified live that the auto-applied default (`order by "avg(trace.output_tokens)" desc`) is
  accepted by the backend: `traceAggOrderIndex` matches an order key by aggregation expression as
  well as by alias. Scalar and time-series, with and without groupBy, all returned 200.
- Decision: leave the validator untouched. No new request-type gating for AI queries.

### 2026-08-07 — example numbering
- `pkg/types/querybuilder_examples_test.go` validates examples matching `--- Example <n>: ... ---`
  and asserts an exact count (3 for traces) plus default limit/order teaching.
- The pre-existing AI examples are deliberately unnumbered and therefore unvalidated; the trace-mode
  one teaches `limit: 20`, which would fail the contract test's `defaultLimitForRequestType` check.
- Decision: keep new AI examples unnumbered for local consistency rather than renumber and dilute
  the trace example's teaching value. Validated them out-of-band instead (see Verification).

### 2026-08-07 — wire-contract budget
- The first wording of the `signoz_execute_builder_query` description pushed it to 1083 bytes against
  the 1024-byte `TestGuardrail_WireContractBudgets` limit.
- Per `CLAUDE.md`, the guardrail was NOT relaxed. The description was tightened instead
  (compressed the existing prompt/response phrasing) so the added "aggregated" routing signal fits
  with headroom to spare.

### 2026-08-07 — example block convention
- Initial draft put explanatory prose between an example header and its JSON, which silently broke
  header→JSON scraping. Moved the prose above the header so every example in the guide keeps the
  `header, blank line, JSON` shape that the contract test's pattern depends on.

### 2026-08-07 — README sync (CMP-3)
- The working tree already carried in-flight `builder_ai_query` work (`manifest.json`,
  `pkg/types/querybuilder.go` + test, and part of the `README.md` diff) from the earlier raw/trace
  support. Left untouched except for one line.
- That README bullet enumerated only `raw` and `trace`, so it went stale for the same reason the
  guide did. CMP-3 requires README, manifest, docs, and tests to move with the contract in the same
  PR, so the bullet was extended to cover `scalar`/`time_series` and the `trace.` prefix.
- `manifest.json` needed no change: its description already says "AI/LLM trace queries
  (builder_ai_query)" without enumerating request types.

### 2026-08-07 — user-question sweep
- Exercised the guidance against the questions a user actually asks, seeded with container-tagged
  gen_ai traces. "Top N containers whose traces exceed X tokens" works end-to-end: resource-attribute
  groupBy, a `trace.total_tokens` threshold, ranked by either `count(trace.trace_id)` or
  `sum(trace.total_tokens)`, with sub-threshold containers correctly dropped.
- Also confirmed live: resource+span multi-key groupBy, per-container time series with and without
  top-N pruning, avg LLM calls per trace, top users by tokens.
- Found two natural questions that are NOT expressible: "how many AI traces had errors"
  (`trace.error_count`) and "traces with more than N spans" (`trace.span_count`). Root cause is
  principled, not a gap: those columns aggregate over every span, while the scalar/time-series scan
  is gate-masked to gen_ai spans, so computing them there would silently use the wrong span set.
  That is why they are non-`Orderable` in the scope definition.
- Decision: teach the reformulation rather than treat it as a limitation to fix. Added a
  "Common questions → query shape" block to the guide covering top-N-by-attribute, the
  above-threshold variant, `count(trace.trace_id)` vs `count()`, the has_error reformulation, and
  the fact that span/trace domains cannot share one table.

### 2026-08-07 — prompt/cost questions and the input/output trap
- "Find the input that cost the most" is a LIST question, not an aggregation one: `trace` mode
  ordered by `estimated_total_cost desc` returns the prompt preview beside cost and tokens. Verified
  against seeded traces with distinct prompts and costs; the $4.20 contract-summary prompt ranks first.
- The aggregated variant also works and is worth teaching: groupBy `gen_ai.input.messages` with
  `sum(trace.estimated_total_cost)` gives a ranked total per distinct prompt.
- Trap found: `input` / `output` are `SpanLevel` preview columns, so in a filter or groupBy the bare
  names resolve as ordinary (nonexistent) span attributes. `groupBy: input` returns 200 with a single
  `input: null` bucket, and `input = '...'` returns 200 matching zero traces — plausible output, no
  error. The correct keys are `gen_ai.input.messages` / `gen_ai.output.messages`; `trace.input` is
  rejected outright with a targeted 400.
- Decision: document both answer shapes and the bare-name trap in the guide's common-questions block.

## Open Questions
- [ ] EVL-1 suggests before/after prompt evals for selection-critical description changes. Not run
      here; the change is additive routing vocabulary ("aggregated"), no boundary was narrowed.
      Worth a spot check if AI queries start landing on `signoz_aggregate_traces`.
- [x] Does `source: "ai"` need documenting? No — resolved upstream. The backend selects the AI path
      purely by envelope `type`; `spec.source` is never read for `builder_ai_query`, and
      `telemetrytypes.Source` defines only audit/meter/unspecified.
