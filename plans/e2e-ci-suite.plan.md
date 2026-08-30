# Plan: E2E CI Suite

## Status
Planning

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
│   ├── mcpserver.py          # go build ./cmd/server; start TRANSPORT_MODE=http; poll /readyz; stop
│   ├── mcpclient.py          # thin JSON-RPC-over-HTTP client (initialize/tools/list/tools/call)
│   ├── telemetry.py          # OTLP HTTP seeding (traces/logs/metrics) + poll-until-visible helpers
│   └── reuse.py              # --reuse/--teardown pytest-cache wrapper
└── e2e/
    ├── bootstrap/setup.py    # test_setup / test_teardown entrypoints
    └── tests/                # PR-1 smoke set only; PR-2 adds the ported families
        ├── conftest.py       # function-scoped fixtures: test_id slugs, per-test cleanup
        ├── test_protocol.py  # initialize + tools/list against the live-backed server
        ├── test_logs.py      # seed logs via OTLP → signoz_search_logs finds them
        ├── test_dashboards.py# dashboard create/get/delete round-trip through MCP tools
        └── test_stdio.py     # stdio transport smoke against the real backend
```

Principles (from the reference suites):

- **One command contract**: CI and developers both run `make test-e2e`; all orchestration lives in the suite.
- **Config via CLI flags** (`--reuse`, `--teardown`, `--foundry-binary-path`, `--license-key`), not env vars.
- **Session-scoped stack** (cast SigNoz + built MCP server), function-scoped seeds with cleanup; unique `mcp-e2e-<slug>-<rand>` naming; every created resource deleted and confirmed gone (mirrors the AGENTS.md live-verification rules).
- **Throwaway credentials** hardcoded in `casting.yaml`; the API key is minted per session as a service-account key.
- **No secrets required**: license key optional (empty = skip).
- **No Go changes to production code.** No guardrail policy changes.

### PR-1: CI workflow `.github/workflows/e2e.yaml` (new)

- Triggers: `pull_request` (opened/synchronize/reopened/labeled) + `pull_request_target: labeled` + `workflow_dispatch`. No `safe-to-e2e` cost label and no cron in v1: the job runs on **every non-fork PR** (user decision, 2026-08-30); per-PR cadence provides the upstream-drift signal.
- Job `lint`: ruff format/check on `tests/` (uv).
- Job `e2e`: gated by exactly the ci.yaml dual-event fork-gate expression (non-fork, non-dependabot `pull_request`, or `pull_request_target` with `safe-to-test`) — so fork PRs only run after maintainer labeling, and secrets never reach unlabeled forks. `permissions: contents: read`, `timeout-minutes: 45`.
- Steps: checkout PR head → setup-go (go.mod) → setup-python 3.13 → setup-uv → install foundryctl (pinned via `FOUNDRY_VERSION` env at workflow top; overridable on dispatch) → `make test-e2e` (passes `--license-key "${{ secrets.PRIMUS_LICENSE_KEY }}"`, empty when unset) → `if: always()` teardown via `make cleanup-test-e2e`.
- `actionlint` the new workflow before handoff (guardrails README workflow-lint requirement).

### PR-1: Make + docs

- Makefile: `test-e2e`, `test-e2e-reuse`, `setup-e2e-env`, `cleanup-test-e2e` (plain `uv run pytest` invocations; this repo does not use primus make includes).
- `tests/README.md`: local run instructions (Docker, foundryctl, Go, uv; reuse loop).
- `README.md`: short "Running the e2e suite" pointer.
- `AGENTS.md`: extend Local Verification with the e2e commands.

### PR-2: port the Go e2e families to Python

Source inventory (~3.4k lines) and target modules under `tests/e2e/tests/`:

| Go file (deleted) | Covers | Python module |
|---|---|---|
| `e2e_familya_test.go` | silent-failure fixes (#363); N4 row-count paths; completeness notes | `test_family_a.py` |
| `e2e_familyb_test.go` | error/validation flows, canonical strings, upstream error envelope (#364) | `test_family_b.py` |
| `e2e_familyc_test.go` | output envelopes: structuredContent, JSON-first query_metrics, error codes | `test_family_c.py` |
| `e2e_familyd_test.go` | param schema, types & descriptions (#367) | `test_family_d.py` |
| `e2e_familye_test.go` | parameter changes: timestamps, limit, requestType, id rename (#366) | `test_family_e.py` |
| `e2e_trace_fields_test.go` | trace field snake_case migration | `test_trace_fields.py` |
| `e2e_org_overview_test.go` | org overview tool | `test_org_overview.py` |
| `e2e_docs_test.go` | docs search against live instance | `test_docs.py` |
| `nil_arguments_e2e_test.go` | nil-arguments panic fix | fold into `test_protocol.py` |

- Port through the MCP transport (initialize → tools/call), not in-process handler calls; assertions that only make sense in-process are dropped with a note in the PR-2 description.
- Staging-assumption triage happens during the port: anything that depended on staging data seeds its own fixtures via `telemetry.py` or the SigNoz API.
- After the port, the Go e2e files and the now-unused `e2e` build-tag helpers are removed in the same PR-2.

## Files to Modify

**PR-1:**
- `tests/**` — new harness + smoke suite (all new files)
- `.github/workflows/e2e.yaml` — new workflow
- `Makefile` — add e2e targets
- `README.md`, `AGENTS.md` — documentation pointers
- `plans/e2e-ci-suite.{context,plan}.md` — this file pair

**PR-2:**
- `tests/e2e/tests/test_family_{a,b,c,d,e}.py`, `test_trace_fields.py`, `test_org_overview.py`, `test_docs.py`, additions to `test_protocol.py` — new ports
- `internal/handler/tools/e2e_*.go`, `internal/mcp-server/{e2e_docs_test.go, nil_arguments_e2e_test.go}` — deleted after port

## Verification

1. Local: `cd tests && uv sync && uv run pytest e2e/bootstrap/setup.py --reuse` brings up the stack; `uv run pytest e2e/tests --reuse` runs suites; `--teardown` cleans up. Full cold run target ≤ ~15 min on a runner.
2. `go test ./...`, `go build ./cmd/server`, `make fmt goimports` stay green (no production changes expected).
3. `actionlint .github/workflows/e2e.yaml`; confirm guardrail suite untouched (`go test -count=1 -run '^TestGuardrail_' ./...`).
4. CI on PR-1: e2e job runs automatically on the PR — provisions SigNoz, seeds telemetry, passes the smoke suite, tears down on success and failure.
5. CI on PR-2 (stacked): ported families pass; coverage parity with the deleted Go files is reviewed in the PR-2 description.
6. Confirm a fork PR runs only after a maintainer applies `safe-to-test` (privileged `pull_request_target` path).
7. PR summaries note: no MCP contract change (no agent-skills companion needed), doc updates included.
