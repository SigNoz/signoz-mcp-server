# Plan: Result Size Budget for the List Tools

Status: Done
Issue: [SigNoz/nerve-pod#673](https://github.com/SigNoz/nerve-pod/issues/673)
PR: https://github.com/SigNoz/signoz-mcp-server/pull/478

## Context

List tools page by item count, not by size, and their pages are often larger than a client
shows its model. Estimated from seven days of production telemetry (text bytes taken as half of
the serialized result, because these tools send the payload as text and as `structuredContent`),
`signoz_list_services` text was over 32,000 bytes on about 46% of calls and over 50,000 on about
34%; `signoz_list_alert_rules` 26% and 12%; `signoz_list_dashboards` 24% and 16%;
`signoz_list_alerts` 3%.

This is the second PR of SigNoz/nerve-pod#673 and stacks on SigNoz/signoz-mcp-server#476
(`plans/2026-11-21-result-size-budget.md`), which bounds `signoz_search_logs` and
`signoz_search_traces`. It covers the list tools whose page this server builds and that take
`offset`, except `signoz_list_views` and `signoz_get_ai_pricing` (see the decision below). Out
of scope: the passthroughs without `offset` (`signoz_list_metrics`,
`signoz_get_field_keys`, `signoz_get_field_values`, `signoz_get_service_top_operations`,
`signoz_get_top_metrics`), `signoz_get_alert_history`, `signoz_list_dashboard_templates`,
`signoz_get_dashboard` and the dashboard write tools, the series tools, and
`signoz_get_trace_details`.

## Approach

`listResult` becomes a `Handler` method that takes the tool name and the page's shape, and
`budgetedListResult` serves the two envelopes it does not build: the notification channel list
(items at `channels`) and SigNoz's dashboard body (items at `data.dashboards`, no `pagination`
object). Each builds the whole result exactly as before and measures its text blocks. A page that
fits is returned unchanged. Only a page over the server budget is split at its items, kept as
`json.RawMessage`; the longest prefix of whole items that fits is re-encoded with its pagination
object marked as cut. The first item always comes back whole.

The budget, the decorator hand-off (`resultFit`), and telemetry come from the base branch.
`deliverFitted` now holds the hand-off for both kinds of tool, and `recordBudget` takes the tool
name, budget, and item count instead of a raw search page. Neither changes what the raw searches
return.

## Files to Modify

- `internal/handler/tools/list_budget.go`: `listResult`, `budgetedListResult`, the split, the cut
  pagination, and the note
- `internal/handler/tools/result_budget.go`, `results.go`: `deliverFitted` and the generalized
  `recordBudget`
- `internal/handler/tools/params.go`: the old `listResult` moves to `list_budget.go`
- `internal/handler/tools/services.go`, `alerts.go`, `notification_channels.go`,
  `dashboards.go`: route each list page through the budget; the `signoz_list_dashboards`
  description and `offset` text
- `internal/handler/tools/views.go`, `ai_pricing.go`: `unbudgetedListResult`, the result they
  returned before
- `pkg/paginate/paginate.go`: `Truncated` and `MaxResultBytes` on `Metadata`, so the derived
  output schemas of `signoz_list_alerts` and `signoz_list_alert_rules` declare them
- `internal/mcp-server/testdata/wire-catalog/tools-list.json`: those two schemas and the
  dashboard list text only
- `internal/handler/tools/list_budget_test.go`: handler tests
- `internal/mcp-server/result_budget_guardrail_test.go`:
  `TestGuardrail_OffsetToolResultsFitTheirSizeBudget`
- `guardrails/tests.txt`, `guardrails/README.md`
- `tests/fixtures/mcpserver.py`, `tests/e2e/tests/test_list_result_budget.py`, `tests/README.md`
- `pkg/otel/attr.go`: the budget attribute comment names list items
- `README.md`, `docs/architecture.md`

## Key Decisions

### 2026-11-22 — Decisions set by the issue owner

