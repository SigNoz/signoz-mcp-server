# Plan: Result Size Budget for the Raw Searches

Status: Done
Issue: [SigNoz/nerve-pod#673](https://github.com/SigNoz/nerve-pod/issues/673)
PR: https://github.com/SigNoz/signoz-mcp-server/pull/476

## Context

Row limits do not bound result size. Over seven days of production telemetry (631,703 tool
calls, 98% on v0.19.0, which already defaults the raw searches to 20 rows), 11.6% of results
were over 50 KB serialized and 2,544 were over 2 MiB. In Claude Code alone, 10.5% of
`signoz_search_logs` and 7.6% of `signoz_search_traces` results were over 50 KB.

Clients handle an oversized result worse than the server can: Codex keeps the first and last
halves of about 48,000 bytes and drops the middle, Gemini CLI cuts above 40,000 characters,
Claude Code replaces a result over 50,000 characters with a file path, and hosted clients have
no file at all. A server-side cut keeps valid JSON and says how to continue.

This is the first of the issue's three PRs: the raw row tools `signoz_search_logs` and
`signoz_search_traces`. Series tools, list tools, and dashboards follow separately;
`signoz_get_trace_details` is covered by SigNoz/nerve-pod#651. Out of scope here: compact
output formats, cursor paging (see `plans/2026-10-27-query-tool-result-shaping.md`), lowering
the row count requested upstream, moving other notes into the JSON, and removing the prose
note.

One production caller matters for compatibility: a tenant's scheduled program polls
`signoz_search_logs` every 15 seconds with `limit: 1000` and gets 145 to 180 KB pages. It never
feeds a model. After this change it must follow `nextOffset` or pass a larger `maxResultBytes`.

## Approach

`rawSearchResult` splits the query_range body at the rows of its one result,
`data.data.results[0].rows[]`, with each row kept as `json.RawMessage`, so rows and integers
above 2^53 keep their bytes. It measures the UTF-8 bytes of every text block, and when the
result is over budget keeps the longest prefix of whole rows that fits. When the first row alone
does not fit, it shortens that row's long strings and returns it. A page that keeps every row is
SigNoz's body with a top-level `pagination` object added; a cut page re-encodes the objects
around the rows, in upstream key order.

The validation decorator appends its notices after the handler returns. The handler registers
a finisher in the call's context, and the decorator hands the finisher its notices, so the rows
are fitted with those notices counted.

## Files to Modify

- `internal/config/config.go`, `config_test.go`: `MaxResultBytes` from `MCP_MAX_RESULT_BYTES`
- `internal/handler/tools/result_budget.go`: budget parsing and clamping, the row page walker,
  fitting, shortening, the decorator hand-off, and telemetry
- `internal/handler/tools/results.go`: `rawSearchResult` becomes a `Handler` method that pages
  by size and fails open
- `internal/handler/tools/schema_compat.go`: the validation decorator finishes a budgeted result
  with its notices
- `internal/handler/tools/logs.go`, `traces.go`, `query_param_descriptions.go`: the
  `maxResultBytes` parameter
- `internal/handler/tools/result_budget_test.go`, `tool_error_codes_test.go`,
  `filter_param_consistency_test.go`, `structured_content_test.go`: handler tests
- `internal/mcp-server/server.go`, `telemetry_safety_test.go`: `mcp.tool.result.text_bytes`
- `internal/mcp-server/result_budget_guardrail_test.go`:
  `TestGuardrail_RowSearchResultsFitTheirSizeBudget`
- `pkg/otel/attr.go`, `metrics.go`: attribute keys, the corrected `size_bytes` comment, and the
  `mcp.tool.result.budget.events` counter
- `guardrails/policy.go`, `guardrails/tests.txt`, `guardrails/README.md`
- `internal/mcp-server/testdata/wire-catalog/tools-list.json`: the `maxResultBytes` property of
  the two tools only
- `tests/e2e/tests/test_result_budget.py`, `tests/README.md`; `tests/e2e/tests/test_ai_traces.py`
  passes `maxResultBytes` in the helper that needs every AI span row in one page
- `README.md`, `docs/architecture.md`

## Key Decisions

### 2026-11-21 — Decisions set by the issue owner

- Decision: the budget is the UTF-8 bytes of every text block in the delivered result, default
  32,000, set by `MCP_MAX_RESULT_BYTES` (positive values only, like `MCP_MAX_REQUEST_BYTES`).
  A per-call `maxResultBytes` (integer or string) is clamped to [1,000, 500,000], or to the
  server default when that is higher, and a clamp is explicit. Under budget nothing is cut.
  Over budget, whole leading rows are kept byte for byte, measured after the final payload and
  notes are built. One row always comes back: when the first does not fit, its long strings are
  shortened with `…(+N chars)`, and if it still does not fit it is returned with a WARN and a
  counter. Every countable result gains top-level `pagination` with `returned`, `limit`,
  `offset`, `hasMore`, `nextOffset` (-1 without more), and `truncated`, plus `maxResultBytes` and
  `shortenedRows` when they apply. A budget cut sets `hasMore` and `nextOffset = offset +
  returned` and removes that result's upstream `nextCursor`. The prose completeness note stays
  and, after a budget cut, names the next offset, `selectFields`, and `maxResultBytes` for
  clients that can take more. A body the walker cannot page, or one with its own top-level
  `pagination`, is returned unchanged with today's note and a WARN. Every tool span records the
  result's text bytes.
- Rationale: SigNoz/nerve-pod#673.

### 2026-11-21 — Cut where the notices are known, not with headroom

- Decision: the validation decorator creates a per-call `resultFit` in the context;
  `rawSearchResult` registers a finisher there and returns its own fitted result. The decorator
  passes its input-validation and ignored-arguments notices to the finisher, which fits the rows
  again with them counted, and uses that result instead of appending. Without notices the
  handler's result is reused, so the common path fits once.
- Rationale: the ignored-arguments notice depends on which undeclared keys the handler read, so
  only the decorator knows it, after the handler returns. A fixed reserve would cut every
  over-budget page short by up to the largest possible notice (about 1.5 KB, ten 64-byte names)
  and exceed small per-call budgets outright. The finisher measures the bytes actually sent.
- Alternatives rejected: headroom; a separate budget decorator outside validation, which would
  need the handler's page state anyway and re-parse block 0.

### 2026-11-21 — Choices inside the owner's decisions

- Decision: `pagination.maxResultBytes` is also present when a row was shortened, not only when
  the budget ended the page or the value was clamped, so an agent sees the budget that changed
  the row.
- Decision: shortening cuts every string value of the first row longer than one shared length to
  that length, choosing the largest length that fits by binary search, down to zero kept
  characters, and leaves a string alone unless it is more than 16 characters over (the suffix
  costs about that much). Keys, numbers, `webUrl` values, and strings of at most 48 characters
  are never shortened: a shortened link is broken, and timestamps and IDs identify the row.
- Decision: a `maxResultBytes` that is not an integer is a coded `VALIDATION_FAILED` error (SCH-2);
  zero and negative values clamp to the minimum with the clamp note, instead of silently taking the
  default as `limit` does. The server default is used as given; the minimum applies only per call.
- Decision: under budget, block 0 is SigNoz's body with `"pagination":{...}` appended as its
  last top-level member. Over budget, the objects around the rows are re-encoded, in upstream
  key order, and each kept row keeps its bytes.
- Decision: fail open keeps one WARN per cause. A body whose rows cannot be counted already logs
  `row count unknown on non-empty body`; a countable body the walker still rejects, or one with
  its own `pagination`, logs `raw search result left unbounded because its rows could not be
  paged`. Both count `mcp.tool.result.budget.events{event="fail_open"}`; review round 2 below
  extends the count to a trivial body (`{}`, `[]`, `null`). A body over budget with no rows to
  cut is returned as is with the over-budget WARN.
- Decision: telemetry is `mcp.tool.result.text_bytes` on every tool span; the
  `mcp.tool.result.budget.bytes`, `.rows_dropped`, and `.shortened_rows` span attributes on the
  two tools; and the counter `mcp.tool.result.budget.events` labeled by tool and by
  `mcp.tool.result.budget.event` (`rows_dropped`, `row_shortened`, `over_budget`, `fail_open`).
  The `mcp.tool.result.size_bytes` comment now says it counts the whole serialized result.
- Rationale: each keeps the delivered result honest (OUT-3, OUT-5) and every fail-open path
  detectable.

### 2026-11-21 — Guardrail relaxation: one more property on `signoz_search_traces`

- Decision: `maxResultBytes` takes the grandfathered `signoz_search_traces` inventory in
  `guardrails/policy.go` from 16 to 17 properties.
- Rationale: the budget is a per-call result-shaping parameter like `limit` and `offset`; both
  raw searches need it, and a separate tool would repeat every other parameter.
  `signoz_search_logs` goes to 14 and stays under the limit of 15.

### 2026-11-21 — Review round 1 (codex-loop)

- Decision: the fitting loop confirms from one row above its estimate, and tries one whole row
  before it shortens. The estimate counts a separator before the first row, so a budget a page
  filled exactly returned one row fewer, or shortened a row that fit.
- Decision: shortening has no fixed floor of kept characters (it was 32); strings of at most 48
  characters are exempt instead. Four 200-emoji fields went over a 1,000-byte budget that eight
  kept characters each would have met.
- Decision: the walker holds a large body once as the payload and once as its rows. It drops
  each value it has walked through, and `rawSearchResult` counts rows from the walker instead
  of parsing the body twice. It used to keep about six copies of a row-dominated body.
- Decision: a body with more than one result fails open. One offset cannot continue two row
  lists, and both tools send one query.
- Decision: the budget note tells the caller to pass the `start` and `end` from the window note
  with the next offset. A relative `timeRange` is resolved again on each call, so new rows
  shift the page. The notes leave out `selectFields` for trace rows, where it does not apply.
- Declined: rejecting a fractional `maxResultBytes` (every integer parameter truncates through
  `looseInt`); fitting once when a notice is present (a second fit happens only on calls that
  carry a notice).

### 2026-11-21 — Review round 2 (codex-loop)

- Decision: every walker failure increments `fail_open`, including a `{}`, `[]` or `null` body.
  The existing drift WARN still skips those bodies on purpose, but their notes alone can be over
  a small budget, so the counter is the signal.
- Decision: an uncut page keeps whatever follows the body's closing brace, such as a line ending.

### 2026-11-22 — One layer per rule in the tests

- Decision: `TestGuardrail_RowSearchResultsFitTheirSizeBudget` keeps one check per tool: at the
  default configuration, through the production HTTP handler, a 400 KB page comes back within
  the default budget. The e2e walk keeps what needs real SigNoz: several pages, and every row
  once, in order.
- Rationale: `docs/testing.md` pins a rule once, at the lowest layer that runs the real code.
  The guardrail and the e2e walk repeated rules the handler tests pin: the per-call budget, the
  notices counted in the budget, and the pagination fields of a cut page.

### 2026-11-22 — The cut note does not name the override

- Decision: the note on a cut page no longer ends with "Raise maxResultBytes only if your client
  can show a larger result." The parameter description still says so.
- Rationale: the sentence reached a model on every cut, and a model cannot know what its client
  can show. Raising the budget to its maximum returns a result larger than most clients pass
  on, which is the failure the budget exists to prevent. The note on a shortened first row
  keeps its mention: there the override is one of two ways to read the full value.

## Reference Links

- [SigNoz/nerve-pod#673: bound MCP tool results to a size budget](https://github.com/SigNoz/nerve-pod/issues/673)
- [SigNoz/nerve-pod#225: prior art for result budgets](https://github.com/SigNoz/nerve-pod/issues/225)
- [OUT-3, OUT-5, CMP-1, CMP-2, CMP-3 in the MCP best practices](../docs/mcp-best-practices.md)

## Verification

Each new test was run with the production line it covers broken (one mutation at a time,
restored after) and failed:

- `TestRowSearchOverBudgetEndsThePageOnAWholeRow`: fit returning every row, the upstream
  `nextCursor` kept, a page one row short of the longest prefix that fits, and the server
  default ignored.
- `TestRowSearchWithinBudgetReturnsTheBodyWithPagination`: no `pagination` on a result that fits.
- `TestRowSearchShortensAFirstRowThatAloneIsOverBudget`: no shortening, and `webUrl` shortened.
- `TestRowSearchReturnsAnOverBudgetRowWithAWarning`: the over-budget WARN and counter removed.
- `TestRowSearchClampsMaxResultBytesAndSaysSo`: no minimum clamp, and a ceiling that ignores a
  higher server default.
- `TestRowSearchBudgetCountsDecoratorNotices`: notices appended after the cut.
- `TestSearchTracesBudgetCountsEnrichedWebURLs`: enrichment removed.
- `TestRowSearchFailsOpenOnABodyItCannotPage`: a payload's own `pagination` overwritten.
- `TestToolHandlers_RejectInvalidArgumentsBeforeCallingSigNoz` (two new rows): a non-integer
  `maxResultBytes` accepted.
- `TestGuardrail_RowSearchResultsFitTheirSizeBudget`: the default budget not applied when the
  operator sets none (both tools fail).
- `TestGuardrail_ToolCallSpanHasSerializedResultBytes`: the `mcp.tool.result.text_bytes`
  attribute removed.
- `TestLoadConfig_MaxResultBytesTakesOnlyPositiveValues`: `MCP_MAX_RESULT_BYTES=0` accepted.
- `TestRowSearchFillsABudgetItFitsExactly`: the fitting loop confirming from its estimate, not
  from one row above it.
- `TestRowSearchShortensLongStringsUntilTheRowFits`: the 32-character floor restored.
- `TestRowSearchCountsEveryBodyItLeavesWhole`: a body with two results paged, and an empty
  body left uncounted.
- `TestSearchTracesBudgetNotesSkipSelectFieldsForTraceRows`: `selectFields` advice kept for
  trace rows.

`GOTOOLCHAIN=go1.26.11`, each `make ci` step run separately:

- `check-fmt`, `lint` (golangci-lint v2.12.2: 0 issues), `check-build`, `test-race`,
  `check-guardrails` (17 guarded tests), `check-e2e-style`, `check-repo-docs`: pass.
- `check-protocol` (legacy and modern stdio, plus HTTP) and `check-conformance` (6 legacy and 6
  modern scenarios): pass, with `MCP_PROTOCOL_PORT=18090` because Docker holds 18080.
- `check-deps`: `go mod tidy -diff` is clean and `go.mod`/`go.sum` are unchanged; `go mod verify`
  fails only because the local module cache copy of `github.com/shirou/gopsutil/v4@v4.25.5`
  reports "dir has been modified". A machine problem, not this change: CI's `deps / go` passes.
- The wire-catalog diff is the `maxResultBytes` property of the two tools, 14 lines, added by
  hand; `TestGuardrail_WireCatalogGoldens` passes.

CI on the PR, before it was restacked on SigNoz/signoz-mcp-server#477: all 13 checks pass. The e2e job, against a foundry cast of the
pinned SigNoz release, ran 109 tests with 1 skipped (the existing skip in `test_logs.py`). It
includes `test_search_logs_pages_by_result_size` and `test_search_traces_pages_by_result_size`
from the new `test_result_budget.py`, and the `test_ai_traces.py` tests whose helper now sends
`maxResultBytes`.

Not run locally: the e2e suite. `foundryctl` is not installed on the development machine.

The branch was then rebased onto SigNoz/signoz-mcp-server#477, with one conflict, in
`tests/README.md`. The full local suite passes on the rebased branch. CI starts only on a pull
request whose base is `main`, so it runs here again once that PR merges.

## Outcome

Implemented as planned. Breaking change (CMP-1): with the default budget, a page can hold fewer
rows than `limit` while more exist, and every row result gains a top-level `pagination` key.
The compatibility path (CMP-2) is `pagination.nextOffset`, or `maxResultBytes` up to 500,000
per call, or a higher `MCP_MAX_RESULT_BYTES` for the server. SigNoz/agent-skills needs a
companion change (CMP-3): `signoz-generating-queries` treats a raw search with fewer rows than
`limit` as complete.

Not done yet: the before-and-after prompt runs (EVL-1). SigNoz/nerve-pod#673 commits to evals
before its final PR merges. The SigNoz/agent-skills companion change is not opened yet.

Deferred to the later PRs of SigNoz/nerve-pod#673: the series tools, the list tools, and the
dashboard tools. `signoz_get_trace_details` is left to SigNoz/nerve-pod#651.
