# Plan: E2E CI Suite

## Status
In Progress (PR-1 implemented, in review; PR-2 not started)

## Context

nerve-pod#219 asks for a full E2E GitHub Action for the MCP server. Today the repo has:

- Go `//go:build e2e` handler tests (~3.4k lines) that only run manually against staging with a session JWT — never in CI, so upstream contract drift is caught by users, not tests (violates the AGENTS.md cross-contract mandate).
- A protocol lane (`mcp-protocol.yaml`) that runs the real binary against a **fake** backend (`example.invalid`) — protocol shape only, no data path.
- No provisioned SigNoz anywhere in CI.

The org has converged on one pattern for this (signoz backend, terraform-provider, signoz-operator): a pytest + uv harness under `tests/` that provisions a real, ephemeral SigNoz, runs behaviour tests against it, and is gated in CI by the dual-event `safe-to-test` fork gate. We follow that pattern, closest to terraform-provider-signoz (Docker Compose cast, no k8s needed).

## Approach

Delivered as **two stacked PRs** (user decision, 2026-08-30):

- **PR-1** — branch `nerve-pod/issues/219`, base `main`: the foundry-based harness, CI workflow, Makefile targets, docs, and a minimal smoke suite so CI exercises the harness end-to-end. The Go e2e files stay untouched in PR-1 (still manual/staging-only).
- **PR-2** — branch `nerve-pod/issues/219-port`, base `nerve-pod/issues/219`: ports the Go `//go:build e2e` families to Python on the harness and **removes the Go e2e files** (one suite, one transport-level path; PR-2's description notes any handler-level assertions that don't translate).

### PR-1: `tests/` pytest harness (new, uv-managed)

Layout (operator/terraform conventions):

```
tests/
├── pyproject.toml            # pytest + requests; ruff as dev dep; --import-mode=importlib, log_cli
├── conftest.py               # pytest_plugins registration + CLI flags
├── casting.yaml              # foundry Installation: docker/compose, sqlite, root user admin@e2e.test, stats off
├── .gitignore                # .venv/, .pytest_cache/, pours/, tmp/, casting.yaml.lock
├── README.md                 # how to run locally
├── fixtures/
│   ├── commander.py          # frozen-dataclass subprocess wrapper (from operator)
│   ├── foundry.py            # cast/teardown + two-phase readiness (port, then login)
│   ├── signoz.py             # session: login as root → optional license → service account + API key
│   ├── mcpserver.py          # docker build Dockerfile.e2e; run container; docker-assigned free port; poll /readyz; stop
│   ├── mcpclient.py          # official Python MCP SDK client, sync facade (background event loop)
│   ├── telemetry.py          # OTLP HTTP seeding (traces/logs/metrics) + poll-until-visible helpers
│   └── reuse.py              # --reuse/--teardown pytest-cache wrapper
└── e2e/
    ├── bootstrap/setup.py    # test_setup / test_teardown entrypoints
    └── tests/                # PR-1 smoke set only; PR-2 adds the ported suites
        ├── test_protocol.py  # initialize + tools/list against the live-backed server
        ├── test_logs.py      # seed logs via OTLP → signoz_search_logs finds them
        └── test_dashboards.py# dashboard create/get/delete round-trip through MCP tools
```

Principles (from the reference suites):

- **One command contract**: CI and developers both run `make test-e2e`; all orchestration lives in the suite.
- **Config via CLI flags** (`--reuse`, `--teardown`, `--foundry-binary-path`, `--license-key`), not env vars.
- **All fixtures live in `tests/fixtures/`** — including per-test slug/cleanup helpers — registered through the root `tests/conftest.py` `pytest_plugins`. No suite-local `conftest.py` with function-scoped fixtures.
- **Session-scoped stack** (cast SigNoz + built MCP server), function-scoped seeds with cleanup; unique `mcp-e2e-<slug>-<rand>` naming; every created resource deleted and confirmed gone (mirrors the AGENTS.md live-verification rules).
- **Throwaway credentials** hardcoded in `casting.yaml`; the API key is minted per session as a service-account key.
- **No secrets required**: license key optional (empty = skip).
- **No Go changes to production code.** No guardrail policy changes.
- **No stdio-transport suite** in the first iteration (user, 2026-08-30); HTTP transport only.

### PR-1: CI workflow `.github/workflows/e2e.yaml` (new)

- Triggers: `pull_request` (opened/synchronize/reopened/labeled) + `pull_request_target: labeled` + `workflow_dispatch`. No `safe-to-e2e` cost label and no cron in v1: the job runs on **every non-fork PR** (user decision, 2026-08-30); per-PR cadence provides the upstream-drift signal.
- Job `lint`: ruff format/check on `tests/` (uv).
- Job `e2e`: gated by exactly the ci.yaml dual-event fork-gate expression (non-fork, non-dependabot `pull_request`, or `pull_request_target` with `safe-to-test`) — so fork PRs only run after maintainer labeling, and secrets never reach unlabeled forks. `permissions: contents: read`, `timeout-minutes: 45`.
- Steps: checkout PR head → setup-go (go.mod) → setup-python 3.13 → setup-uv → install foundryctl (pinned via `FOUNDRY_VERSION` env at workflow top; overridable on dispatch) → **setup** (`make setup-e2e-env`; passes `--license-key` only when `secrets.PRIMUS_LICENSE_KEY` is set — an empty flag broke the first CI run) → **run** (`make test-e2e-reuse`) → **teardown** (`if: always()`; `make cleanup-test-e2e` plus a pours/compose fallback for a cast orphaned by a failed setup).
- `actionlint` the new workflow before handoff (guardrails README workflow-lint requirement).

### PR-1: Make + docs

