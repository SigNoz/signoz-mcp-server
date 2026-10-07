# Testing

Read this when you write or review a test. The Tests section in `CLAUDE.md` is the short
version; this doc has the reasoning, the map of where a new test belongs, and real
examples of bad tests from this repo. Keep a test only if both of these hold

- a failure means a behavior someone depends on broke
- no build step could have removed the need for the test (a test that our hand-typed copy
  of an upstream enum rejects unknown values is one a generated enum would delete)

A test in this repo answers exactly one of three questions:

- Did we keep a decision we made? The translation from the SigNoz contract to the MCP
  contract, and its side effects.
- Is the agent-facing surface still right? Guardrail budgets hold, retrieval quality
  holds, and changes to the catalog (everything an LLM client gets from this server:
  tool names, schemas, descriptions, resources) get reviewed.
- Is the world still what we built against? The pinned e2e suite and runtime WARNs.

A test that answers none of these should not exist, and a test about upstream's data
model marks a construction gap, not a coverage gap.

Much of the existing suite predates this standard. Where a test and this doc disagree, the
doc wins so treat the disagreement as debt, shrink it when you touch the test, and never
copy an existing test as a template without running the six questions at the end of this
doc against it.

## What a test buys in this repo

This server's API is its wire contract i.e tool schemas, descriptions, coded errors, result
shapes, and `signoz://` resources. LLM clients call it, and SigNoz/agent-skills is written
against it; the skills hardcode tool names and payload shapes in what they teach, which is
why a contract change can need a companion skills change (CMP-3 in `CLAUDE.md`).
Most production code translates between that contract and the SigNoz APIs. The expensive
bugs are therefore contract drift and broken recovery paths.
Remove as much of that risk as possible by construction, i.e. derive artifacts from one
source of truth instead of hand-maintaining copies (see Construction before detection),
and make tests cover what is left. The remainder is the decisions made in the translation
and their side effects, a few deliberate pins on hand-written surfaces, and the e2e check
that the pinned SigNoz release still behaves the way we assume. A pin is a test that
compares a surface against a recorded copy of it, so every change fails and gets
reviewed; descriptions and instructions earn one because they are authored English that
nothing can generate. A test
records a decision or checks that agreement. It never re-asserts something a build step
derives, and it never tests internals.

Most diffs here are written by agents and reviewed by humans, which gives every test
three readers:

- the spec the next agent reads to learn what a handler promises before changing it,
- the pass/fail signal the agent iterates against while working,
- the reviewer's evidence that the contract held after the change.

A weak test fails all three readers at once. A test that passes against wrong code teaches
the agent that the code is right. A test that fails on an intended change teaches the
agent that editing tests until they pass is normal work. Both lessons compound across
every future change, which is why these rules are strict.

## Construction before detection

A test is a tax paid on a gap that construction failed to close. Before writing one, try
to delete the gap instead. Keep one source of truth and derive every other copy; then
neither the bug nor the test can exist. Every parity check in the suite is a build step
nobody has written yet.

