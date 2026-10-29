# Plan: AI Observability Support

Status: In Progress
Issue: https://github.com/SigNoz/nerve-pod/issues/289
PR:

## Context

Agents need to answer AI observability questions in SigNoz: cost, tokens, latency and errors per
model or tool call and per AI trace, which models are unpriced, and whether a framework's spans
are mapped to `gen_ai.*`. The design was agreed in the Notion doc "AI Observability in the MCP
Server" ([PRD] ai-olly). It supersedes PR #279.

The pinned SigNoz spec (`SIGNOZ_SPEC_REF` 09bba89) already has the `builder_ai_query` envelope
type, the `signoz/AIBuilderQuery` dashboard plugin, and the AI fields, pricing, span mapper and
trace thread endpoints, so no spec bump is needed. Each PR adds the operations it uses to
`skaff.yml` and regenerates the client.

Not planned: evals and scores (no backend), AI-specific ClickHouse SQL guides, sessions (until
SigNoz supports them).

Open question (PR 5): how `signoz_get_trace_details` exposes the thread, a mode parameter or
thread parameters beside the existing ones, and how its cursor paging maps to the tool's
pagination contract.

## Approach

- **Same tools, explicit routing.** `queryType` (`builder_query` default, `builder_ai_query`) on
  `signoz_search_traces`, `signoz_aggregate_traces` and `signoz_get_field_keys`. On the trace
  tools it becomes the envelope `type`; on field keys it selects the AI fields keys endpoint.
  `signoz_execute_builder_query`, alerts, dashboards and views already carry the envelope type.
- **The agent picks `queryType`.** Its description names the eight per-trace keys that need
  `builder_ai_query` (`trace.llm_call_count`, `trace.tool_call_count`,
  `trace.distinct_tool_count`, `trace.input_tokens`, `trace.output_tokens`,
  `trace.total_tokens`, `trace.estimated_total_cost`, `trace.max_llm_duration_nano`). The MCP
  never inspects the query to choose the type.
- **No MCP-side validation.** SigNoz judges keys, operators, windows and values; its errors go
  through `upstreamError` (ERR-6).
- **Search picks the row shape with `requestType`**: `raw` (default, one row per span) or `trace`
  (one row per trace). `queryType` only sets the envelope type, so both query types support
  both shapes. `orderBy` orders span rows by span fields (default `timestamp desc`) and AI trace
  rows by `trace.*` keys (default latest activity); SigNoz ignores it for plain trace rows.
- **Reads only** for pricing and mappings.
- **Naming:** `ai`, never `llm`, on MCP surfaces; upstream column names stay as SigNoz defines
  them.
- **A separate guide**, `signoz://ai/query-builder-guide`, linked from the trace tool and
  `signoz_execute_builder_query` descriptions.
- **Saved AI queries** need only e2e coverage. Full `builder_ai_query` requests come from the
  SigNoz spec (`ai_traces_*` examples on `query_range`) through `make gen`, so the guide holds
  only MCP tool-call examples.

### PR breakdown

1. **`feat(fields)`: `queryType` on field keys.** `builder_ai_query` lists the computed per-trace
   fields (even before data arrives) with the span fields and `gen_ai.*` attributes, whatever the
   `signal`. `fieldContext` on field keys accepts `trace`, the whole-trace level; today only the
   AI keys endpoint lists fields under it. `signoz_get_field_values` gets neither: nothing is
   stored under `trace`, so its values are always empty, and `gen_ai.*` values already come
   from the default endpoint.
2. **`feat(traces)`: AI trace search.** `queryType`, `requestType` and `orderBy` on
   `signoz_search_traces`, built by `SpanSearch` (raw) and `TraceList` (trace).
   2b. **`feat(traces)`: AI aggregates and guide.** `queryType` on aggregate, the
   `builder_ai_query` default limit in `ApplyBounds`, and the AI guide. Companion agent-skills
   PR.
3. **`feat(ai)`: pricing and mapping read tools.** `signoz_get_ai_pricing`
   (`view: rules|unpriced_models`) and `signoz_list_ai_attribute_mappings`. Additive, so no
   agent-skills change is required.
4. **`feat(ai)`: AI result content and routing wording.** Ships with the routing fixes from the
   Haiku eval (see the 2026-10-29 decisions). Notes on `builder_ai_query` results (done: trace-row
   window and full-trace values, aggregation window, cost as a USD estimate, cache tokens inside
   `total_tokens`, wider window when empty); a $0-cost hint pointing at unpriced models (done as
   fixed pointers: the trace-rows note and a note on cost aggregations name
   `signoz_get_ai_pricing`); the empty-result note also points at
   `signoz_list_ai_attribute_mappings` (done); `gen_ai.*` in `signoz_get_trace_details` default columns
   (done: operation, model, input and output tokens, cost, tool, agent; null on other spans).
