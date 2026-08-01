# Plan: Alert Policy Routing and Completed Preflights

## Status
Done

## Context
The MCP alert validation layer is stricter than SigNoz: it rejects channel-less payloads even when org-level policy routing is enabled. Separately, the `signoz_update_alert` description phrases GET/resource/channel prerequisites unconditionally, so clients can repeat already-completed reads or stop before the prepared update.

## Approach
1. Detect `notificationSettings.usePolicy=true` from the submitted alert payload after schema validation.
2. Match upstream schema contracts before routing validation: v2 threshold/PromQL rules always require `condition.thresholds`; v1 anomaly rules omit them.
3. Validate routing by schema and mode. Direct v2 requires valid channel names on every threshold; v2 `preferredChannels` is rejected as unused. Policy-routed v2 may omit threshold channels but still validates every supplied name. V1 anomaly requires valid top-level `preferredChannels`.
4. Rewrite create/update tool metadata, typed schemas, alert resources, README, and manifest so policy routing is explicitly v2-only and anomaly routing stays direct.
5. Make update guidance explicitly reuse a rule, authoring resources, and resolved names only within the same still-current prepared operation, refreshing when state may have changed.
6. Add focused create/update regressions for per-tier direct routing, v2 `preferredChannels`, policy routing, anomaly routing, absent-only payloads, and invalid/blank supplied names, plus metadata guardrails.
7. Update `SigNoz/agent-skills` so `signoz-creating-alerts` teaches the corrected routing contract and stops when an org policy is unconfirmed; add positive and negative focused evals and link the companion PR under CMP-3.

## Files to Modify
- `internal/handler/tools/alerts.go` — policy-aware validation and state-aware tool descriptions.
- `internal/handler/tools/alerts_test.go` — focused validation regressions.
- `pkg/alert/validate.go` and tests — align required v2 thresholds with current upstream validation.
- `internal/mcp-server/contract_budget_test.go` — pin policy-routing and completed-preflight metadata.
- `pkg/alert/resources.go` — correct routing and preflight guidance.
- `pkg/alert/resources_test.go` — pin policy-aware, state-aware resource guidance.
- `pkg/instructions/instructions.go` — make the shared mutation-preparation rule reuse completed reads.
- `pkg/types/alertrule.go` — synchronize client-visible field descriptions.
- `README.md` and `manifest.json` — synchronize client-visible behavior.
- `plans/alert-policy-routing-preflights.context.md` and this plan — decision trail and current status.
- `SigNoz/agent-skills/plugins/signoz/skills/signoz-creating-alerts/` — companion playbook and eval update in a linked PR.

## Verification
Run focused alert/tool metadata tests, formatting/import checks, `go test ./...`, `go vet ./...`, `go build ./cmd/server`, manifest JSON validation, `git diff --check`, the applicable guardrail suite, and Agent CI. Validate the companion skill/eval with its repository tooling. Obtain an independent review before publishing both linked draft PRs.