The gaps in this repo are hand-maintained copies. `manifest.json` repeats the registered
tool names, so a test enforces parity. Request and response shapes are hand-typed copies
of SigNoz's Go types, so fixtures only check that we copied correctly. Instruction
resources hand-write example payloads, so their executable claims can drift from what
SigNoz accepts. A copy and its test can be wrong together, because the same hand writes
both. For example, the saved-view instructions once taught query envelope types SigNoz
never had, while the handler test asserted the same made-up names
([the fix](https://github.com/SigNoz/signoz-mcp-server/pull/318) edits the instruction
text and the test fixture in the same diff; nothing in the repo could have failed while
both copies agreed). Close these by construction. Import upstream types from the SigNoz
Go module or generate them from its published OpenAPI spec, generate `manifest.json` from
the registered tools, and render instruction examples by marshaling real values of the
imported types. Each change deletes the copy, the bug class, and the test that guarded
it. Until the replacement build step is implemented and verified, keep the existing
parity coverage; the test leaves with the gap, not before it.

Two gaps cannot be closed by construction, and tests carry them:

- The unclosed wire. Generated types guarantee the static side: our request and response
  shapes match the release we compiled against. They say nothing about what a live
  deployment actually sends back: an older SigNoz emitting the legacy error envelope, an
  ingress answering a 502 with HTML, a wrong tenant URL returning some other service's
  JSON. A lenient decoder accepts all of those as zero values. The only way to learn what
  arrives is to run against it, so `tests/e2e` runs against the pinned release (the
  compatibility matrix in executable form, proven again on every version bump) and
  runtime WARN logs cover the deployed versions the suite cannot reach. The External
  Contracts section of `CLAUDE.md` is the authority on this boundary.
- Our own decisions. Strip read-only fields before the PUT, never retry a mutation, abort
  the write when a page of channels returns 401, map an upstream code to ours, keep the
  upstream suggestions, redact an echoed credential, say so when results truncate. Nothing
  upstream defines these, and typing both ends does not test the middle, because the
  translation from the SigNoz contract to the MCP contract is a hand-written function
  either way. Types constrain the data, not what the handler does with it. These
  decisions are the product, and hand-written tests are their spec.

Work in this order: make the issue impossible first; where that is not possible, make
drift loud (pins, the e2e suite, WARN logs at runtime); hand-write tests only for the
decisions that are ours.

## One behavior, one layer

Pin each behavior once, at the lowest layer that runs the real code. A higher layer may
assert its own wiring on top, never the same rule again. If the same expected string or
rule appears in tests at two layers, one of them is a copy; delete it or reduce it to the
wiring it actually adds.

| Layer | What its tests pin | Example |
|---|---|---|
| `pkg/*` | Pure logic that is ours: URL injection, time math | `pkg/util/weburl_inject_test.go` |
| `internal/client` | What we send upstream (URL, method, headers, body, retry rules) and the mapping of responses and errors into our vocabulary, against `httptest` servers; shape checking belongs to generated types, not tests | `internal/client/client_test.go` |
| `internal/handler/tools` | The tool contract: arguments in, result or coded error out, plus the upstream call the handler makes | `internal/handler/tools/nil_arguments_test.go` |
| `internal/mcp-server` | The wire: transports, both protocol eras, middleware composition, the recorded catalog | `internal/mcp-server/wire_catalog_golden_test.go` |
| `guardrails/` | What the catalog costs clients. The catalog loads into every session's context, so description, property, and nesting budgets cap that cost; name-length caps encode hard client limits (Cursor rejects a combined alias and tool name over 60 bytes); the memory budget keeps the docs index inside server memory | `guardrails/policy.go` |
| `internal/docs` | Retrieval quality against the golden query set | `internal/docs/golden_test.go` |
| `tests/e2e` | Reality: agreement with the pinned SigNoz release, which no fixture or generated type can prove | `tests/README.md` |

A caution on the validation code in `pkg/alert` and the channel specs in `pkg/types`: most
of it is a hand-copy of upstream's rules (required fields, enums, envelope-type pairings),
and it drifts the way copies drift. On SigNoz v0.143.0 the hand-pinned Slack spec rejected
settings upstream had added, and every Slack channel update failed with
`config.spec: unknown field "color"`
([the fix](https://github.com/SigNoz/signoz-mcp-server/pull/319) re-copies upstream's
validator by hand). This layer should dissolve into imported or generated types; what
stays ours is the guidance each rejection carries and the defaults we inject.
Do not grow it, and do not use its tests as templates.

Client-side rules split three ways, and only the first kind is permanent. Adapter work
(unwrap a pasted envelope, strip server-populated fields, translate rejections into coded
guidance) is this server's own job. A guard that compensates for validation SigNoz lacks,
where upstream accepts a write its own UI cannot render, is a stopgap: file the upstream
issue, pin the premise with a tripwire in `tests/e2e/tests/test_upstream_premises.py`
that drives SigNoz directly, and when that tripwire fails because upstream now enforces
the rule, delete the guard, its tests, and the tripwire together. A rule upstream already
enforces needs no copy here at all: let SigNoz reject, and let the recovery path carry
the guidance.

## What a high-ROI test looks like

- **It asserts what a client can observe.** Tool result, coded error, structured content,
  or the request sent upstream. Any refactor that keeps behavior leaves it green. The
  cross-tool tables show the form. `internal/handler/tools/nil_arguments_test.go` runs
  every handler with nil arguments and requires a coded error, not a panic, and
  `internal/handler/tools/upstream_query_error_test.go` feeds every query handler the same
  key-not-found 400 and requires the coded error with its guidance. Both pin behavior no
  type can promise. One table over all tools pins a rule everywhere; per-tool copies
  drift.
- **The body is the spec.** Inputs and expected outputs are visible in the test function
  without opening helpers or scrolling to a fixture. Start from a named valid baseline and
  make the one change inside the test: begin with arguments that succeed, swap `filter`
  for the retired `query` alias, and expect the coded error that names `filter` as the
  replacement. Repeating ten obvious lines beats hiding
  the point in a clever helper. Helpers may share setup, never the meaning of the test.
- **The name states the behavior.** Someone scanning `go test` output should know what
  broke before opening the file. Never name a test after a ticket, a review round, or the
  incident that prompted it; the behavior outlives all three.
- **The failure says what to do.** Print the contract, the measured value, and what to
  change, the way the guardrail budget failures do. A failure that prints two truncated
  blobs makes the fixer work out what the test meant, and an agent will guess.
- **Both directions of a contract.** A test that proves we accept the right input proves
  little without a second test that rejects the wrong input with the documented code. The
  happy path plus the most important failure path is the minimum, not the target.

## Recovery paths are contracts

An error result is the instruction the agent executes next. The server instructions teach
loops like "on key not found, discover valid keys and retry", and clients branch on error
codes, so the error surface is parsed as much as the success surface. Error paths get
almost no exercise in development and constant exercise in production, because agents
probe with wrong filters, stale ids, and missing permissions. Tests are the only routine
execution they get.

For one failed call the chain is: decode the body, classify what happened, map it to our
error code, compose the guidance, decide the side effects, emit the result. Imported types
make decode correct by construction and delete its tests. Everything after decode is ours,
so tests pin it: the code mapping (a global 403 stays `PERMISSION_DENIED` with the
upstream code attached, never an empty result), preserved upstream guidance and
suggestions, redaction of echoed credentials, and the side-effect decisions, such as no
retry on mutations and aborting a write on mid-pagination auth failure. A broken recovery
path produces the worst failure this server has, a confident wrong answer built on a
swallowed error.

## What we do not write, with examples

Each pattern below links a real test in this repo at pinned commit `767fdc9`. The links
are there so you can read the pattern in its real form; many hands, human and agent,
wrote these tests. When you touch one, fix it or delete it.

- **Asserts nothing.**
  [`TestHandleListAlerts_WithPagination`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/alerts_test.go#L66-L92)
  sends `limit: "2"` while the mock returns three alerts, then asserts only that the
  result is not an error. Whether pagination truncated, forwarded, or dropped the
  parameter is unchecked; the handler could ignore `limit` entirely and this stays green.
  `internal/client/mock.go` returns an empty JSON object for every method you did not
  set, so a bare `!result.IsError` accepts a handler that did nothing. The comment in
  `internal/handler/tools/nil_arguments_test.go` records the production regression this
  pattern let through. Fix: delete the test.
- **Tests the mock.**
  [`TestHandleCheckMetricUsage_PreservesDedupedDashboards`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/metric_usage_test.go#L202-L237)
  stubs the client to return a single dashboard name, then asserts the output has one
  dashboard name and calls it deduplication. No dedup code runs; the test proves the mock
  round-trips. Fix: delete.
- **The name claims what the body cannot prove.**
  [`search docs prefers searchText over legacy query`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/docs_test.go#L92-L103)
  sends a real `searchText` next to an empty `query` and asserts results are non-empty.
  Preference is only proven by sending two different live values and asserting whose
  results came back. Fix: make the inputs distinguishable, or rename the test to what it
  checks.
- **A hand-rebuilt copy of the real artifact.**
  [`TestIntOrStringType_DocsLimitAdvertisesUnion`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/params_test.go#L45-L48)
  constructs its own `mcp.NewTool("signoz_search_docs", ...)` with `intOrStringType()`
  and asserts the copy advertises the union. The registered tool could lose the union and
  this stays green. `internal/handler/tools/param_schema_test.go` shows the correct form;
  it pins what the registered schema actually carries.
- **Count and config change detectors.**
  [`len(acceptedMigrationDifferences) != 13`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/mcp-server/wire_catalog_golden_test.go#L167-L170)
  pins the length of a list declared in the same file; the focused assertions that follow
  it carry all the value, and the count only fails on intended edits.
  [`require.Equal(t, 20, sharedTransport.MaxIdleConnsPerHost)`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/client/client_test.go#L1957)
  pins an unexported tuning constant to its own literal; no client-observable behavior is
  asserted. Fix for both: delete the line.
- **A change detector bolted onto a good test.**
  [The channel display-name pagination test](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/alerts_test.go#L896-L905)
  rightly asserts all 201 names arrive, then also pins the internal fetch offsets
  `[0, 73, 146]`. Change the page size and behavior is identical while the test fails.
  Fix: keep the observable assertions, drop the offsets.
- **Tests keeping a dead copy alive.**
  [`TestSavedView_RoundTripJSON`](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/pkg/types/view_test.go#L9)
  exercises `types.SavedView`, a hand-typed copy of the SigNoz model that no production
  code references. The tests are the only thing keeping the copy alive. Fix: delete the
  type and its tests together.
- **The test shared the code's wrong belief.** Before
  [PR #318](https://github.com/SigNoz/signoz-mcp-server/pull/318), the views handler test
  asserted the envelope type `promql_query`, the same made-up name the instructions
  taught; SigNoz's real types are `promql` and `clickhouse_sql`. Code and test were
  written by the same hand from the same wrong belief, so the suite could not fail; an
  external consumer's evals caught it. The
  [fix](https://github.com/SigNoz/signoz-mcp-server/commit/e2874f940e2063250fcf6ab45329b57ce9292b6a)
  updates the fixture to the new belief, so the same thing can happen again. The lasting
  fix is construction. Derive the names from imported upstream types and let `tests/e2e`
  do the one live check.
- **Expected values far from the assertion.**
  [The org-overview projection asserts](https://github.com/SigNoz/signoz-mcp-server/blob/767fdc98dbe9b7e1122912b16bed9284ce8ce97b/internal/handler/tools/org_overview_test.go#L101-L113)
  join up to eight clauses in one `if` and compare against counts whose meaning lives in a
  fixture hundreds of lines below. The behavior under test is valuable, but a failure
  prints a whole struct and leaves the reader to work out which clause broke. Fix: one
  assertion per clause, with the expected value named where it is checked.

Two more patterns need no examples: a second copy at a higher layer of a rule already
pinned below (default limits and URL injection each have one home in the layer table),
and tests that pin description wording beyond the concept handles that
`docs/client-visible-writing-style.md` anchors.

## Drift pins

Pins are the deliberate exception to "never fail on an intended change", and they exist
only where construction has nothing to offer: the hand-authored catalog in
`internal/mcp-server/testdata/wire-catalog/` (descriptions and instructions are written
English, so nothing can generate them; the pin turns every change into a reviewable
diff), the budgets in `guardrails/`, and the golden query baseline in `internal/docs`. A
recorded real upstream response is a stopgap pin for what no current spec describes, such
as the legacy error envelope, and lasts only until the versions that emit it leave the
fleet. A pin fails on edits you meant to make; that cost is only worth paying under these
rules:

- A pin guards a named external contract, never an internal shape. Say in the test name or
  a one-line comment what drift it catches.
- Keep the pinned surface minimal. The full tool catalog is a contract; the number of
  entries in it is not.
- Update only the entries your change intends to alter. Never re-record wholesale, because
  a re-record converts every unnoticed regression into a new baseline.
- The failure must print a diff a human can review and an agent can act on.
- A pin that guards a hand-maintained copy marks a construction gap. Derive the copy and
  the pin goes with it.

## Six questions before a test merges

1. Could a build step make this test unnecessary, by deriving the copy, importing the
   type, or generating the artifact? Then close the gap instead of taxing it. Example: a
   manifest parity test; generate `manifest.json` from the registered tools instead.
2. Can it fail for any reason other than a depended-on behavior changing? Then it is a
   change detector: rewrite it against the behavior or delete it. Flakiness counts: a
   test that can fail on timing, ordering, or a live dependency alone also fails here.
3. Would it still pass if the code under test returned the wrong thing? Then it asserts
   nothing: strengthen the stub and the assertions, or delete it.
4. Could a reader reconstruct the contract from the test body alone? If they must open
   helpers or fixtures to know what is expected, inline the meaning, and keep each
   expected value one pasteable literal; a SQL string assembled from `+` fragments makes
   the reader rebuild it by hand.
5. Is this behavior already pinned at a lower layer? Then test only the wiring this layer
   adds, or move the test down.
6. Does the failure output name the broken contract and the fix direction? If it prints
   two blobs, fix the message before merging.

A test that clears all six is one we can delete code against, refactor against, and hand
to an agent as the definition of done.
