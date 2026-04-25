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

## Regenerating OpenAPI-derived tools

Auto-generated tools live under `internal/handler/tools/zz_generated_*.go`, `pkg/types/gentools/zz_generated_*.go`, and the `tools` array of `manifest.json`. Regenerate them after the SigNoz OpenAPI spec changes:

```bash
SIGNOZ_SPEC=../signoz/docs/api/openapi.yml make gen
```

CI should run `make gen-check` to verify the generated output is up to date. The generator is deterministic — two runs against the same spec produce byte-identical output.

## Suggested validation

Run what is relevant for your change:

```bash
go test ./...
```

For documentation-only changes, at minimum ensure formatting and links are sensible, and run `go test ./...` when the local environment allows it. Mention what was validated in the PR.

## Pull request checklist

- [ ] Code/tests updated as needed
- [ ] README/docs updated for user-facing changes
- [ ] `manifest.json` updated for MCP tool metadata changes
- [ ] If the SigNoz OpenAPI spec moved, ran `make gen` and committed the result
- [ ] Validation commands and results included in PR description
