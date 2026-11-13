# Plan: Chart Bump PR on Release

Status: In Progress
Issue: https://github.com/SigNoz/keystone-pod/issues/209
PR: https://github.com/SigNoz/signoz-mcp-server/pull/444

## Context

`charts/signoz-mcp-server` lives in SigNoz/charts (added by SigNoz/charts#952) and nothing bumps it
when this server releases, while releases ship about weekly. SigNoz/signoz-operator already opens
its own chart bump PR from its release workflow; this mirrors it until charts has a central flow.

## Approach

- `dockerbuildci.yaml` triggers on `vX.Y.Z` and `vX.Y.Z-rc.N` tag patterns. On a tag push, a
  `charts` job (`if: github.ref_type == 'tag'`) runs after `build` and calls
  `scripts/chart.sh <tag>`.
- The script is a trimmed copy of the operator's `hack/chart.sh`, with the same env contract. It
  exits 0 on a pre-release tag before cloning. Otherwise it clones SigNoz/charts anonymously,
  rewrites only `version` and `appVersion` in the chart's `Chart.yaml`, regenerates the README best
  effort, and opens the PR. The MCP chart has nothing generated from this repository, so the CRD and
  role sync is dropped and the script reads nothing from this checkout.
- The chart keeps its own SemVer: `appVersion` is the tag and `version` steps from its current value
  (see the decision below).
- A rerun after a failed `gh pr create` opens only the missing PR; an existing PR in any state makes
  the run a no-op.
- The script refuses a non-SemVer tag, and exits 1 when the base branch has no
  `charts/signoz-mcp-server/Chart.yaml` or its `version` or `appVersion` is not SemVer.

## Files to Modify

- `scripts/chart.sh` — new: opens the chart bump PR on SigNoz/charts.
- `.gitignore` — track `scripts/chart.sh`; `/scripts/*` is ignored except listed CI helpers.
- `.github/workflows/dockerbuildci.yaml` — `charts` job; narrows `on.push.tags` to release tag
  patterns.

## Key Decisions

### 2026-11-13 — independent chart SemVer

- Decision: `appVersion` is the tag. The chart `version` moves one step from its current value, at
  the level the tag moved past the current `appVersion` on charts main: major `X+1.0.0`, minor
  `X.Y+1.0`, patch `X.Y.Z+1`. A hand-set prerelease `appVersion` counts as a patch. An equal or
  older tag is a no-op. The chart's own prerelease suffix is dropped when bumping.
- Rationale: a chart-only fix can bump `version` by hand between releases without waiting for a
  server release.
- Alternatives rejected:
  - Lockstep versioning (`version` is the tag without `v`, as for the operator): the chart version
    must not equal the server version, and chart versions start at 0.1.0.
  - Depending on semver-tool: it has little adoption, so the script ports its compare, diff and
    bump semantics into bash functions instead.

### 2026-11-13 — release tags only, pre-releases skipped

- Decision: the trigger uses SigNoz/signoz-operator's tag patterns, so a malformed tag such as
  `v0.20` builds nothing. Every existing tag matches except a hand-made `V0.1.1`, which `v*` never
  matched either. The `charts` job runs on any tag push, and the script exits 0 on a pre-release tag
  before cloning, so a manual run on an rc tag cannot leave `appVersion` on an rc.
  `publish-mcp-registry` keeps its own gate.
- Rationale: this repository already treats release candidates as unpublished, and a gate in the
  script holds for manual runs too.

### 2026-11-13 — reruns never duplicate or reopen

- Decision: the branch is `release/signoz-mcp-server-<tag>`. If the branch exists with no PR in any
  state, the script opens only the PR. If a PR exists in any state, the run is a no-op: a closed PR
  is never reopened, and nothing is force pushed.
- Rationale: a failed `gh pr create` after the push must be recoverable by a rerun.

### 2026-11-13 — `needs: build`

- Decision: the job waits for the image push.
- Rationale: the bumped chart must never point at an image that does not exist yet.

### 2026-11-13 — push with the app token

- Decision: push and open the PR with the `PRIMUS_APP_ID` app token, not `GITHUB_TOKEN`.
- Rationale: PRs opened with `GITHUB_TOKEN` do not trigger workflows, so the chart's lint, ct and
  unittest checks would not run.

## Reference Links

- [SigNoz/charts#952: signoz-mcp-server chart](https://github.com/SigNoz/charts/pull/952)
- [signoz-operator hack/chart.sh](https://github.com/SigNoz/signoz-operator/blob/main/hack/chart.sh)

## Verification

Every run used PATH shims that intercept `git push` and `gh`, a dummy token, and disabled credential
helpers.

- Base `version`/`appVersion`, tag, resulting chart `version`:
  - `0.0.1-rc.1`/`v0.19.0`: v0.20.0 gives 0.1.0; v0.19.1 gives 0.0.2.
  - `0.1.0`/`v0.19.0`: v0.19.0 is a no-op; v0.19.1 gives 0.1.1; v0.20.0 gives 0.2.0.
  - `0.1.0`/`v0.20.0`: v0.20.1 gives 0.1.1; v0.20.0 and v0.19.5 are no-ops.
  - `0.1.1`/`v0.20.1`: v0.21.0 gives 0.2.0.
  - `0.2.0`/`v0.21.0`: v1.0.0 gives 1.0.0.
  - `0.1.9`/`v0.20.9`: v0.20.10 gives 0.1.10.
  - `0.9.0`/`v0.9.0`: v0.10.0 gives 0.10.0.
  - `0.1.0`/`v0.20.0-rc.1`: v0.20.0 gives 0.1.1.
- Skipped and refused: `v0.20.0-rc.1` is skipped with no clone; `0.20.0`, `v0.20` and `v01.2.3`
  are refused with exit 1; a chart `version: abc` exits 1.
- Against the real SigNoz/charts `feat/signoz-mcp` branch, v0.20.0 gives 0.1.0 and the README
  badges are regenerated.
- Reruns: with the branch present and no PR, only the PR is opened; with both present, nothing
  happens.
- The semver function unit tests (51 checks) pass on bash 3.2 and 5.2.
- `shellcheck scripts/chart.sh`, `actionlint` v1.7.7 on `dockerbuildci.yaml` and
  `make check-repo-docs`: clean.
- Known gaps:
  - Not yet run in CI: the first stable release exercises the app token and the real push.
  - Two unmerged bump PRs compute the same chart version, because each reads charts main.
  - A `+build` tag is treated as its core version.

## Outcome

The workflow and script merge ahead of their first real run, which needs SigNoz/charts#952 merged
so the chart exists on charts main; until then a stable tag's `charts` job exits 1 at the chart
guard. Mark this plan Done once a stable release opens a bump PR on SigNoz/charts.