- Decision: a page that fits is returned exactly as before. Over budget, the leading whole items
  that fit are kept and the rest dropped; `hasMore` is `true`, `nextOffset` is `offset` plus the
  items returned, and `total` is unchanged. One item always comes back, whole: list items are
  never shortened, because a caller may write one back. A first item over the budget is
  returned with the base branch's over-budget WARN and counter. The text block and
  `structuredContent` stay built from the same bytes, and every note goes through `appendNotes`.
  The delivered result fits with the decorator's notices counted, through the base branch's
  hand-off, not headroom. Telemetry reuses the base branch's span attributes and counter.
- Decision: no new tool parameter. List tools use only the server budget
  (`MCP_MAX_RESULT_BYTES`); `offset` already reaches every item.
- Rationale: SigNoz/nerve-pod#673.

### 2026-11-22 — The cut page's fields and note

- Decision: a cut adds `truncated: true` and `maxResultBytes` to the page's `pagination` object,
  named as on the raw searches, and sets `hasMore` and `nextOffset`. Both fields are absent from
  a page that fits, unlike the raw searches, whose `truncated` is always present, because a page
  that fits must not change. `signoz_list_notification_channels` also sets `count` to the items
  returned, since its `count` is the page's length.
- Decision: SigNoz's dashboard body has no `pagination` object, so a cut page gains a top-level
  one with the shape of the other list tools (`total`, `offset`, `limit`, `hasMore`,
  `nextOffset`, `truncated`, `maxResultBytes`), built from SigNoz's `data.total`, which stays.
  A body over budget without a `data.dashboards` array or an integer `data.total` fails open:
  returned unchanged, with a WARN and `mcp.tool.result.budget.events{event="fail_open"}`.
