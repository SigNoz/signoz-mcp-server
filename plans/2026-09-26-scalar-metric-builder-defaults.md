# Plan: Scalar metric builder defaults

Status: In Progress
Issue: https://github.com/SigNoz/nerve-pod/issues/359
PR:

## Context

Scalar metric builder requests without reduceTo fail upstream. The convenience
metrics tool already chooses a reducer from metric type and monotonicity. Invalid
order keys caused further failures; valid keys depend on the signal and aggregation.

## Approach

Fill only absent scalar metric aggregation reduceTo fields using the existing
metric rules and source-aware metadata lookup. Preserve authored values and all
other aggregations, including formula inputs. Report applied reducers in a separate
decisions note. Propagate metadata authentication/permission errors. If metadata
cannot identify a safe default, ask the caller to provide reduceTo explicitly.
Document signal-specific order keys without rewriting caller intent.

## Files to Modify

- `internal/handler/tools/query_builder*.go` — default reducers and handler coverage
- `internal/handler/tools/metrics_query.go` — retain metadata identity for exact-match checks
- `pkg/metricsrules/guide.go`, `README.md`, `manifest.json` — synchronized guidance
- `internal/mcp-server/testdata/wire-catalog/` — only changed contract entries
- `tests/e2e/tests/test_query_response_paths.py` — seeded scalar metric regression
- Companion agent-skills query skill and evaluation cases

## Key Decisions

### 2026-09-26 — Metadata, not aggregation guesses

Metric type and monotonicity determine the existing default (counter sum; other
supported types avg). Time aggregation alone cannot reliably distinguish types.
Explicit reducers bypass metadata lookup. Query-local caching is scoped by metric
name and source. Missing monotonicity for sums cannot safely select a reducer.
Ordering guidance preserves explicit order instead of guessing its meaning.

## Reference Links

- https://github.com/SigNoz/nerve-pod/issues/359
- `docs/mcp-best-practices.md`, section 11
- https://github.com/SigNoz/signoz/blob/v0.143.0/pkg/types/querybuildertypes/querybuildertypesv5/validation.go

## Verification

- Passed focused handler, routing, wire-catalog, and description-budget checks.
- Passed `GOTOOLCHAIN=go1.26.0 make ci`, including race tests, guardrails,
  Inspector, both protocol-era conformance scenarios, Python style, and repo docs.
- Added a seeded scalar-gauge e2e regression requiring numeric data and equivalence
  to explicit avg; the PR's ephemeral SigNoz CI suite will execute it.
- Companion skill passes quick_validate, pinned skills-ref, version/config checks,
  clean portable packaging, and plugin/MCP schemas. Evaluation cases added;
  before/after model sessions were not run (EVL-1 deviation supported by the
  issue's recurring production failures and focused contract/regression checks).
- Reviewed applicable MCP best-practices section 11 items; no MUST exceptions,
  guardrail relaxations, or breaking changes. Existing annotations, searchContext,
  output envelope, and coded upstream errors are preserved.

## Outcome

Implemented absent-field defaults with exact metric/source lookup, shared metric
rules, explicit-value preservation, and decision notes. Documented order keys
without normalizing authored order. Companion:
https://github.com/SigNoz/agent-skills/pull/103.
No implementation scope deferred. E2E execution and automated PR review are pending
CI and will be monitored before handoff.
