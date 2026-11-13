#!/bin/bash
# Opens a pull request on SigNoz/charts that bumps the signoz-mcp-server chart to
# a release of this server.
#
# The chart is hand written in SigNoz/charts; only its version follows this
# repository. Its README is generated from the chart and is regenerated after
# the bump, best effort. Needs git, gh and the go toolchain the chart
# repository's docs target reaches for.
#
# Usage: scripts/chart.sh <version>
#
#   version  release tag, e.g. v0.19.0. The chart version and appVersion are
#            kept in lockstep with it: appVersion is the tag, version is the tag
#            without the leading v.
#
# Environment:
#   GITHUB_TOKEN  token allowed to push to CHARTS_REPO and open pull requests on
#                 it, read from the environment to keep it out of the process list
#   GIT_USER      name recorded as the author of the chart commit
#   GIT_EMAIL     email recorded as the author of the chart commit
#   CHARTS_REPO   chart repository, defaults to SigNoz/charts
#   CHARTS_BASE   branch the pull request targets, defaults to main
#
# Doing nothing is a success: the script exits 0 when the release branch is
# already on the remote, or when the chart already carries the version.

set -euo pipefail

readonly VERSION="${1:?usage: scripts/chart.sh <version>}"
readonly TOKEN="${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
readonly AUTHOR="${GIT_USER:?GIT_USER is required}"
readonly EMAIL="${GIT_EMAIL:?GIT_EMAIL is required}"
readonly REPO="${CHARTS_REPO:-SigNoz/charts}"
readonly BASE="${CHARTS_BASE:-main}"

# Chart.yaml's version must be SemVer, and the build triggers on any v* tag.
if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "not a release version: ${VERSION}, want vMAJOR.MINOR.PATCH[-PRERELEASE]" >&2
  exit 1
fi

readonly CHART="charts/signoz-mcp-server"

# Rewrites version and appVersion in place and leaves the rest of Chart.yaml
# alone. yq would reindent every sequence in it.
version() {
  sed -E \
    -e "s|^version: .*|version: ${VERSION#v}|" \
    -e "s|^appVersion: .*|appVersion: \"${VERSION}\"|" \
    "$1" > "$1.next"
  mv "$1.next" "$1"
  if ! grep -qx "version: ${VERSION#v}" "$1" ||
    ! grep -qx "appVersion: \"${VERSION}\"" "$1"; then
    echo "could not set the version in $1" >&2
    exit 1
  fi
}

WORK="$(mktemp -d)"
readonly WORK
trap 'rm -rf "${WORK}"' EXIT

readonly BRANCH="release/signoz-mcp-server-${VERSION#v}"

echo ">> Cloning ${REPO}"
# Anonymously, so the token never reaches the fetch URL; only the push and the
# pull request need it.
git clone --quiet --depth 1 --branch "${BASE}" "https://github.com/${REPO}.git" "${WORK}"
git -C "${WORK}" remote set-url --push origin "https://x-access-token:${TOKEN}@github.com/${REPO}.git"

if [[ ! -f "${WORK}/${CHART}/Chart.yaml" ]]; then
  echo "not a signoz-mcp-server chart checkout: ${REPO}@${BASE}" >&2
  exit 1
fi

if git -C "${WORK}" ls-remote --exit-code --heads origin "${BRANCH}" >/dev/null; then
  echo ">> Branch ${BRANCH} already exists, nothing to do"
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
git -C "${WORK}" push --quiet origin "${BRANCH}"

echo ">> Opening the pull request"
body="#### Chores

- Bump the \`signoz-mcp-server\` chart to ${VERSION#v}."
if [[ -n "${stale}" ]]; then
  body+="

> [!NOTE]
> The README could not be regenerated here, so its version badge may still read
> the previous release. Run \`make chart-docs CHARTS=${CHART}\` on this branch."
fi

GH_TOKEN="${TOKEN}" gh pr create --repo "${REPO}" --base "${BASE}" --head "${BRANCH}" \
  --title "chore(signoz-mcp-server): bump SigNoz MCP Server to ${VERSION}" \
  --body "${body}"
