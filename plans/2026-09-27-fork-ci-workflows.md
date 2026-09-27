# Plan: Require Trusted Approval for Fork CI

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/329

## Context

Fork PR #317 fails when `safe-to-test` invokes existing `pull_request_target`
workflows that check out contributor code with privileged credentials. Checkout
rejects this combination. Primus also requires private-repository credentials.
The workflow repair must reach `main`; the browser-link feature stays in #317.

## Approach

Keep internal PRs on Primus and require explicit approval for all fork and
Dependabot validation. A trusted, metadata-only target-event workflow records
pending commit statuses on the head and available merge revision, then dispatches
a default-branch worker for the approved SHA. Both revisions are tested. The worker validates the current approval, runs public checks and community
E2E with read-only permissions and no secrets or cache access, and reports results
from a separate runner. Code runners never receive write tokens.

New commits, reopening, readiness changes, and label removal invalidate approval.
Unrelated labels preserve it. Pending statuses use existing required check names,
so skipped or manufactured check runs cannot bypass the trusted approval gate.
GitHub's repository fork-run policy remains responsible for arbitrary new
contributor workflows, which cannot be prohibited by editable PR YAML alone.

## Files to Modify

- `.github/workflows/ci.yaml`, `e2e.yaml`, `checks.yaml`, `guardrails.yaml`, and
  `mcp-protocol.yaml` — retain ordinary internal jobs; route forks to the worker.
- `.github/workflows/fork-approval.yaml` — trusted approval and reset dispatcher.
- `.github/workflows/fork-ci.yaml` — approval validation, isolated code jobs, and reporter.
- `tools/mcp-ci/fork-ci.test.cjs`, package manifests, and `Makefile` — regression checks.
- `tests/README.md`, `docs/architecture.md` — workflow and trust-boundary documentation.

## Key Decisions

### 2026-09-27 — Land the CI repair before the feature

- Extract CI changes into #329; #317 retains only issue #303 changes.
- After merge, refresh #317 against `main` and approve its new head.
- Keep the full public `make ci` gate and ready-plan validation. No checkout
  safety override or runtime guardrail relaxation is introduced.

### 2026-09-27 — Keep approval outside contributor-controlled YAML

- Review identified that ordinary PR job conditions are editable by a fork and
  that unrelated labels invalidated the docs check. Both require a trusted gate.
- The dispatcher uses only the built-in token for label, status, and dispatch
  APIs. It verifies the label actor has write access and pins the approved SHA.
- The worker verifies approval provenance, current SHA, draft state, label, and
  latest approval identity. Its reporter uses trusted job conclusions only.
- Dispatcher and reporter serialize status writes per PR. Results from a revoked
  or superseded approval cannot overwrite pending checks for the new approval.
- Preserve all six contexts in the current main-branch ruleset. GitHub requires
  both a commit status and a check run to pass when they share a required name.
- Disable worker cache access with GitHub's scoped `cache-mode: none` control.
  Actionlint v1.7.12 lacks this new schema field; exclude only that documented
  unknown-key diagnostic, while the regression test pins the no-cache boundary.
  This is a linter compatibility exception, not a relaxation of cache security.

### 2026-09-27 — Fix verified review findings

- Run both checks and E2E for each revision that will receive success, including
  the pinned merge commit. A failure in either matrix blocks the required gate.
- Stop comparing the frozen merge SHA to GitHub's recomputed current merge SHA.
  Publish only to the tested revisions. The existing strict up-to-date branch
  rule requires a head update and fresh approval before merging after `main` moves.
- Keep approval identity on the already-required `contract` status, including
  after reports. Compare trusted workflow run numbers and snapshot labels so
  queued old resets cannot erase newer approvals.
- Invalidate `contract` first, merge revision before head, before label mutation
  or other status writes. Restore it last after both test suites pass. Check its
  identity on both revisions so an interrupted reset also fences old reporters.
- Regression tests cover tested/reported revisions, advancing `main`, reordered
  events, and API failures during invalidation, label removal, and reporting.
  An initial API failure propagates without pretending the reset succeeded.

## Verification

- Previous iteration `3dec54c`: all active GitHub checks and 61/61 live E2E tests
  passed, with resource and environment cleanup confirmed by a delegated verifier.
- The final dispatcher/worker scripts have 52 behavior and security-boundary
  tests against mocked GitHub APIs: approval, stale/revoked metadata, required
  statuses, unrelated labels, untrusted origins, API errors, failures, and cleanup
  of approval state, and retrying the same approved run. These are part of `make ci` and the protocol CI job.
- Six targeted regressions fail against the previous workflows and pass with
  these fixes. All 52 policy tests pass.
- Final `make ci`, workflow lint (with the cache schema exception), the ready
  docs check, and all 52 approval tests pass. Results are recorded in the PR body.
- Live dispatcher/worker activation requires these files on `main`. Local tests
  do not claim to exercise GitHub's event delivery or branch-rule integration.

## Outcome

Implementation complete. Merge, fork refresh, and live approval-to-worker rollout
remain pending on #329. PR #317 contains no GitHub workflow changes. The final
revision's check results and rollout constraints are recorded in the PR body.

## Reference Links

- [Feature PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
- [Passing 61-test E2E run](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36307808454)
- [GitHub event semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows)
- [Required status and check names](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks)
- [Scoped cache permissions](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching#controlling-cache-access-with-cache-mode)
