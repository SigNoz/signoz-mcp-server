# SigNoz MCP Server — E2E Tests

End-to-end tests that run the MCP server (built from the working tree) against a
real, ephemeral SigNoz instance provisioned by [foundry](https://github.com/SigNoz/foundry)
(Docker Compose), and drive it over the MCP HTTP transport.

## Requirements

- Docker (builds `Dockerfile.e2e` and runs the server container)
- `foundryctl` on `PATH` (or pass `--foundry-binary-path`)
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
- The session-scoped `mcp_server` fixture builds `Dockerfile.e2e` from the
  working tree and runs the server as a container with `TRANSPORT_MODE=http`,
  reaching the cast SigNoz through `host.docker.internal`. Its MCP port is
  published to a docker-assigned free host port (docker-py's
  `client.api.port`, the same mechanism testcontainers' `get_exposed_port`
  wraps in the signoz repo tests), then waits for `/readyz`.
- Tests talk to the server through the official Python MCP SDK
  (`fixtures/mcpclient.py` wraps it in a sync facade over a background event
  loop) and to SigNoz directly (`SigNoz.api`) for setup and verification.
  Telemetry is seeded over OTLP/HTTP (`fixtures/telemetry.py`).
- Every resource a test creates uses a unique `mcp-e2e-<slug>` name and is
  deleted before the test returns.

All fixtures live in `fixtures/` and are registered via the root `conftest.py`
`pytest_plugins`. Configuration flows through pytest CLI flags, not environment
variables: `--reuse`, `--teardown`, `--foundry-binary-path`, `--license-key`.

## Suites

- `test_protocol.py` — initialize handshake, live-backed tools/list, and the
  nil-arguments validation path.
- `test_query_response_paths.py` — upstream QB/response JSON-path drift checks
  (row paths, completeness notes, execute_builder_query).
- `test_param_coercion.py` — tolerant inputs: numbers/booleans as strings,
  timestamp magnitude auto-detect, limit clamp.
- `test_param_validation.py` — canonical validation strings, trace-timestamp
  parameter error, requestType rejection codes.
- `test_output_envelopes.py` — structuredContent presence/absence, JSON-first
  query_metrics, error-code taxonomy, mutation envelope.
- `test_enums_and_grammar.py` — enum values, advertised aggregation set vs the
  backend, timeRange/stepInterval grammar, docs param + alias, top-operations tags.
- `test_notification_channels.py` — canonical notification v2 lifecycle for every
  provider with `test: false`, config round-trip, config-free listing, an opt-in
  webhook test sink, and confirmed deletion. The sink runs on the foundry-generated
  Docker network and exposes captured requests only on host loopback; no external
  destination is contacted.
- `test_saved_views.py` — view CRUD round-trip cloned from a seeded source view.
- `test_get_by_id_aliases.py` — canonical id and legacy alias (ruleId/uuid) reads.
- `test_trace_fields.py` — snake_case trace fields, filters, aggregations.
- `test_org_overview.py` — org overview conservation vs `GET /api/v1/stats`.
- `test_docs.py` — docs search/fetch, out-of-scope coded error, sitemap resource.
- `test_upstream_errors.py` — uniform upstream error prefix; rejected-credential
  coded error.
- `test_logs.py` — seeded log search, explicit scoped/unscoped search grammar,
  quoted terms, body-only legacy search text, and upstream warning preservation.
- `test_dashboards.py` — TextPanel create/get/update/patch/default/layout
  lifecycle, cleanup verification, and read-only system dashboard behavior.

## CI

`.github/workflows/e2e.yaml` runs this suite on internal pull requests and on
`workflow_dispatch`. All checks on fork and Dependabot PRs, including E2E, wait
for a maintainer to add `safe-to-test`. That label event approves its exact head
commit. New commits, reopening a PR, or marking it ready for review require fresh
approval; an existing label does not start checks on those events. These runs use
the ordinary `pull_request` event, read-only permissions, and community SigNoz
without repository secrets. Internal and manual runs can use the optional license.
The workflow calls `make setup-e2e-env`, `make test-e2e-reuse`, and
`make cleanup-test-e2e` to keep setup, test results, and teardown in separate steps.

`.github/workflows/ci.yaml` runs `make ci` for approved fork and Dependabot PRs
without repository secrets. The separate formatting, repository-docs, guardrail,
and protocol jobs require the same approval. The repository-docs job reports
missing approval before checking out any code, so a skipped check cannot bypass
ready-for-review plan validation. Internal PRs retain the shared Primus jobs.

`.github/workflows/fork-approval.yaml` removes stale approval labels. It uses
`pull_request_target` only to edit PR labels and never checks out or executes PR
code. If the label has not yet been removed, a maintainer can remove and reapply it.

The workflow repair must reach `main` before label events stop invoking the old
test workflows and the approval-reset workflow takes effect. GitHub reads
`pull_request_target` workflows from the default branch, even when a pull request
changes those files.
