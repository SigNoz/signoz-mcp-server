# MCP Guardrails

This directory is the central review surface for CI-enforced MCP contract guardrails.
The test implementations remain beside the packages they protect because several need
access to unexported retry, registration, middleware, and server-composition helpers.

## Review-sensitive files

- `policy.go` contains shared limits, official aliases, and explicitly grandfathered exceptions.
- `tests.txt` is the exact inventory executed by the `guardrails / contract` GitHub check.
- `internal/mcp-server/testdata/wire-catalog/` holds the immutable pre-migration JSON-RPC oracle.
- `.github/workflows/guardrails.yaml` verifies the inventory and runs the guarded tests.
- `.github/workflows/mcp-protocol.yaml` runs the real-server Inspector and
  selected official conformance checks.
- Package-local functions named `TestGuardrail_*` contain the enforcement logic.

## Invariants covered

- MCP names and descriptions stay within reviewed byte budgets; schema shape is constrained by reviewed property inventories and nesting depth.
- Advertised `signoz://` pointers resolve to non-empty resources with matching metadata.
- Tools, resources, templates, and prompts cannot silently overwrite duplicate registrations.
- Mutating POST requests are not replayed after ambiguous failures; audited read-only POSTs may retry.
- Tool results remain JSON-safe through the production transport.
- Tool-result telemetry measures the complete serialized result, including structured content.
- The production HTTP handler preserves discovery descriptors, deterministic resource and prompt contents, and representative tool/error results across the MCP SDK migration.

`TestGuardrail_WireCatalogGoldens` sends hand-written JSON-RPC requests through
the production HTTP handler and imports no MCP SDK type. It compares complete
catalog entries, compact content inventories, and small shape-sensitive literal
results. Only request IDs, the build-stamped server version, and top-level
discovery ordering are normalized; nested order, null, false, schema keywords,
annotations, MIME types, and `_meta` remain significant.

The oracle is compare-only and has no regeneration path. It was captured before
the SDK swap and is intentionally immutable; an intentional future contract
change must update the specific fixture through normal review, never by
re-recording the full catalog from the new runtime.

The guardrails intentionally do not impose a total serialized-schema byte ceiling.
Complex tools may need extensive field-local schema guidance; review material catalog
growth through normal code and client compatibility review. This is separate from JSON
arguments sent in a tool call: streamable HTTP request bodies retain the configurable
`MCP_MAX_REQUEST_BYTES` limit (4 MiB by default), while that middleware does not apply
to stdio.

## Protocol compatibility

The `protocol / inspector` check builds the real production binary. A bounded
raw stdio smoke verifies both legacy `2025-11-25` initialize/initialized and
modern `2026-07-28` discover/direct-call lifecycles. The script then starts the
HTTP server on loopback and uses `@modelcontextprotocol/inspector-cli@1.0.0` as
an independent initialized-client check for tools, resources, resource
templates, and prompts. It runs on every pull request to `main` without
credentials or a live SigNoz backend.

The separate `protocol / conformance` check pins
`@modelcontextprotocol/conformance@0.2.0-alpha.11` and runs selected official
scenarios against the same real `cmd/server` HTTP binary. It supplies a fake
API key and `https://example.invalid`; the selected catalog operations do not
contact a live SigNoz backend. Each scenario runs independently through the
official CLI:

```text
node tools/mcp-ci/node_modules/@modelcontextprotocol/conformance/dist/index.js \
  server --url http://127.0.0.1:<port>/mcp \
  --scenario <name> --spec-version <version> --output-dir <temp>
```

The exact selected scenarios are:

- legacy `2025-11-25`: `server-initialize`, `ping`, `tools-list`,
  `resources-list`, `prompts-list`, `dns-rebinding-protection`;
- modern `2026-07-28`: `tools-list`, `resources-list`,
  `sep-2164-resource-not-found`, `prompts-list`,
  `dns-rebinding-protection`, `caching`.

This is selected official conformance, not a claim that the full frozen
requirements pass. The unselected requirements depend on diagnostic fixture
tools and product features that SigNoz intentionally does not expose. Do not
add an everything-server copy, separate fixture binary, expected-failures
baseline, or `--requirements` claim to make those scenarios appear applicable.
The SDK-free wire oracle, focused Go matrices, and Inspector continue to own
complete SigNoz catalog and transport compatibility.

Focused Go matrices own exact HTTP and stdio wire behavior: both protocol eras,
standardized modern headers, JSON Content-Type/Accept handling, stateless
GET/DELETE 405 responses, absent `Mcp-Session-Id`, cancellation, and accepted
official-SDK differences. Do not duplicate those matrices in the shell script.

Protocol policy is split across these review-sensitive files:

- `tools/mcp-ci/package.json` and its lockfile pin the Inspector and official
  conformance CLIs exactly.
- `scripts/test-mcp-protocol.sh` owns the Inspector server lifecycle and
  selective response assertions.
- `scripts/test-mcp-conformance.sh` owns the selected official scenario list,
  production server lifecycle, and temporary report cleanup.
- `.github/workflows/mcp-protocol.yaml` owns the Node/Go toolchain and the
  separate `protocol / inspector` and `protocol / conformance` check names.

Keep assertions focused on usable protocol surfaces, stable identity, and
non-empty result envelopes. Do not assert deprecated logging behavior or turn
this check into a full catalog snapshot by pinning counts, ordering,
descriptions, schemas, or ranked documentation content. `tests.txt` remains the
inventory for Go `TestGuardrail_*` tests only.

Run the protocol lane on Ubuntu with:

```bash
npm ci --ignore-scripts --prefix tools/mcp-ci
bash -n scripts/test-mcp-protocol.sh
shellcheck scripts/test-mcp-protocol.sh
bash -n scripts/test-mcp-conformance.sh
shellcheck scripts/test-mcp-conformance.sh
actionlint .github/workflows/mcp-protocol.yaml
scripts/test-mcp-protocol.sh
scripts/test-mcp-conformance.sh
```

After each check succeeds once on the default branch, configure both
`protocol / inspector` and `protocol / conformance` as required `main` branch
checks. Upgrade either CLI only in a reviewed dependency change that reruns its
documented assertions; do not float a dist-tag or version range.

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
