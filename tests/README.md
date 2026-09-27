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

Internal PRs use the shared Primus jobs and `.github/workflows/e2e.yaml`.
Internal and manual E2E runs can use the optional license. Fork and Dependabot
PRs require a maintainer with repository write access to add `safe-to-test`.

`.github/workflows/fork-approval.yaml` runs from the trusted default branch on
`pull_request_target`. It only edits GitHub metadata: it records pending commit
statuses and dispatches `.github/workflows/fork-ci.yaml` from the default branch
for the exact approved head SHA. It never checks out PR code. New commits,
reopening, marking ready, or removing `safe-to-test` invalidate approval. Other
label changes leave the existing approval and results alone.

The dispatcher waits up to 30 seconds for GitHub to compute a mergeable test
revision. It leaves the head gate pending and fails without dispatching if the
merge revision is unavailable. Resolve conflicts or retry the approval workflow
after GitHub finishes computing it; head-only approval is rejected.

The dispatched worker rechecks the approval, then runs `make ci`, the ready-plan
check when applicable, and live E2E for both the approved head and its pinned merge
revision on separate GitHub-hosted runners. Code jobs
receive only a read-only token, no repository secrets or persisted Git credentials,
and no cache access. E2E uses community SigNoz. A separate metadata-only reporter
publishes results on the approved SHA, preserving the required check names. A
newer approval or reset prevents an old worker from publishing stale results.
A failed run can be retried while its approval remains current.

The existing required `contract` status also records approval identity. Resets
invalidate that gate first, starting with the merge revision GitHub evaluates,
and reporters restore it last, only after both test suites pass. Later API errors
therefore leave the gate blocked. Older events cannot overwrite a newer approval,
even when GitHub schedules concurrency groups out of order. Both metadata jobs
use `queue: max` so a later reporter cannot replace a pending reset; GitHub permits
up to 100 pending jobs in this queue.
Before removing a label, a reset also checks for a newer approval workflow run
for the same PR, head, and draft state. This preserves a re-applied label while
its approval is still queued and has not written its status marker yet.

If `main` advances during a run, results still apply to the frozen revisions that
were tested; the newly computed merge commit receives no success from that run.
The repository's existing strict up-to-date rule requires updating the branch
before merging. That head change triggers the usual fresh-approval requirement.

Pending commit statuses keep the required checks blocked even if a contributor
edits a PR workflow to report successful or skipped jobs. GitHub's own fork-run
approval settings govern arbitrary contributor-added workflows; the label policy
controls this repository's validation and trusted results.

`make check-fork-ci` tests the dispatcher, approval validation, and reporter with
mock GitHub APIs. `actionlint` v1.7.12 does not yet recognize GitHub's documented
`cache-mode` and concurrency `queue` keys. Exclude only those schema diagnostics:

```bash
actionlint \
  -ignore '^unexpected key "cache-mode" for "workflow" section' \
  -ignore '^unexpected key "queue" for "concurrency" section' \
  .github/workflows/fork-approval.yaml .github/workflows/fork-ci.yaml
```

Keep `cache-mode: none`: disabling cache actions alone does not revoke a job's
cache token. Keep `queue: max` on both jobs sharing the status-write lock. See
[GitHub's concurrency queue](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#concurrency)
and [GitHub's cache access controls](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching#controlling-cache-access-with-cache-mode).

The workflow repair must reach `main` before the dispatcher and worker can run.
Then refresh fork PRs against `main` and add `safe-to-test`. Rerunning an old
`pull_request_target` failure continues using its old workflow definition.
