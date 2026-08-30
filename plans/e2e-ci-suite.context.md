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

## Open Questions
- [x] Harness: pytest + foundryctl (org standard) vs Go-native testcontainers suite? → **pytest + foundryctl** (user, 2026-08-30)
- [x] Wire the existing Go `//go:build e2e` families into the new workflow against the cast instance? → superseded: **port them to Python on the harness in PR-2 and delete the Go files** (user, 2026-08-30)
- [x] Gating: label-gated vs every non-fork PR? → **every non-fork PR**, no extra cost label; `workflow_dispatch` kept; no cron for v1 (user, 2026-08-30)
- [x] MCP client inside the pytest suite? → **raw JSON-RPC over `requests`** (user, 2026-08-30)
- [x] `PRIMUS_LICENSE_KEY` passthrough? → **yes, optional** (empty = skip) (user, 2026-08-30)
