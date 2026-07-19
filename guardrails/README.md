# MCP Guardrails

This directory is the central review surface for CI-enforced MCP contract guardrails.
The test implementations remain beside the packages they protect because several need
access to unexported retry, registration, middleware, and server-composition helpers.

## Review-sensitive files

- `policy.go` contains shared limits, official aliases, and explicitly grandfathered exceptions.
- `tests.txt` is the exact inventory executed by the `guardrails / contract` GitHub check.
- `.github/workflows/guardrails.yaml` verifies the inventory and runs the guarded tests.
- Package-local functions named `TestGuardrail_*` contain the enforcement logic.

## Invariants covered

- MCP names, descriptions, schemas, and nesting stay within reviewed wire budgets.
- Advertised `signoz://` pointers resolve to non-empty resources with matching metadata.
- Tools, resources, templates, and prompts cannot silently overwrite duplicate registrations.
- Mutating POST requests are not replayed after ambiguous failures; audited read-only POSTs may retry.
- Tool results remain JSON-safe through the production transport.
- Tool-result telemetry measures the complete serialized result, including structured content.

## Changing a guardrail

Do not loosen a limit, add an exception, remove a test, or weaken an assertion merely to make CI pass.
When a contract change is intentional:

1. Explain the reason in the feature context log or PR description.
2. Update `policy.go` when a limit, alias, or grandfathered exception changes.
3. Update the package-local `TestGuardrail_*` implementation.
4. Update `tests.txt` only when a guarded test is intentionally added, removed, or renamed.
5. Run:

   ```bash
   actionlint .github/workflows/guardrails.yaml
   go test -count=1 -run '^TestGuardrail_' ./...
   go test -count=1 ./...
   ```

The dedicated workflow rejects an unsorted, duplicate, missing, or unexpected test inventory.
