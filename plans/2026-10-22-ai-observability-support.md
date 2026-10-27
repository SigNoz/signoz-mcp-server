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
trace thread endpoints, so no spec bump is needed. Each PR adds the operations it uses to `skaff.yml` and
regenerates the client.

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
- **`orderBy` on search** for both query types; the description states each default
  (`timestamp desc` for `builder_query`, none for `builder_ai_query`).
- **Reads only** for pricing and mappings.
- **Naming:** `ai`, never `llm`, on MCP surfaces; upstream column names stay as SigNoz defines
  them.
- **A separate guide**, `signoz://ai/query-builder-guide`, plus one server-instructions line.
- **Saved AI queries** need only e2e coverage; AI examples for `pkg/*/examples.generated.md` come
  from an upstream SigNoz change and a later `make gen`.

### PR breakdown

1. **`feat(fields)`: `queryType` on field keys.** `builder_ai_query` lists the per-trace columns
   and `gen_ai.*` before data arrives, and returns trace fields whatever the `signal`.
   `signoz_get_field_values` gets no `queryType`: the per-trace columns have no value
   suggestions, and `gen_ai.*` values already come from the default endpoint.
2. **`feat(traces)`: query AI data.** `queryType` on search and aggregate, `orderBy` on search,
   the `builder_ai_query` default limit in `ApplyBounds`, the AI guide, and the instructions
   line. Companion agent-skills PR.
3. **`feat(ai)`: pricing and mapping read tools.** `signoz_get_ai_pricing`
   (`view: rules|unpriced_models`), `signoz_list_ai_attribute_mappings`,
   `signoz_test_ai_attribute_mapping`. Companion agent-skills PR (can share PR 2's).
4. **`feat(ai)`: AI result content.** Notes on `builder_ai_query` results (window, cost is an
   estimate in USD, wider window when empty, trace rows include spans outside the window,
   `total_tokens` excludes cache and reasoning tokens); previews cut to 1,000 characters with a
   truncation flag; a $0-cost hint naming unpriced models, with a WARN log when expected fields
   are missing; `gen_ai.*` in `signoz_get_trace_details` default columns. Depends on PRs 2 and 3.
5. **`feat(traces)`: AI thread in `signoz_get_trace_details`.** Reads
   `GET /api/v1/traces/{traceID}/thread`: the trace's model, tool and agent spans in time order
   with decoded input and output messages, paged by cursor. Messages use PR 4's preview cut. The
   endpoint is alpha upstream, so e2e pins its shape. Depends on PR 4.

## Files to Modify

- `skaff.yml`, `internal/apiclient/` (generated) — AI fields, pricing, mapping, thread operations
- `internal/client/client.go`, `internal/client/interface.go` — client methods
- `internal/handler/tools/fields.go` — `queryType` on field keys (PR 1)
- `internal/handler/tools/traces.go` — `queryType`, `orderBy`, result notes, thread (PRs 2, 4, 5)
- `pkg/querybuilder/` — default limit and AI guide (PR 2)
- `pkg/instructions/instructions.go` — routing line (PR 2)
- `internal/handler/tools/` — pricing and mapping tools (PR 3)
- `guardrails/policy.go` — exception for `signoz_aggregate_traces` (PR 2)
- `manifest.json`, `README.md`, `internal/mcp-server/testdata/wire-catalog/`, `tests/e2e/tests/`
  — per PR

## Key Decisions

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

### 2026-10-27 — Reads only for pricing and mappings

- Decision: no write tools.
- Rationale: both redeploy collectors, a wrong price is stamped permanently on ingested spans,
  and there is no price test endpoint.

### 2026-10-27 — Guardrail exception for `signoz_aggregate_traces`

- Decision: accept going further over the 15-parameter cap with `queryType`.
- Rationale: the alternative is a separate tool pair, rejected above.

## Reference Links

- Notion: AI Observability in the MCP Server ([PRD] ai-olly)

## Verification

- `make ci` and the new e2e cases per PR.
- PR 1: `make ci` passes. Checked by hand against a local SigNoz: `builder_ai_query` lists the
  eight `trace` columns and `gen_ai.*` keys, `signal=logs` returns the same set, the default
  keys omit the `trace` columns, and an out-of-enum `queryType` runs on the default endpoint with
  the validation notice. With fresh `gen_ai.*` spans, the default values endpoint returns
  `gen_ai.request.model` and `gen_ai.tool.name` values, confirming values need no `queryType`.
  The e2e cases run in CI.
- Agent eval by hand: about 25 questions, including negative ones that must not set
  `builder_ai_query`, against a local SigNoz seeded with gen_ai traces, pricing rules and one
  unpriced model. Baseline before PR 2, rerun after PR 5.

## Outcome

This plan spans PRs 1 to 5 and stays `In Progress` until PR 5 merges; each PR updates its line.

- PR 1 (`feat(fields)`): `queryType` on `signoz_get_field_keys`.
- PRs 2 to 5: not started.
