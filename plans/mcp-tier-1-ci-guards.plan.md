# Plan: MCP Tier 1 CI Guards

## Status
In Progress

## Context
SigNoz MCP metadata and results cross several client-specific delivery limits and can silently drift when registrations, schemas, resources, retry behavior, or serialization change. Tier 1 of SigNoz/nerve-pod#34 makes these measurable invariants blocking in ordinary PR CI.

PR #247 already established initialized-wire instruction/description budgets and grandfathered wide-schema inventories. This plan extends that baseline across the remaining Tier 1 contracts.

## Approach

### Tranche A — Wire and transport safety
- Keep all budget measurements at the initialized MCP wire boundary.
- Add tool-name, combined official-alias, total input-schema byte, and schema-nesting budgets.
- Route tools, resources, resource templates, and prompts through checked registration helpers that fail on duplicate keys before mcp-go can overwrite them.
- Scan every advertised catalog surface for `signoz://` pointers and read each referenced static resource through the in-process MCP client; require non-empty content and matching MIME metadata.
- Make retries operation-aware: GET and idempotent PUT/DELETE may retry; a POST-specific helper permits retries only for audited read-only queries; create, test-notification, and other mutating POSTs are single-attempt because the upstream APIs have no idempotency keys. Pass the callers' existing byte slices through the internal request API and create a fresh reader per permitted attempt without copying the body.
- Validate complete tool results for JSON safety before returning them to the SDK transport. Reuse `CallToolResult.MarshalJSON` so validation and size measurement match the SDK wire representation without an additional outer marshal. Convert invalid numeric values to a coded internal error and test the real JSON-RPC serialization path with NaN, infinities, large integers, invalid UTF-8, and control characters.
- Measure telemetry using the complete serialized `CallToolResult`, including structured content and non-text blocks.
- Run the tranche's guardrails in a dedicated `guardrails / contract` pull-request workflow. Give every guarded test a `TestGuardrail_` prefix and pin the exact discovered test inventory in `guardrails/tests.txt` so additions, removals, and renames require an explicit central-policy diff before the suite runs.
- Keep review-sensitive limits, official aliases, grandfathered schema exceptions, suite membership, and contributor guidance under `guardrails/`. Leave tests that require unexported internals beside their packages instead of exporting production implementation details for test organization.

### Tranche B — Semantic release contract
- Generate and commit a normalized catalog snapshot from the initialized server.
- Hard-fail parameter removal/rename, type narrowing, optional-to-required changes, enum removal, and annotation safety changes.
- Define an explicit alias plus migration-note escape hatch for reviewed intentional breaks.
- Bring runtime resource metadata and `manifest.json` into a single parity contract.

### Tranche C — Result and error contracts
- Define representative default/max fixtures and serialized-result byte budgets per tool class.
- Require explicit truncation/cursor metadata where a result is intentionally bounded.
- Inject wrapped 401/403 failures through every upstream client method and require top-level coded error results.
- Migrate remaining raw error-result paths to the stable error taxonomy.

## Files to Modify
- `internal/handler/tools/handler.go` — registration state scoped to each server.
- `internal/handler/tools/registration.go` — checked tool/resource/template/prompt registration helpers.
- `internal/handler/tools/*` registration sites — use checked helpers.
- `internal/client/client.go` and tests — operation-aware retry eligibility.
- `internal/mcp-server/server.go` and tests — JSON safety and complete serialized-result measurement.
- `internal/mcp-server/contract_budget_test.go` — initialized-wire budgets and resource integrity.
- `.github/workflows/guardrails.yaml` — explicit guardrail inventory and dedicated blocking test suite.
- `guardrails/` — central policy, exact suite inventory, and reviewer guidance.
- `CLAUDE.md` — mandatory handling and verification rules for guardrail changes.
- `pkg/toolerrors/errors.go` — stable internal serialization error code.
- `manifest.json`, `README.md`, and `docs/` — update only when a later tranche changes advertised metadata or behavior.

## Verification
- Focused retry, registration, wire-budget, resource-integrity, and JSON-RPC serialization tests.
- `actionlint .github/workflows/guardrails.yaml`
- `go test -count=1 -run '^TestGuardrail_' ./...`
- `go test -count=1 ./...`
- `go vet ./...`
- `go build ./...`
- `go mod tidy -diff`
- `go mod verify`
- `jq empty manifest.json`
- `git diff --check`
- Confirm whether the companion SigNoz/agent-skills contract changes; the current tranche is internal enforcement and does not rename/remove a tool or parameter.
