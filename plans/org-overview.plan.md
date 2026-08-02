# Plan: Org Overview

## Status
Done

## Context

Agents currently need several list calls to understand whether a workspace has logs, traces, or metrics flowing and whether dashboards, alerts, channels, views, pipelines, and integrations exist. Issue #50 proposes one cheap, read-only posture snapshot backed by the workspace stats API.

## Approach

- Add `signoz_get_org_overview`, a read-only and idempotent tool with only the non-required `searchContext` input.
- Add a client method for the workspace stats endpoint using the shared request/auth/error path.
- Return an owned, grouped envelope containing only organization-scoped dashboard, configured-rule, channel, saved-view, log-pipeline, and cloud-integration aggregates.
- Explicitly exclude deployment-global telemetry/infra and alert-runtime keys. Report those groups as unavailable with a tenant-scope reason; never pass the raw upstream bag through.
- Fail open for compatible new keys only within approved organization-scoped observability prefixes by preserving them in `additionalStats` and emitting a WARN. A malformed top-level envelope returns a coded `UPSTREAM_ERROR` because it cannot be filtered safely.
- Preserve missing-vs-zero and large-integer semantics. Expose sentinel-derived availability for required org-scoped groups, explicit per-provider/source availability for current AWS/Azure cloud stats, and machine-readable partial recovery metadata with exact fallback tools. Treat zero reported cloud providers as ambiguously unavailable (unsupported/edition-gated or both queries failed) without marking the whole snapshot partial; treat one reported provider as partial. WARN on missing/invalid collectors, keep malformed known stats out of `additionalStats`, and mark dashboard panel coverage as legacy-v1-only rather than presenting it as a complete v2/Perses inventory.
- Route authentication and permission failures through the shared coded top-level error path.
- Register the handler and synchronize `README.md`, `manifest.json`, mocks/interfaces, nil-argument coverage, and contract tests.
- Record the completed companion `SigNoz/agent-skills` audit: no required change for this additive tool; optional workflow follow-up only.
- Run formatting, focused tests, guardrails, full tests, build, an independent best-practices review, Claude Opus review, and delegated live E2E verification.

## Files to Modify

- `internal/client/` — add the upstream stats client method and contract-drift coverage.
- `internal/handler/tools/` — add the tool definition, handler, error-path tests, and nil-argument coverage.
- `internal/handler/tools/register.go` — register the tool through the centralized tool inventory.
- `README.md` — add tool catalog and parameter/result documentation.
- `manifest.json` — add synchronized tool metadata.
- `plans/org-overview.context.md` — append-only design and review decisions.
- `plans/org-overview.plan.md` — keep the current implementation plan and status.

## Verification

- `make fmt goimports`
- Focused client and handler tests for success, malformed/drifted envelopes, nil arguments, and 401/403 classification.
- Guardrail workflow lint and focused suite from `guardrails/README.md`.
- `go test -count=1 ./...`
- `go build ./cmd/server`
- Independent diff-local review against every applicable section 11 rubric item.
- Claude Opus 5 read-only review in the current repository at high effort, with model usage verified.
- Delegated staging E2E through the MCP tool using the supplied bearer token and URL; no created resources expected for this read-only tool.
