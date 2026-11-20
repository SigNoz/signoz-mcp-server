# Plan: Notes in Structured Content

Status: Done
Issue: [SigNoz/nerve-pod#654](https://github.com/SigNoz/nerve-pod/issues/654)
PR: [SigNoz/signoz-mcp-server#472](https://github.com/SigNoz/signoz-mcp-server/pull/472)

## Context

Claude Code gives the model only a tool result's `structuredContent` when one is present and
drops every `content` text block. This server built structured results and appended its
`note:` guidance as extra text blocks, so Claude Code users never saw those notes. In
production, `signoz_list_services` with `limit: 5000` came back clamped to 1000, and the clamp
note never reached the model.

Affected: every result built with `structuredResult` plus notes. That covers the limit-clamp
note from `listResult` on paginated list tools, the `structuredResultWithNotes` callers
(dashboards list, notification channel create and update), and the three notes of
`signoz_get_service_map`. The issue owner also brought `validationDecorator` into scope. It
appends the input validation notice and the ignored-arguments notice to every successful
result, so any of the roughly 28 structured tools can carry a note, including the six with an
output schema. In production, `signoz_list_alert_rules` called with `filter: {"status":"firing"}`
dropped the non-string filter and returned rules in every state; the notice was the only sign.

Text-only results (`resultWithNotes`: search, aggregate, query tools and
`signoz_get_service_top_operations`) are unaffected and keep today's shape.

## Approach

One helper, `appendNotes`, adds every block after a result's first. It appends each non-blank
note as a text block, and on a successful structured result it also adds the notes to a
top-level `notes` array of strings in `structuredContent` and rewrites block 0 as that object's
JSON. `structuredResultWithNotes`, `listResult`, `resultWithNotes`, the service map, the
alert-history recovery text, and the validation decorator all go through it. A static
guardrail rejects any other assignment to a result's `Content` in production code.

Registration declares `notes` on every object output schema, so results with notes keep
validating, and a future output-schema tool gets the property without a code change.

## Files to Modify

- `internal/handler/tools/results.go`: `appendNotes`, `addStructuredNotes`; route both note helpers through it
- `internal/handler/tools/params.go`: `listResult` takes ctx, logger, and extra notes
- `internal/handler/tools/services.go`, `alerts.go`, `views.go`, `ai_pricing.go`, `dashboards.go`, `notification_channels.go`: call sites
- `internal/handler/tools/schema_compat.go`: decorator notices through `appendNotes`; `declareResultNotes` on output schemas
- `internal/handler/tools/result_notes_guardrail_test.go`: `TestGuardrail_StructuredResultNotesGoThroughAppendNotes`
- `internal/handler/tools/*_test.go`: handler and helper tests
- `internal/mcp-server/testdata/wire-catalog/tools-list.json`, `tool-success-fail-open-input.json`: the intended entries only
- `guardrails/tests.txt`, `docs/mcp-best-practices.md` (OUT-6), `README.md`

## Key Decisions

### 2026-11-20 — Block 0 carries the notes too

- Decision: block 0 is re-serialized from the `structuredContent` object, `notes` included, and
  each note still follows as its own text block.
- Rationale: `structuredResult` documents that block 0 and `structuredContent` never diverge,
  the MCP spec asks for the serialized structured content in a text block, and the e2e helper
  `assert_structured_matches_text` compares the two. The numbers decode as `json.Number`, so
  large integers keep their digits; HTML characters are not escaped. Keys come out sorted, which
  only changes block 0 bytes when a note is present.
- Alternatives rejected: keeping block 0 as the bare payload (breaks the invariant and the e2e
  check); dropping the trailing note blocks (the issue keeps them for `content` readers).

### 2026-11-20 — Fail open on non-object payloads and key collisions

- Decision: when `structuredContent` is not a JSON object, or already has a top-level `notes`
  field it did not get from `appendNotes`, the payload is left untouched, the note stays a text
  block, and a WARN is logged ("Structured result keeps its notes only as text blocks").
- Rationale: overwriting a payload's own field would corrupt a resource; wrapping a non-object
  would change the result's shape. Notes the helper added are stored as `[]string`, while a
  decoded payload field is `[]any` or another type, so a second call (decorator after handler)
  extends the same array and never mistakes a payload's field for its own.

### 2026-11-20 — `signoz_check_metric_usage` keeps its map shape

- Decision: its output stays a map from metric name to usage. `notes` is declared as a named
  property beside `additionalProperties` (the usage object), so a result with notes validates.
  On a map-shaped output schema the property is `anyOf` the entry schema and the notes array,
  so a metric named `notes` still validates as a usage entry (review finding).
- Rationale: wrapping the map under a new key would break every client on every call to fix a
  rare case. The only note this tool can carry is a decorator notice. `notes` is an array of
  strings while every metric entry is an object, so a reader can tell them apart, and a metric
  literally named `notes` takes the collision path: its entry stays and the notice stays text.
  SigNoz/agent-skills reads the result by the metric names it passed, so it is unaffected.

### 2026-11-20 — Error results keep their envelope

- Decision: the `signoz_get_alert_history` 404 recovery text goes through `appendNotes`, which
  leaves error results text-only, so the guardrail needs no exception.
- Rationale: error `structuredContent` is the coded error envelope (ERR-1), a separate contract
  that this issue does not change.

### 2026-11-20 — Guardrail is a source scan

- Decision: `TestGuardrail_StructuredResultNotesGoThroughAppendNotes` parses the production Go
  files in `internal/handler/tools`, `internal/docs`, and `internal/mcp-server` and fails on any
  assignment to a `.Content` field outside `appendNotes`. Writes to one block
  (`res.Content[i] = ...`) edit text and stay allowed. Handler tests pin what `appendNotes` does.
- Rationale: no runtime test can call every tool against every upstream state, but every way of
  adding a block is an assignment to `Content`. Reverting either the service map or the
  decorator to a bare append fails the guardrail at that line.

## Reference Links

- [SigNoz/nerve-pod#654: notes never reach clients that read only structured content](https://github.com/SigNoz/nerve-pod/issues/654)
- [MCP tools: structured content](https://modelcontextprotocol.io/specification/2025-11-25/server/tools#structured-content)
- [OUT-6 in the MCP best practices](../docs/mcp-best-practices.md)

## Verification

Fail-before (new tests run on unfixed production code in a temporary worktree):
`TestListToolCarriesTheClampNoteInStructuredContent`, `TestServiceMapNotesWhenNoEdgeTouchesTheService`,
`TestServiceMapNotesAnEmptyGraph`, `TestNoticeOnAStructuredResultStillMatchesTheOutputSchema`,
`TestSuccessfulResultNamesTheArgumentsTheToolIgnored/update_asking_to_lock_the_dashboard`, and
`TestDecoratorNoticeJoinsHandlerNotesInStructuredContent` all failed with empty structured notes.
All pass after the fix. The guardrail failed at `services.go:170` with the service map's bare
append restored, and at `schema_compat.go:269` with the decorator's bare append restored.

CI, run on a clean worktree of the commit so the untracked local packages in the main checkout
stay out of it, with `GOTOOLCHAIN=go1.26.11`:

- `check-fmt`, `lint` (golangci-lint v2.12.2: 0 issues), `check-build`, `test-race`,
  `check-guardrails` (16 guarded tests), `check-protocol` (legacy and modern stdio plus HTTP),
  `check-conformance` (6 legacy and 6 modern scenarios), `check-e2e-style`: pass.
- `check-deps`: `go mod tidy -diff` is clean and `go.mod`/`go.sum` are unchanged, but
  `go mod verify` fails on this machine because the local module cache copy of
  `github.com/shirou/gopsutil/v4@v4.25.5` reports "dir has been modified". The failure is in the
  developer's module cache, not this change; CI's fresh cache is unaffected. Unverified locally.
- `check-protocol` ran with `MCP_PROTOCOL_PORT=18090` because Docker holds the default port 18080.
- `check-repo-docs` and `check-repo-docs READY=1`: pass.

No live SigNoz instance was called and the live e2e suite was not run. `tests/e2e/tests` has no
assertion on exact top-level key sets; `assert_structured_matches_text` still holds because
block 0 carries `notes`.

## Outcome

Shipped as planned. Review added one change: on a map-shaped output schema `notes` also accepts
an entry, so a metric named `notes` stays valid. Follow-up outside this change: the guardrail's
source scan does not see a result built as a composite literal with several blocks; no
production code builds one. Breaking-change review (CMP-1): additive. A new optional `notes` key appears
only when a note exists, and the existing text blocks are unchanged. No SigNoz/agent-skills
companion change is needed (CMP-3).

Not changed: `signoz_list_services` decodes the upstream body with a plain `json.Unmarshal`, so
an integer above 2^53 is rounded before this path sees it. That rounding predates this change.
