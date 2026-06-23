# Contributing to SigNoz MCP Server

Thanks for contributing to SigNoz MCP Server.

## Development workflow

1. Fork the repository and create a feature branch.
2. Make your changes with tests where applicable.
3. Run local checks before opening a PR.
4. Open a pull request with a clear summary and validation steps.

## Releases & the MCP Registry

The server is listed on the official [MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.SigNoz/signoz-mcp-server`. Publishing is automated: on every stable `vX.Y.Z` tag,
`.github/workflows/dockerbuildci.yaml` builds and pushes the Docker image, then the
`publish-mcp-registry` job pins `server.json` to the released version and immutable image tag and
publishes it to the registry via GitHub OIDC (no secret required). Pre-release tags (`-rc.N`) are
skipped, and the job is idempotent — a re-run for an already-published version is a no-op.

To publish out of band, prefer **re-running the `dockerbuildci` workflow on the release tag** — it
does the version/image-tag pinning for you. If you must publish from a workstation, pin the package
to the immutable image tag first (the committed `server.json` carries a mutable `:latest`):

```bash
TAG=v0.5.1   # the release tag
jq --arg v "${TAG#v}" --arg id "docker.io/signoz/signoz-mcp-server:${TAG}" \
   '.version = $v | .packages[0].identifier = $id' server.json > server.tmp && mv server.tmp server.json
mcp-publisher login github   # as a SigNoz org member
mcp-publisher publish
```

## Required sync for MCP changes

If your PR adds, removes, or renames MCP tools/resources/config behavior, update docs and metadata in the same PR:

- `README.md` (tool list and parameter references)
- `manifest.json` (`tools` names/descriptions)
- Relevant files in `docs/` when user-facing behavior changes

This prevents drift between implementation, manifest metadata, and public docs.

## Suggested validation

Run what is relevant for your change:

```bash
go test ./...
```

For documentation-only changes, at minimum ensure formatting and links are sensible, and run `go test ./...` when the local environment allows it. Mention what was validated in the PR.

## Testing across external contracts

This server depends on external parties — it consumes the SigNoz backend / query-builder (QB) API (upstream) and produces tool outputs that MCP clients and the AI assistant consume (downstream). Fixture-based unit tests only prove our code matches our *assumption* of those contracts; they do not catch the contract drifting out from under us (a renamed field, a changed QB response envelope, a new output shape). When you parse an upstream response or shape a tool output:

- **Pin the contract, and test against reality where you can.** Beyond fixture unit tests, add a periodic/integration test against a live instance (or a recorded real response) so upstream drift fails a test, not a user.
- **When tests can't catch it, observability must.** If a break only manifests against real data, add a metric or WARN log that fires when the contract appears violated (e.g. a passthrough that found rows but could not locate the expected field), so silent degradation is detectable in production.
- **Fail open, but never fail silent.** Pair every fail-open cross-boundary parse with a detectable signal.

## Pull request checklist

- [ ] Code/tests updated as needed
- [ ] README/docs updated for user-facing changes
- [ ] `manifest.json` updated for MCP tool metadata changes
- [ ] Validation commands and results included in PR description
