# Plan: Catalog Metadata Sweep

## Status
Done

## Context
PR #248 improves model-facing metadata across the initialized MCP catalog. Its first iteration also committed a large nondeterministic model-evaluation framework whose maintenance and execution cost outweigh its value in the production repository.

## Approach
1. Review every changed tool and resource against the issue #34 placement rubric and the actual handler/schema contract.
2. Keep external-model comparisons as out-of-tree review evidence only. Do not commit model runners, prompt corpora, raw fixtures, generated result reports, or CI jobs that cannot reproduce the external calls.
3. Retain only deterministic checks that protect a production contract: description/name/schema-shape budgets, manifest parity, resolvable resource pointers, readable static resources, schema compatibility, and changed resource-template behavior. Do not impose a total serialized-schema byte ceiling.
4. Remove literal-description phrase inventories and overlapping metadata tests. Prefer one catalog-wide invariant over per-family copies, and test runtime behavior instead of prose whenever possible.
5. Keep README and `manifest.json` synchronized with the initialized catalog and state evidence limitations accurately in the PR body.
6. Prefer plain, direct wording over phrases such as "adaptable canonical payload shapes." Remove prose that only repeats an obvious numeric sign constraint, while retaining non-obvious units, defaults, bounds, and special zero behavior.

## Commit Batches
- Catalog metadata and resource improvements by tool family.
- Minimal deterministic contract checks and documentation synchronization.
- Eval-suite removal and final PR evidence cleanup.

## Files to Modify
- `internal/handler/tools/`, `pkg/**`, and `pkg/types/` — model-facing metadata and resource behavior.
- `internal/mcp-server/` — compact wire-budget, manifest, and resource-integrity checks.
- `guardrails/` — remove the serialized-schema byte ceiling while retaining other contract limits.
- `README.md`, `manifest.json`, and PR metadata — synchronized user-facing catalog.
- `plans/catalog-metadata-sweep.context.md` and this plan — decision trail and current scope.

## Verification
Run focused changed-package tests, `go test ./...`, `go vet ./...`, JSON parsing for `manifest.json`, and `git diff --check`. Verify that no committed model-eval harness, corpus, result artifact, or evaluator-only CI job remains. External model observations may explain metadata choices but do not establish deterministic performance or non-degradation.
