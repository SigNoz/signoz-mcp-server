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

- `internal/client/client.go`, `internal/client/interface.go` — exact metric metadata lookup
- `internal/handler/tools/query_builder*.go` — default reducers and handler coverage
- `internal/handler/tools/metrics_query.go` — retain metadata identity for exact-match checks
- `pkg/metricsrules/guide.go`, `README.md` — synchronized guidance (manifest description stays unchanged)
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

### 2026-10-17 — Rebase onto the current pass-through and testing contracts

Rebased onto main at `2284b68`. Keep the generated Query Builder request type for
reading input and insert reducers into the original JSON object, so unknown
fields and explicit values reach SigNoz unchanged. Do not restore the retired
QueryPayload validator or MockClient. Preserve main's missing-start guard and
its existing bounds behavior.

Use the real HTTP client against strict test upstreams for reducer wiring,
metadata recovery, source isolation, and discovery-budget behavior. The live
regression retains only the check that requires SigNoz: a seeded gauge hidden
beyond the first substring catalog page executes successfully and agrees with
explicit avg. Decision-note assertions belong in handler coverage. The metricsrules package
owns the full metric-type reduction table; handler coverage proves wiring with
different counter/gauge metadata for the same metric in each source.

Coordinated HTTP cancellation replaces the mock-only timing test. Exact
30-second elapsed-time and local context-deadline assertions were dropped; the
shared timeout and query-context independence remain code-reviewed rather than
directly timed, avoiding wall-clock sleeps and production-only test hooks.

An outstanding review found that the exact-name endpoint could return a
conflicting nonempty metricName. Preserve that identity and reject it through
the existing metadata validation/WARN path; synthesize the requested name only
when upstream omits it.

### 2026-10-17 — Claude Opus 5.5 review

Accepted the missing-metadata recovery and source-normalization findings.
The exact endpoint returns 404 for missing metadata; this now reaches the same
field-specific recovery as an exhausted meter catalog and tells the caller to
verify metricName and source with signoz_list_metrics. Normalize the discovery
source by trimming and lowercasing, as SigNoz does for builder queries, while
leaving authored JSON untouched. The source-isolation test covers both `meter`
and ` Meter ` sharing discovery without reading the ordinary store.

Kept explicit null/empty reducers unchanged: the defined contract defaults only
absent fields, preserves invalid supplied values for SigNoz validation, and
avoids guessing intent (SCH-2). README, guide, and tests agree on that policy.
Kept the conflicting-identity guard and its negative test: it exercises our
fail-closed choice for unexpected upstream data, not a claim that current SigNoz
normally sends metricName in this response. Nonempty conflicts still WARN.

Strengthened the live gauge case to span at least two fixed 60-second buckets
and require explicit sum to differ from avg, so selecting the wrong reducer
cannot pass merely because both reductions saw one bucket. Current live
verification will use main's pinned SigNoz release, not the September run.

The second Claude Opus 5.5 review confirmed those decisions and identified three
more narrow issues, all addressed: meter discovery now retries its bounded
catalog without a time filter because hourly buckets can precede the requested
start; source, metricName, and reducer presence honor the case-insensitive keys
SigNoz accepts; and metadata recovery consistently lists reducers while a missing
metric name identifies the metricName field itself. Strict HTTP tests cover the
empty-window meter case, preserved `ReduceTo`, aliased source/metric fields,
and missing-name recovery before any upstream call. Broader bounds casing and
presentation nits remain outside this change; no correctness finding is deferred.

## Reference Links

- https://github.com/SigNoz/nerve-pod/issues/359
- `docs/mcp-best-practices.md`, section 11
- https://github.com/SigNoz/signoz/blob/v0.143.0/pkg/types/querybuildertypes/querybuildertypesv5/validation.go

## Verification

Current-main verification (2026-10-17):

- `GOTOOLCHAIN=go1.26.9 make ci` passes, including race tests, wire catalog,
  guardrails, both protocol-era checks, Python style, and repository docs. This
  matches the patch version in the latest GitHub CI run.
- Go 1.26.0 reports six staticcheck warnings in unchanged test helpers; the same
  warnings reproduce on an untouched archive of main. Go 1.26.9 reports none.
- The focused real-HTTP reducer suite and its race run pass.
- Claude Opus 5.5 completed two reviews. All substantive findings were fixed;
  explicit null/empty preservation and the negative identity test were accepted
  as intentional contracts on follow-up. Full CI passed after both rounds of fixes,
  including the final `GOTOOLCHAIN=go1.26.9 make ci` run. Current live e2e
  results are owned by the PR checks after push.

The September results below describe the pre-rebase implementation.

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
