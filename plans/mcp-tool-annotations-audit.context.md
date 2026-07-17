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

### 2026-07-17 — Codex (gpt-5.6-sol) review findings, both accepted
- **`signoz_update_notification_channel` is not idempotent**: after a successful PUT, the handler sends a live test notification (`client.TestNotificationChannel`, `notification_channels.go`), so a client retry fires another Slack/email/PagerDuty message. Reclassified to `(readOnly=false, destructive=true, idempotent=false)` via a new `withNonIdempotentUpdateToolAnnotations()` helper; pinned as `nonIdempotentUpdateTriple` in the inventory test. (`signoz_create_notification_channel` also test-fires, but creates are already `idempotentHint(false)`.)
- **Registration lists had drifted into three hand-maintained copies** (production `internal/mcp-server/server.go`, `integration_test.go`, `schema_inventory_test.go`) — a new handler group registered in production but not in the test list would bypass the pinned annotation inventory. Consolidated into `Handler.RegisterAllToolHandlers` (`register.go`), now the single source of truth used by production and both test setups. `RegisterResourceTemplates` (resources, not tools) stays a separate call.

### 2026-07-17 — Opus /code-review (xhigh) findings and dispositions
- **Accepted: `signoz_create_notification_channel` must not be plain create-class.** Its handler also fires a live test notification on every call (`handleCreateNotificationChannel` → `client.TestNotificationChannel`) — an irreversible outward-facing action (Slack post, email, page). Flipping it to `destructiveHint(false)` would have loosened confirmation gating relative to main for a tool that can page on-call. Both channel mutations now share `withTestNotifyMutationToolAnnotations()` → `(readOnly=false, destructive=true, idempotent=false)`. This is a documented, deliberate deviation from #142's blanket "creates → destructive(false)" rule.
- **Rejected: "creates generally should stay destructive(true)".** The destructive→false flip on `create_alert`/`create_dashboard`/`create_view`/`import_dashboard` is the core intent of #142 and matches the MCP spec (additive data writes; no live side effects in those handlers).
- **Rejected: "deletes idempotent(true) causes spurious retry errors".** The hint is accurate per spec — double-delete has no additional upstream effect; retry presentation is client policy. #142 explicitly prescribes this triple for deletes.
- **Accepted: stale `Files to Modify` list in `.plan.md`** — rewritten to include `register.go`, the fifth helper, and the mcp-server registration changes.

### 2026-07-17 — Maintainer decision: create_notification_channel is plain create-class
- Vishal overruled the Opus-review deviation: `signoz_create_notification_channel` uses `withCreateToolAnnotations()` like `signoz_create_alert`. Rationale: per the MCP spec, `destructiveHint` covers destroying/overwriting existing data — the always-sent test notification is part of the documented create contract, not data destruction — and `idempotentHint(false)` already tells clients a retry is not free (it re-notifies and duplicates the channel).
- `signoz_update_notification_channel` keeps `(readOnly=false, destructive=true, idempotent=false)` — destructive because the PUT overwrites existing config, non-idempotent because of the per-call test notification (Codex finding stands). Helper renamed back to `withNonIdempotentUpdateToolAnnotations()`; the test-notify class is dropped.

## Open Questions
- [ ] Should `openWorldHint(false)` be set on all tools (single configured SigNoz backend)? Deferred — not part of #142's triple.
- [ ] Revisit `signoz_update_view` idempotent/destructive hints when nerve-pod#100 is fixed upstream.
