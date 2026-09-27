# Plan: Public URLs for Resource Deep Links

Status: Done
Issue: #303
PR: https://github.com/SigNoz/signoz-mcp-server/pull/317

## Context

Deployments can reach SigNoz through an internal API URL while users open the UI
through a public URL. Resource `webUrl` values currently use the request's API
URL, which may not be reachable from the user's browser.

## Approach

Add optional `SIGNOZ_WEB_URL` for links on requests using configured
`SIGNOZ_URL`. Keep the request's tenant URL for API calls and for links when the
backend differs from configured `SIGNOZ_URL`. When unset, retain current link
behavior.

## Files to Modify

- `internal/config/config.go`, `internal/handler/tools/handler.go` — load and
  select the optional link base.
- `internal/handler/tools/*.go` — use the selected base for resource links.
- `internal/config/config_test.go`, `internal/handler/tools/dashboards_test.go`
  — cover configuration and API/UI URL separation.
- `pkg/util/url.go` — distinguish browser-origin validation from tenant backend
  validation while sharing origin parsing and canonicalization.
- `tests/fixtures/mcpserver.py`, `tests/e2e/tests/test_dashboards.py` — exercise
  configured and per-request browser origins against live SigNoz through HTTP MCP.
- `README.md`, `docs/architecture.md`, `tests/README.md` — document the option
  and E2E setup.
- `.github/workflows/ci.yaml`, `.github/workflows/e2e.yaml` — let this contributor
  PR run the CI and live E2E gates without privileged fork checkouts.

## Key Decisions

### 2026-09-24 — Keep tenant URL selection request-scoped

- Decision: use `SIGNOZ_WEB_URL` only when the request's backend URL matches
  the configured `SIGNOZ_URL`.
- Rationale: OAuth and `X-SigNoz-URL` can select another tenant; its own URL must
  remain the deep-link host, and API client identity must not change.

### 2026-09-27 — Support localhost without changing tenant validation

- Decision: accept an exact, nonempty match with the configured backend before
  attempting tenant URL normalization. Permit localhost in `SIGNOZ_WEB_URL` for
  browser access through port forwarding.
- Rationale: configured API URLs already accept localhost, but tenant URL
  normalization rejects it. Reusing that validator caused the browser override
  to be silently ignored and prevented localhost browser URLs at startup.
- Tenant backend validation, OAuth forms, token contents, and API routing retain
  their existing behavior. Browser origins still reject user information, paths,
  queries, fragments, unsupported schemes, and unspecified bind addresses.

### 2026-09-27 — Run fork validation without repository secrets

- Decision: use ordinary `pull_request` jobs with read-only permissions for fork
  and Dependabot PRs. Run `make ci` with public tools; retain private Primus jobs
  for internal PRs. Keep `safe-to-test` as the cost gate for fork E2E runs, which
  receive no license secret. Check out immutable revisions without storing Git
  credentials and explicitly install Go for the native localhost fixture.
- Rationale: seven checks on this PR failed before reaching the tests because
  `actions/checkout` rejects fork code in `pull_request_target`. Primus also needs
  private-repository credentials, so changing the event alone is insufficient.
- The user requested this CI repair on the same PR. No checkout safety bypass or
  guardrail relaxation is introduced; `make ci` runs the existing local gate.

## Verification

- Expanded the existing dashboard handler tests to exercise the real HTTP client
  against a local stub, covering localhost and numeric-loopback API URLs,
  localhost browser links, an unset override, and retained API credentials.
- Both localhost regressions failed before the fix. Tenant-selection coverage
  includes other hosts, different ports, equivalent canonical origins, and
  missing configured or request URLs.
- Expanded configuration tests cover browser-origin normalization and rejection
  of malformed or unsupported origins. Existing tenant URL tests cover the
  stricter backend host policy.
- Configuration, handler, URL utility, OAuth, and MCP-server package tests pass.
- `GOTOOLCHAIN=go1.26.0 make ci` passes, including the race detector, guardrails,
  protocol checks, and conformance checks.
- `GOTOOLCHAIN=go1.26.0 make check-repo-docs READY=1 BASE=main` passes.
- Reviewed against `docs/mcp-best-practices.md` section 11 with no MUST exceptions
  or SHOULD deviations.
- Added a parameterized live E2E regression for public and localhost browser
  origins with a localhost API backend. It clones a system dashboard, checks
  create/get/list/update/patch browser links, verifies backend fields through
  direct reads, switches to a different per-request URL, and confirms deletion.
- E2E Python style and collection pass (63 cases collected across the suite).
- The two browser-origin E2E cases pass against live SigNoz v0.143.0. A temporary
  in-memory fixture override reused the existing local test stack without casting
  or tearing it down. The native MCP server used `http://localhost:8080`; the
  alternate request used `http://127.0.0.1:8080`.
- Direct backend reads confirmed `id`, `name`, `schemaVersion`, `tags`, and `spec`
  round-tripped, while `webUrl` remained response-only. Both temporary dashboards
  returned 404 after deletion, the temporary session key was revoked and confirmed
  absent, and the native MCP processes were reaped.
- Live invocation from `tests/` used a credential-free temporary runner:
  `GOTOOLCHAIN=go1.26.0 uv run python /tmp/signoz-pr317-focused-live.py`, selecting
  `test_dashboard_web_urls_keep_localhost_api_routing_and_request_backend_fallback`.
  The other E2E cases were not rerun in this verification.
- CI repair: `actionlint` v1.7.12 passes for both changed workflows. Evaluated
  their conditions for internal, fork, and Dependabot PRs with and without the
  label, plus manual E2E dispatch. Fork and Dependabot cases select public checks
  and never receive the license, including when labeled.
- Reran `GOTOOLCHAIN=go1.26.0 make ci` after the workflow changes; all checks pass.
  GitHub execution of the new fork path is verified after pushing.

## Outcome

Implementation now supports separate API and browser origins, including
localhost deployments, while keeping links scoped to each request's backend.
The localhost regressions are fixed, the full local CI gate passes, and the
focused live E2E cases confirm the behavior through HTTP MCP.

Fork validation now uses ordinary pull-request jobs with read-only permissions.
The optional E2E license and private Primus credentials remain limited to trusted
contexts. The CI repair is included here to unblock this contributor PR, as
requested by the maintainer.

No tool metadata, schemas, server instructions, or wire-catalog entries change.
No SigNoz/agent-skills companion change is needed for this additive server setting.

## Reference Links

- [Issue #303](https://github.com/SigNoz/signoz-mcp-server/issues/303)
- [PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
