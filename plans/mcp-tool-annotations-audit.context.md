# Feature: MCP Tool Annotations Audit — Context & Discussion

## Original Prompt
> Let's work on https://github.com/SigNoz/nerve-pod/issues/142
> Run /codex 5.6 sol review along with /code-review after changes
> Work on a worktree from main

## Reference Links
- [nerve-pod#142 — MCP tool annotations audit](https://github.com/SigNoz/nerve-pod/issues/142)
- [nerve-pod#100 — signoz_update_view on a deleted view returns success with sentinel metadata](https://github.com/SigNoz/nerve-pod/issues/100)
- [nerve-pod#13 — encode constraints in JSON Schema, not prose](https://github.com/SigNoz/nerve-pod/issues/13)
- [MCP spec — tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-annotations)

## Key Decisions & Discussion Log

### 2026-07-17 — Tool classification (41 registered tools)
- Enumerated via `mcp.NewTool(` registrations in `internal/handler/tools/`; count is 41 (issue estimated ~43), matching `manifest.json`'s 41 tool entries.
- **Reads (28):** all `list_*`/`get_*`/`search_*`/`aggregate_*`/`query_*`/`check_*`/`execute_builder_query`/`fetch_doc` tools. Pure GET/POST-query calls with no state change.
- **Creates (5):** `create_alert`, `create_dashboard`, `import_dashboard`, `create_view`, `create_notification_channel`. All POST to upstream create endpoints (`internal/client/client.go`); `import_dashboard` POSTs a new dashboard from a template each call, so it is create-class (a second identical call makes a duplicate dashboard → `idempotentHint(false)`).
- **Updates (4):** `update_alert`, `update_dashboard`, `update_view`, `update_notification_channel`. All PUT full-replacement of the resource by id (verified in `internal/client/client.go`: `http.MethodPut` for rules, dashboards, views, channels). Repeating the same payload converges to the same state → `idempotentHint(true)`.
- **Deletes (4):** `delete_alert`, `delete_dashboard`, `delete_view`, `delete_notification_channel`. Upstream DELETE by id; a second delete of the same id fails with not-found upstream but performs no additional state change → `idempotentHint(true)` (same reasoning as HTTP DELETE idempotency).

### 2026-07-17 — Creates are NOT destructive
- Per MCP spec, `destructiveHint` means "may destroy/overwrite existing data". Pure POST creates are additive; they were previously marked `destructiveHint(true)`, which caused clients to over-prompt. Now `destructiveHint(false)` + `idempotentHint(false)` (repeat call creates a duplicate).

### 2026-07-17 — nerve-pod#100 caveat on `signoz_update_view` idempotency
- #100 shows update-after-delete on views returns a fake success with sentinel metadata. That is an error-reporting bug, not an idempotency violation: repeating the same `signoz_update_view` call (against a live or deleted view) produces no *additional* state change, so `idempotentHint(true)` remains honest. Noted here so the decision is revisited if #100's resolution changes update semantics.

### 2026-07-17 — Implementation shape
- Added four composite `mcp.ToolOption` helpers in `annotations.go` (`withReadOnlyToolAnnotations`, `withCreateToolAnnotations`, `withUpdateToolAnnotations`, `withDeleteToolAnnotations`) instead of repeating three raw hint options at 41 call sites. Update/delete triples are identical today but stay separate helpers so the call site documents intent and the classes can diverge later.
- Pinning test: `annotations_inventory_test.go` holds an exact tool→triple map checked both directions (unpinned registered tool fails; pinned-but-unregistered tool fails), so a new tool cannot ship without a deliberate annotation choice.
- `openWorldHint` left unset: out of scope for #142's triple; worth a follow-up discussion since all tools target one configured SigNoz backend (arguably closed-world).

### 2026-07-17 — Docs/metadata sync checklist outcome
- `manifest.json` tool entries carry only `name` + `description` — no annotation mirror → no manifest change needed.
- README/docs contain no tool-annotation tables → no docs change needed.
- agent-skills repo: annotations are advisory client-side hints and do not alter the tool contract skills teach → no companion change needed.

## Open Questions
- [ ] Should `openWorldHint(false)` be set on all tools (single configured SigNoz backend)? Deferred — not part of #142's triple.
- [ ] Revisit `signoz_update_view` idempotent/destructive hints when nerve-pod#100 is fixed upstream.
