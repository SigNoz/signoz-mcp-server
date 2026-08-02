# Plan: Data Retention Tool

## Status
In Progress

## Context
The telemetry-cost skill currently cannot read the workspace's configured retention periods and must ask the user. Add one read-only MCP tool that returns a trustworthy cross-signal retention snapshot, including custom log-retention overrides.

## Approach
1. Add a typed client contract and implementation that fetches metrics and traces retention concurrently from the v1 signal endpoint and probes logs through the v2 custom-retention-aware endpoint. When logs are in legacy mode, follow with a v1 read to preserve exact hour values rather than the v2 compatibility response's whole-day conversion.
2. Normalize every meaningful v1/v2 response state into one stable hours-based output containing current and pending/failed target deletion retention, current and target cold-storage move thresholds, change status, and active/target custom log rules. Treat omitted, zero, and `-1` move thresholds as disabled; reject values below `-1` as upstream contract drift. When a v2 pending or failed response does not expose the active policy, report the current state as unknown and label the returned attempted configuration as the target rather than active.
3. Detect malformed upstream response shapes, emit a WARN signal, and fail the call rather than returning invented or partial retention values. Preserve shared authentication/permission error classification.
4. Register `signoz_get_data_retention` with a typed output schema, `searchContext`, and read-only annotations; return matching text and structured content.
5. Synchronize README and manifest metadata, annotation/schema inventories, and tool-registration parity.

## Files to Modify
- `internal/client/data_retention.go` — upstream reads, parsing, normalization, and response types.
- `internal/client/data_retention_test.go` — route/query/shape/fallback and contract-drift tests.
- `internal/client/testdata/retention/` — sanitized recorded live responses that pin the upstream contract.
- `internal/client/interface.go` and `internal/client/mock.go` — client interface and test double.
- `internal/handler/tools/data_retention.go` — MCP registration and handler.
- `internal/handler/tools/data_retention_test.go` — success and coded upstream-error coverage.
- `internal/handler/tools/register.go` — register the new handler group.
- `internal/handler/tools/{annotations_inventory_test.go,schema_inventory_test.go,structured_content_test.go,output_schema_test.go,nil_arguments_test.go}` — pin annotations, nil-argument compatibility, and structured output behavior.
- `README.md` and `manifest.json` — publish the client-visible tool contract.
- `plans/data-retention.context.md` and `plans/data-retention.plan.md` — decision log and implementation plan.

## Verification
- Run focused client and handler tests for data retention.
- Run formatting/imports with `make fmt goimports`.
- Run workflow lint, focused guardrails, the full Go suite, and `go build ./cmd/server`.
- If live SigNoz credentials are available, delegate a read-only live response check and report which fields round-trip; no resources need to be created.

## Follow-up
- After this additive server tool is released, update the companion `SigNoz/agent-skills` cost-reduction workflow to call it, handle `currentStateKnown=false`, preserve the newly-ingested-data caveat, and link that companion PR in the server PR summary. No existing skill-taught server contract changes in this branch.
