# Plan: Trace details on the new trace routes

Status: In Progress
Issue: SigNoz/nerve-pod#651
PR:

## Context

`signoz_get_trace_details` returns every span of a trace in one result, up to 1,000. In the
assistant's usage 39% of its results were too large for the client and were saved to a file, and a
trace over 1,000 spans is read without its root (nerve-pod#31).

SigNoz is adding routes that return a trace in bounded pieces: `/summary` and `/highlights`,
`/spans` with `view`, `query`, `selectFields`, `offset` and `limit`, `/spans/{spanID}` and its
`children`, `descendants` and `ancestors` lists, and the existing `/thread`. They are in SigNoz PRs
#13653, #13657, #13664, #13667 and #13745, not yet merged or released.

Open questions:

- A span in a list prints empty `attributes` and `events` unless `selectFields` asks. In the eval
  agents read that as "this span has none" in about 27 answers. The `selectFields` description says
  what the empty fields mean; whether to drop them from the result is not decided.
- On a small trace the old tool answered better where the answer sat in a span attribute, because it
  returned every span whole. Returning a trace of up to 50 spans whole on the first call is not
  built and was not evaluated.

## Approach

One tool, the same name. The inputs choose the result: `traceId` alone for the overview, `spanId`
for one span, and `scope`, `view` or `search` for a paged list. `view: thread` returns the AI
conversation. The MCP server owns the envelope and three things the routes leave to it: one opaque
`cursor` for every paged result, the 50,000-character limit, and cutting long values.

Constraints:

- The routes are not in a SigNoz release. `SIGNOZ_SPEC_REF` points at the head of SigNoz/signoz#13745
  (`eed2c7d177`) so the client is generated from a spec that has them. The pin must move to the first
  release with the routes before this merges.
- That spec is newer than the last release in other places too. `make gen` also changed the
  dashboard create and update input schemas (a description, and `audit` in an enum) and four
  generated types the server does not read differently.
- The e2e suite casts a SigNoz release without the routes. The e2e tests that call this tool
  (`test_trace_fields.py`, `test_ai_traces.py`, `test_output_envelopes.py`) are not updated yet and
  fail against the pinned release with this change. This PR cannot merge before the SigNoz release.
- Breaking (CMP-1): the result envelope changes, and `includeSpans` is retired with a coded
  validation error that names its replacement (CMP-2).

## Files to Modify

- `Makefile`, `skaff.yml` — the spec pin and the eight trace operations
- `internal/apiclient/`, `internal/handler/tools/schemas/` — regenerated
- `internal/client/trace_details.go` — the five route calls, on the generated client
- `internal/client/client.go`, `interface.go` — remove `GetTraceDetails`
- `internal/handler/tools/trace_details.go` — the handler
- `internal/handler/tools/traces.go`, `query_param_descriptions.go` — registration and descriptions
- `internal/mcp-server/testdata/wire-catalog/tools-list.json` — this tool's entry, and the two
  dashboard entries the regenerated schemas changed
- `README.md`, `manifest.json` — the tool reference

## Key Decisions

### 2026-11-26 — One tool, with inputs that choose the result

- Decision: keep `signoz_get_trace_details` and add `spanId`, `includeOverview`, `scope`, `view`,
  `search`, `selectFields`, `pageSize` and `cursor`.
- Rationale: every call is about one trace, so the agent picks inputs and each session carries one
  description (SUR-3, SUR-4).
- Alternatives rejected: a second tool for the lists. It removes the rules about which input applies
  where, at the cost of one more tool to choose from. Not evaluated.

### 2026-11-26 — `includeOverview` as an input

- Decision: `traceId` with `spanId` returns the span alone; `includeOverview` adds the trace's
  overview beside it.
- Rationale: in an eval of five variants (11 questions, 2 runs each) the input read the least, 1.5
  calls per answer against 1.9 for the span alone, with correctness the same. Returning the overview
  always cost 49% more characters and the agent still asked for the overview on its own.

### 2026-11-26 — Guidance in the description, no skill

- Decision: the tool description says which call answers which question.
- Rationale: in an eval of three variants (13 questions, 2 runs each) the guidance and a skill both
  made the agent pick `view: error_origins` for "which call under this span failed"; the plain
  description did not. The skill cost about 37% more per run, the guidance nothing measurable.
- Gap: the eval ran the guidance as 854 characters appended to the description. The shipped
  description folds it into the 1,024-byte budget, so the exact text was not the one evaluated.

### 2026-11-27 — A single span keeps its values whole

- Decision: `traceId` with `spanId` returns the span as SigNoz sends it. Only a span too long for one
  result has its longest values cut, then its events dropped from the end. `selectFields` applies to
  lists only.
- Rationale: `/spans/{spanID}` has no field selection, so reading one value whole meant fetching the
  span again and picking it out in the MCP server. That existed only to undo a 500-character cut the
  tool itself made, and it cost an extra call on the stack-trace eval question.

### 2026-11-27 — SigNoz validates and names; the tool only routes

- Decision: the handler checks which inputs combine and nothing else. View names and page sizes go
  to SigNoz unchecked, and the overview keeps SigNoz's keys (`errorOrigins`, not `error_origins`).
- Rationale: `docs/testing.md` rules out a client-side copy of upstream validation, because the copy
  drifts. The first version of the handler had one for views and page sizes.
- Consequence: the eval prototype named the overview's views by their `view` values. The shipped
  keys differ, and a note maps each key to the `view` that lists it.

### 2026-11-26 — The size limit counts the escaped text

- Decision: a result is measured as the JSON string a client receives.
- Rationale: in the eval a list page whose raw JSON was capped at 48,500 characters was counted by
  the client as 54,955 and saved to a file.

## Reference Links

- [nerve-pod#31](https://github.com/SigNoz/nerve-pod/issues/31)

## Verification

- `go test ./...` passes, with handler tests for each kind of call, the cursor, the size limit and
  the rejected input combinations.
- Eval, 40 questions, 2 runs each, Sonnet, on a Python prototype of this contract against a SigNoz
  built from the route PRs: 76 of 78 answers correct against 73 of 78 for the current tool, 2.1 tool
  calls per answer against 2.7, and 3 results over the client's limit against 19.
- Live check of this code: a server built from this branch, against a SigNoz built from the route
  PRs, answered 10 of the eval questions (1 run each, Sonnet): 9 correct, 1.6 tool calls per answer,
  no result over the client's limit. The miss is the small-trace case in Open questions. Results
  carried `webUrl` and their notes. This is a spot check, not the 40-question eval.
- After moving to the generated client the live check was repeated on 6 questions: 6 correct. On the
  final code, 7 questions: 7 correct, 1.1 tool calls per answer.
- `GOTOOLCHAIN=go1.26.11 make ci` passes on the staged change: format, lint, dependencies, build,
  race tests, guardrails, protocol, conformance, e2e style, and repository docs. `make gen-check`
  was not run.
- Not run: the e2e suite, for the reason in Approach.

## Outcome

