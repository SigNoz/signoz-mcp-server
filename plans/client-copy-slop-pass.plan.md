# Plan: Client-Facing Copy Slop Pass

## Status
Done

## Context
Every description, instruction, and resource this server ships is read by an MCP
client and, through it, by a model. AI-slop patterns there cost tokens and blur
routing signals. This pass applies the `no-ai-slop` editing rules to the
client-read surface only, without changing any contract.

## Approach
1. Scan all client-read strings against the skill's pattern list before editing.
2. Edit only genuine slop: prose em dashes, templated boilerplate, trailing `-ing`
   analysis clauses, abstraction, and puffery.
3. Preserve: fixed-width table separators, markdown heading separators,
   load-bearing "X rather than Y" routing contrasts, and upstream-owned strings.
4. Patch wire-catalog fixtures with the identical string edits; recompute only the
   derived numeric fields.

## Files Modified
- `pkg/dashboard/widgets.go` — removed 5 templated `Note: This panel is best used`
  lead-ins (→ `Best for:`), trailing `-ing` clauses, `temporal evolution`, empty
  closers, and one puffery line; all 10 em dashes gone.
- `pkg/alert/resources.go` — 16 prose em dashes → colons/semicolons/periods;
  9 doc-link bullets → colon form. Headings kept.
- `pkg/metricsrules/guide.go`, `pkg/promql/instructions.go`,
  `pkg/querybuilder/{logs,traces}_guide.go`, `pkg/views/instructions.go`,
  `pkg/dashboard/{basics,dashboard_examples}.go` — prose em dashes only.
- `internal/handler/tools/{logs,traces,metrics,dashboards}.go` — 3 unspaced and
  9 spaced em dashes in tool/parameter descriptions.
- `pkg/instructions/instructions.go` — unspaced em dash in rule 2.
- `pkg/prompts/prompts.go` — 3 prompt descriptions rewritten to lead with the verb;
  prompt body em dashes; `operation landscape` → `operations ranked by p99 latency`.
- `internal/mcp-server/testdata/wire-catalog/` — 6 fixtures (4 textual, 2 derived).
- `README.md` — synced only the lines mirroring changed tool/param descriptions.

## Not Modified (deliberate)
- `internal/handler/tools/schemas/dashboard_patch.json` — upstream-generated.
- `internal/handler/tools/dashboard_templates.json` — upstream dashboard titles.
- `manifest.json` — already free of these patterns; descriptions still accurate.

## Verification
- `go build ./...`
- `go test -count=1 ./...` (full suite green)
- `go test -count=1 -run '^TestGuardrail_' ./...` (green, including the description
  byte-budget guardrail; max tool description 888 B against the 1024 B budget)
- `actionlint .github/workflows/guardrails.yaml`
- `make fmt goimports`
- Post-edit rescan: zero em dashes remain in any repo-authored tool or parameter
  description; the single remaining one is the upstream `dashboard_patch.json` string.
