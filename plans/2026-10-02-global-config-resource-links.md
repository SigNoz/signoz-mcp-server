# Plan: Discover Resource Links from SigNoz Global Configuration

Status: Done
Issue: #303
PR: https://github.com/SigNoz/signoz-mcp-server/pull/339
Companion PR: https://github.com/SigNoz/signoz.io/pull/4262

## Context

Deployments can reach SigNoz through an internal API address while users open
the UI through a browser-accessible address. SigNoz owns the canonical instance
URL in `global.external_url`, exposed by `GET /api/v1/global/config`. Standard
self-hosted defaults leave it unset; README and website setup docs must explain
where to configure it.

## Approach

- Discover the browser base from each authenticated SigNoz client and cache it
  with bounded freshness. Keep API destinations and tenant credentials intact.
- Preserve an external base path. Validate browser URLs without reusing the
  tenant-backend origin restrictions. Never follow the discovered URL for API calls.
- Fall back to the request backend when discovery is unavailable or unconfigured;
  warn on lookup or response-contract failures. Propagate 401/403 through coded
  tool errors before performing resource operations, including writes.
- Keep instance configuration on SigNoz and API connection settings on MCP.
- Update behavioral tests and live E2E coverage. Document the setup and create
  linked draft server and website PRs. No CI workflow changes.

## Files to Modify

- `internal/client/` — cached global URL discovery and observable fallback.
- `internal/config/` — retain API connection settings.
- `internal/handler/tools/` — use discovered links across resource handlers.
- `pkg/util/url.go` — backend-only normalization.
- `docker-compose.yml`, `manifest.json` — API connection settings for launchers.
- `tests/` — verify discovery and tenant-specific links against SigNoz.
- `README.md`, `docs/architecture.md` — central configuration.

## Key Decisions

### 2026-10-02 — SigNoz owns instance configuration

- Browser addresses are instance configuration owned by SigNoz.
- Cache discovered configuration per existing tenant client, whose identity
  includes backend URL, auth header, and credential; never share caller state.
- Keep the website refresh in a companion draft PR in SigNoz/signoz.io.

## Reference Links

- https://github.com/SigNoz/signoz-mcp-server/issues/303
- https://github.com/SigNoz/signoz-mcp-server/pull/332
- https://github.com/SigNoz/signoz/blob/1643df620bbe82c67a794e736dc91825a9826385/pkg/global/signozglobal/provider.go

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci`: all PR-gate checks passed, including race,
  guardrails, both protocol eras, conformance, E2E style, and repo documentation.
- Independent full review of the server and website diffs completed. Resolved
  unreadable/oversized 401/403 status preservation, the real unset sentinel,
  and website contract wording. Focused race-enabled regressions passed.
- Delegated live SigNoz verification: 64 E2E tests passed; the three browser-link
  cases passed again against final code. The full suite used a locally
  cross-compiled Linux test image after a slow standard image build was stopped.
- Actual global config projection: `{"status":"success","data":{"external_url":"//<unset>"}}`.
  Covered configured public paths, localhost browser URLs, default fallback,
  per-request backend aliases, and dashboard create/get/list/update/patch.
  `id`, `name`, `schemaVersion`, `tags`, and `spec` round-tripped; `webUrl` was
  not persisted. Dashboard deletion returned 404, final resource lists were
  empty, and test stacks/volumes/images were removed.
- Website companion: all metadata, redirect, CMS frontmatter, and staged URL
  checks passed (94 validator tests). Both edited routes rendered HTTP 200.
  Production `yarn build --webpack` passed; default Turbopack build failed in
  unchanged Google-font resolution. Browser automation failed on the preview
  client, so screenshots could not be captured.
- Reviewed applicable best-practices section 11 items: tenant scoping,
  observable fallback, top-level auth failures, central configuration,
  synchronized README/manifest/docs/tests. No MUST exceptions or SHOULD
  deviations. No companion agent-skills change is needed: tool contracts and
  client setup instructions remain unchanged.

## Outcome

Browser links now use SigNoz global configuration while API routing and
credentials remain request-scoped. README and website docs explain how to set
the browser URL on SigNoz. The implementation and website refresh are separate
draft PRs; website publication waits for a server release. No CI workflows changed.
