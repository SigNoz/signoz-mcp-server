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
- `README.md`, `docs/architecture.md` — document the option.

## Key Decisions

### 2026-09-24 — Keep tenant URL selection request-scoped

- Decision: use `SIGNOZ_WEB_URL` only when the context URL normalizes to the
  configured `SIGNOZ_URL`.
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
- No live SigNoz verification is needed for the local origin-selection change;
  upstream request and response contracts are unchanged.

## Outcome

Implementation now supports separate API and browser origins, including
localhost deployments, while keeping links scoped to each request's backend.
The localhost regressions are fixed and the full local CI gate passes.

No tool metadata, schemas, server instructions, or wire-catalog entries change.
No SigNoz/agent-skills companion change is needed for this additive server setting.

## Reference Links

- [Issue #303](https://github.com/SigNoz/signoz-mcp-server/issues/303)
- [PR #317](https://github.com/SigNoz/signoz-mcp-server/pull/317)
