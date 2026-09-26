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

Run the short campaign after changes to schemas, response enrichment, Query Builder
payloads, or timestamps:

```bash
GOTOOLCHAIN=go1.26.0 make test-fuzz
```

This runs four native Go fuzz targets for 10 seconds each, with two workers. Each
process has a two-minute deadline, including compilation, with a five-second kill
grace period. Failure minimization is limited to five seconds per attempt. GNU `timeout` is required
(`brew install coreutils` on macOS). A cold build may need the longer command.

| Target | User-visible failure it guards against | Seed sources |
| --- | --- | --- |
| `FuzzSchemaNormalization` | Invalid advertised JSON, changed property defaults, or unnormalized boolean schemas break client tool discovery. | Schema compatibility regressions and generated alert/dashboard input schemas. |
| `FuzzWebURLEnrichment` | Adding a link rounds large durations, drops fields, or loses partial rows. | Large-integer enrichment regressions and the existing raw trace response fixture. |
| `FuzzQueryPayloadRoundTrip` | Typed decoding drops a query expression, source, cursor, or authored bounds before forwarding upstream. | PromQL, SQL, formula, builder, and trace-operator round-trip regressions. |
| `FuzzExplicitTimestampUnits` | Mixed epoch units or overflow silently change the query window. | Existing conversion regressions, unit-band edges, and saturation boundaries. |

These are local invariants, not proof that a fabricated query executes in SigNoz.
The response fixture models the upstream shape; it is not a newly captured live
response. Existing live E2E coverage remains the check against upstream drift.
The targets require no credentials or services, use no shared mutable state, and
exercise explicit timestamps so expectations do not depend on the clock. Byte and
string inputs are capped at 64 KiB per case to keep worker memory use bounded.

For a longer run, or to focus on one target:

```bash
GOTOOLCHAIN=go1.26.0 make test-fuzz-long
FUZZ_TARGET=FuzzWebURLEnrichment GOTOOLCHAIN=go1.26.0 make test-fuzz
```

The long command spends five minutes per target with an eight-minute process limit.
`FUZZ_TIME`, `FUZZ_LONG_TIME`, and `FUZZ_PARALLEL` are Make overrides; keep the time
budgets below the process limits. The `fuzz` workflow runs the long campaign weekly
and on manual dispatch, saving logs and failure inputs as artifacts for 14 days.
Fresh-input campaigns are separate from PR gates. `go test -count=1 ./...` and
`make ci` run the seed corpus deterministically, including saved regressions.

Every local campaign prints a unique directory under `.fuzz-artifacts/` containing
the Go version, commit, working-tree status, settings, and target logs. When Go
finds a failure, it writes the input into the package's `testdata/fuzz` corpus.
The runner copies it into the artifact directory and writes exact replay commands
to `replay.txt`. Go reproduces a failure from this saved input and its hash, not
from a numeric seed or the original mutation schedule.

To replay a CI failure, copy the artifact's package-relative `testdata/fuzz`
directory back into the checkout and run its `replay.txt` command. Diagnose the
failure, fix the behavior, and retain the minimized input as a committed regression.
Do not commit the generated coverage cache. A timeout or build failure may have
no replay input; seed failures identify an existing case in the target log.
Minimize any unminimized crash input before committing it. Add a new target only
when it protects a distinct, credible failure that existing coverage does not exercise.

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
