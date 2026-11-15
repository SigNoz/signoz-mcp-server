#!/bin/bash
# Opens a SigNoz/charts PR that bumps charts/signoz-mcp-server to a release tag.
# The chart's version is the tag without its leading v, and its appVersion is the tag.
# Usage: GITHUB_TOKEN=... GIT_USER=... GIT_EMAIL=... scripts/chart.sh vX.Y.Z

set -euo pipefail

readonly VERSION="${1:?usage: scripts/chart.sh <version>}"
# From the environment, so the token stays out of the process list.
readonly TOKEN="${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
readonly AUTHOR="${GIT_USER:?GIT_USER is required}"
readonly EMAIL="${GIT_EMAIL:?GIT_EMAIL is required}"
readonly REPO="${CHARTS_REPO:-SigNoz/charts}"
readonly BASE="${CHARTS_BASE:-main}"

readonly CHART="charts/signoz-mcp-server"

# Rewrites version and appVersion in the Chart.yaml at $1, in place, and leaves
# the rest alone. yq would reindent every sequence in it.
version() {
  sed -E \
    -e "s|^version: .*|version: ${VERSION#v}|" \
    -e "s|^appVersion: .*|appVersion: \"${VERSION}\"|" \
    "$1" >"$1.next"
  mv "$1.next" "$1"
  if ! grep -qx "version: ${VERSION#v}" "$1" ||
    ! grep -qx "appVersion: \"${VERSION}\"" "$1"; then
    echo "could not set the version in $1" >&2
    exit 1
  fi
}

open_pr() {
  echo ">> Opening the pull request"
  local body="#### Chores

- Bump the \`signoz-mcp-server\` chart to ${VERSION#v}."
  if [[ -n "${stale:-}" ]]; then
    body+="

> [!NOTE]
> The README could not be regenerated here, so its version badge may still read
> the previous release. Run \`make chart-docs CHARTS=${CHART}\` on this branch."
  fi

  GH_TOKEN="${TOKEN}" gh pr create --repo "${REPO}" --base "${BASE}" --head "${BRANCH}" \
    --title "chore(signoz-mcp-server): bump SigNoz MCP Server to ${VERSION}" \
    --body "${body}"
}

WORK="$(mktemp -d)"
readonly WORK
trap 'rm -rf "${WORK}"' EXIT

readonly BRANCH="release/signoz-mcp-server-${VERSION#v}"

echo ">> Cloning ${REPO}"
# Anonymously, so the token never reaches the fetch URL; only the push and the
# pull request need it.
git clone --quiet --depth 1 --branch "${BASE}" "https://github.com/${REPO}.git" "${WORK}"

if [[ ! -f "${WORK}/${CHART}/Chart.yaml" ]]; then
  echo "not a signoz-mcp-server chart checkout: ${REPO}@${BASE}" >&2
  exit 1
fi

if git -C "${WORK}" ls-remote --exit-code --heads origin "${BRANCH}" >/dev/null; then
  # Any state, so a bump pull request someone closed is neither reopened nor
  # opened again.
  prs="$(GH_TOKEN="${TOKEN}" gh pr list --repo "${REPO}" --head "${BRANCH}" --state all --json number --jq length)"
  if [[ "${prs}" != "0" ]]; then
    echo ">> Branch ${BRANCH} and its pull request already exist, nothing to do"
    exit 0
  fi
  echo ">> Branch ${BRANCH} exists without a pull request"
  open_pr
  exit 0
fi

echo ">> Setting chart version to ${VERSION#v}"
version "${WORK}/${CHART}/Chart.yaml"

echo ">> Regenerating chart docs"
# Through the chart repository's own target, after the bump, since the README's
# badges read Chart.yaml. A stale badge is not worth holding back a release.
stale=""
if ! make -C "${WORK}" chart-docs CHARTS="${CHART}"; then
  echo ">> Could not regenerate chart docs, leaving the README as it was" >&2
  git -C "${WORK}" checkout --quiet -- "${CHART}/README.md" || true
  stale="yes"
fi

git -C "${WORK}" config user.name "${AUTHOR}"
git -C "${WORK}" config user.email "${EMAIL}"
git -C "${WORK}" checkout --quiet -b "${BRANCH}"
git -C "${WORK}" add "${CHART}"
if git -C "${WORK}" diff --cached --quiet; then
  echo ">> Chart is already up to date, nothing to do"
  exit 0
fi

echo ">> Pushing ${BRANCH}"
git -C "${WORK}" commit --quiet -m "chore(signoz-mcp-server): bump SigNoz MCP Server to ${VERSION}"
# A one-off helper hands git the token at push time, so it is never on argv or in
# .git/config. The single quotes leave ${GITHUB_TOKEN} for git's shell to expand.
# shellcheck disable=SC2016
git -C "${WORK}" -c credential.helper= \
  -c credential.helper='!f() { echo username=x-access-token; echo "password=${GITHUB_TOKEN}"; }; f' \
  push --quiet origin "${BRANCH}"

open_pr