- Makefile: `test-e2e`, `test-e2e-reuse`, `setup-e2e-env`, `cleanup-test-e2e` (plain `uv run pytest` invocations; this repo does not use primus make includes).
- `tests/README.md`: local run instructions (Docker, foundryctl, Go, uv; reuse loop).
- `README.md`: short "Running the e2e suite" pointer.
- `AGENTS.md`: extend Local Verification with the e2e commands.

### PR-2: port the Go e2e families to Python

The Go files were named after tracker batches (#363–#367), not concerns. The port re-groups by behavior into semantically named suites (user, 2026-08-30). Mapping — source Go tests → target modules under `tests/e2e/tests/`:

| Target Python suite | Behavior covered | Source Go tests (deleted) |
|---|---|---|
| `test_query_response_paths.py` | Upstream QB/response JSON-path drift: search_logs/search_traces row paths, list_metrics/top_metrics paths, alert_history path; execute_builder_query success + warning-note path; list tools succeed | Family A N1–N5 (`e2e_familya_test.go`) |
| `test_param_coercion.py` | Tolerant input handling: stepInterval number/string, booleans as bool/legacy string (+ garbage rejected), timestamp auto-detect (ns/ms), docs limit number/string + list clamp | Family A N2–N3; Family E K1, K3 |
| `test_param_validation.py` | Canonical validation error strings; requestType validation; trace-timestamp parameter error; nil-arguments validation error | Family B (validation half); Family E K4; `nil_arguments_e2e_test.go` |
| `test_output_envelopes.py` | structuredContent on list/get tools, absent on raw QB passthrough; JSON-first query_metrics; error-code taxonomy; mutation envelopes | Family C (`e2e_familyc_test.go`) |
| `test_enums_and_grammar.py` | requestType/signal/alert-history enum values; aggregation set matches backend; timeRange/stepInterval grammar; search_docs param + alias; service top-operations tags | Family D (`e2e_familyd_test.go`) |
| `test_notification_channels.py` | Bad-webhook create → success + warning note → delete; normal create/verify/delete lifecycle | Family A N6 |
| `test_saved_views.py` | View CRUD round-trip (clone existing, unique name, read via id + legacy viewId, delete via legacy key, confirm gone) | Family E K5 CRUD |
| `test_get_by_id_aliases.py` | get_alert by id/ruleId, get_dashboard by id/uuid | Family E K5 reads |
| `test_trace_fields.py` | Trace field snake_case migration | `e2e_trace_fields_test.go` |
| `test_org_overview.py` | Org overview tool against live instance | `e2e_org_overview_test.go` |
| `test_docs.py` | Docs agent flow: search_docs, fetch_doc happy path, out-of-scope URL coded error, sitemap resource (embedded corpus — no backend data needed) | `e2e_docs_test.go::TestE2EDocsAgentFlow` |
| `test_upstream_errors.py` | Upstream error classification/prefix; invalid API key → coded auth error (401 propagation per AGENTS.md) | Family B (upstream half); transport-visible part of `TestE2EAuthFailureTelemetry` |

- Port through the MCP transport (initialize → tools/call), not in-process handler calls.
- In-process-only assertions do not port: `TestE2EAuthFailureTelemetry`'s OTel span-emission check becomes an untagged Go unit test or is dropped — the choice and justification go in the PR-2 description.
- Staging-assumption triage happens during the port: anything that depended on staging data seeds its own fixtures via `telemetry.py` or the SigNoz API.
- After the port, the Go e2e files and the now-unused `e2e` build-tag helpers are removed in the same PR-2.

## Files to Modify

**PR-1:**
- `tests/**` — new harness + smoke suite (all new files)
- `Dockerfile.e2e`, `.dockerignore` — the image the suite builds and runs the server from
- `.github/workflows/e2e.yaml` — new workflow
- `Makefile` — add e2e targets
- `README.md`, `CLAUDE.md` — documentation pointers (CLAUDE.md carries the content AGENTS.md symlinks to)
- `plans/e2e-ci-suite.{context,plan}.md` — this file pair

**PR-2:**
- `tests/e2e/tests/` — new ports: `test_query_response_paths.py`, `test_param_coercion.py`, `test_param_validation.py`, `test_output_envelopes.py`, `test_enums_and_grammar.py`, `test_notification_channels.py`, `test_saved_views.py`, `test_get_by_id_aliases.py`, `test_trace_fields.py`, `test_org_overview.py`, `test_docs.py`, `test_upstream_errors.py`
- `internal/handler/tools/e2e_*.go`, `internal/mcp-server/{e2e_docs_test.go, nil_arguments_e2e_test.go}` — deleted after port (any surviving in-process assertion re-lands as an untagged Go unit test)

## Verification

1. Local: `cd tests && uv sync && uv run pytest e2e/bootstrap/setup.py --reuse` brings up the stack; `uv run pytest e2e/tests --reuse` runs suites; `--teardown` cleans up. Full cold run target ≤ ~15 min on a runner.
2. `go test ./...`, `go build ./cmd/server`, `make fmt goimports` stay green (no production changes expected).
3. `actionlint .github/workflows/e2e.yaml`; confirm guardrail suite untouched (`go test -count=1 -run '^TestGuardrail_' ./...`).
4. CI on PR-1: e2e job runs automatically on the PR — provisions SigNoz, seeds telemetry, passes the smoke suite, tears down on success and failure.
5. CI on PR-2 (stacked): ported families pass; coverage parity with the deleted Go files is reviewed in the PR-2 description.
6. Confirm a fork PR runs only after a maintainer applies `safe-to-test` (privileged `pull_request_target` path).
7. PR summaries note: no MCP contract change (no agent-skills companion needed), doc updates included.
