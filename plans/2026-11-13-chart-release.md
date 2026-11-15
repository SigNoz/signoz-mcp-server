# Plan: Chart Bump PR on Release

Status: In Progress
Issue: https://github.com/SigNoz/keystone-pod/issues/209
PR: https://github.com/SigNoz/signoz-mcp-server/pull/444

## Context

`charts/signoz-mcp-server` lives in SigNoz/charts (added by SigNoz/charts#952) and nothing bumps it
when this server releases, while releases ship about weekly. SigNoz/signoz-operator already opens
its own chart bump PR from its release workflow; this mirrors it until charts has a central flow.

## Approach

- `dockerbuildci.yaml` and `binarybuildci.yaml` trigger on the `vX.Y.Z` and `vX.Y.Z-*` tag
  patterns. On a tag push, the `charts` job in `dockerbuildci.yaml`
  (`if: github.ref_type == 'tag'`) runs after `build` and calls `scripts/chart.sh <tag>`.
- The script follows the operator's `hack/chart.sh`, with the same env contract and the same
  lockstep versioning: `version` is the tag without `v`, and `appVersion` is the tag. It clones
  SigNoz/charts anonymously, rewrites only those two fields in the chart's `Chart.yaml`, regenerates
  the README best effort, and opens the PR. The MCP chart has nothing generated from this
  repository, so the CRD and role sync is dropped and the script reads nothing from this checkout.
- Every tag the triggers accept ships a chart, pre-releases included. The trigger patterns, not the
  script, decide the tag shape.
- A rerun after a failed `gh pr create` opens only the missing PR; an existing PR in any state makes
  the run a no-op. A chart already at the tag's version is a no-op too.
- The token reaches git only at push time, through a one-off credential helper.
- The script exits 1 when the base branch has no `charts/signoz-mcp-server/Chart.yaml`.

## Files to Modify

- `scripts/chart.sh` — new: opens the chart bump PR on SigNoz/charts.
- `.gitignore` — track `scripts/chart.sh`; `/scripts/*` is ignored except listed CI helpers.
- `.github/workflows/dockerbuildci.yaml` — `charts` job; narrows `on.push.tags` to the SemVer tag
  patterns.
- `.github/workflows/binarybuildci.yaml` — narrows `on.push.tags` from `v*` to the same patterns.
- `RELEASE.md` — new: the release steps, including the chart bump.

## Key Decisions

### 2026-11-15 — lockstep chart versioning

- Decision: `version` is the tag without `v` and `appVersion` is the tag, as for the operator.
  Every release ships a chart, pre-releases included; Helm hides pre-release chart versions unless
  `--devel` is passed.
- Rationale: server and chart releases line up one to one.
- Alternative rejected: an independent chart SemVer, built 2026-11-13 and dropped so the versions
  stay aligned.

### 2026-11-15 — both build workflows on SemVer tags only

- Decision: `dockerbuildci` and `binarybuildci` trigger on `vX.Y.Z` and `vX.Y.Z-*`, so every tag
  that builds an image and binaries also yields a valid chart version.
- Rationale: a tag like `v0.20` builds nothing. Helm 3.20.0 rejects `version: 0.20` in lint
  ("version should be of type string but it's of type float64", since YAML reads it as 0.2), and
  `helm package` would silently produce 0.2. `v0.20.0-beta.0` lints and packages fine.
- Caveat: a nonsense tag ending in a bare `-` would still match.

### 2026-11-13 — reruns never duplicate or reopen

- Decision: the branch is `release/signoz-mcp-server-<version without v>`. If the branch exists with
  no PR in any state, the script opens only the PR. If a PR exists in any state, the run is a
  no-op: a closed PR is never reopened, and nothing is force pushed.
- Rationale: a failed `gh pr create` after the push must be recoverable by a rerun.

### 2026-11-15 — token only at push time

- Decision: the push gets the token from a one-off credential helper that reads `GITHUB_TOKEN` from
  the environment, so the token is never on argv or in `.git/config`. The clone stays anonymous,
  and `gh` gets the token through `GH_TOKEN`.
- Rationale: a push URL carrying the token put it on argv and in the clone's `.git/config`.

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
helpers. The `git` shim was first shown to intercept a push with `-c` options before the
subcommand, against a local bare remote that stayed empty.

- Against the real SigNoz/charts `main`, where the chart is at `0.0.1-rc.1`/`v0.19.0`:
  - `v0.20.0` gives `version: 0.20.0` and `appVersion: "v0.20.0"`, regenerated README badges,
    branch `release/signoz-mcp-server-0.20.0`, and the expected commit message, PR title and body.
  - `v0.20.0-rc.0` gives `0.20.0-rc.0`/`v0.20.0-rc.0`, and `v0.20.0-beta.0` gives
    `0.20.0-beta.0`/`v0.20.0-beta.0`.
  - With `make` dirtying the README and failing, the README is restored, only `Chart.yaml` is
    committed, and the PR body carries the stale-README note.
  - A base branch without the chart (`feat/ch-backup`) exits 1 at the chart guard.
- Token: the clone's `.git/config` holds only the anonymous URL, and the push argv carries the
  literal `${GITHUB_TOKEN}`. Offline, `git credential fill` with the script's helper returns
  `username=x-access-token` and the token.
- Against a local fixture: a chart already at `0.20.0`/`v0.20.0` makes `v0.20.0` a no-op with no
  push or PR. With the branch present and no PR, only `gh pr create` runs; with both present,
  nothing happens.
- Tag patterns, as anchored ERE: all 32 existing `vX.Y.Z` and `vX.Y.Z-rc.N` tags match. `V0.1.1`,
  `v0.20`, `v1`, `vfoo` and `v0.20.0.1` do not; `v0.20.0-` does.
- `shfmt -d -i 2 -ci` and `shellcheck` on the script, `actionlint` v1.7.7 on `dockerbuildci.yaml`,
  and `make check-repo-docs`: clean. On `binarybuildci.yaml`, actionlint reports only the
  `inputs.GO_VERSION` and `matrix.workdirs` errors, which predate this change.
- Known gaps:
  - Not yet run in CI.
  - A manual rerun of an older tag after a newer one merged would open a downgrade PR, the same as
    the operator.

## Outcome

Pending the first release run. SigNoz/charts#952 merged on 2026-11-15 with `version: 0.0.1-rc.1`
and `appVersion: "v0.19.0"`; to be lockstep from the start, the chart should be at
`version: 0.19.0` and `appVersion: "v0.19.0"` before then. Mark this plan Done once a release opens
a bump PR on SigNoz/charts.
