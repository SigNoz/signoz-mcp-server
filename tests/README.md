# SigNoz MCP Server — E2E Tests

End-to-end tests that run the MCP server (built from the working tree) against a
real, ephemeral SigNoz instance provisioned by [foundry](https://github.com/SigNoz/foundry)
(Docker Compose), and drive it over the MCP HTTP transport.

## Requirements

- Docker
- `foundryctl` on `PATH` (or pass `--foundry-binary-path`)
- Go (to build the server under test)
- Python ≥ 3.11 and [uv](https://docs.astral.sh/uv/)

## Running

```sh
# Full run: cast SigNoz, build the server, run the suites, tear everything down.
make test-e2e

# Keep the SigNoz environment for the next run.
make setup-e2e-env

# Fast reruns against the kept environment.
make test-e2e-reuse

# Tear down the kept environment.
make cleanup-test-e2e
```

Equivalent direct invocation:

```sh
cd tests
uv sync
uv run pytest --basetemp=./tmp/ e2e/tests
```

Narrow to a file or a single test the usual way:

```sh
uv run pytest --basetemp=./tmp/ e2e/tests/test_logs.py::test_seeded_logs_are_searchable
```

## How it works

- `casting.yaml` describes the SigNoz installation foundry renders (Docker
  Compose, sqlite metastore, a throwaway root admin, stats reporting disabled).
- The session-scoped `signoz` fixture casts the stack, logs in as root,
  optionally applies a license (`--license-key`), and mints a service-account
  API key. Session teardown runs `docker compose down -v`.
- The session-scoped `mcp_server` fixture builds `./cmd/server` and starts it
  with `TRANSPORT_MODE=http`, `SIGNOZ_URL`, and `SIGNOZ_API_KEY` pointing at the
  cast instance, then waits for `/readyz`.
- Tests talk to the server through a thin JSON-RPC client
  (`fixtures/mcpclient.py`) and to SigNoz directly (`SigNoz.api`) for setup and
  verification. Telemetry is seeded over OTLP/HTTP (`fixtures/telemetry.py`).
- Every resource a test creates uses a unique `mcp-e2e-<slug>` name and is
  deleted before the test returns.

All fixtures live in `fixtures/` and are registered via the root `conftest.py`
`pytest_plugins`. Configuration flows through pytest CLI flags, not environment
variables: `--reuse`, `--teardown`, `--foundry-binary-path`, `--go-binary-path`,
`--license-key`.

## CI

`.github/workflows/e2e.yaml` runs this suite on every non-fork pull request
(fork PRs run only after a maintainer applies `safe-to-test`) and on
`workflow_dispatch`, calling the same `make test-e2e` entrypoint.