5. **`feat(traces)`: AI thread in `signoz_get_trace_details`.** Reads
   `GET /api/v1/traces/{traceID}/thread`: the trace's model, tool and agent spans in time order
   with decoded input and output messages, paged by cursor. Deferred: see the 2026-10-28
   decision below.

## Files to Modify

- `skaff.yml`, `internal/apiclient/` (generated) — AI fields, pricing, mapping, thread operations
- `internal/client/client.go`, `internal/client/interface.go` — client methods
- `internal/handler/tools/fields.go` — `queryType` on field keys (PR 1)
- `internal/handler/tools/traces.go` — `queryType`, `orderBy`, result notes, thread (PRs 2, 4, 5)
- `pkg/querybuilder/` — default limit and AI guide (PR 2b)
- `internal/handler/tools/` — pricing and mapping tools (PR 3)
- `guardrails/policy.go` — exceptions for `signoz_search_traces` (PR 2) and
  `signoz_aggregate_traces` (PR 2b)
- `manifest.json`, `README.md`, `internal/mcp-server/testdata/wire-catalog/`, `tests/e2e/tests/`
  — per PR

## Key Decisions

### 2026-10-29 — Routing wording from a Haiku eval of real questions

- Decision: name the user's vocabulary (LLM, AI agent, token, cost, model, tool-call, prompt) in
  the `signoz_search_traces` and `signoz_aggregate_traces` descriptions; say in the aggregate
  `queryType` description and the guide that `count()` and `count_distinct(trace_id)` are
  span-level, so AI traces are counted in a separate call; name the user's LLM apps and agents in
  the server instructions, dropping a sentence rules 1 and 2 repeat and the heading to fit 2,048
  bytes; add a slowest-LLM-calls example to the guide.
- Rationale: of 36,655 questions to SigNoz's production assistant (Langfuse), about 475 were
  about users' own LLM or agent apps, and only 7 said "AI observability". Haiku, with plugin
  skills disabled, routed 12 of 15 adapted AI questions correctly and all 3 controls; the misses
  were "tool calls" and "prompt" questions read as about its own session, and 6 errors came from
  pairing `count_distinct(trace_id)` with `trace.` aggregations. The count wording removed those
  errors in a re-run; the instructions sentence fixed the prompt question but not the tool-call
  ones, which stay ambiguous inside a coding agent.

### 2026-10-29 — No per-tool truncation; the cost hint is a fixed pointer

- Decision: AI trace rows' `input` and `output` pass through uncut, and the unpriced-cost hint is
  a fixed pointer in the trace-rows note rather than a count from walking every row.
