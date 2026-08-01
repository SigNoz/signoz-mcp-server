# Plan: Alert Policy Routing and Completed Preflights

## Status
In Progress

## Context
The MCP alert validation layer is stricter than SigNoz: it rejects channel-less payloads even when org-level policy routing is enabled. Separately, the `signoz_update_alert` description phrases GET/resource/channel prerequisites unconditionally, so clients can repeat already-completed reads or stop before the prepared update.

## Approach
1. Detect `notificationSettings.usePolicy=true` from the submitted alert payload after schema validation.
2. Require at least one referenced channel only for direct-routing payloads. When references are present, validate every name against the current tenant channel list in both routing modes.
3. Rewrite create/update tool metadata and alert resources so policy routing permits no direct channel, while direct routing still requires verified names.
4. Make update guidance explicitly reuse a current fetched rule, already-read authoring resources, and already-verified channel names instead of repeating those calls.
5. Add focused create/update validation tests for policy routing, direct routing, and invalid supplied channel names, plus a metadata regression test for completed-preflight reuse.
6. Synchronize README and `manifest.json`.
7. Update `SigNoz/agent-skills` so `signoz-creating-alerts` teaches the corrected direct-vs-policy routing branch, add a focused eval, and link the companion PR under CMP-3.

## Files to Modify
- `internal/handler/tools/alerts.go` — policy-aware validation and state-aware tool descriptions.
- `internal/handler/tools/alerts_test.go` — focused validation regressions.
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
