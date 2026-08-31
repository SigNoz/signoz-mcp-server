# Feature: E2E CI Suite — Context & Discussion

## Original Prompt
> Read https://github.com/SigNoz/nerve-pod/issues/219.
> We need to build an e2e suite for signoz-mcp-server which can be run on the CI.
> The integration suite in signoz, the integration suite in terraform and e2e suite in
> signoz-operator is your reference. Look at the existing e2e in signoz-mcp-server.
> Plan first.

## Reference Links
- [nerve-pod#219 — (task) Enable full E2E test github action for MCP](https://github.com/SigNoz/nerve-pod/issues/219) — placeholder body, no additional detail; title is the requirement
- SigNoz/signoz integration suite — `tests/` (pytest + testcontainers-python, builds the SigNoz image in-test), `.github/workflows/integrationci.yaml`, `e2eci.yaml`, `cacheci.yml`
- SigNoz/terraform-provider-signoz integration suite — `tests/` (pytest + foundryctl + Docker Compose), `.github/workflows/testci.yml`
- SigNoz/signoz-operator e2e suite (branch `test/e2e-suite`) — `tests/` (pytest + kind + foundryctl), `.github/workflows/testci.yml`
- Existing mcp-server e2e: `internal/handler/tools/e2e_family{a..e}_test.go`, `e2e_trace_fields_test.go`, `e2e_org_overview_test.go`, `internal/mcp-server/{e2e_docs_test.go, nil_arguments_e2e_test.go}` — `//go:build e2e`, env-gated (`SIGNOZ_E2E_URL`/`SIGNOZ_E2E_TOKEN`), manual staging runs only
- Existing protocol CI: `scripts/test-mcp-protocol.sh`, `scripts/test-mcp-conformance.sh`, `.github/workflows/mcp-protocol.yaml` — real binary, fake backend (`example.invalid`)

## Key Decisions & Discussion Log

### 2026-08-30 — reference-suite survey
- All three sibling repos converged on **pytest + uv** harnesses, even for Go codebases. terraform-provider and operator provision SigNoz with **foundryctl** (`cast` → Docker Compose on the runner; operator adds kind because it needs k8s). The signoz backend instead uses testcontainers-python and builds the app image inside the test run.
- CI trust model is identical everywhere: dual events `pull_request` + `pull_request_target: labeled`, a fork/dependabot gate (`safe-to-test`), and a second opt-in label for expensive suites (`safe-to-integrate` on terraform, `safe-to-e2e` on operator). The mcp-server's `ci.yaml` already implements the `safe-to-test` half; the e2e workflow stacks the cost gate on top, exactly like the operator.
- Conventions worth copying wholesale: bootstrap-as-test entrypoints (`e2e/bootstrap/setup.py`), the `--reuse`/`--teardown` pytest-cache state machine for local iteration, session-scoped stack fixtures, throwaway hardcoded test credentials (`admin@e2e.test`), optional license key (empty = skip, so fork/community runs still work), config via pytest CLI flags rather than env vars, diagnostics straight to the Actions log via `log_cli`.
- The existing Go e2e families already support raw API-key auth (`SIGNOZ_E2E_AUTH_HEADER=SIGNOZ-API-KEY` + `SIGNOZ_E2E_RAW_TOKEN=1`), so they can run against a foundry-cast instance with a minted service-account key — no code change needed for auth.
- The mcp-server needs no k8s: the terraform-style **Docker Compose cast** is the right provisioning shape (simpler and faster than the operator's kind path).
- Seeding telemetry: prefer OTLP HTTP push to the cast SigNoz's collector (most realistic path: telemetry in via OTLP → out via MCP tools). Fallback if the foundry compose flavor does not expose OTLP ports: direct ClickHouse inserts, as the signoz backend fixtures do. To be confirmed at implementation time.
- AGENTS.md mandates upstream-drift detection ("upstream drift fails a test, not a user"): a weekly scheduled run with a floating foundry/SigNoz version gives drift signal even when no PRs are open; PR runs stay label-gated for cost.

### 2026-08-30 — open questions settled with user
- Harness: **pytest + foundryctl** (org standard), Docker Compose cast, no k8s.
- Existing Go `//go:build e2e` families: **wired into CI** against the cast instance (raw API-key auth), with a triage pass for fresh-instance compatibility.
- Gating: **every non-fork PR** — the same dual-event `safe-to-test` expression the existing ci.yaml jobs use, with no extra cost label. Simpler, at the price of CI minutes; drift signal comes from PR cadence. `workflow_dispatch` kept for manual/debug runs; no weekly cron for v1 (can be added if PR cadence proves too low for drift detection).
- MCP client: **thin hand-rolled JSON-RPC over `requests`**; protocol conformance stays with the inspector/conformance lanes.
- License: **optional `PRIMUS_LICENSE_KEY` passthrough** (terraform pattern; empty = license step skipped, so fork/community runs still work). Secrets only ever reach maintainer-approved code because fork PRs run exclusively via the `pull_request_target` + `safe-to-test` path.

### 2026-08-30 — direction change: port the Go e2e families to Python, two stacked PRs
- User decision: the Go `//go:build e2e` families are **ported to Python** on the new harness, not run as Go tests in CI. This supersedes the earlier "wire Go e2e into CI" answer below.
- Delivery in two stacked PRs (user-defined):
  - **PR-1** (branch `nerve-pod/issues/219`, base `main`): the foundry-based harness — `tests/` pytest project, CI workflow, Makefile targets, docs — plus a minimal smoke suite so CI exercises the harness end-to-end.
  - **PR-2** (branch `nerve-pod/issues/219-port`, base PR-1's branch): ports the Go e2e families (A–E, trace-fields, org-overview, docs, nil-arguments) to pytest modules on the harness and removes the Go e2e files, so we never maintain two suites.
- Process: open PR-1 containing only the plan files first; user reviews; PR-1 implementation is then pushed to the same PR, and PR-2 is opened against it.
- Rationale for removing the Go files post-port: keeping both would duplicate coverage and drift apart; the pytest harness drives the server through the real MCP transport, which is strictly broader than the in-process handler calls the Go tests make. Any handler-level assertions that lose value through the transport (e.g. direct struct-field checks) will be noted in PR-2's description.

### 2026-08-30 — plan review feedback (user)
- **No letter-named port modules.** The Go families were named after tracker batches (#363–#367), not concerns. The port groups tests by behavior into semantically named suites (see plan §PR-2 mapping): response-path drift checks, parameter coercion, parameter validation, output envelopes, enums/grammar, notification channels, saved views, get-by-id aliases, trace fields, org overview, docs, upstream/auth errors.
- **No function-scoped fixtures in suite conftests.** All fixtures — including per-test slug/cleanup helpers — live in `tests/fixtures/` and are registered through the root `tests/conftest.py` `pytest_plugins`.
- **No `test_stdio.py` in the first iteration.** HTTP transport only for PR-1; stdio smoke can be reconsidered later.
- Noticed while reading the Go files: `TestE2EDocsAgentFlow` and `TestE2EAuthFailureTelemetry` run against an in-process `httptest` server with the embedded docs corpus — they never needed a live backend. The port covers their transport-visible behavior; the in-process-only OTel span assertion for auth-failure telemetry either becomes an untagged Go unit test or is dropped with justification in the PR-2 description.

### 2026-08-30 — PR-1 implemented and verified locally
- Harness built per plan; verified end-to-end on a cold cast: **4/4 smoke tests pass in ~73s**, teardown confirmed (zero leftover containers). The `--reuse` loop (setup → rerun → cleanup) also verified.
- **Upstream drift found and handled**: service-account role assignment moved from `POST /api/v1/service_accounts/{id}/roles` (the terraform suite's API shape) to `POST /api/v1/service_account_roles {serviceAccountId, roleId}` → 201 on current SigNoz. Our fixture uses the new route; the terraform suite will hit this when its cast floats forward.
- **New readiness phase the reference suites don't need**: the otel collector waits for ClickHouse migrations before serving, so its published ports reset connections for the first minutes of a cold cast. The `telemetry` fixture polls an empty OTLP export until accepted (360s) before any suite seeds.
- **Poll-closure discipline**: `wait_for` treats any truthy value as success, so closures must return strict bools and log error text — returning error strings made the first connection reset look like readiness. Fixed in the telemetry fixture and test_logs.
- **Setup-failure cleanup**: `create()` tears the stack down on failure because `reuse.wrap` registers its finalizer only after `create()` returns (deviation-hardening vs the reference wrapper).
- Docs: `CLAUDE.md` Local Verification updated instead of AGENTS.md (AGENTS.md is a one-line pointer the user is editing locally); README gained an End-to-End Tests section.
- Deviation from plan: no `if: always()` teardown step in the workflow — the pytest session finalizer owns teardown (verified leak-free on failing runs) and runners are ephemeral.
- Local sandbox notes (environment only, not repo requirements): the sandbox needed pypi.org, files.pythonhosted.org, storage.googleapis.com, registry-1.docker.io, auth.docker.io, production.cloudfront.docker.com, and release-assets.githubusercontent.com opened; foundryctl v0.2.17 was built from source because release tarballs were unreachable at the time. CI uses the standard `foundry.sh` installer like the operator workflow.

### 2026-08-30 — first CI run failure + workflow 3-step split
- The first CI run on PR #297 failed at collection: the workflow passed `--license-key ${PRIMUS_LICENSE_KEY}` unconditionally, and with the secret empty the flag had no value (`pytest: error: argument --license-key: expected one argument`). Fixed: the setup step passes the flag only when the secret is set.
- User request: the e2e job is split into explicit **setup / run / teardown** steps (superseding my earlier finalizer-only deviation). setup → `make setup-e2e-env` (cast + cache), run → `make test-e2e-reuse`, teardown (`if: always()`) → `make cleanup-test-e2e` plus a pours/compose fallback for a cast orphaned by a failed setup. Verified locally: setup 1/1, run 4/4, teardown clean (0 containers, pours removed).

### 2026-08-30 — containerized MCP server under test (Dockerfile.e2e)
- User-provided `Dockerfile.e2e` skeleton completed: `golang:1.26-alpine` build stage → `gcr.io/distroless/base` final (same base as production multi-arch), ENTRYPOINT to the binary. No apk installs (the alpine image ships ca-certificates; modules fetch via the Go proxy, so no git). No BuildKit-only features: cache mounts were dropped so the legacy builder also works — BuildKit's dockerfile-session walker stats the `AGENTS.md → CLAUDE.md` symlink for xattrs and fails on filesystems that return ELOOP (this sandbox's mount; CI Linux handles it either way).
- `.dockerignore` completed (VCS/GitHub metadata, build outputs, e2e artifacts, node_modules, AGENTS.md/CLAUDE.md).
- The `mcp_server` fixture now builds with a plain `docker build --file Dockerfile.e2e --tag signoz-mcp-server:e2e .` (copied from the signoz repo tests' fixture) and runs the server as a container via docker-py, reaching the cast SigNoz through `host.docker.internal`. Its MCP port is published to a **docker-assigned free host port** and read back with `client.api.port` — docker-py's free-port mechanism, the same one testcontainers' `get_exposed_port` wraps in the signoz tests (per user direction; SigNoz and the otel collector stay foundry-driven on fixed ports).
- Host Go is no longer required: the `--go-binary-path` option and the workflow's `go-install` step were removed. The suite is Docker-only.
- Local sandbox note: run with `DOCKER_BUILDKIT=0` (legacy builder honors `.dockerignore` and skips the AGENTS.md symlink); CI defaults to BuildKit, also fine.
- Verified: the 3-step flow and a fully cold run, 4/4 pass with clean teardown; the container was reachable on docker-assigned port 32769.

### 2026-08-30 — official Python MCP SDK client
- User decision: `fixtures/mcpclient.py` uses the **official Python `mcp` SDK** (2.1.1) instead of the hand-rolled JSON-RPC client, superseding the earlier raw-`requests` answer below. The fixture wraps the async `streamable_http_client` in a sync facade: a dedicated event loop in a daemon thread, the transport + session held inside one long-lived task (anyio cancel scopes must enter/exit in the same task), and results returned as JSON-wire dicts so test assertions read like the protocol.
- Verified against the live server: the SDK negotiates protocol `2025-11-25`, lists 43 tools, and round-trips tool calls; `terminate_on_close=False` because the server is stateless (no session to DELETE). Full 3-step flow and a cold run: 4/4 pass with clean teardown and no cancel-scope warnings.

## Open Questions
- [x] Harness: pytest + foundryctl (org standard) vs Go-native testcontainers suite? → **pytest + foundryctl** (user, 2026-08-30)
- [x] Wire the existing Go `//go:build e2e` families into the new workflow against the cast instance? → superseded: **port them to Python on the harness in PR-2 and delete the Go files** (user, 2026-08-30)
- [x] Gating: label-gated vs every non-fork PR? → **every non-fork PR**, no extra cost label; `workflow_dispatch` kept; no cron for v1 (user, 2026-08-30)
- [x] MCP client inside the pytest suite? → superseded: **official Python `mcp` SDK** (user, 2026-08-30)
- [x] `PRIMUS_LICENSE_KEY` passthrough? → **yes, optional** (empty = skip) (user, 2026-08-30)
