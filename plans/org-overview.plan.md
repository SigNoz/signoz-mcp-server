# Plan: Org Overview

## Status
Done

## Context

Agents currently need several list calls to understand whether a workspace has logs, traces, or metrics flowing and whether dashboards, alerts, channels, views, pipelines, and integrations exist. Issue #50 proposes one cheap, read-only posture snapshot backed by the workspace stats API.

## Approach

- Add `signoz_get_org_overview`, a read-only and idempotent tool with only the non-required `searchContext` input.
- Add a client method for the workspace stats endpoint using the shared request/auth/error path.
- Return `data.sourceStats` as the authoritative complete flat bag copied from every entry reported in the upstream `data` object. Preserve current telemetry, alert-runtime, workspace, authentication, authorization, license, configuration, and infrastructure fields, and automatically preserve future fields without an allowlist.
- Add typed convenience projections for every current stats family: `signals`, `dashboards`, `alerts`, `views`, `logPipelines`, `cloudIntegrations`, `users`, `authentication`, `serviceAccounts`, `authorization`, `license`, and `configuration`. The projections do not replace or narrow `sourceStats`.
- Preserve missing-vs-zero and large-integer semantics. A key absent from `sourceStats` was not reported and must not be treated as zero; an invalid known projection value remains present in `sourceStats` even when it cannot populate its typed field.
- Make completeness metadata projection-specific: reconcile `reportedStatCount`, `projectedStatCount`, and `unprojectedStatCount`; use `projectionPartial`, `incompleteGroups`, and `invalidProjectionFields` only to describe gaps in the convenience projections. Future source fields remain authoritative in `sourceStats` and increase `unprojectedStatCount` without being dropped.
- Keep sentinel-derived availability and machine-readable recovery guidance for typed groups. Preserve the current AWS/Azure provider-availability semantics.
- Return a coded `UPSTREAM_ERROR` when the top-level envelope or `data` object cannot be decoded, because there is then no authoritative stats bag to return. WARN when an expected typed projection is missing or invalid, while retaining every successfully decoded source entry.
- Route authentication and permission failures through the shared coded top-level error path.
- Return immediate URL/fallback recovery guidance when the stats route returns HTTP 404.
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
