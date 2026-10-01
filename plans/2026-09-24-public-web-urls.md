# Plan: Public URLs for Resource Deep Links

Status: Done
Issue: #303
PR: https://github.com/SigNoz/signoz-mcp-server/pull/332

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
- `docker-compose.yml`, `manifest.json` — forward the optional browser origin
  through Docker Compose and the Claude Desktop bundle.

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

### 2026-10-01 — Forward browser origins through packaged launchers

- Docker Compose forwards `SIGNOZ_WEB_URL` from the shell or `.env` file, with
  an empty value when unset. The Desktop bundle exposes an optional
  `signoz_web_url` field with an empty default and maps it to `SIGNOZ_WEB_URL`.
- This fixes the launch-configuration omission reproduced in review. The user
  selected this fix and deferred the malformed-URL logging and expanded IPv6
  unspecified-address findings. Runtime URL validation is unchanged.
- The user requested an independent review of the full PR before pushing.

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
- The subsequent full GitHub E2E run on `03b9010` passed all 63 tests, including
  both browser-origin cases and their dashboard-deletion checks. The feature
  code and tests are unchanged after extracting the workflow changes to #329.
- The launcher follow-up passes `GOTOOLCHAIN=go1.26.0 make ci`, including
  formatting, lint, dependencies, builds, race tests, guardrails, protocol,
  conformance, E2E style, and repository docs checks.
- Real Docker Compose rendering confirms the browser URL is empty when unset
  and forwarded for HTTPS and localhost origins, while the API URL is unchanged.
  These checks used synthetic settings and did not launch containers.
- The official `@anthropic-ai/mcpb@2.1.2` configuration resolver forwards omitted,
  blank, HTTPS, and localhost settings correctly, retaining the API URL and key.
  Its strict manifest validator rejects the preexisting top-level `resources`
  key identically on `main`, the prior PR head, and the launcher follow-up.
  All other fields pass the official v0.2 schema when that key is excluded from
  an in-memory validation copy; the tracked resource metadata is unchanged.
- An independent agent reviewed the full 17-file PR before pushing, including
  all affected handlers, tenant isolation, API routing and credentials, URL
  normalization, launcher wiring, documentation, metadata, E2E fixtures, and
  cleanup. It found no additional actionable issues and passed focused
  configuration, handler-link, and URL utility tests with Go 1.26.0.
  The review was read-only and performed no new live SigNoz verification;
  runtime E2E evidence predates the launcher follow-up.

## Outcome

Implementation now supports separate API and browser origins, including
localhost deployments, while keeping links scoped to each request's backend.
The localhost regressions are fixed, the full local CI gate passes, and the
focused live E2E cases confirm the behavior through HTTP MCP.

The Desktop bundle's configuration schema and environment mapping now include
the optional browser origin. Tool metadata, tool schemas, server instructions,
and wire-catalog entries are unchanged.
No SigNoz/agent-skills companion change is needed for this additive server setting.

PR #317 was recreated as #332 with the original commits and contributor
authorship preserved. The launcher follow-up is limited to the optional browser
URL setting and its documentation. The user closed #329 and permanently
excluded CI changes from this work.

## Reference Links

- [Issue #303](https://github.com/SigNoz/signoz-mcp-server/issues/303)
- [PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
- [PR #332](https://github.com/SigNoz/signoz-mcp-server/pull/332)
- [Full E2E verification](https://github.com/SigNoz/signoz-mcp-server/actions/runs/36305264712)
