# Plan: MCP Tool Annotations Audit

## Status
In Progress

## Context
MCP clients use tool annotations to decide auto-approval policy: accurate `readOnlyHint` lets reads run without confirmation prompts, and `destructiveHint`/`idempotentHint` gate genuine mutations. Today reads declare `readOnlyHint(true)` + `destructiveHint(false)` but no `idempotentHint`; mutations declare only `destructiveHint(true)` — including pure creates, which is inaccurate per the MCP spec (destructive = may destroy/overwrite existing data; a POST create is additive). No tool sets `idempotentHint`. nerve-pod#142 asks for the full triple, explicitly, on every tool, plus a pinning test.

## Approach
Annotation triples per class (see `.context.md` for per-tool verification of upstream semantics):

| Class | readOnlyHint | destructiveHint | idempotentHint |
|---|---|---|---|
| read (28) | true | false | true |
| create (4, incl. `signoz_import_dashboard`) | false | false | false |
| update (3, upstream PUT full-replace) | false | true | true |
| test-notify mutation (2: `signoz_create_notification_channel`, `signoz_update_notification_channel` — each fires a live test notification per call) | false | true | false |
| delete (4, upstream DELETE by id) | false | true | true |

1. Add `internal/handler/tools/annotations.go` with five composite `mcp.ToolOption` helpers, one per class, each setting the full triple. Doc comments carry the spec reasoning.
2. Replace the existing per-tool hint options at all 41 `mcp.NewTool` call sites with the matching class helper.
3. Add `internal/handler/tools/annotations_inventory_test.go`: exact map of tool name → expected triple, verified in both directions against `registeredTestTools`, with non-nil assertions on all three hint pointers.
4. Consolidate tool registration into `Handler.RegisterAllToolHandlers` (`internal/handler/tools/register.go`) used by production `server.go`, `integration_test.go`, and the inventory tests — one source of truth so a new handler group cannot bypass the pinned inventory.
5. Docs/metadata sync: manifest.json and README carry no annotation mirror (verified) — no changes; note in PR summary.

## Files to Modify
- `internal/handler/tools/annotations.go` — new; five class helpers (read, create, update, test-notify mutation, delete)
- `internal/handler/tools/register.go` — new; `RegisterAllToolHandlers` single-source registrar
- `internal/handler/tools/alerts.go`, `dashboards.go`, `views.go`, `notification_channels.go`, `docs.go`, `fields.go`, `logs.go`, `metrics.go`, `metrics_top.go`, `metric_usage.go`, `metric_cardinality.go`, `query_builder.go`, `services.go`, `traces.go` — swap raw hint options for class helpers
- `internal/handler/tools/annotations_inventory_test.go` — new; pinned triple inventory
- `internal/handler/tools/schema_inventory_test.go` — drop local registrar copy, use `RegisterAllToolHandlers`
- `internal/mcp-server/server.go`, `internal/mcp-server/integration_test.go` — register via `RegisterAllToolHandlers`
- `plans/mcp-tool-annotations-audit.{context,plan}.md` — this pair

## Verification
- `go build ./...` and `go test ./...` green.
- New inventory test fails if: a tool registers without a pinned triple, a pinned tool disappears, or any advertised hint differs from the pin.
- Grep check: no remaining bare `WithDestructiveHintAnnotation`/`WithReadOnlyHintAnnotation` call sites outside `annotations.go`.
- /codex 5.6 sol review + /code-review (opus) on the diff.
