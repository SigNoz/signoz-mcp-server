# Feature: search_logs `filter` Param Handoff — Context & Discussion

## Original Prompt
> [Screenshot from a user/agent session] An agent repeatedly called `signoz_search_logs`
> with `{"filter": "host LIKE 'storm%'", "start": ..., "end": ..., "limit": "50"}` and could
> not find the expected logs. It kept second-guessing its *filter syntax* (LIKE, log IDs)
> when the real problem was the argument was being dropped. User: "i think ur remote mcp tool
> description is probably broken?" → asked for a handoff doc to the MCP dev owner.

## Reference Links
- Tool schema: `internal/handler/tools/logs.go:45-63` (`signoz_search_logs`)
- Arg parser: `internal/handler/tools/logs_helper.go:28-59` (`parseSearchLogsArgs`)
- Sibling tool using `filter`: `internal/handler/tools/logs.go:19-41` (`signoz_aggregate_logs`)

## Key Decisions & Discussion Log
### 2026-06-22 — root cause
- The agent passed `filter` to `signoz_search_logs`, but that tool exposes **no `filter`
  parameter**. Its free-form filter argument is named `query` (`logs.go:52`).
- `parseSearchLogsArgs` only reads `args["query"]` (`logs_helper.go:29`). Any unknown key —
  including `filter` — is **silently dropped**. MCP does not reject unknown properties, so
  there is no error surfaced to the model.
- Result: the search ran with an *empty* filter expression (time-window only) and returned
  arbitrary logs. The agent then wasted turns "fixing" filter syntax that was never the
  problem, because the entire argument had been discarded.

### 2026-06-22 — why the model guessed wrong
- Naming inconsistency between the two log tools is the trap:
  - `signoz_aggregate_logs` → filter arg is **`filter`** (`logs.go:29`)
  - `signoz_search_logs` → filter arg is **`query`** (`logs.go:52`)
- `filter` is also the more natural term, so the model reaches for it. The two sibling tools
  disagreeing on the name is the design smell to fix.

### 2026-06-22 — recommended direction (for MCP owner to ratify)
- Prefer **accepting `filter` as an alias** over a hard rename. A bare rename (`query`→`filter`)
  would break any caller currently passing `query`. Aliasing tolerates both and converges the
  two tools' vocabulary.
- Check `signoz_search_traces` for the same `query` vs `filter` inconsistency while here.
- Per CLAUDE.md doc-sync checklist: this DOES change a tool schema, so README.md tool tables
  and manifest.json tool metadata must be updated in the same PR.

## Open Questions
- [ ] Alias (`filter` accepted, `query` still works) vs. rename with `query` kept as deprecated
      alias — owner's call. Recommendation: alias, keep both documented.
- [ ] Should unknown/unrecognized args produce a soft warning in the tool result instead of
      being silently dropped? Broader fix that would have surfaced this class of bug
      immediately. Out of scope for the immediate fix but worth a decision.
- [ ] Does `signoz_search_traces` (and any other search-style tool) share the `query` vs
      `filter` divergence? Audit before closing.
