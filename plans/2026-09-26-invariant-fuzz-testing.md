# Plan: Invariant fuzz verification

Status: In Progress
Issue: https://github.com/SigNoz/nerve-pod/issues/136
PR:

## Context

Implement the MCP-server portion of the issue. Existing regressions document schema
compatibility, large-integer corruption during URL enrichment, lost Query Builder
expressions, and epoch conversion overflow. Fixed examples do not cover combinations
of nested payloads, unusual strings, and numeric boundaries.

The user requires a credible user-visible failure for every new target. The AI
Assistant perturbation layer remains separate work under the same issue.

## Approach

Add four native Go fuzz targets, one per boundary area. Reuse existing regression
shapes and generated production schemas. Mix malformed inputs with constructed
valid envelopes so rejection does not dominate coverage. Check preserved fields,
exact numbers, valid JSON, normalization stability, and documented time units.
Do not assert internal call order or use production code as the expected-value oracle.

Provide short and long bounded commands and a weekly/manual workflow. Save native
Go corpus inputs and replay commands on failure. Ordinary Go tests run the committed
corpus; fresh-input campaigns remain separate from deterministic PR gates. Runs are
credential-free and make no live SigNoz requests.

## Files to Modify

- `internal/handler/tools/schema_compat_fuzz_test.go` — schema validity and stability.
- `pkg/util/weburl_fuzz_test.go` — enrichment without field or numeric loss.
- `pkg/types/querybuilder_fuzz_test.go` — authored query field preservation.
- `pkg/timeutil/time_fuzz_test.go` — explicit epoch conversion and saturation.
- `Makefile`, `scripts/test-fuzz.sh`, `.gitignore` — bounded campaign commands.
- `.github/workflows/fuzz.yaml` — periodic runs and failure artifacts.
- `CONTRIBUTING.md` — failure scenarios, execution, replay, and corpus promotion.

## Key Decisions

### 2026-09-26 — Four focused targets

- Group the three URL enrichment paths under one preservation target; each other
  target protects a distinct boundary failure already represented in regressions.
- Use native corpus input identifiers for replay, not a numeric PRNG seed. Go's
  mutation schedule is nondeterministic; the saved failing input is reproducible.
- Production contracts are unchanged. No agent-skills companion change is expected.
- Demonstrate oracle sensitivity with temporary plausible production defects,
  restore them, and record results. Do not add a mutation-testing framework.

## Reference Links

- [Issue](https://github.com/SigNoz/nerve-pod/issues/136)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)

## Verification

- All four seed corpora and the 10-second-per-target campaign passed on Go 1.26.0.
- `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...` passed.
- `GOTOOLCHAIN=go1.26.0 make ci` passed, including race, protocol, and conformance checks.
- `actionlint` v1.7.7 and `bash -n scripts/test-fuzz.sh` passed.
- Temporary Go overlays proved all four targets detect plausible defects: leaving
  boolean schemas unnormalized, round-tripping enrichment through float64, losing
  a PromQL expression, and removing the epoch multiplication overflow guard.
  No production files were modified by these probes.
- An isolated temporary Go module verified a failing campaign returns nonzero,
  saves its minimized input as an artifact, reproduces the failure with the logged
  command, and passes that command after fixing the injected defect.
- All four Go fuzz targets passed five-minute campaigns with two workers each.
  The running shell wrappers failed after those successful tests because the
  runner file was edited while Bash was still reading it. The final unchanged
  runner then passed `make test-fuzz-long FUZZ_LONG_TIME=1s` across all four targets;
  its failure-artifact and toolchain-pinned replay probe also passed again.

Live E2E is not needed for test-only changes; these fuzz targets supplement existing
upstream integration coverage. No relevant recorded live payload was found in these
packages; the reused raw trace fixture models the upstream response shape.

## Outcome

Local implementation and verification are complete. This plan stays In Progress
until a PR is opened: the repository validator requires a PR link for Done plans.
Add that link and mark Done before the PR leaves draft; it should not merge incomplete.

Added four focused native targets, short/long commands, weekly/manual CI with
failure artifacts, and contributor instructions. No production behavior or public
contracts changed; no metadata or agent-skills updates are needed. No real defect
was found in these campaigns, so there is no new minimized regression corpus to
commit. Seed cases remain inline and the runner preserves future failures.

The AI Assistant portion remains under the linked issue; this change does not
complete or close that cross-repository task.
