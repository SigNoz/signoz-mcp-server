# Plan: Run Fork CI Without Privileged Checkouts

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/329

## Context

Fork PR #317 fails when `safe-to-test` invokes the existing
`pull_request_target` workflows. Checkout rejects executing fork code in the
privileged base-repository context. The shared Primus workflows also require
private-repository credentials.

The corrected workflows on #317 passed their full local gate and all 63 live
E2E cases. Reapplying the label still ran the old workflows from `main`, because
`pull_request_target` reads its workflow there. This CI-only change must land
first; the resource browser-link feature stays in #317.

## Approach

Use ordinary `pull_request` jobs with read-only permissions for fork and
Dependabot PRs. Run the existing `make ci` gate using public tools, retain
Primus for internal PRs, and require `safe-to-test` for every fork CI job.
Only the label-added event authorizes its immutable head revision; new commits,
reopening, and marking a PR ready require fresh approval. A metadata-only
`pull_request_target` workflow removes stale labels without checking out code.
Fork and Dependabot E2E runs receive no license secret. Disable persisted Git
credentials and explicitly install Go in E2E.

After this lands on `main`, refresh #317 against `main` and verify the new
head's checks. Rerunning old target-event runs does not adopt the fixed workflow.

## Files to Modify

- `.github/workflows/ci.yaml` — public fork checks and internal Primus routing.
- `.github/workflows/e2e.yaml` — ordinary PR events and conditional license access.
- `.github/workflows/checks.yaml`, `.github/workflows/guardrails.yaml`,
  `.github/workflows/mcp-protocol.yaml` — approval before any fork checkout.
- `.github/workflows/fork-approval.yaml` — reset stale approval using only PR metadata.
- `tests/README.md` — CI execution and default-branch rollout behavior.

## Key Decisions

### 2026-09-27 — Land the workflow repair before the feature

- Decision: extract the already-verified workflows into a CI-only PR.
- Rationale: edits confined to a fork cannot retire a default-branch event.
- No checkout safety bypass or guardrail relaxation is introduced. Existing
  race, lint, dependency, build, guardrail, protocol, and style targets still run.

### 2026-09-27 — Require approval for all fork CI

- Decision: gate every fork and Dependabot check on the addition of `safe-to-test`,
  as requested by the maintainer. Merely finding the label on a synchronize event
  does not authorize the new revision, even if label cleanup has not run yet.
- The repository-docs job fails before checkout when approval is missing. This
  preserves the required check on `ready_for_review` instead of silently skipping
  the ready-plan rules. The reset workflow also handles reopening and ready events.
- The only privileged workflow edits labels with the built-in GitHub token and
  executes no checked-out code. Test jobs keep read-only permissions and no fork
  repository secrets. Internal jobs and manual E2E/protocol runs retain their paths.

## Verification

- The initial workflows passed `actionlint` v1.7.12 and a condition matrix
  for internal, fork, Dependabot, labeled/unlabeled, and manual contexts on #317.
- #317 commit `03b9010`: public `make ci` job and all 63 E2E tests passed.
- CI-only branch: `GOTOOLCHAIN=go1.26.0 make ci` and `actionlint` v1.7.12 pass.
- Expanded approval policy: a local 25-case matrix passes against the workflow
  conditions and the inline reset script. It covers unlabeled and stale-label
  fork/Dependabot events, explicit approval, unrelated labels, all public check
  jobs, pinned revisions, read-only permissions, absent license secrets, internal
  PRs, manual dispatch, ready-plan enforcement, and reset API 404/403 behavior.
- Reran `GOTOOLCHAIN=go1.26.0 make ci` and `actionlint` v1.7.12 across all six
  changed workflows after adding the approval gates; both pass.
- Live label-reset triggering requires this workflow to reach `main`; the local
  reset tests use a mock GitHub API and do not claim live label-reset verification.

## Outcome

Implementation validated for the default branch. Merge and refresh of #317
remain pending; the old label-triggered failures will persist until that rollout.
Every fork and Dependabot check now requires explicit approval for its revision.
Only the separate metadata job can write PR labels, and it never checks out code.

## Reference Links

- [Feature PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
- [Passing fork CI](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36304528422)
- [Passing full E2E](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36304528210)
- [GitHub event semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request_target)
