#!/bin/bash
# Opens a pull request on SigNoz/charts that bumps the signoz-mcp-server chart to
# a release of this server.
#
# The chart is hand written in SigNoz/charts; only its versions follow this
# repository. Its README is generated from the chart and is regenerated after
# the bump, best effort. Needs git, gh and the go toolchain the chart
# repository's docs target reaches for.
#
# Usage: scripts/chart.sh <version>
#
#   version  release tag, e.g. v0.19.0. It becomes the chart's appVersion. The
#            chart keeps its own SemVer, bumped from its current version by the
#            level the tag moved past the current appVersion.
#
# Environment:
#   GITHUB_TOKEN  token allowed to push to CHARTS_REPO and open pull requests on
#                 it, read from the environment to keep it out of the process list
#   GIT_USER      name recorded as the author of the chart commit
#   GIT_EMAIL     email recorded as the author of the chart commit
#   CHARTS_REPO   chart repository, defaults to SigNoz/charts
#   CHARTS_BASE   branch the pull request targets, defaults to main
#
# Doing nothing is a success: the script exits 0 for a pre-release tag, when the
# release branch and its pull request already exist, or when the chart's
# appVersion is already at or past the tag. A release branch on the remote
# without a pull request only gets the pull request.

set -euo pipefail

readonly VERSION="${1:?usage: scripts/chart.sh <version>}"
readonly TOKEN="${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
readonly AUTHOR="${GIT_USER:?GIT_USER is required}"
readonly EMAIL="${GIT_EMAIL:?GIT_EMAIL is required}"
readonly REPO="${CHARTS_REPO:-SigNoz/charts}"
readonly BASE="${CHARTS_BASE:-main}"

# semver.org's SemVer 2.0 regex in ERE form, with an optional leading v or V.
readonly SEMVER='^[vV]?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'\
'(-((0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?'\
'(\+([0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*))?$'

# The semver_* semantics follow semver-tool's compare, diff and bump.

# Prints MAJOR MINOR PATCH PRERELEASE of $1, and fails when $1 is not SemVer.
semver_parse() {
  [[ "$1" =~ ${SEMVER} ]] || return 1
  echo "${BASH_REMATCH[1]} ${BASH_REMATCH[2]} ${BASH_REMATCH[3]} ${BASH_REMATCH[5]}"
}

# Prints -1, 0 or 1 as $1 ranks below, level with or above $2. Build is ignored,
# and two prereleases of one core rank level, since prerelease tags are skipped.
semver_compare() {
  local a b i
  local -a x y
  a="$(semver_parse "$1")" || return 1
  b="$(semver_parse "$2")" || return 1
  read -ra x <<<"${a}"
  read -ra y <<<"${b}"
  for i in 0 1 2; do
    if ((x[i] > y[i])); then
      echo 1
      return
    elif ((x[i] < y[i])); then
      echo -1
      return
    fi
  done
  if [[ -z "${x[3]:-}" && -n "${y[3]:-}" ]]; then
    echo 1
  elif [[ -n "${x[3]:-}" && -z "${y[3]:-}" ]]; then
    echo -1
  else
    echo 0
  fi
}

# Prints the most significant part where $1 and $2 differ: major, minor, patch
# or prerelease, or nothing.
semver_diff() {
  local a b
  local -a x y
  a="$(semver_parse "$1")" || return 1
  b="$(semver_parse "$2")" || return 1
  read -ra x <<<"${a}"
  read -ra y <<<"${b}"
  if ((x[0] != y[0])); then
    echo major
  elif ((x[1] != y[1])); then
    echo minor
  elif ((x[2] != y[2])); then
    echo patch
  elif [[ "${x[3]:-}" != "${y[3]:-}" ]]; then
    echo prerelease
  fi
}

