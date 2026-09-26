# Plan: Invariant fuzz verification

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/136
PR: https://github.com/SigNoz/signoz-mcp-server/pull/327

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

Provide short and long bounded commands and a PR/manual workflow. Save native Go
corpus inputs and logs on failure; keep replay instructions in contributor docs.
Ordinary Go tests run the committed
corpus; a separate PR job generates fresh inputs for 30 seconds per target. Build
and setup time are additional, with no custom job, step, or runner deadlines. Runs
are credential-free and make no live SigNoz requests.

## Files to Modify

- `internal/handler/tools/schema_compat_fuzz_test.go` — schema validity and stability.
- `pkg/util/weburl_fuzz_test.go` — enrichment without field or numeric loss.
- `pkg/types/querybuilder_fuzz_test.go` — authored query field preservation.
- `pkg/timeutil/time_fuzz_test.go` — explicit epoch conversion and saturation.
- `Makefile`, `scripts/test-fuzz.sh`, `.gitignore` — bounded campaign commands.
- `scripts/fuzz_runner_test.go` — current-failure artifact regression coverage.
- `.github/workflows/fuzz.yaml` — PR runs and failure artifacts.
- `CONTRIBUTING.md` — concise commands, CI budget, and replay instructions.
- `CLAUDE.md` — keep the `make ci` check list current and guide future fuzz coverage.

## Key Decisions

### 2026-09-26 — Four focused targets

- Group the three URL enrichment paths under one preservation target; each other
  target protects a distinct boundary failure already represented in regressions.
- Use native corpus input identifiers for replay, not a numeric PRNG seed. Go's
  mutation schedule is nondeterministic; the saved failing input is reproducible.
- Production contracts are unchanged. No agent-skills companion change is expected.
- Demonstrate oracle sensitivity with temporary plausible production defects,
  restore them, and record results. Do not add a mutation-testing framework.

### 2026-09-26 — Replace scheduled fuzzing with a PR check

- At the user's request, remove the weekly schedule and run fresh-input fuzzing on
  every PR in one job. Spend 30 seconds per target (two minutes of fuzzing total),
  plus build and setup time.
- Include the short campaign in `make ci`; retain longer local campaigns for manual
  investigation. This user-directed change replaces the issue's periodic-run requirement.
- Keep contributor instructions concise; target rationale and seed provenance live
  in this plan and the tests.
- Document how to grow coverage: prefer extending existing targets and require a
  distinct realistic failure plus a stable invariant for each new target.

### 2026-09-26 — Remove deadlines and correct failure replay

- The user superseded the earlier five-minute CI cap: remove the workflow's job/step
  timeouts and the runner's GNU timeout wrapper and custom Go test timeout. The
  30-second input-generation budget remains. Cold compilation is no longer limited
  by the old two-minute wrapper.
- Copy only the corpus hash explicitly reported by the current Go failure log.
  Build and seed failures get a labeled fallback command, not unrelated old files.
- Shell-escape replay arguments and accept only Go's reported hexadecimal corpus
  names. A regression test covers generated, seed, and build failures with hostile
  pre-existing filenames; it reproduces both replay defects on the old runner.
- The review's Homebrew-only-gtimeout claim does not match the current formula or
  the local installation, both of which expose timeout too. Removing the wrapper
  removes the fuzz runner's dependency regardless.

### 2026-09-26 — Keep executable replay commands out of artifacts

- PR code can overwrite artifact files, so escaping generated commands does not
  make a downloaded replay script trustworthy. Remove generated commands entirely
  and upload only logs, environment metadata, and corpus data.
- Keep a fixed replay command in CONTRIBUTING.md. Contributors review the PR code
  and inputs before restoring corpus files; artifact text is never executable guidance.
- Retain the runner regression for copying only the current failure and labeling
  failures without new inputs. Remove the obsolete executable-replay assertions.

## Reference Links

- [Issue](https://github.com/SigNoz/nerve-pod/issues/136)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Homebrew coreutils](https://formulae.brew.sh/formula/coreutils)

## Verification

- All four seed corpora and the 10-second-per-target campaign passed on Go 1.26.0.
- `GOTOOLCHAIN=go1.26.0 go test -count=1 ./...` passed.
- `GOTOOLCHAIN=go1.26.0 make ci` passed, including race, protocol, and conformance checks.
- After switching to PR fuzzing, `GOTOOLCHAIN=go1.26.0 make ci FUZZ_TIME=30s`
  passed, including all four 30-second fuzz campaigns; workflow lint and ready-plan
  validation passed for the updated workflow and documentation.
- After removing custom deadlines and correcting replay, the same full CI command
  passed again. The new runner regression test failed on the prior script for both
  shell execution and stale-corpus reporting, then passed on the corrected script.
  A native Go fuzz probe with hostile pre-existing corpus names verified that only
  the current failure is archived and its replay fails before a fix and passes after it.
- After removing generated replay commands, `GOTOOLCHAIN=go1.26.0 make ci FUZZ_TIME=30s`
  passed again. The runner's generated/seed/build failure regression and workflow
  lint passed. A native Go probe restored the archived corpus and verified that the
  fixed command in contributor docs fails before a defect fix and passes after it.
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

Added four focused native targets, short/long commands, PR/manual CI with
failure artifacts, and contributor instructions. No production behavior or public
contracts changed; no metadata or agent-skills updates are needed. No real defect
was found in these campaigns, so there is no new minimized regression corpus to
commit. Seed cases remain inline and the runner preserves future failures.

The AI Assistant portion remains under the linked issue; this change does not
complete or close that cross-repository task.
