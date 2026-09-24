# Plan: Public URLs for Resource Deep Links

Status: In Progress
Issue: #303
PR:

## Context

Deployments can reach SigNoz through an internal API URL while users open the UI
through a public URL. Resource `webUrl` values currently use the request's API
URL, which may not be reachable from the user's browser.

## Approach

Add optional `SIGNOZ_WEB_URL` for links on requests using configured
`SIGNOZ_URL`. Keep the request's tenant URL for API calls and for links on
per-tenant requests. When unset, retain current link behavior.

## Files to Modify

- `internal/config/config.go`, `internal/handler/tools/handler.go` — load and
  select the optional link base.
- `internal/handler/tools/*.go` — use the selected base for resource links.
- `internal/config/config_test.go`, `internal/handler/tools/dashboards_test.go`
  — cover configuration and API/UI URL separation.
- `README.md`, `docs/architecture.md` — document the option.

## Key Decisions

### 2026-09-24 — Keep tenant URL selection request-scoped

- Decision: use `SIGNOZ_WEB_URL` only when the context URL normalizes to the
  configured `SIGNOZ_URL`.
- Rationale: OAuth and `X-SigNoz-URL` can select another tenant; its own URL must
  remain the deep-link host, and API client identity must not change.

## Verification

- `go test ./internal/config ./internal/handler/tools -count=1` passed.
- `make ci` passed all targets through e2e style. Its repo-docs check first
  failed because this plan lacked required fields; `make check-repo-docs` passed
  after adding them and leaving the plan In Progress until a PR exists.

## Outcome

Implementation is complete. The plan remains In Progress until a PR exists so
its PR field can link to the review.

Added `SIGNOZ_WEB_URL` for deep links when the request uses the configured API
URL. Per-tenant requests continue to use their own URL. Configuration accepts
an HTTP(S) origin and rejects paths, queries, and fragments.

## Reference Links

- Issue #303
