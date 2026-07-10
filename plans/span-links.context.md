# Feature: Trace Span Links — Context & Discussion

## Original Prompt

> Implement the approved local-only SigNoz MCP issue #229 reproduction. Revalidate issue/PR state first. Produce a failing regression test or fixture with an OpenTelemetry span link, identify the actual owning layer, and if safe within scope implement the minimal local fix and prove tests. Do not treat the issue author's claimed root cause as fact. No external writes.

## Reference Links

- [SigNoz MCP issue #229](https://github.com/SigNoz/signoz-mcp-server/issues/229)
- [OpenTelemetry span links](https://opentelemetry.io/docs/concepts/signals/traces/#span-links)
- [SigNoz backend trace field mapper](https://github.com/SigNoz/signoz/blob/main/pkg/telemetrytraces/field_mapper.go)

## Key Decisions & Discussion Log

### 2026-07-10 — Scope and ownership investigation

- Pinned the MCP repository at `28c9bfd4fc7b7af33b3ab933c656ef2b2e431503`.
- Revalidated that issue #229 remains open, unassigned, and has no competing PR.
- Traced `signoz_get_trace_details` through the MCP client to the v5 query-range request and verified the response is raw passthrough JSON rather than a typed span decode/re-encode.
- Independently checked current SigNoz backend source: canonical field `links` maps to the stored trace `links` string column, while deprecated `references` maps to `links`.
- The proposed fix must use canonical `links`, not deprecated `references`.
- A client-level HTTP fixture will model backend projection: it will emit a realistic linked-span value only when `links` is present in `selectFields`. This demonstrates the user-visible omission and pins the request/response contract in one test.

### 2026-07-10 — Regression and implementation

- The query-aware HTTP fixture failed before implementation with `get_trace_details must request the canonical links field`, proving the request projection is the owning layer.
- Added `links` to the shared server-authored trace select list with canonical span/string metadata; no response parser or handler transformation was added.
- The shared list intentionally makes links available in both trace details and search results.
- Updated README, registered tool description, and manifest metadata because the additive field changes the documented MCP output contract.
- Checked the companion `SigNoz/agent-skills` repository. Its trace-detail guidance does not parse or document the returned row structure, so it requires no matching change.
- Full unit tests, vet, build, manifest JSON validation, and diff whitespace checks pass.

### 2026-07-10 — Contribution checklist audit

- Re-read the current `CONTRIBUTING.md` before publication.
- Synced the top-level README tool table in addition to the detailed tool documentation, handler description, and manifest metadata.
- Revalidated issue #229 as open, unassigned, zero comments, with no competing implementation PR.

## Open Questions

- [x] Is the omission in API response decoding or MCP serialization? No. The MCP server passes query-range JSON through unchanged (apart from `webUrl` enrichment).
- [x] Does the current backend recognize a selectable canonical field? Yes: `links`, span context, string data type.
- [x] Should public docs explicitly say `get_trace_details` includes `links`, and should search-trace docs mention the additive row field? Yes; README, handler description, and manifest were updated.
- [x] Can a recorded or live SigNoz response be validated without credentials? A query-aware deterministic fixture pins the current contract. Live verification was unavailable because no E2E URL/token was configured and remains a documented limitation.
