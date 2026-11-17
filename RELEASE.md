# Releasing

## Versions

- The version is `.version` in [`manifest.json`](manifest.json) and [`server.json`](server.json), with no `v` prefix: `0.20.0`. `server.json` also pins the image tag the MCP Registry lists.
- The git tag and GitHub release are the version with a `v` prefix: `v0.20.0`.
- Only `vX.Y.Z` and `vX.Y.Z-<suffix>` tags build anything.
- A version with a suffix such as `-rc.N` is a pre-release. It builds the image, binaries and chart, but is not published to the MCP Registry.
- The `signoz-mcp-server` chart's `version` and `appVersion` in SigNoz/charts follow the release: `0.20.0` and `v0.20.0`.

## Steps

1. Run the [pre-release](.github/workflows/pre-release.yaml) workflow with the version, e.g. `0.20.0` or `0.20.0-rc.0`. It opens a `chore: pre-release v<version>` PR that sets the version in `manifest.json` and `server.json` and adds the `CHANGELOG.md` entry.
2. Review and merge that PR.
3. [Create the release](https://github.com/SigNoz/signoz-mcp-server/releases/new) in the GitHub UI: tag `v<version>` on the merge commit, title `v<version>`, generated release notes, and "Set as a pre-release" for a pre-release version. Publish it.
4. The tag runs two workflows:
   - [dockerbuildci](.github/workflows/dockerbuildci.yaml) pushes the image to Docker Hub and GCP, then publishes the committed `server.json` to the MCP Registry and runs [`scripts/chart.sh`](scripts/chart.sh), which opens a `chore(signoz-mcp-server): bump SigNoz MCP Server to v<version>` PR on SigNoz/charts. To redo either, re-run the workflow on the tag; both skip what is already done.
   - [binarybuildci](.github/workflows/binarybuildci.yaml) builds the linux and darwin binaries with GoReleaser and attaches their `tar.gz` archives and a checksums file to the release.
5. Review and merge the chart PR on SigNoz/charts.
6. The [post-release](.github/workflows/post-release.yaml) workflow runs on the published release. If `manifest.json`, `server.json` or `CHANGELOG.md` on `main` do not match the tag, it opens a `chore: post-release v<version>` PR to fix them; otherwise it does nothing.