- Decision: the note mirrors the raw searches' budget note: `note: returned N items (limit L);
  the result size budget of B bytes ended this page, so more results exist (hasMore=true). Fetch
  the next page with offset=M.` `signoz_list_services` and `signoz_get_service_map` add `, and
  pass window.startMs and window.endMs as start and end so the page does not shift`, because a
  relative `timeRange` is resolved again on each call. It does not mention `maxResultBytes`,
  which these tools do not take.
- Decision: `Truncated` and `MaxResultBytes` (both `omitempty`) join `paginate.Metadata`, so the
  output schemas of `signoz_list_alerts` and `signoz_list_alert_rules`, derived from it, accept a
  cut page. The other list tools advertise no output schema.
- Rationale: one shape for every cut page (OUT-3), and no change to a page that fits.

### 2026-11-22 — Measure the result as built, split only over budget

- Decision: the whole result is built first, as before, and measured. Only a page over budget is
  split at its items. The fitting estimate comes from the items' encoded lengths and is then
  walked down or up with real builds, because a cut always carries a note, so block 0 is
  re-encoded from `structuredContent` (sorted keys, no HTML escaping) and an item's delivered
  length can differ from its encoded length.
- Rationale: the common page that fits costs what it did before and cannot change; the walk keeps
  the longest prefix that fits whatever the re-encoding does.
- Alternatives rejected: a render callback per handler, which would repeat the envelope logic in
  six handlers; refactoring the raw search walker into a generic one, which would touch the raw
  searches' byte-exact paths.

### 2026-11-22 — Breaking change

- Decision: breaking under CMP-1, so the PR title takes `!`. A page can now hold fewer items than
  `limit` while more exist. Clients that follow `pagination.hasMore` and `nextOffset` are
  unaffected, but a client that pages by `offset += limit`, as the `signoz_list_dashboards`
  description taught, skips the items a cut dropped, and the cut pages carry new fields. The
  compatibility path is `pagination.nextOffset` (or, on a dashboard page that fits, `offset` plus
  the dashboards returned), or a higher `MCP_MAX_RESULT_BYTES` for the server.
- Decision: the `signoz_list_dashboards` description and its `offset` text said to raise `offset`
  by `limit`, which skips dashboards after a cut; both now say to raise it by the number of
  dashboards returned. No other list description is wrong: they say to follow
  `pagination.nextOffset` or `hasMore`, or name `limit` and `offset` without saying how far to
  move.

### 2026-11-22 — Saved views and AI pricing are not cut

- Decision: `signoz_list_views` and `signoz_get_ai_pricing` return their page whole, as before,
  through `unbudgetedListResult`.
- Rationale: review found that SigNoz returns saved views with no `ORDER BY` and unpriced models
  with ties in span count unordered. A cut makes the caller continue from an offset, where an
  item that moved between the two calls comes back twice or not at all. The issue owner chose to
  leave both tools uncut. Both are small: about 100 and 20 calls in the seven days measured.
- Alternatives rejected: sorting in this server, which would give the views list a new visible
  order and cannot fix span counts that change between two calls.

### 2026-11-22 — Review (codex-loop)

- Decision: a dashboard body whose `data.total` is `null` fails open. `null` decodes into an
  `int` as 0, so a cut page would have said `total: 0` beside `hasMore: true`.
- Decision: the e2e helper that starts a native server takes the `SigNoz` object, not its API
  key, because pytest prints a failing call's arguments and the key's field is hidden there.
- Declined: a shorter guardrail test. It is the only test that sends every covered tool through
  full registration and the validation decorator.

### 2026-11-22 — A rule and a guardrail for new paging tools

- Decision: OUT-7 in `docs/mcp-best-practices.md` asks every new tool to bound its result and
  state its largest one in the PR, and requires a tool that pages with `offset` to return a
  fixed order and fit the result size budget.
  `TestGuardrail_OffsetToolResultsFitTheirSizeBudget` reads the registered tools and holds each
  one with `offset` to the default budget, unless `guardrails.UnbudgetedOffsetTools` names it;
  that list holds saved views and AI pricing with the reason.
- Rationale: both budget guardrails named their tools in a list, so a new list tool would have
  shipped unbounded with nothing failing. The evidence for the rule (EVL-2) is production usage:
  the oversized list results measured in SigNoz/nerve-pod#673. Added after the codex-loop review.

### 2026-11-22 — Bot review: a bounded search for the items that fit

- Decision: the fit probes its estimate and the item after it, then bisects. A cut page is
  re-encoded with its note, which writes `\u003c` as `<`, so an item can be much shorter
  delivered than upstream and the estimate far too low. The fit used to rebuild the page once
  per extra item: 108 ms for a 1,000-item page whose items each hold a few such characters, and
  11 ms now, with the same items kept.
- Decision: comments over three lines are cut to the reason, as `CLAUDE.md` asks.

### 2026-11-22 — One layer per rule in the tests

- Decision: one guardrail, `TestGuardrail_OffsetToolResultsFitTheirSizeBudget`, replaces the row
  search guardrail, the list guardrail, and the check that every `offset` tool was in one of
  their lists. It reads the tools from the registry and keeps the one thing the wire adds: at
  the default configuration, through the production HTTP handler, an over-budget page comes
  back within the default budget. This reverses the decline above: the handler tests also run
  full registration and the validation decorator.
- Decision: the handler test of a cut page takes over the two checks that had their own tests
  (`structuredContent` carries the cut page; the cut page matches its output schema), states
  its expected pagination and note where it asserts them, and gains strings SigNoz escaped.
  The notes reaching `structuredContent.notes` is pinned by the tests of `appendNotes`.
- Decision: the e2e test keeps what needs real SigNoz: its dashboard list splits into several
  pages, following `nextOffset` returns every dashboard once, in order, and `data.total` counts
  the whole list at every offset.
- Rationale: `docs/testing.md` pins a rule once, at the lowest layer that runs the real code,
  and prefers deriving a list to checking a hand-kept copy of it. The guardrails and the e2e
  walk repeated what the handler tests pin: the fit with notices, and the pagination of a cut.

### 2026-11-22 — Review of the later commits (codex-loop)

- Decision: the guardrail reads `pagination.truncated` from the decoded page, not the text, and
  fails if `tools/list` returns a second page it did not read.
- Decision: the handler test of a cut page compares each item's whole padded string, so a
  shortened item fails it. The e2e walk requires every page to hold a dashboard.
- Decision: OUT-7 says a tool that pages with `offset` must apply the budget, not fit it: the
  first item comes back even when it alone is over the budget.

## Reference Links

- [SigNoz/nerve-pod#673: bound MCP tool results to a size budget](https://github.com/SigNoz/nerve-pod/issues/673)
- [SigNoz/signoz-mcp-server#476: the raw search budget](https://github.com/SigNoz/signoz-mcp-server/pull/476)
- [OUT-3, OUT-5, OUT-6, OUT-7, CMP-1, CMP-2, CMP-3 in the MCP best practices](../docs/mcp-best-practices.md)

## Verification

Each new test was run with the production line it covers broken (one mutation at a time,
restored after) and failed on the assertion meant for it:

- `TestListToolWithinBudgetReturnsThePageAsBuilt`: a page that fits sent down the cut path.
- `TestListToolOverBudgetEndsThePageOnAWholeItem`: an over-budget page returned whole; a page one
  item short of the longest prefix; the search stopped before the longest prefix when SigNoz
  escaped the strings; `nextOffset` as `offset + limit`; `truncated` not set; the channel
  `count` not updated; the window advice dropped; a cut page built as text only; `Truncated`
  and `MaxResultBytes` removed from `paginate.Metadata`.
- `TestListToolReturnsAnOversizedFirstItemWhole`: the over-budget WARN and counter dropped for a
  lone item; a page allowed to keep zero items.
- `TestListToolBudgetCountsDecoratorNotices`: notices appended after the cut.
- `TestListDashboardsFailsOpenOnABodyItCannotPage`: the fail-open WARN and counter removed; a
  `null` total read as 0.
- `TestListToolsWithoutAFixedOrderReturnAnOverBudgetPageWhole`: saved views or AI pricing sent
  through the budget.
- `TestGuardrail_OffsetToolResultsFitTheirSizeBudget`: the default budget not applied when the
  operator sets none (all eight tools fail); the saved views exemption removed; an exemption
  for a tool that does not page with `offset`.

The raw search tests pass with their expectations unchanged.

`GOTOOLCHAIN=go1.26.11`, each `make ci` step run separately: `check-fmt`, `lint`, `check-build`,
`test-race`, `check-guardrails`, `check-protocol`, `check-conformance`, `check-e2e-style`, and
`check-repo-docs` pass. `check-deps` fails only in `go mod verify`, on a modified local
module-cache copy of `gopsutil`; `go mod tidy -diff` is clean.

e2e: the suite does not run locally (`foundryctl` is not installed). A run started by hand at
4a6beb9, which holds all three changes of the stack, passed 113 tests with 1 skipped, including
`test_list_dashboards_pages_by_result_size` and the refactored native-server fixture the whole
suite shares. The e2e test was reduced after that run (see the decision above); CI has run on
this pull request since it joined the GitHub stack, and its `e2e` check covers that version.

## Outcome

Implemented for six list tools; saved views and AI pricing are left uncut (see the decision
above). Breaking change (CMP-1): a list page can hold fewer items than `limit` while more exist.
The compatibility path (CMP-2) is `pagination.nextOffset`, or for `signoz_list_dashboards` the
number of dashboards returned, or a higher `MCP_MAX_RESULT_BYTES` for the server.

Not done yet: the before-and-after prompt runs (EVL-1); SigNoz/nerve-pod#673 commits to evals
before its final PR merges.

SigNoz/agent-skills needs a companion change (CMP-3), tracked in
SigNoz/nerve-pod#436: `signoz-explaining-dashboards` teaches raising `offset` by `limit` until
`total` is covered, which skips dashboards after a budget cut.
