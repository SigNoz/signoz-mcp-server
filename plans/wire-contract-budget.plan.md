# Plan: Wire Contract Budget

## Status
Done

## Context
The registered MCP server currently exceeds verified client delivery budgets: initialize instructions are larger than 2 KiB, five tool descriptions exceed 1,024 bytes, and two duplicated parameter descriptions exceed the 512-byte target. Six existing schemas are also wide enough to risk client-side parameter stripping and must not grow while a future redesign is evaluated.

## Approach
1. Rewrite the server instructions with a self-contained preamble before the first heading, preserving the routing, filtering, timestamp, and `webUrl` rules within the 2,048-byte hard limit and near the 1,800-byte target.
2. Apply the researched description rubric to `signoz_create_alert`, `signoz_list_alerts`, `signoz_create_dashboard`, `signoz_update_dashboard`, and the additional wire offender `signoz_execute_builder_query`: lead with “Use this when”, distinguish adjacent tools, summarize the result when it affects selection, verify notification-channel names before alert creation with create-time validation as fallback, explain domain-specific input language on the relevant parameter, and point to on-demand resources for long-form construction guidance.
3. Keep detailed Query Builder argument-construction guidance beside the `query` property, repeat the empirically critical formula bounds/order rule in the top-level discovery description, and replace the duplicated logs/traces aggregation `requestType` prose with field-semantic guidance within 512 bytes.
4. Add integration guards against the initialized server and `tools/list` wire catalog for instruction size/preamble, description budgets, targeted selection guidance, Query Builder construction guidance, and exact property inventories for all six currently grandfathered wide schemas.
5. Review mirrored descriptions in `README.md`, `manifest.json`, and `docs/`; update only stale user-facing text. Confirm no companion agent-skills contract update is needed.
6. Build HEAD and working-tree server binaries, then run an identical Claude Sonnet 5 routing corpus against each through strict MCP-only configs. Repeat paired trials, score tool selection and key arguments, and rerun affected cases after any evidence-driven wording revision.
7. Attempt a separate delegated read-only staging smoke subset with credentials supplied only through process environment. Do not execute mutations. If organization policy prohibits sending private staging results to the model even after explicit user approval, record the pre-execution block and credential/resource safety outcome without attempting a workaround.

## Files to Modify
- `pkg/instructions/instructions.go` — compact initialize instructions and add the preamble.
- `internal/handler/tools/alerts.go` — trim alert list/create descriptions.
- `internal/handler/tools/dashboards.go` — trim dashboard create/update descriptions.
- `internal/handler/tools/query_builder.go` — trim the additional over-budget raw Query Builder description while retaining routing guidance.
- `internal/handler/tools/params.go` — define concise shared aggregation-shape guidance.
- `internal/handler/tools/logs.go` — use the shared `requestType` description.
- `internal/handler/tools/traces.go` — use the shared `requestType` description.
- `internal/handler/tools/param_schema_test.go` — pin raw Query Builder selection routing and argument-construction guidance in their intended surfaces.
- `pkg/alert/resources.go`, `pkg/alert/resources_test.go` — teach and pin notification-channel preflight as the primary alert-creation flow.
- `pkg/dashboard/widgets.go` — retain query-type-to-resource routing moved out of dashboard tool descriptions.
- `pkg/dashboard/widgets_test.go` — pin every moved Query Builder, ClickHouse, and PromQL resource route.
- `internal/mcp-server/contract_budget_test.go` — enforce budgets and wide-schema inventories at the wire boundary.
- `README.md`, `manifest.json` — align mirrored Query Builder authoring guidance with the final tested description and server-side normalization behavior.
- `docs/` — update only if review finds mirrored text made stale by the trims.

## Verification
Run focused MCP-server integration tests, the full Go test suite, formatting/static checks used by the repository, and a final wire-catalog measurement that reports instruction, tool-description, parameter-description, and top-level-property maxima. Verify `git diff` contains no parameter/name/type/default changes. For the Claude evaluation, preserve the exact case corpus, model identifier, effort, repetition count, variant ordering, structured outputs, score rules, and any permitted read-only live-smoke result without persisting credentials; if policy blocks the live model call before execution, preserve only the sanitized block/safety outcome.
