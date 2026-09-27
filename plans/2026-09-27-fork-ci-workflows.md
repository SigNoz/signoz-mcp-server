# Plan: Run Fork CI Without Privileged Checkouts

Status: In Progress
Issue:
PR:

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
Primus for internal PRs, and keep `safe-to-test` as the cost gate for fork E2E.
Fork and Dependabot E2E runs receive no license secret. Check out immutable
revisions without persisted Git credentials and explicitly install Go in E2E.

After this lands on `main`, refresh #317 against `main` and verify the new
head's checks. Rerunning old target-event runs does not adopt the fixed workflow.

## Files to Modify

- `.github/workflows/ci.yaml` — public fork checks and internal Primus routing.
- `.github/workflows/e2e.yaml` — ordinary PR events and conditional license access.
- `tests/README.md` — CI execution and default-branch rollout behavior.

## Key Decisions

### 2026-09-27 — Land the workflow repair before the feature

- Decision: extract the already-verified workflows into a CI-only PR.
- Rationale: edits confined to a fork cannot retire a default-branch event.
- No checkout safety bypass or guardrail relaxation is introduced. Existing
  race, lint, dependency, build, guardrail, protocol, and style targets still run.

## Verification

- These exact workflow files passed `actionlint` v1.7.12 and a condition matrix
  for internal, fork, Dependabot, labeled/unlabeled, and manual contexts on #317.
- #317 commit `03b9010`: public `make ci` job and all 63 E2E tests passed.
- CI-only branch: `GOTOOLCHAIN=go1.26.0 make ci` and `actionlint` v1.7.12 pass.

## Outcome

Implementation validated for the default branch. Merge and refresh of #317
remain pending; the old label-triggered failures will persist until that rollout.

## Reference Links

- [Feature PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
- [Passing fork CI](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36304528422)
- [Passing full E2E](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36304528210)
- [GitHub event semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request_target)