# Prints $2 with part $1 (major, minor or patch) bumped, the parts after it
# zeroed, and its prerelease and build dropped.
semver_bump() {
  local v
  local -a x
  v="$(semver_parse "$2")" || return 1
  read -ra x <<<"${v}"
  case "$1" in
    major) echo "$((x[0] + 1)).0.0" ;;
    minor) echo "${x[0]}.$((x[1] + 1)).0" ;;
    patch) echo "${x[0]}.${x[1]}.$((x[2] + 1))" ;;
    *) return 1 ;;
  esac
}

# The chart bump reads the tag as SemVer, and the build triggers on any v* tag.
if [[ "${VERSION}" != v* ]] || ! tag="$(semver_parse "${VERSION}")"; then
  echo "not a release version: ${VERSION}, want vMAJOR.MINOR.PATCH[-PRERELEASE]" >&2
  exit 1
fi
# Like the job's if, so a pre-release never reaches the chart.
read -r _ _ _ tag_prerelease <<<"${tag}"
if [[ -n "${tag_prerelease}" ]]; then
  echo ">> ${VERSION} is a pre-release, skipped"
  exit 0
fi

readonly CHART="charts/signoz-mcp-server"

# Rewrites version to $2 and appVersion to $3 in the Chart.yaml at $1, in place,
# and leaves the rest alone. yq would reindent every sequence in it.
version() {
  sed -E \
    -e "s|^version: .*|version: $2|" \
    -e "s|^appVersion: .*|appVersion: \"$3\"|" \
    "$1" > "$1.next"
  mv "$1.next" "$1"
  if ! grep -qx "version: $2" "$1" ||
    ! grep -qx "appVersion: \"$3\"" "$1"; then
    echo "could not set the version in $1" >&2
    exit 1
  fi
}

open_pr() {
  echo ">> Opening the pull request"
  local body="#### Chores

- Bump the \`signoz-mcp-server\` chart to ${CHART_VERSION}, with appVersion \`${VERSION}\`."
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

readonly BRANCH="release/signoz-mcp-server-${VERSION}"

echo ">> Cloning ${REPO}"
# Anonymously, so the token never reaches the fetch URL; only the push and the
# pull request need it.
git clone --quiet --depth 1 --branch "${BASE}" "https://github.com/${REPO}.git" "${WORK}"
git -C "${WORK}" remote set-url --push origin "https://x-access-token:${TOKEN}@github.com/${REPO}.git"

if [[ ! -f "${WORK}/${CHART}/Chart.yaml" ]]; then
  echo "not a signoz-mcp-server chart checkout: ${REPO}@${BASE}" >&2
  exit 1
fi

base_version="$(sed -nE 's/^version: *"?([^"]*)"?$/\1/p' "${WORK}/${CHART}/Chart.yaml")"
base_app="$(sed -nE 's/^appVersion: *"?([^"]*)"?$/\1/p' "${WORK}/${CHART}/Chart.yaml")"
if ! semver_parse "${base_version}" >/dev/null || ! semver_parse "${base_app}" >/dev/null; then
  echo "could not read version and appVersion from ${REPO}@${BASE}:${CHART}/Chart.yaml" >&2
  exit 1
fi

if [[ "$(semver_compare "${VERSION}" "${base_app}")" != "1" ]]; then
  echo ">> Chart appVersion ${base_app} is already at or past ${VERSION}, nothing to do"
  exit 0
fi

level="$(semver_diff "${VERSION}" "${base_app}")"
# A release after its own prerelease appVersion, set by hand, moves the chart a patch.
if [[ "${level}" == "prerelease" ]]; then
  level="patch"
fi
CHART_VERSION="$(semver_bump "${level}" "${base_version}")"
readonly CHART_VERSION

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

echo ">> Setting chart version ${base_version} to ${CHART_VERSION}, appVersion ${base_app} to ${VERSION}"
version "${WORK}/${CHART}/Chart.yaml" "${CHART_VERSION}" "${VERSION}"

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

open_pr
