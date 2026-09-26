# Contributing to SigNoz MCP Server

Thanks for contributing to SigNoz MCP Server.

## Development workflow

1. Fork the repository and create a feature branch.
2. Make your changes with tests where applicable.
3. Run local checks before opening a PR.
4. Open a pull request with a clear summary and validation steps.

## Releases & the MCP Registry

The server is listed on the official [MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.SigNoz/signoz-mcp-server`. The version that gets published lives in the committed
`server.json`, kept correct by the release process:

1. Run the **prereleaser** (`.github/workflows/pre-release.yaml`, manual dispatch). It raises a PR
   bumping `manifest.json`, `CHANGELOG.md`, and `server.json` — both `.version` and the pinned OCI
   image tag (`docker.io/signoz/signoz-mcp-server:vX.Y.Z`).
2. Merge that PR, then create the GitHub release on the bumped commit so the tag carries the correct
   `server.json`.
3. The `vX.Y.Z` tag triggers `.github/workflows/dockerbuildci.yaml`: it builds and pushes the Docker
   image, then the `publish-mcp-registry` job publishes the **committed** `server.json` to the
   registry via GitHub OIDC (no secret). It asserts the file matches the tag, waits for the image to
   be pullable, and is idempotent (skips an already-published version). Pre-release tags (`-rc.N`)
   are not published.

To publish out of band, re-run the `dockerbuildci` workflow on the release tag, or — from a
workstation checked out at the tagged commit — run `mcp-publisher login github` (as a SigNoz org
member) followed by `mcp-publisher publish`.

## Required sync for MCP changes

If your PR adds, removes, or renames MCP tools/resources/config behavior, update docs and metadata in the same PR:

- `README.md` (tool list and parameter references)
- `manifest.json` (`tools` names/descriptions)
- Relevant files in `docs/` when user-facing behavior changes

This prevents drift between implementation, manifest metadata, and public docs.

## Suggested validation

Run `make ci` before pushing. It runs everything the PR gate runs except the live e2e suite, and each step is also its own target (see the `CI` section of the `Makefile`). For a quicker loop, run what is relevant for your change:

```bash
go test ./...
```

For documentation-only changes, at minimum run `make check-repo-docs`, which validates plans and flags stale repo paths in docs. Mention what was validated in the PR.

## Fuzz verification

Four targets check schema normalization, URL enrichment, Query Builder field
preservation, and timestamp conversion without credentials or live SigNoz requests.

```bash
GOTOOLCHAIN=go1.26.0 make test-fuzz       # 10 seconds per target; included in make ci
GOTOOLCHAIN=go1.26.0 make test-fuzz-long  # 5 minutes per target; local use
```

Requires GNU `timeout` (`brew install coreutils` on macOS). Use `FUZZ_TARGET` to
select one target, or override `FUZZ_TIME`, `FUZZ_LONG_TIME`, and `FUZZ_PARALLEL`.

Every PR runs all four targets in one job at 30 seconds each. The entire job has a
five-minute timeout, including setup and artifact upload; GitHub queue time is
separate. Manual dispatch uses the same budget. There is no scheduled run.
Use `make ci FUZZ_TIME=30s` to match the PR fuzzing budget.

Logs and replay commands are saved under `.fuzz-artifacts/` and retained as CI
artifacts for 14 days. To reproduce a CI failure, restore its package-relative
`testdata/fuzz` files and run the command in `replay.txt`. Commit minimized failure
inputs with their fixes; ordinary `go test` reruns them as regression tests.

## Testing across external contracts

This server depends on external parties — it consumes the SigNoz backend / query-builder (QB) API (upstream) and produces tool outputs that MCP clients and custom agents and clients consume (downstream). Fixture-based unit tests only prove our code matches our *assumption* of those contracts; they do not catch the contract drifting out from under us (a renamed field, a changed QB response envelope, a new output shape). When you parse an upstream response or shape a tool output:

- **Pin the contract, and test against reality where you can.** Beyond fixture unit tests, add a periodic/integration test against a live instance (or a recorded real response) so upstream drift fails a test, not a user.
- **When tests can't catch it, observability must.** If a break only manifests against real data, add a metric or WARN log that fires when the contract appears violated (e.g. a passthrough that found rows but could not locate the expected field), so silent degradation is detectable in production.
- **Fail open, but never fail silent.** Pair every fail-open cross-boundary parse with a detectable signal.

## Pull request checklist

- [ ] Code/tests updated as needed
- [ ] README/docs updated for user-facing changes
- [ ] `manifest.json` updated for MCP tool metadata changes
- [ ] Validation commands and results included in PR description
