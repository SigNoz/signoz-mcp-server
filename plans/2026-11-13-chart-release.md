# Plan: Chart Bump PR on Release

Status: In Progress
Issue: https://github.com/SigNoz/keystone-pod/issues/209
PR:

## Context

`charts/signoz-mcp-server` lives in SigNoz/charts (added by SigNoz/charts#952) and nothing bumps it
when this server releases, while releases ship about weekly. SigNoz/signoz-operator already opens
its own chart bump PR from its release workflow; this mirrors it until charts has a central flow.

## Approach

- On a stable `vX.Y.Z` tag, a `charts` job in `dockerbuildci.yaml` runs after `build` and calls
  `scripts/chart.sh <tag>`.
- The script is a trimmed copy of the operator's `hack/chart.sh`, with the same env contract and
  no-op exits. It clones SigNoz/charts anonymously, rewrites only `version` and `appVersion` in the
  chart's `Chart.yaml`, regenerates the README best effort, and opens the PR. The MCP chart has
  nothing generated from this repository, so the CRD and role sync is dropped and the script reads
  nothing from this checkout.
- The script refuses a non-SemVer tag, and exits 1 when the base branch has no
  `charts/signoz-mcp-server/Chart.yaml`.

## Files to Modify

- `scripts/chart.sh` — new: opens the chart bump PR on SigNoz/charts.
- `.gitignore` — track `scripts/chart.sh`; `/scripts/*` is ignored except listed CI helpers.
- `.github/workflows/dockerbuildci.yaml` — `charts` job.

## Key Decisions

### 2026-11-13 — lockstep chart versioning

- Decision: the chart's `version` is the tag without `v` and `appVersion` is the tag, as for the
  operator.
- Rationale: one version to reason about; the chart only pins the image.
- Cost: a chart-only template fix waits for the next MCP release, since chart-releaser skips an
  existing version.

### 2026-11-13 — stable tags only

- Decision: the job uses `publish-mcp-registry`'s gate, so `main` pushes and `-rc` tags open no PR.
- Rationale: this repository already treats release candidates as unpublished.

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

- `shellcheck scripts/chart.sh` and `actionlint` v1.7.7 on `dockerbuildci.yaml`: clean.
- `make check-repo-docs`: passes.
- Dry run against SigNoz/charts with PATH shims that intercept `git push` and `gh`, a dummy token
  and credential helpers disabled:
  - `CHARTS_BASE=feat/signoz-mcp scripts/chart.sh v0.20.0`: exit 0. The commit sets
    `version: 0.20.0` and `appVersion: "v0.20.0"`, regenerates the README badges, and `gh` receives
    the expected title and `#### Chores` body. With `make` failing, the README is restored and the
    body carries the stale-README note.
  - `scripts/chart.sh 0.19.0`: exit 1, version refused.
  - `CHARTS_BASE=main`: exit 1 at the chart guard, since the chart is not on main yet.
- Not yet run in CI: the first stable release exercises the app token and the real push.

## Outcome

Pending the first stable release run. Mark Done once it opens a bump PR on SigNoz/charts.
