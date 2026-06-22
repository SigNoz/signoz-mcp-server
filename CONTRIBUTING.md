# Contributing to SigNoz MCP Server

Thanks for contributing to SigNoz MCP Server.

## Development workflow

1. Fork the repository and create a feature branch.
2. Make your changes with tests where applicable.
3. Run local checks before opening a PR.
4. Open a pull request with a clear summary and validation steps.

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
