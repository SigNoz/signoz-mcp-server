# Plan: Client-Facing Copy Slop Pass

## Status
Done

## Context
Every description, instruction, and resource this server ships is read by an MCP
client and, through it, by a model. AI-slop patterns there cost tokens and blur
routing signals. This pass applies the `no-ai-slop` editing rules to the
client-read surface, then a writing-craft pass, a README prose pass, and a
convention so the rules outlive the PR. No tool, parameter, or payload contract
changes.

## Approach
1. Scan all client-read strings against the skill's pattern list before editing.
2. Edit only genuine slop: prose em dashes, templated boilerplate, trailing `-ing`
   analysis clauses, abstraction, and puffery.
3. Preserve: fixed-width table separators, markdown heading separators,
   load-bearing "X rather than Y" routing contrasts, and upstream-owned strings.
4. Patch wire-catalog fixtures with the identical string edits; recompute only the
   derived numeric fields.
5. Follow with a writing-craft pass for first-pass misreadings, then a human-facing
   README em-dash pass, then extract the style rules to `docs/`.

## Files Modified
- `pkg/dashboard/widgets.go`: removed 5 templated `Note: This panel is best used`
  lead-ins (`Best for:`), trailing `-ing` clauses, `temporal evolution`, empty
  closers, and one puffery line; all 10 em dashes gone.
- `pkg/alert/resources.go`: 16 prose em dashes to colons/semicolons/periods;
  9 doc-link bullets to colon form. Headings kept.
- `pkg/metricsrules/guide.go`, `pkg/promql/instructions.go`,
  `pkg/querybuilder/{logs,traces}_guide.go`, `pkg/views/instructions.go`,
  `pkg/dashboard/{basics,dashboard_examples}.go`: prose em dashes only.
- `internal/handler/tools/{logs,traces,metrics,dashboards,alerts,views}.go`:
  unspaced and spaced em dashes in tool/parameter descriptions, plus the
  writing-pass clarity edits (`respectively`, delete-tool misattachment, alert
  timeline antecedent, metrics top-100 wording).
- `pkg/instructions/instructions.go`: unspaced em dash in rule 2; rule 7 kept
  reuse-first (`Reuse reads from the same still-current prepared operation; do
  not repeat them. Otherwise resolve names/IDs and fetch replacement objects.`)
  so the fetch is conditional on there being no reusable current read.
- `pkg/dashboard/widgets_examples.go`: causal `since` to `because`; remaining
  prose em dashes in the intro, limit/top-N note, example bodies, and example
  titles rewritten as colons, semicolons, or parentheses.
- `pkg/prompts/prompts.go`: 3 prompt descriptions rewritten to lead with the verb;
  prompt body em dashes; `operation landscape` to `operations ranked by p99 latency`.
- `internal/mcp-server/testdata/wire-catalog/`: textual fixtures mirrored the
  string edits; derived `size`/`serializedLength`/`sha256` recomputed through
  the oracle helpers (never re-recorded).
- `README.md`: first synced the lines mirroring changed tool/param descriptions,
  then a general remaining-em-dash prose pass (unspaced connectors, queryEnvelope
  colon form, webUrl note restructure). Option A/B heading labels kept.
- `docs/client-visible-writing-style.md`: extracted client-visible writing rules
  (banned words, slop patterns, allowed em-dash separators, concept-handle
  pinning, upstream-generated exclusions, fixture/budget mechanics).
- `CLAUDE.md`: short pointer to that doc plus a Documentation & Metadata Sync
  Checklist bullet. Inline style section removed so agents load the rules only
  when editing client-facing text.

## Not Modified (deliberate)
- `internal/handler/tools/schemas/dashboard_patch.json`: upstream-generated.
- `internal/handler/tools/dashboard_templates.json`: upstream dashboard titles.
- `manifest.json`: already free of these patterns; descriptions still accurate.

## Verification
- `go build ./...`
- `go test -count=1 ./...` (full suite)
- `go test -count=1 -run '^TestGuardrail_' ./...` (including the description
  byte-budget guardrail; max tool description 888 B against the 1024 B budget)
- `actionlint .github/workflows/guardrails.yaml`
- `make fmt goimports`
- Post-edit rescan: zero em dashes remain in any repo-authored tool or parameter
  description; the single remaining tool-surface one is the upstream
  `dashboard_patch.json` string. Guide table columns and markdown headings keep
  their separator em dashes.