- Rationale: long values come back from many tools, so cutting them belongs in one cross-tool
  response-size change, not in each tool (raised in review of #410). Without the cut, a full
  decode and walk of every AI trace-rows response only bought the unpriced-row count; agents in
  the evals went to the pricing tool unprompted on empty cost, and the e2e already pins the
  per-trace columns.

### 2026-10-29 — Notes on `builder_ai_query` results

- Decision: `builder_ai_query` results carry short notes: trace rows say filters, orderBy, and
  the limit use the window while values cover the whole trace; aggregations say only in-window
  spans count; both say cost is a USD estimate (null when unpriced) and `total_tokens` includes
  cache tokens; empty results suggest a wider window, or finding the app's own model and tool keys
  with `signoz_get_field_keys` and mapping them to `gen_ai.*`. Plain
  `builder_query` results get none.
- Rationale: the guide is read in about 1 of 15 sessions, so caveats that change how a number is
  read travel with the result.

### 2026-10-27 — Same tools with an explicit `queryType`

- Decision: no `signoz_search_ai_traces` / `signoz_aggregate_ai_traces`.
- Rationale: routing stays observable and deterministic without duplicating 21 parameters.
- Alternatives rejected: routing on the `trace.` prefix (not reserved for AI); a `source`
  parameter (a storage separator); a `rows` parameter; auto-routing on the key list (hidden).

### 2026-10-27 — No MCP-side validation

- Decision: SigNoz is the only validator.
- Rationale: MCP-side checks drift from SigNoz.
- Exception recorded: deviates from SCH-2 (MUST).

### 2026-10-27 — Out-of-enum `queryType` on field keys uses the default endpoint

- Decision: only `builder_ai_query` selects the AI endpoint; other values run on the default one
  with the schema's "Input validation notice", as an out-of-enum `signal` does.
- Rationale: SigNoz never sees `queryType` on this tool, so the MCP must pick an endpoint, and
  the notice keeps the substitution visible.

### 2026-10-27 — Reads only for pricing and mappings

- Decision: no write tools.
- Rationale: both redeploy collectors, a wrong price is stamped permanently on ingested spans,
  and there is no price test endpoint.

### 2026-10-28 — Pricing and mapping read shapes

- Decision: `signoz_get_ai_pricing` pages rules across SigNoz's 100-rule page cap and filters and
  pages the unpriced-model list itself, because SigNoz returns that list whole with no search.
  `signoz_list_ai_attribute_mappings` joins each group with its mappers; a group whose mappers
  fail comes back with `mappers: null`, is named in `failedGroups`, and sets `incomplete`, while a
  401 or 403 fails the whole call.
- Rationale: one pagination contract for the agent and visible partial reads.
- PR 3 targets `main` on its own and merged after PR 2b, so it adds the AI guide's links: the
  cost warning to `signoz_get_ai_pricing` and the vendor-key warning to
  `signoz_list_ai_attribute_mappings`.
- Deferred from PR 3, then added in its own PR: `signoz_test_ai_attribute_mapping` wraps SigNoz's
  mapping test endpoint (`POST /api/v1/span_mapper_groups/test`). It defaults to the saved groups
  by sending each group without mappers, which SigNoz fills from the saved group of that name;
  an empty `groups` list tests none. The endpoint simulates in memory and saves nothing, so the
  client marks the POST replay-safe and `TestGuardrail_ReadOnlyPOSTRetries` covers it; a response
  without mapped spans is a coded upstream error. Its generated `SpanMapperTest` type landed in a
  `_test.go` file that `go build` skips, so `make gen-client` renames those files.

### 2026-10-27 — Guardrail exception for `signoz_aggregate_traces`

- Decision: accept going further over the 15-parameter cap with `queryType`.
- Rationale: the alternative is a separate tool pair, rejected above.

### 2026-10-27 — Full AI requests come from upstream examples

- Decision: SigNoz publishes `ai_traces_raw`, `ai_traces_raw_select_fields`, `ai_traces_trace`,
  `ai_traces_scalar` and `ai_traces_time_series` request examples ([SigNoz/signoz#13282](https://github.com/SigNoz/signoz/pull/13282));
  PR 2b pins `SIGNOZ_SPEC_REF` to that branch head and the guide points to
  `signoz://query-builder/examples` instead of carrying a request body.
- Rationale: one source of truth (construction before detection); `test_spec_examples.py` runs
  every published example against the pinned release.
- Follow-up: the SigNoz change merges first; bump the ref to its merge commit before PR 2b
  merges, then to the release tag.

### 2026-10-28 — The AI thread (PR 5) is deferred

- Decision: no thread support in `signoz_get_trace_details` for now. A built version
  (`view=thread`, with cursor paging, raw message attributes dropped, and a 1,000-character
  cut) was closed unmerged.
- Rationale: premature while SigNoz's thread endpoint is alpha; revisit when it stabilizes.
  The preview cut planned for PR 4 then lands with PR 4.

### 2026-10-28 — AI trace rows filter and rank on the window, then show full-trace values

- Decision: the guide says trace-level filters, orderBy, and the limit use in-window spans, while
  the returned columns cover each trace's full extent and the page is re-sorted on them.
- Rationale: SigNoz's trace-list SQL (`matched` CTE, then an enrichment pass with no timestamp
  bound; `TestBuild_FullSQL_TraceList_AggregateFilterOnly`) works this way; the guide had said
  only that columns span the whole trace (found in review of #404).

### 2026-10-28 — AI span rows are only the gen_ai spans

- Decision: descriptions and the guide say `builder_ai_query` span rows are the LLM, tool, and
  agent spans of AI traces, and the e2e seed adds a plain child span to prove it.
- Rationale: SigNoz gates raw AI rows on `gen_ai.request.model`, `gen_ai.tool.name`, or
  `gen_ai.agent.name` (found in review of SigNoz/signoz#13282); "spans of AI traces" overstated it.

### 2026-10-27 — No server-instructions line for the AI guide

- Decision: the trace tool and `signoz_execute_builder_query` descriptions link the guide; the
  server instructions stay unchanged.
- Rationale: the instructions hold cross-cutting rules and route to no other guide; agents reach
  AI queries through those tools.

### 2026-10-27 — `requestType` on `signoz_search_traces`

- Decision: `requestType` (`raw` default, `trace`) picks span or trace rows, independent of
  `queryType`; search takes a guardrail exception at 16 parameters. Out-of-enum `requestType` and
  `queryType` values run as `raw` `builder_query` with the validation notice, like an
  out-of-enum `queryType` on field keys.
- Rationale: `requestType: trace` is not AI-specific (it is the Traces tab list for
  `builder_query`), so tying it to `queryType` would couple row shape to the data source.
- Alternatives rejected: inferring trace rows from `trace.*` keys (fails for AI questions with no
  such key); a separate `signoz_list_traces` tool (repeats the filter, time and paging
  parameters).

## Reference Links

- [Notion: AI Observability in the MCP Server](https://app.notion.com/p/signoz/AI-Observability-in-the-MCP-Server-3f1fcc6bcd198054ab68e01f77ac8aed) ([PRD] ai-olly)

## Verification

- `make ci` and the new e2e cases per PR.
- PR 1: `make ci` passes. Checked by hand against a local SigNoz: `builder_ai_query` lists the
  eight `trace` columns and `gen_ai.*` keys, `signal=logs` returns the same set, the default
  keys omit the `trace` columns, and an out-of-enum `queryType` runs on the default endpoint with
  the validation notice. `fieldContext=trace` returns exactly the eight columns on the AI
  endpoint and is ignored by the default one. With fresh `gen_ai.*` spans, the default values
  endpoint returns `gen_ai.request.model` and `gen_ai.tool.name` values, confirming values need
  no `queryType`.
  The e2e cases run in CI.
- PR 2: checked by hand against a local SigNoz: span rows for both query types, `trace.*`
  filters on AI span rows, `trace.*` order keys rejected on span rows and honored on AI trace
  rows, plain trace rows slowest first with `orderBy` ignored, and an out-of-enum `requestType`
  running as span rows with the notice.
- PR 2b: checked by hand against a local SigNoz: `count_distinct(trace_id)` counts 2 AI traces
  with `builder_ai_query` and 3 traces without; `trace.` keys aggregate to null with
  `builder_query`; per-call input tokens by model match the trace rows, with a null group for
  spans without the model; SigNoz rejects OR across trace- and span-level filters; and
  `signoz_execute_builder_query` reports the default limit and inserts no order. Seeded cost reads
  back null, so the e2e asserts on tokens. The alert and dashboard round-trips run only in CI,
  because the local key cannot create them.
- PR 2b agent check: 15 questions through headless Claude Code against a local SigNoz. Routing
  was right for all 15. Two fixes followed: the guide now names which `ai_traces_*` example to
  copy (an agent had hand-built a request), and its resource description names cache tokens
  (an agent searched the docs first). The `signoz-generating-queries` skill loaded in 6 sessions
  and does not know the AI guide; the agent-skills companion should link it.
- PR 3: checked by hand against a local SigNoz: the mappings list returns the shipped
  `gen_ai.llm`, `gen_ai.tool` and `gen_ai.agent` groups with their mappers, and `llm.model_name`
  is a source of `gen_ai.request.model`. The local instance had no pricing rules, so the rules
  view was checked only for its envelope; the handler tests cover paging across SigNoz's
  100-rule page and local filtering.
- PR 3 agent check: 11 questions through headless Claude Code against a local SigNoz with no
  pricing rules, two unpriced models, and one OpenInference-only span. Routing and answers were
  right for all 11, including declining to add a price and keeping two negative questions off the
  AI tools. The span gained `gen_ai.request.model` at ingest. Fixes followed: `searchText` says an
  empty page means no match and that it matches substrings, and both tools name the AI
  Observability tab where prices and mappings are changed.
- Routing eval (2026-10-29): 18 questions adapted from real Langfuse questions, run through
  headless `claude -p --model haiku` with plugin skills disabled: 15 of 18 correct before the
  wording changes; the count-rule re-run had no 400s.
- Agent eval by hand: about 25 questions, including negative ones that must not set
  `builder_ai_query`, against a local SigNoz seeded with gen_ai traces, pricing rules and one
  unpriced model. Baseline before PR 2, rerun after PR 5.

## Outcome

This plan spans PRs 1 to 5 and stays `In Progress` until PR 5 merges; each PR updates its line.

- PR 1 (`feat(fields)`): `queryType` and `fieldContext=trace` on `signoz_get_field_keys`;
  `trace` is honored only by the AI fields keys endpoint.
- PR 2 (`feat(traces)`): `queryType`, `requestType` and `orderBy` on `signoz_search_traces`.
- PR 2b (`feat(traces)`): `queryType` on `signoz_aggregate_traces`, the AI guide, and the
  `builder_ai_query` default limit in `signoz_execute_builder_query`.
- PRs 3 to 5: not started.
