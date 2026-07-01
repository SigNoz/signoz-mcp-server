# Plan: Typed-struct tool field descriptions (#359)

## Status
In Progress

## Context
The four typed-struct tools — `signoz_create_alert`, `signoz_update_alert`, `signoz_create_dashboard`, `signoz_update_dashboard` — register their input schema via `mcp.WithInputSchema[T]()`, which generates the schema with `google/jsonschema-go`. That library uses the **`jsonschema` tag value as the field description** and never reads the `jsonschema_extras` tags the structs were (mistakenly) authored with. It also treats `jsonschema:"required"` as description text, so those fields surfaced the literal word "required". Net: the model saw the most structurally complex tools with missing or garbage field docs. (SigNoz/signoz-ai-assistant#359 — a tag-dialect mismatch: the tags were written in `invopop/jsonschema`'s dialect but fed to `google/jsonschema-go`.)

## Approach (native — final)
Author the descriptions in the tag the generator actually reads, so descriptions flow natively with no post-process:

- Migrate every field tag in `pkg/types/alertrule.go` and `pkg/types/dashboard.go`:
  - `jsonschema:"required" jsonschema_extras:"description=X"` → `jsonschema:"X"`
  - `jsonschema_extras:"description=X"` → `jsonschema:"X"`
  - standalone `jsonschema:"required"` → removed (inert; requiredness comes from `omitempty`).
- Constraint honored: a `jsonschema` tag value must not match `^[^ \t\n]*=` (a `WORD=` prefix). Verified all 192 descriptions comply.
- No runtime code path: revert the 4 sites to plain `addTool`; the schema generator now emits descriptions directly.

This is preferred over the post-process alternative (a reflection-guided injector, the first cut on this branch) because it removes runtime reflection, removes coupling to jsonschema-go's full-inlining behavior, and matches the option-style `mcp.Description` convention used by every other tool.

## Files to Modify
- `pkg/types/alertrule.go` — migrate all field tags to native `jsonschema` descriptions; drop `jsonschema:"required"`.
- `pkg/types/dashboard.go` — same.
- `internal/handler/tools/alerts.go` / `dashboards.go` — revert the 4 sites to `addTool`.
- `internal/handler/tools/schema_descriptions.go` — **deleted** (post-process no longer needed).
- `internal/handler/tools/schema_compat_test.go` — unit tests reading the native normalized schema: authored descriptions present (top-level + slice `items` + map `additionalProperties`); `"required"` never appears as a description in any of the 4 schemas.
- `internal/mcp-server/integration_test.go` — registration-boundary guard (sole guard): lists tools via the in-process client and fails if any tool exposes a `"required"` description, plus an end-to-end authored-prose check on `signoz_create_alert`.

## Verification
- `go build ./...`, `go vet ./...`, full `go test ./...` pass.
- Requiredness preserved: before/after schema dumps for all 4 tools are byte-identical once `description` keys are stripped (every `required` array unchanged). Descriptions added: 89 / 90 / 197 / 199; literal-`"required"` descriptions: 0.
- Codex gpt-5.5/xhigh review of the native diff.
- No live SigNoz calls needed. No README/manifest sync required (descriptions not duplicated there).
