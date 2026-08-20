# Plan: AI Aggregation Query Guidance

## Status
Done — the server-side change is complete. The capability it documents depends on SigNoz PR #12121
shipping; on older backends the guide's scalar/time_series AI examples are rejected by the server,
which the AI section already warns about for the envelope type as a whole.

## Context

`builder_ai_query` gained `scalar` and `time_series` request types in SigNoz PR #12121, on top of
the existing `raw` and `trace` modes. The traces query-builder guide still said "Two request types
are supported", so an agent asking "total LLM cost per model" or "token usage over time" had no way
to build a correct AI aggregation and would fall back to `builder_query`, which aggregates over
gen_ai *spans* rather than per-trace values — plausible but wrong numbers for any trace with more
than one LLM call.

## Approach

Teach the three things the model cannot infer from the spec shape:

1. **The domain split.** Within an aggregation expression, a bare key aggregates spans while a
   `trace.`-prefixed key aggregates one window-clipped value per trace. `count(trace.trace_id)` is
   the trace-count idiom; plain `count()` counts spans.
2. **The rules that hard-error** rather than silently degrading: no mixing domains in one query,
   groupBy on span attributes only, span and trace-level filter conditions may be AND-ed but never
   OR-ed, no `*If` combinators over `trace.` columns, unknown `trace.` columns rejected with the
   usable-column list. Also NULL semantics for tool-only traces (avg/sum skip, not zero).
3. **The spellings that fail SILENTLY**, which matter more than the ones that error because nothing
   signals them: `count()` where `count(trace.trace_id)` was meant, and the `input`/`output` preview
   columns used in a filter or groupBy, where the bare names resolve as nonexistent span attributes
   and yield a single NULL bucket or zero matches with a 200.

Tool descriptions gain the word "aggregated" so AI aggregation questions route to the AI envelope
instead of `signoz_aggregate_traces`.

No validator changes: `builder_ai_query` already flows through builder validation with
`signal: traces` pinned, and the auto-applied default order (by aggregation expression) is accepted
by the backend for both scalar and time_series.

## Files to Modify

- `pkg/querybuilder/traces_guide.go` — AI section: two request types → four; new "Aggregations"
  subsection covering the domain split, usable trace-level columns, and the error rules; a
  "Common questions → query shape" block mapping the asks users actually make (top-N by attribute,
  above-threshold variants, trace-vs-span counting, the has_error reformulation, costliest
  prompt/conversation) onto query shapes, plus the display-only-column note covering the
  `input`/`output` trap; two new examples (scalar cost/trace-count per model, time-series tokens
  with a mixed filter).
- `internal/handler/tools/query_builder.go` — `signoz_execute_builder_query` description: note that
  `builder_ai_query` covers aggregated LLM metrics, not only per-trace ones. Tightened surrounding
  wording to stay inside the 1024-byte contract budget.
- `internal/handler/tools/traces.go` — `signoz_aggregate_traces` description: state that AI
  aggregates belong to `builder_ai_query`, and why (this tool counts per span, not per trace).
- `README.md` — the `builder_ai_query` bullet listed only `raw` and `trace`; extended to cover
  `scalar`/`time_series` and the `trace.` prefix. Required by CMP-3, which keeps README, manifest,
  docs, and tests moving with the contract in one PR.

## Verification

- `go build ./...`, `go test ./...` — all 17 packages pass, including
  `TestGuardrail_WireContractBudgets` and `TestQueryBuilderGuideExamplesUseExecutableBoundsContract`.
- Every JSON example in the guide was scraped by the header→JSON pattern (7 total, numbered and
  unnumbered) and each of the 4 `builder_ai_query` examples was executed against a live SigNoz with
  seeded gen_ai traces. All returned 200; the scalar example returned the expected per-model cost
  and trace counts.
- Backend behaviours documented in the guide were each confirmed live against seeded data rather
  than read off the source: domain-mixing, groupBy-on-trace-column, OR-mixed filters, `*If` over
  `trace.` columns, and unknown `trace.` columns all return targeted 400s; tool-only traces
  contribute NULL rather than 0.
- The "Common questions" block was validated by running each shape against container-tagged seed
  data: top-N by resource attribute (with and without a token threshold, ranked by trace count and
  by token sum), resource+span multi-key grouping, and per-container time series with top-N pruning.
- The prompt/cost shapes were validated against traces seeded with distinct prompts and costs:
  `trace` mode ordered by `estimated_total_cost desc` surfaces the costliest prompt with its preview,
  and groupBy `gen_ai.input.messages` ranks spend per distinct prompt. The documented trap was
  reproduced: `groupBy: input` returns 200 with one `input: null` bucket, and `input = '...'` returns
  200 matching nothing, while `gen_ai.input.messages` matches correctly.
