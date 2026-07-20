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

- MCP names and descriptions stay within reviewed byte budgets; schema shape is constrained by reviewed property inventories and nesting depth.
- Advertised `signoz://` pointers resolve to non-empty resources with matching metadata.
- Tools, resources, templates, and prompts cannot silently overwrite duplicate registrations.
- Mutating POST requests are not replayed after ambiguous failures; audited read-only POSTs may retry.
- Tool results remain JSON-safe through the production transport.
- Tool-result telemetry measures the complete serialized result, including structured content.

The guardrails intentionally do not impose a total serialized-schema byte ceiling.
Complex tools may need extensive field-local schema guidance; review material catalog
growth through normal code and client compatibility review. This is separate from JSON
arguments sent in a tool call: streamable HTTP request bodies retain the configurable
`MCP_MAX_REQUEST_BYTES` limit (4 MiB by default), while that middleware does not apply
to stdio.

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
