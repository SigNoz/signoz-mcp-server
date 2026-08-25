# Plan: Retire Live Resource Templates

## Status
Done

## Context
The server exposes two live resource templates for alert and dashboard definitions, but production usage is limited to nine clustered reads over 90 days. The equivalent read tools are used thousands of times and provide a stronger model-controlled contract. Remove the dynamic template surface while preserving all static authoring and query-guide resources.

## Approach
1. Stop registering `signoz://alert/{id}/summary` and `signoz://dashboard/{id}/summary` and delete their handlers and focused tests.
2. Keep generic MCP resource-template registration and duplicate-detection support so the protocol surface remains available for future differentiated templates.
3. Update runtime catalog, protocol matrices, integration assertions, immutable wire fixtures, and representative read tests to expect zero resource templates and resource-not-found for the retired concrete URIs.
4. Remove the two template entries from user-facing documentation and update internal MCP best practices so live tenant reads default to tools; resource templates require a demonstrated application-controlled attachment workflow.
5. Audit companion repositories under CMP-3. Record that maintained `agent-skills` workflows already use the replacement tools and that deployed `signoz-ai-assistant` code has no dependency; keep the draft assistant cleanup in its existing PR rather than mixing repositories into this server change.

## Files to Modify
- `internal/handler/tools/resource_templates.go` — remove the two registered templates and handlers.
- `internal/handler/tools/resource_templates_test.go` — remove obsolete handler tests.
- `internal/mcp-server/server.go` — stop registering the retired template group.
- `internal/mcp-server/integration_test.go` — assert an empty template catalog.
- `internal/mcp-server/protocol_matrix_test.go` — update template counts to zero.
- `internal/mcp-server/wire_catalog_golden_test.go` — remove concrete template-read oracle cases.
- `internal/mcp-server/testdata/wire-catalog/resource-templates-list.json` — pin the empty catalog.
- `internal/mcp-server/testdata/wire-catalog/resource-template-*.json` — remove retired literal fixtures.
- `scripts/test-mcp-protocol.sh` — assert that the Inspector returns an empty resource-template catalog.
- `README.md` — remove advertised summary URIs.
- `docs/mcp-best-practices.md` — align surface-placement guidance with the measured tool-first decision.
- `plans/resource-template-deprecation.*` — retain decision evidence, companion impact, and current plan.
- Previously shipped plan/context pairs — correct stale `In Progress` statuses to `Done` and append supersession notes without rewriting shipped plan bodies.

## Verification
- `make fmt goimports` completed without drift.
- Focused handler/server tests, `go vet ./...`, and `go build -o /dev/null ./cmd/server` passed.
- `go test -count=1 -run '^TestGuardrail_' ./...` and `go test -count=1 ./...` passed.
- `go mod tidy -diff` and `go mod verify` passed.
- Actionlint passed for the guardrail and MCP-protocol workflows; ShellCheck 0.10.0 passed for both protocol scripts.
- The Inspector script passed legacy/modern stdio plus initialized HTTP discovery with an empty template catalog.
- The conformance script passed all 6 selected legacy and 6 selected modern scenarios against the production server.
- `git diff --check` and shell syntax validation passed.
