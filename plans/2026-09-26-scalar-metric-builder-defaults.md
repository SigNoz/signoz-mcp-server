# Plan: Scalar metric builder defaults

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/359
PR: https://github.com/SigNoz/signoz-mcp-server/pull/328

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
name and source; discovery uses the query start/end so historical queries do not
rely on the default catalog window. Missing monotonicity for sums cannot safely select a reducer.
Ordering guidance preserves explicit order instead of guessing its meaning.

### 2026-09-26 — Keep scalar details on relevant surfaces

The tool and manifest descriptions retain their cross-signal scope and routing.
The query parameter gives one sentence about scalar metric defaults; reducer
choices and the metadata request belong in the metrics guide. Applied defaults
remain visible in result notes. This follows the user's feedback about repeated
reduceTo guidance and the field-local placement rule (DSC-3).

### 2026-09-26 — Compatibility review and bounded discovery

SigNoz v0.143.0 and current main explicitly validate reduceTo for scalar metrics;
the backend sums or averages the resulting bucket values. The MCP's existing
counter-sum policy is not a backend default or proof of the user's intended
statistic: summing rates is not a count, and averaging percentiles is not a
full-window percentile. Keep the API's explicit contract; document these
semantics in the metrics guide and keep authored reducers explicit in skills.

Security review found unbounded per-request discovery. Preflight at most 16
distinct metric/source pairs before any upstream call and share a 30-second
deadline across discovery. These bounds leave normal small composite queries
room while preventing request-size-dependent network amplification and sequential
per-call timeout accumulation. Existing explicit-reducer queries bypass both.
Handler tests cover the boundary, duplicates, source separation, shared timeout,
parent deadline, and unchanged query-execution context.

The live regression covers a gauge against pinned SigNoz v0.143.0; type-specific
handler tests use mocked metadata. This is not exhaustive live coverage for
counters, histograms, shifted windows, or future backend releases. Metadata
availability remains an extra dependency only when reduceTo is omitted.

### 2026-09-26 — Exact-name fallback and type drift

A ten-result substring catalog page can exclude the requested name. When that
happens, ordinary metrics use the exact-name metadata endpoint; Cost Meter stays
in its own store and widens to the backend's maximum 5000 catalog results, since
the exact endpoint does not accept a source. If a meter metric still cannot be
identified, the caller must supply reduceTo. Fallbacks share the same discovery
deadline and request-local cache. Unsupported metadata types emit a structured
WARN before returning field-specific recovery guidance.

Extend the existing seeded gauge e2e with 11 substring collisions, verify that the
first catalog page excludes the target, then require successful scalar output
equal to explicit avg. HTTP handler tests exercise exact-name query encoding,
Cost Meter store isolation, reducer selection, and fallback 401/403 propagation.

## Reference Links

- https://github.com/SigNoz/nerve-pod/issues/359
- `docs/mcp-best-practices.md`, section 11
- https://github.com/SigNoz/signoz/blob/v0.143.0/pkg/types/querybuildertypes/querybuildertypesv5/validation.go

## Verification

- Passed focused handler, routing, wire-catalog, and description-budget checks.
- Passed `GOTOOLCHAIN=go1.26.0 make ci`, including race tests, guardrails,
  Inspector, both protocol-era conformance scenarios, Python style, and repo docs.
- Re-ran the full gate and `make check-repo-docs READY=1` after adding the
  discovery budget, shared deadline, regression cases, and semantic caveats; all passed.
- Full `make ci` passed again with exact-name fallback, structured type-drift
  warnings, HTTP-level fallback regressions, and the extended e2e scenario.
  CI owns live execution of the substring-collision scenario after the push.
- Added a seeded scalar-gauge e2e regression requiring numeric data and equivalence
  to explicit avg; the PR's ephemeral SigNoz CI suite owns live execution.
- Companion skill passes quick_validate, pinned skills-ref, version/config checks,
  clean portable packaging, and plugin/MCP schemas. Evaluation cases added;
  before/after model sessions were not run (EVL-1 deviation supported by the
  issue's recurring production failures and focused contract/regression checks).
- Reviewed applicable MCP best-practices section 11 items; no MUST exceptions,
  guardrail relaxations, or breaking changes. Existing annotations, searchContext,
  output envelope, and coded upstream errors are preserved.
- SigNoz main reducer and validation tests pass with its Go 1.25.7 toolchain:
  `go test ./pkg/types/querybuildertypes/querybuildertypesv5 -run 'TestFunctionReduceTo|Test.*Valid' -count=1`.
  Go 1.26 cannot build that checkout's pinned sonic dependency; no upstream files
  or dependencies were changed.

## Outcome

Implemented absent-field defaults with exact metric/source lookup, shared metric
rules, explicit-value preservation, and decision notes. Documented order keys
without normalizing authored order. Companion:
https://github.com/SigNoz/agent-skills/pull/103.
No implementation scope deferred. Live e2e outcomes and automated review findings
are tracked in the linked PR checks and review threads.
